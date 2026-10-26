package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	dpauth "github.com/rakunlabs/dataport/internal/auth"
	"github.com/rakunlabs/dataport/internal/config"
	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/store"
)

// The MCP endpoint lets AI agents work with the databases shared with a
// user. A client signs in with one of the user's access tokens and gets
// exactly what the user gets in the workspace: the same grants, read-only
// enforcement, production confirmation and query log.

const mcpToolPrefix = "dataport_"

type mcpCaller struct {
	user  *store.User
	token *store.AccessToken
}

func (s *Server) mcpHandler() http.Handler {
	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		caller := mcpCallerFrom(r.Context())
		if caller == nil {
			return nil
		}

		return s.newMCPServer(caller)
	}, &mcp.StreamableHTTPOptions{
		SessionTimeout:               30 * time.Minute,
		Stateless:                    s.cfg.MCP.Stateless,
		PropagateRequestCancellation: true,
		// Every request carries a bearer token, which a page tricked into
		// calling the endpoint does not have; a reverse proxy on the same
		// host would otherwise be refused.
		DisableLocalhostProtection: true,
	})

	verify := func(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		user, t, err := s.auth.AuthenticateToken(ctx, token)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, fmt.Errorf("%w: unknown or expired token", auth.ErrInvalidToken)
			}

			return nil, err
		}

		info := &auth.TokenInfo{UserID: user.ID, Extra: map[string]any{"caller": &mcpCaller{user: user, token: t}}}
		if t.ExpiresAt > 0 {
			info.Expiration = time.UnixMilli(t.ExpiresAt)
		}

		return info, nil
	}

	return limitBody(4 << 20)(auth.RequireBearerToken(verify, &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})(handler))
}

func mcpCallerFrom(ctx context.Context) *mcpCaller {
	info := auth.TokenInfoFromContext(ctx)
	if info == nil {
		return nil
	}

	c, _ := info.Extra["caller"].(*mcpCaller)

	return c
}

// callerOf returns the caller of a tool call. The token is checked again on
// every HTTP request, so a revoked token or a disabled user stops at once.
func callerOf(req *mcp.CallToolRequest) (*mcpCaller, error) {
	if req.Extra != nil && req.Extra.TokenInfo != nil {
		if c, _ := req.Extra.TokenInfo.Extra["caller"].(*mcpCaller); c != nil {
			return c, nil
		}
	}

	return nil, errors.New("not signed in")
}

func (s *Server) newMCPServer(caller *mcpCaller) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "dataport",
		Title:   "dataport",
		Version: config.Service,
	}, &mcp.ServerOptions{
		Instructions: mcpInstructions(s.cfg.MCP, caller.token.ReadOnly),
	})

	s.registerMCPTools(server, caller.token.ReadOnly)

	return server
}

func mcpInstructions(cfg config.MCP, readOnly bool) string {
	var b strings.Builder

	b.WriteString("dataport gives you the databases an administrator shared with the user who created your access token. ")
	b.WriteString("You act as that user: their grants, read-only limits and production rules apply, and every statement is written to an audit log.\n\n")
	b.WriteString("Workflow:\n")
	b.WriteString("1. Call " + mcpToolPrefix + "list_connections to see the databases and their SQL dialect.\n")
	b.WriteString("2. Call " + mcpToolPrefix + "schema_context for a compact overview of a schema, or " +
		mcpToolPrefix + "list_tables and " + mcpToolPrefix + "describe_table for a few tables.\n")
	b.WriteString("3. Write SQL against the real names you read and run it with " + mcpToolPrefix + "query.\n\n")
	b.WriteString("Rules:\n")
	b.WriteString("- Never invent table or column names; inspect the schema first.\n")
	b.WriteString("- Write SQL in the dialect of the connection's db_type.\n")
	fmt.Fprintf(&b, "- Results are capped at %d rows per statement and long values are shortened. ", cfg.MaxRows)
	b.WriteString("If truncated is true you see part of the result: narrow it with WHERE, LIMIT or aggregates.\n")
	b.WriteString("- Each call runs in one transaction managed by dataport; BEGIN, COMMIT and ROLLBACK are refused.\n")

	if readOnly {
		b.WriteString("- This token is read-only: no write tool is available. If the user needs a change, tell them to make it in dataport.\n")
	} else {
		b.WriteString("- " + mcpToolPrefix + "query only reads. Use " + mcpToolPrefix + "execute for changes, only on connections where writable is true, ")
		b.WriteString("and only after the user asked for the change. Always scope UPDATE and DELETE with a specific WHERE clause.\n")
		b.WriteString("- On production connections the user is asked to confirm the exact SQL before it runs.\n")
	}

	b.WriteString("- A refused statement is a policy decision; do not try to work around it.\n")

	return b.String()
}

