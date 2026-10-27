package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/store"
)

func (s *Server) mcpConnections(ctx context.Context, caller *mcpCaller) ([]mcpConnection, error) {
	grants, err := s.store.ListGrants(ctx, caller.user)
	if err != nil {
		return nil, err
	}

	creds, err := s.store.UserCredentials(ctx, caller.user.ID)
	if err != nil {
		return nil, err
	}

	policy, err := s.store.GetMCPPolicy(ctx, caller.user.ID)
	if err != nil {
		return nil, err
	}

	list := make([]mcpConnection, 0, len(grants))

	for _, g := range grants {
		if caller.scope != "" && g.Connection.ID != caller.scope {
			continue
		}

		mode := capMCPMode(policy, caller, g)
		if mode == store.MCPOff {
			continue
		}

		c := g.Connection
		list = append(list, mcpConnection{
			Name:        c.Name,
			Description: c.Description,
			DBType:      c.DBType,
			Database:    c.Database,
			Access:      mode,
			Writable:    mode.AtLeast(store.MCPWrite),
			Production:  c.Production,
			NeedsSignIn: c.UserCredentials && creds[c.ID] == nil,
		})
	}

	return list, nil
}

// mcpGrant finds a connection by name, or by id, among those the caller may
// use.
func (s *Server) mcpGrant(ctx context.Context, req *mcp.CallToolRequest, name string) (*store.Grant, string, *mcpCaller, error) {
	caller, err := callerOf(req)
	if err != nil {
		return nil, "", nil, err
	}

	name = strings.TrimSpace(name)

	// An endpoint or token for one connection needs no name.
	id := caller.scope

	if id == "" {
		if name == "" {
			return nil, "", nil, errors.New("connection is required; call dataport_list_connections")
		}

		grants, err := s.store.ListGrants(ctx, caller.user)
		if err != nil {
			return nil, "", nil, err
		}

		g := findGrant(grants, name)
		if g == nil {
			return nil, "", nil, fmt.Errorf("unknown connection %q; call dataport_list_connections", name)
		}

		id = g.Connection.ID
	}

	g, password, err := s.resolveGrant(ctx, caller.user, id)
	if err == nil && caller.scope != "" && name != "" && findGrant([]*store.Grant{g}, name) == nil {
		return nil, "", nil, fmt.Errorf("this endpoint only serves connection %q", g.Connection.Name)
	}

	if err != nil {
		var credErr *credentialsError
		if errors.As(err, &credErr) {
			return nil, "", nil, fmt.Errorf("%s: the user has to open this connection in dataport and sign in with their database account first", credErr.msg)
		}

		return nil, "", nil, err
	}

	mode, err := s.mcpMode(ctx, caller, g)
	if err != nil {
		return nil, "", nil, err
	}

	if mode == store.MCPOff {
		return nil, "", nil, mcpAllows(mode, g, "")
	}

	return g, password, caller, nil
}

// mcpSchema returns the schema to use, the connection's first one when the
// model did not name one.
func (s *Server) mcpSchema(ctx context.Context, g *store.Grant, password, schema string) (string, error) {
	if schema = strings.TrimSpace(schema); schema != "" {
		return schema, nil
	}

	list, err := s.engine.Schemas(ctx, g.Connection, password)
	if err != nil {
		return "", mcpEngineError(err)
	}

	if len(list) == 0 {
		return "", errors.New("the connection has no schema you can read")
	}

	return list[0], nil
}

func (s *Server) schemaContext(ctx context.Context, g *store.Grant, password string, in schemaContextIn) (schemaContextOut, error) {
	schema, err := s.mcpSchema(ctx, g, password, in.Schema)
	if err != nil {
		return schemaContextOut{}, err
	}

	objects, err := s.engine.Objects(ctx, g.Connection, password, schema)
	if err != nil {
		return schemaContextOut{}, mcpEngineError(err)
	}

	out := schemaContextOut{Connection: g.Connection.Name, Schema: schema, TableCount: len(objects)}

	if len(in.Tables) > 0 {
		want := map[string]bool{}
		for _, t := range in.Tables {
			want[strings.ToLower(t)] = true
		}

		filtered := objects[:0:0]

		for _, o := range objects {
			if want[strings.ToLower(o.Name)] {
				filtered = append(filtered, o)
			}
		}

		objects = filtered
	}

	limit := s.mcpCfg().MaxSchemaTables
	if limit > 0 && len(objects) > limit {
		objects = objects[:limit]
		out.Truncated = true
	}

	var b strings.Builder

	for _, o := range objects {
		cols, err := s.engine.Columns(ctx, g.Connection, password, schema, o.Name)
		if err != nil {
			return schemaContextOut{}, mcpEngineError(err)
		}

		b.WriteString(o.Name)

		if o.Kind != "table" {
			b.WriteString(" [" + o.Kind + "]")
		}

		b.WriteString("(")

		for i, c := range cols {
			if i > 0 {
				b.WriteString(", ")
			}

			b.WriteString(c.Name + " " + c.Type)

			if c.PrimaryKey {
				b.WriteString(" pk")
			} else if !c.Nullable {
				b.WriteString(" not null")
			}
		}

		b.WriteString(")\n")

		out.Described++
	}

	out.Compact = b.String()

	return out, nil
}

// schemaReportText writes a schema document as plain lines for a model.
func schemaReportText(doc *engine.SchemaDoc) string {
	var b strings.Builder

	b.WriteString("## tables\n")

	for _, t := range doc.Tables {
		b.WriteString(t.Name)

		if t.Kind != "table" {
			b.WriteString(" [" + t.Kind + "]")
		}

		if t.Rows != nil {
			fmt.Fprintf(&b, " ~%d rows", *t.Rows)
		}

		fmt.Fprintf(&b, ", %d columns", len(t.Columns))

		for _, fk := range t.ForeignKeys {
			fmt.Fprintf(&b, "; %s -> %s(%s)", strings.Join(fk.Columns, ","), fk.RefTable, strings.Join(fk.RefColumns, ","))
		}

		if len(t.ReferencedBy) > 0 {
			refs := make([]string, len(t.ReferencedBy))
			for i, r := range t.ReferencedBy {
				refs[i] = r.Table
			}

			b.WriteString("; referenced by " + strings.Join(refs, ", "))
		}

		if t.Comment != "" {
			b.WriteString(" -- " + t.Comment)
		}

		b.WriteString("\n")
	}

	b.WriteString("\n## anomalies\n")

	if !doc.Keys {
		b.WriteString("(this database keeps no keys or indexes; key checks skipped)\n")
	}

	if len(doc.Anomalies) == 0 {
		b.WriteString("none\n")
	}

	for _, a := range doc.Anomalies {
		b.WriteString(a.Kind + ": " + a.Table)

		if len(a.Columns) > 0 {
			b.WriteString("(" + strings.Join(a.Columns, ", ") + ")")
		}

		if a.Ref != "" {
			b.WriteString(" -> " + a.Ref)

			if len(a.RefCols) > 0 {
				b.WriteString("(" + strings.Join(a.RefCols, ", ") + ")")
			}
		}

		b.WriteString("\n")
	}

	return b.String()
}