// ////////////////////////////////////////////
// tool types

type mcpConnection struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	DBType      string `json:"db_type" jsonschema:"database type, which decides the SQL dialect"`
	Database    string `json:"database,omitempty"`
	Writable    bool   `json:"writable"`
	Production  bool   `json:"production,omitempty"`
	// NeedsSignIn is set when the user has not signed in with their own
	// database account yet, which only works in dataport itself.
	NeedsSignIn bool `json:"needs_sign_in,omitempty"`
}

type listConnectionsOut struct {
	Connections []mcpConnection `json:"connections"`
}

type connectionIn struct {
	Connection string `json:"connection" jsonschema:"connection name from dataport_list_connections"`
}

type listSchemasOut struct {
	Connection string   `json:"connection"`
	Schemas    []string `json:"schemas"`
}

type listTablesIn struct {
	Connection string `json:"connection" jsonschema:"connection name from dataport_list_connections"`
	Schema     string `json:"schema,omitempty" jsonschema:"schema (or database) to list; omit for the first one dataport_list_schemas returns"`
}

type listTablesOut struct {
	Connection string          `json:"connection"`
	Schema     string          `json:"schema"`
	Tables     []engine.Object `json:"tables"`
}

type describeTableIn struct {
	Connection string `json:"connection" jsonschema:"connection name from dataport_list_connections"`
	Schema     string `json:"schema,omitempty" jsonschema:"schema of the table; omit for the default schema"`
	Table      string `json:"table" jsonschema:"table or view name"`
}

type describeTableOut struct {
	Connection  string              `json:"connection"`
	Schema      string              `json:"schema"`
	Table       string              `json:"table"`
	Columns     []engine.ColumnInfo `json:"columns"`
	Indexes     []engine.IndexInfo  `json:"indexes,omitempty"`
	ForeignKeys []engine.ForeignKey `json:"foreign_keys,omitempty"`
}

type schemaContextIn struct {
	Connection string   `json:"connection" jsonschema:"connection name from dataport_list_connections"`
	Schema     string   `json:"schema,omitempty" jsonschema:"schema to describe; omit for the default schema"`
	Tables     []string `json:"tables,omitempty" jsonschema:"describe only these tables; omit for every table up to the server cap"`
}

type schemaContextOut struct {
	Connection string `json:"connection"`
	Schema     string `json:"schema"`
	TableCount int    `json:"table_count"`
	Described  int    `json:"tables_described"`
	Truncated  bool   `json:"truncated,omitempty"`
	Compact    string `json:"compact"`
}

type queryIn struct {
	Connection string `json:"connection" jsonschema:"connection name from dataport_list_connections"`
	SQL        string `json:"sql" jsonschema:"one or more read-only statements (SELECT, WITH, SHOW, EXPLAIN) separated by semicolons"`
	MaxRows    int    `json:"max_rows,omitempty" jsonschema:"rows to return per statement; may be lower than the server cap, never higher"`
}

type executeIn struct {
	Connection string `json:"connection" jsonschema:"connection name from dataport_list_connections; it must be writable"`
	SQL        string `json:"sql" jsonschema:"statements that change data or schema, separated by semicolons; they run in one transaction where the database has transactions"`
}

type mcpResult struct {
	Statement      string   `json:"statement"`
	Columns        []string `json:"columns,omitempty"`
	Types          []string `json:"types,omitempty"`
	Rows           [][]any  `json:"rows,omitempty"`
	RowCount       int      `json:"row_count"`
	Truncated      bool     `json:"truncated,omitempty"`
	CellsTruncated int      `json:"cells_truncated,omitempty"`
	RowsAffected   int64    `json:"rows_affected,omitempty"`
	DurationMS     int64    `json:"duration_ms"`
	Error          string   `json:"error,omitempty"`
}

type queryOut struct {
	Connection string      `json:"connection"`
	Results    []mcpResult `json:"results"`
	// Committed is false when nothing was saved: read-only runs and failed
	// scripts are rolled back.
	Committed  bool  `json:"committed"`
	DurationMS int64 `json:"duration_ms"`
}

// ////////////////////////////////////////////
// tools

func (s *Server) registerMCPTools(server *mcp.Server, readOnly bool) {
	closed := false
	destructive := true

	ro := func(title string) *mcp.ToolAnnotations {
		return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closed}
	}

	mcp.AddTool(server, &mcp.Tool{
		Name:        mcpToolPrefix + "list_connections",
		Annotations: ro("List connections"),
		Description: "List the databases shared with you: name, database type (the SQL dialect), whether you may write " +
			"and whether it is a production database. Call this first; every other tool takes a connection name.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listConnectionsOut, error) {
		caller, err := callerOf(req)
		if err != nil {
			return nil, listConnectionsOut{}, err
		}

		list, err := s.mcpConnections(ctx, caller)

		return nil, listConnectionsOut{Connections: list}, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        mcpToolPrefix + "list_schemas",
		Annotations: ro("List schemas"),
		Description: "List the schemas (databases on MySQL and ClickHouse, owners on Oracle) of a connection, the default one first.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in connectionIn) (*mcp.CallToolResult, listSchemasOut, error) {
		g, password, _, err := s.mcpGrant(ctx, req, in.Connection)
		if err != nil {
			return nil, listSchemasOut{}, err
		}

		list, err := s.engine.Schemas(ctx, g.Connection, password)
		if err != nil {
			return nil, listSchemasOut{}, mcpEngineError(err)
		}

		return nil, listSchemasOut{Connection: g.Connection.Name, Schemas: list}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        mcpToolPrefix + "list_tables",
		Annotations: ro("List tables"),
		Description: "List the tables and views of one schema. For columns of many tables at once prefer " + mcpToolPrefix + "schema_context.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listTablesIn) (*mcp.CallToolResult, listTablesOut, error) {
		g, password, _, err := s.mcpGrant(ctx, req, in.Connection)
		if err != nil {
			return nil, listTablesOut{}, err
		}

		schema, err := s.mcpSchema(ctx, g, password, in.Schema)
		if err != nil {
			return nil, listTablesOut{}, err
		}

		list, err := s.engine.Objects(ctx, g.Connection, password, schema)
		if err != nil {
			return nil, listTablesOut{}, mcpEngineError(err)
		}

		return nil, listTablesOut{Connection: g.Connection.Name, Schema: schema, Tables: list}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        mcpToolPrefix + "describe_table",
		Annotations: ro("Describe a table"),
		Description: "Return the columns of a table (name, type, nullability, default, primary key) with its indexes and foreign keys, " +
			"so joins use the real keys.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in describeTableIn) (*mcp.CallToolResult, describeTableOut, error) {
		g, password, _, err := s.mcpGrant(ctx, req, in.Connection)
		if err != nil {
			return nil, describeTableOut{}, err
		}

		schema, err := s.mcpSchema(ctx, g, password, in.Schema)
		if err != nil {
			return nil, describeTableOut{}, err
		}

		out := describeTableOut{Connection: g.Connection.Name, Schema: schema, Table: in.Table}

		st, err := s.engine.Structure(ctx, g.Connection, password, schema, in.Table)
		if errors.Is(err, engine.ErrNoStructure) {
			out.Columns, err = s.engine.Columns(ctx, g.Connection, password, schema, in.Table)
		} else if err == nil {
			out.Columns, out.Indexes, out.ForeignKeys = st.Columns, st.Indexes, st.ForeignKeys
		}

		if err != nil {
			return nil, describeTableOut{}, mcpEngineError(err)
		}

		if len(out.Columns) == 0 {
			return nil, describeTableOut{}, fmt.Errorf("table %q not found in schema %q", in.Table, schema)
		}

		return nil, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        mcpToolPrefix + "schema_context",
		Annotations: ro("Schema context"),
		Description: "Return a compact description of a schema, one line per table with its columns, types and primary keys. " +
			"This is the cheapest way to learn a schema before writing SQL. Large schemas are cut at a cap; pass tables when you know which you need.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in schemaContextIn) (*mcp.CallToolResult, schemaContextOut, error) {
		g, password, _, err := s.mcpGrant(ctx, req, in.Connection)
		if err != nil {
			return nil, schemaContextOut{}, err
		}

		out, err := s.schemaContext(ctx, g, password, in)
		if err != nil {
			return nil, schemaContextOut{}, err
		}

		text := fmt.Sprintf("connection: %s\nschema: %s\ntables described: %d of %d\n", out.Connection, out.Schema, out.Described, out.TableCount)
		if out.Truncated {
			text += "truncated: pass the tables argument to describe the ones you need\n"
		}

		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text + "\n" + out.Compact}}}, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: mcpToolPrefix + "query",
		Annotations: &mcp.ToolAnnotations{
			Title: "Run a read-only query", ReadOnlyHint: true, OpenWorldHint: &closed,
		},
		Description: "Run read-only statements and return their rows. Writes are refused here whatever the grant, and the run is always rolled back. " +
			"Results are capped: if truncated is true, narrow the query instead of paging blindly.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in queryIn) (*mcp.CallToolResult, queryOut, error) {
		return s.mcpRun(ctx, req, in.Connection, in.SQL, in.MaxRows, true)
	})

	// A tool that can never succeed only makes the model try it; read-only
	// tokens do not get one. The engine still enforces the grant either way.
	if readOnly {
		return
	}

	mcp.AddTool(server, &mcp.Tool{
		Name: mcpToolPrefix + "execute",
		Annotations: &mcp.ToolAnnotations{
			Title: "Change data or schema", DestructiveHint: &destructive, OpenWorldHint: &closed,
		},
		Description: "Run statements that change data or schema on a writable connection. They run in one transaction, which is " +
			"rolled back if any statement fails (ClickHouse and Trino have none: each statement applies on its own). " +
			"On production connections the user must confirm the exact SQL first. Only run changes the user asked for.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in executeIn) (*mcp.CallToolResult, queryOut, error) {
		return s.mcpRun(ctx, req, in.Connection, in.SQL, 0, false)
	})
}

// ////////////////////////////////////////////
// helpers

func (s *Server) mcpConnections(ctx context.Context, caller *mcpCaller) ([]mcpConnection, error) {
	grants, err := s.store.ListGrants(ctx, caller.user)
	if err != nil {
		return nil, err
	}

	creds, err := s.store.UserCredentials(ctx, caller.user.ID)
	if err != nil {
		return nil, err
	}

	list := make([]mcpConnection, 0, len(grants))

	for _, g := range grants {
		c := g.Connection
		list = append(list, mcpConnection{
			Name:        c.Name,
			Description: c.Description,
			DBType:      c.DBType,
			Database:    c.Database,
			Writable:    !g.ReadOnly && !caller.token.ReadOnly,
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
	if name == "" {
		return nil, "", nil, errors.New("connection is required; call dataport_list_connections")
	}

	grants, err := s.store.ListGrants(ctx, caller.user)
	if err != nil {
		return nil, "", nil, err
	}

	id := ""

	for _, g := range grants {
		if g.Connection.Name == name || g.Connection.ID == name {
			id = g.Connection.ID

			break
		}
	}

	if id == "" {
		for _, g := range grants {
			if strings.EqualFold(g.Connection.Name, name) {
				id = g.Connection.ID

				break
			}
		}
	}

	if id == "" {
		return nil, "", nil, fmt.Errorf("unknown connection %q; call dataport_list_connections", name)
	}

	g, password, err := s.resolveGrant(ctx, caller.user, id)
	if err != nil {
		var credErr *credentialsError
		if errors.As(err, &credErr) {
			return nil, "", nil, fmt.Errorf("%s: the user has to open this connection in dataport and sign in with their database account first", credErr.msg)
		}

		return nil, "", nil, err
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

	limit := s.cfg.MCP.MaxSchemaTables
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

func (s *Server) mcpRun(ctx context.Context, req *mcp.CallToolRequest, name, sqlText string, maxRows int, readOnly bool) (*mcp.CallToolResult, queryOut, error) {
	if strings.TrimSpace(sqlText) == "" {
		return nil, queryOut{}, errors.New("sql is empty")
	}

	if len(sqlText) > maxSQLLength {
		return nil, queryOut{}, errors.New("sql is too long")
	}

	g, password, caller, err := s.mcpGrant(ctx, req, name)
	if err != nil {
		return nil, queryOut{}, err
	}

	if !readOnly && (g.ReadOnly || caller.token.ReadOnly) {
		return nil, queryOut{}, fmt.Errorf("connection %q is read-only for you", g.Connection.Name)
	}

	rows := s.cfg.MCP.MaxRows
	if maxRows > 0 && (rows <= 0 || maxRows < rows) {
		rows = maxRows
	}

	in := execInput{SQL: sqlText, ReadOnly: readOnly || caller.token.ReadOnly, MaxRows: rows, Source: "mcp"}

	result, err := s.execute(ctx, caller.user, g, password, in)

	var confirm *confirmationError
	if errors.As(err, &confirm) {
		if err := confirmProduction(ctx, req, g.Connection, sqlText, confirm.risk); err != nil {
			return nil, queryOut{}, err
		}

		in.Confirm = true
		result, err = s.execute(ctx, caller.user, g, password, in)
	}

	if err != nil {
		return nil, queryOut{}, mcpEngineError(err)
	}

	out := queryOut{Connection: g.Connection.Name, Committed: result.Committed, DurationMS: result.DurationMS}
	for _, rs := range result.Results {
		out.Results = append(out.Results, toMCPResult(rs, s.cfg.MCP.MaxCellChars))
	}

	slog.InfoContext(ctx, "mcp run", "user", caller.user.Username, "connection", g.Connection.Name, "read_only", readOnly,
		"statements", len(result.Results), "duration_ms", result.DurationMS)

	// A failed statement is the model's to fix; report it as a tool error
	// with the result attached, so the error text is visible.
	if msg := result.FirstError(); msg != "" {
		res := &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "statement failed: " + msg}}}

		return res, out, nil
	}

	return nil, out, nil
}

// confirmProduction asks the user, through the MCP client, to confirm a
// change on a production connection. Clients that cannot ask are refused:
// the user must see the exact SQL before it runs.
func confirmProduction(ctx context.Context, req *mcp.CallToolRequest, c *store.Connection, sqlText string, risk engine.Risk) error {
	refused := fmt.Errorf("%q is a production database; changes need the user's confirmation, which this MCP client cannot ask for. "+
		"Show the SQL to the user and let them run it in dataport", c.Name)

	params := req.Session.InitializeParams()
	if params == nil || params.Capabilities == nil || params.Capabilities.Elicitation == nil {
		return refused
	}

	shown := sqlText
	if len(shown) > 4000 {
		shown = shown[:4000] + "\n…"
	}

	res, err := req.Session.Elicit(ctx, &mcp.ElicitParams{
		Message: fmt.Sprintf("Run these %s statements on the production database %q?\n\n%s", risk, c.Name, shown),
		RequestedSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"confirm": map[string]any{"type": "boolean", "title": "Run on production", "description": "Run exactly this SQL"},
			},
			"required": []string{"confirm"},
		},
	})
	if err != nil {
		return refused
	}

	if res.Action != "accept" || res.Content["confirm"] != true {
		return errors.New("the user did not confirm the change; nothing was run")
	}

	return nil
}

func toMCPResult(rs *engine.ResultSet, maxChars int) mcpResult {
	out := mcpResult{
		Statement:    rs.Statement,
		RowCount:     len(rs.Rows),
		Truncated:    rs.Truncated,
		RowsAffected: rs.RowsAffected,
		DurationMS:   rs.DurationMS,
		Error:        rs.Error,
	}

	for _, c := range rs.Columns {
		out.Columns = append(out.Columns, c.Name)
		out.Types = append(out.Types, c.Type)
	}

	if rs.Kind != "rows" {
		return out
	}

	out.Rows = make([][]any, len(rs.Rows))

	for i, row := range rs.Rows {
		r := make([]any, len(row))

		for j, v := range row {
			if str, ok := v.(string); ok && maxChars > 0 && utf8.RuneCountInString(str) > maxChars {
				v = string([]rune(str)[:maxChars]) + "…"
				out.CellsTruncated++
			}

			r[j] = v
		}

		out.Rows[i] = r
	}

	return out
}

// mcpEngineError hides the host and other connection details from the
// model, as the workspace does for regular users.
func mcpEngineError(err error) error {
	var connErr *engine.ConnError
	if errors.As(err, &connErr) {
		slog.Warn("target database unreachable", "error", err)

		if connErr.Auth {
			return errors.New("the database refused the account; the user has to sign in again in dataport")
		}

		return errors.New("cannot reach the database, contact your administrator")
	}

	return err
}

// ////////////////////////////////////////////
// access tokens

type tokenRequest struct {
	Name     string `json:"name"`
	ReadOnly bool   `json:"read_only"`
	// ExpiresInDays of zero uses the longest lifetime the server allows.
	ExpiresInDays int `json:"expires_in_days"`
}

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListAccessTokens(r.Context(), currentUser(r).ID)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tokens":        list,
		"mcp_enabled":   s.cfg.MCP.Enabled,
		"mcp_path":      s.cfg.Server.BasePath + "/mcp",
		"max_ttl_days":  int(s.cfg.MCP.TokenTTL.Hours() / 24),
		"default_rows":  s.cfg.MCP.MaxRows,
		"external_base": strings.TrimRight(s.cfg.Auth.ExternalURL, "/"),
	})
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	var req tokenRequest
	if !readJSON(w, r, &req) {
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 100 {
		writeError(w, http.StatusBadRequest, "name must be 1-100 characters")

		return
	}

	maxDays := int(s.cfg.MCP.TokenTTL.Hours() / 24)

	switch {
	case req.ExpiresInDays < 0:
		writeError(w, http.StatusBadRequest, "expiry cannot be negative")

		return
	case maxDays > 0 && (req.ExpiresInDays == 0 || req.ExpiresInDays > maxDays):
		req.ExpiresInDays = maxDays
	}

	raw, hash, err := dpauth.NewAccessToken()
	if err != nil {
		writeStoreError(w, err)

		return
	}

	t := &store.AccessToken{UserID: currentUser(r).ID, Name: req.Name, TokenHash: hash, ReadOnly: req.ReadOnly}
	if req.ExpiresInDays > 0 {
		t.ExpiresAt = time.Now().Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour).UnixMilli()
	}

	if err := s.store.CreateAccessToken(r.Context(), t); err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"token": raw, "access_token": t})
}

func (s *Server) deleteToken(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteAccessToken(r.Context(), currentUser(r).ID, r.PathValue("id")); err != nil {
		writeStoreError(w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}
