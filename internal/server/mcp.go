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
	// scope is the one connection the caller may use: the one of the
	// endpoint (/mcp/<name>) or the one the token was made for. Empty
	// allows every connection of the user.
	scope string
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

	return limitBody(4 << 20)(withCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, status, msg := s.mcpAuthenticate(r)
		if caller == nil {
			// The header sends OAuth clients to the metadata, where they
			// learn how to sign in (RFC 9728).
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata=%q, scope="mcp"`, s.resourceMetadataURL(r)))
			http.Error(w, msg, status)

			return
		}

		info := &auth.TokenInfo{UserID: caller.user.ID, Extra: map[string]any{"caller": caller}}
		if caller.token.ExpiresAt > 0 {
			info.Expiration = time.UnixMilli(caller.token.ExpiresAt)
		}

		// The SDK reads the caller from its own context key, which only its
		// middleware sets; the token was checked above.
		verified := func(context.Context, string, *http.Request) (*auth.TokenInfo, error) { return info, nil }
		auth.RequireBearerToken(verified, &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})(handler).ServeHTTP(w, r)
	})))
}

// mcpAuthenticate checks the bearer token of a request and the connection
// of its endpoint. The token is checked again on every request, so a
// revoked token or a disabled user stops at once.
func (s *Server) mcpAuthenticate(r *http.Request) (*mcpCaller, int, string) {
	fields := strings.Fields(r.Header.Get("Authorization"))
	if len(fields) != 2 || !strings.EqualFold(fields[0], "bearer") {
		return nil, http.StatusUnauthorized, "sign in: send an access token as a bearer token"
	}

	user, t, err := s.auth.AuthenticateToken(r.Context(), fields[1])
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			slog.Error("mcp authenticate", "error", err)

			return nil, http.StatusInternalServerError, "cannot check the token"
		}

		return nil, http.StatusUnauthorized, "unknown or expired token"
	}

	caller := &mcpCaller{user: user, token: t, scope: t.ConnectionID}

	name := r.PathValue("conn")
	if name == "" {
		return caller, 0, ""
	}

	g, err := s.grantByName(r, user, name)
	if err != nil {
		return nil, http.StatusNotFound, "no connection " + name + " for you"
	}

	if t.ConnectionID != "" && t.ConnectionID != g.Connection.ID {
		return nil, http.StatusForbidden, "this token is for another connection"
	}

	caller.scope = g.Connection.ID

	return caller, 0, ""
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
		Instructions: s.mcpInstructions(caller),
	})

	s.registerMCPTools(server, caller.token.ReadOnly)

	return server
}

func (s *Server) mcpInstructions(caller *mcpCaller) string {
	text := mcpInstructions(s.cfg.MCP, caller.token.ReadOnly)
	if caller.scope == "" {
		return text
	}

	name := caller.scope
	if c, err := s.store.GetConnection(context.Background(), caller.scope); err == nil {
		name = c.Name + " (" + c.DBType + ")"
	}

	return text + "\nThis endpoint serves one connection, " + name + "; the connection argument of the tools may be omitted.\n"
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
	Connection string `json:"connection,omitempty" jsonschema:"connection name from dataport_list_connections; may be omitted on an endpoint for one connection"`
}

type listSchemasOut struct {
	Connection string   `json:"connection"`
	Schemas    []string `json:"schemas"`
}

type listTablesIn struct {
	Connection string `json:"connection,omitempty" jsonschema:"connection name from dataport_list_connections; may be omitted on an endpoint for one connection"`
	Schema     string `json:"schema,omitempty" jsonschema:"schema (or database) to list; omit for the first one dataport_list_schemas returns"`
}

type listTablesOut struct {
	Connection string          `json:"connection"`
	Schema     string          `json:"schema"`
	Tables     []engine.Object `json:"tables"`
}

type describeTableIn struct {
	Connection string `json:"connection,omitempty" jsonschema:"connection name from dataport_list_connections; may be omitted on an endpoint for one connection"`
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
	Connection string   `json:"connection,omitempty" jsonschema:"connection name from dataport_list_connections; may be omitted on an endpoint for one connection"`
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

type schemaReportIn struct {
	Connection string `json:"connection,omitempty" jsonschema:"connection name from dataport_list_connections; may be omitted on an endpoint for one connection"`
	Schema     string `json:"schema,omitempty" jsonschema:"schema to analyse; omit for the default schema"`
}

type schemaReportOut struct {
	GeneratedAt int64            `json:"generated_at"`
	Status      docStatus        `json:"status"`
	Connection  string           `json:"connection"`
	Schema      string           `json:"schema"`
	TableCount  int              `json:"table_count"`
	Truncated   bool             `json:"truncated,omitempty"`
	Anomalies   []engine.Anomaly `json:"anomalies"`
	Report      string           `json:"report"`
}

type tableDocsIn struct {
	Connection string   `json:"connection,omitempty" jsonschema:"connection name from dataport_list_connections; may be omitted on an endpoint for one connection"`
	Schema     string   `json:"schema,omitempty" jsonschema:"schema of the tables; omit for the default schema"`
	Tables     []string `json:"tables" jsonschema:"tables or views to document, at most 20"`
	// Related is a pointer so that leaving it out means true.
	Related *bool `json:"include_related,omitempty" jsonschema:"also list the columns of the tables they reference or are referenced by, for writing joins; default true"`
}

type tableDocsOut struct {
	GeneratedAt int64             `json:"generated_at"`
	Status      docStatus         `json:"status"`
	Connection  string            `json:"connection"`
	Schema      string            `json:"schema"`
	Tables      []engine.DocTable `json:"tables"`
	// Triggers of each documented table, by table name.
	Triggers  map[string][]engine.TriggerInfo `json:"triggers,omitempty"`
	Related   []engine.DocTable               `json:"related,omitempty"`
	Anomalies []engine.Anomaly                `json:"anomalies,omitempty"`
	Missing   []string                        `json:"missing,omitempty"`
	Doc       string                          `json:"doc"`
}

type queryIn struct {
	Connection string `json:"connection,omitempty" jsonschema:"connection name from dataport_list_connections; may be omitted on an endpoint for one connection"`
	SQL        string `json:"sql" jsonschema:"one or more read-only statements (SELECT, WITH, SHOW, EXPLAIN) separated by semicolons"`
	MaxRows    int    `json:"max_rows,omitempty" jsonschema:"rows to return per statement; may be lower than the server cap, never higher"`
}

type executeIn struct {
	Connection string `json:"connection,omitempty" jsonschema:"connection name from dataport_list_connections; it must be writable. May be omitted on an endpoint for one connection"`
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
		Name:        mcpToolPrefix + "table_docs",
		Annotations: ro("Document tables"),
		Description: "Full documentation of the tables you are about to write code or SQL for: every column with type, " +
			"nullability, default, primary key and comment; the foreign keys it holds and the tables that reference it, " +
			"with ready JOIN clauses; indexes and unique keys; triggers; a view's definition; the estimated row count; and " +
			"design problems found in it. The columns of directly related tables come along, so joins and ORM models use " +
			"the real names. Uses a saved snapshot with generated_at and refresh status; may be older than the live schema. Never reads table data.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in tableDocsIn) (*mcp.CallToolResult, tableDocsOut, error) {
		g, password, caller, err := s.mcpGrant(ctx, req, in.Connection)
		if err != nil {
			return nil, tableDocsOut{}, err
		}

		if len(in.Tables) == 0 || len(in.Tables) > maxTableDocs {
			return nil, tableDocsOut{}, fmt.Errorf("pass 1 to %d tables", maxTableDocs)
		}

		schema, err := s.mcpSchema(ctx, g, password, in.Schema)
		if err != nil {
			return nil, tableDocsOut{}, err
		}

		saved, err := s.documentation(ctx, store.DocKey{ConnectionID: g.Connection.ID, UserID: caller.user.ID, Schema: schema}, false)
		if err != nil {
			return nil, tableDocsOut{}, mcpEngineError(err)
		}

		out := tableDocs(saved.Document, in.Tables, in.Related == nil || *in.Related)
		out.Connection, out.Schema = g.Connection.Name, schema
		out.GeneratedAt, out.Status = saved.Document.GeneratedAt, saved.Status

		// Triggers change what a write does, so code touching the table
		// should know them. They come from the same saved snapshot.
		for _, t := range out.Tables {
			if t.Kind != "table" {
				continue
			}

			if len(t.Triggers) > 0 {
				if out.Triggers == nil {
					out.Triggers = map[string][]engine.TriggerInfo{}
				}

				out.Triggers[t.Name] = t.Triggers
			}
		}

		out.Doc = tableDocsText(g.Connection.DBType, &out)

		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: out.Doc}}}, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        mcpToolPrefix + "schema_report",
		Annotations: ro("Schema report"),
		Description: "Map a whole schema like SchemaSpy: every table with its estimated row count, which tables it references and " +
			"which reference it, and design anomalies (tables without primary key, foreign keys without an index, implied " +
			"relationships no key declares, orphan tables, duplicate indexes and more). Use it to find your way around or " +
			"review a design; for the details of the tables you work on call " + mcpToolPrefix + "table_docs. Uses a saved snapshot with generated_at; never reads table data.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in schemaReportIn) (*mcp.CallToolResult, schemaReportOut, error) {
		g, password, caller, err := s.mcpGrant(ctx, req, in.Connection)
		if err != nil {
			return nil, schemaReportOut{}, err
		}

		schema, err := s.mcpSchema(ctx, g, password, in.Schema)
		if err != nil {
			return nil, schemaReportOut{}, err
		}

		saved, err := s.documentation(ctx, store.DocKey{ConnectionID: g.Connection.ID, UserID: caller.user.ID, Schema: schema}, false)
		if err != nil {
			return nil, schemaReportOut{}, mcpEngineError(err)
		}

		doc := saved.Document
		out := schemaReportOut{
			GeneratedAt: doc.GeneratedAt, Status: saved.Status,
			Connection: g.Connection.Name, Schema: schema, TableCount: doc.TableCount, Truncated: doc.Truncated,
			Anomalies: doc.Anomalies, Report: schemaReportText(doc),
		}

		head := fmt.Sprintf("connection: %s\nschema: %s\ntables: %d\n", out.Connection, schema, doc.TableCount) + docFreshness(doc.GeneratedAt, saved.Status)
		if doc.Truncated {
			head += fmt.Sprintf("truncated: the first %d tables are analysed\n", len(doc.Tables))
		}

		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: head + "\n" + out.Report}}}, out, nil
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
		if caller.scope != "" && g.Connection.ID != caller.scope {
			continue
		}

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

// maxTableDocs bounds one table_docs call; related tables come on top.
const maxTableDocs = 20

// tableDocs picks the asked tables out of a schema document, with the
// tables one key away and the anomalies of the asked ones.
func tableDocs(doc *engine.SchemaDoc, names []string, related bool) tableDocsOut {
	out := tableDocsOut{Tables: []engine.DocTable{}}

	byName := map[string]engine.DocTable{}
	lower := map[string]string{}

	for _, t := range doc.Tables {
		byName[t.Name] = t
		lower[strings.ToLower(t.Name)] = t.Name
	}

	asked := map[string]bool{}

	for _, n := range names {
		name, ok := byName[n]
		if !ok {
			// Models often change the case of a name.
			real, found := lower[strings.ToLower(n)]
			if !found {
				out.Missing = append(out.Missing, n)

				continue
			}

			name = byName[real]
		}

		if !asked[name.Name] {
			asked[name.Name] = true
			out.Tables = append(out.Tables, name)
		}
	}

	if related {
		seen := map[string]bool{}

		add := func(n string) {
			if t, ok := byName[n]; ok && !asked[n] && !seen[n] {
				seen[n] = true
				out.Related = append(out.Related, t)
			}
		}

		for _, t := range out.Tables {
			for _, fk := range t.ForeignKeys {
				add(fk.RefTable)
			}

			for _, r := range t.ReferencedBy {
				add(r.Table)
			}
		}

		for _, a := range doc.Anomalies {
			if a.Kind == engine.AnomalyImplied && asked[a.Table] {
				add(a.Ref)
			}
		}
	}

	for _, a := range doc.Anomalies {
		if asked[a.Table] {
			out.Anomalies = append(out.Anomalies, a)
		}
	}

	return out
}

// tableDocsText writes table documentation for a model: compact, with the
// joins spelled out in the connection's quoting.
func tableDocsText(dbType string, out *tableDocsOut) string {
	d, _ := engine.GetDriver(dbType)
	q := func(s string) string {
		if d == nil {
			return s
		}

		return d.QuoteIdent(s)
	}

	join := func(from string, fromCols []string, to string, toCols []string) string {
		conds := make([]string, 0, len(fromCols))
		for i, c := range fromCols {
			if i < len(toCols) {
				conds = append(conds, q(from)+"."+q(c)+" = "+q(to)+"."+q(toCols[i]))
			}
		}

		return "JOIN " + q(to) + " ON " + strings.Join(conds, " AND ")
	}

	var b strings.Builder

	fmt.Fprintf(&b, "connection: %s\nschema: %s\n", out.Connection, out.Schema)
	b.WriteString(docFreshness(out.GeneratedAt, out.Status))

	if len(out.Missing) > 0 {
		fmt.Fprintf(&b, "not found: %s (see %slist_tables)\n", strings.Join(out.Missing, ", "), mcpToolPrefix)
	}

	for _, t := range out.Tables {
		fmt.Fprintf(&b, "\n# %s", t.Name)

		if t.Kind != "table" {
			b.WriteString(" [" + t.Kind + "]")
		}

		if t.Rows != nil {
			fmt.Fprintf(&b, " (~%d rows)", *t.Rows)
		}

		b.WriteString("\n")

		if t.Comment != "" {
			b.WriteString(t.Comment + "\n")
		}

		refs := map[string]string{}

		for _, fk := range t.ForeignKeys {
			for i, c := range fk.Columns {
				if i < len(fk.RefColumns) {
					refs[c] = fk.RefTable + "." + fk.RefColumns[i]
				}
			}
		}

		b.WriteString("columns:\n")

		for _, c := range t.Columns {
			fmt.Fprintf(&b, "  %s %s", c.Name, c.Type)

			if c.PrimaryKey {
				b.WriteString(" PK")
			}

			if !c.Nullable {
				b.WriteString(" NOT NULL")
			}

			if c.Extra != "" {
				b.WriteString(" " + c.Extra)
			}

			if c.Default != "" {
				b.WriteString(" DEFAULT " + c.Default)
			}

			if r := refs[c.Name]; r != "" {
				b.WriteString(" -> " + r)
			}

			if c.Comment != "" {
				b.WriteString("  -- " + strings.ReplaceAll(c.Comment, "\n", " "))
			}

			b.WriteString("\n")
		}

		var uniques []string

		for _, ix := range t.Indexes {
			kind := "index"

			switch {
			case ix.Primary:
				kind = "primary key"
			case ix.Unique:
				kind = "unique"
				uniques = append(uniques, "("+strings.Join(ix.Columns, ", ")+")")
			}

			fmt.Fprintf(&b, "%s %s (%s)\n", kind, ix.Name, strings.Join(ix.Columns, ", "))
		}

		if len(uniques) > 0 {
			b.WriteString("unique keys (upsert targets): " + strings.Join(uniques, ", ") + "\n")
		}

		for _, fk := range t.ForeignKeys {
			fmt.Fprintf(&b, "references: %s", join(t.Name, fk.Columns, fk.RefTable, fk.RefColumns))

			if fk.OnDelete != "" && fk.OnDelete != "NO ACTION" {
				b.WriteString("  (on delete " + fk.OnDelete + ")")
			}

			b.WriteString("\n")
		}

		for _, r := range t.ReferencedBy {
			fmt.Fprintf(&b, "referenced by: %s\n", join(t.Name, r.RefColumns, r.Table, r.Columns))
		}

		for _, a := range out.Anomalies {
			if a.Table == t.Name && a.Kind == engine.AnomalyImplied {
				fmt.Fprintf(&b, "probably references (no foreign key): %s\n", join(t.Name, a.Columns, a.Ref, a.RefCols))
			}
		}

		for _, tr := range out.Triggers[t.Name] {
			fmt.Fprintf(&b, "trigger %s %s %s", tr.Name, tr.Timing, tr.Event)

			if !tr.Enabled {
				b.WriteString(" (disabled)")
			}

			b.WriteString("\n")
		}

		if t.Definition != "" {
			b.WriteString("definition:\n" + t.Definition + "\n")
		}

		var notes []string

		for _, a := range out.Anomalies {
			if a.Table != t.Name || a.Kind == engine.AnomalyImplied {
				continue
			}

			n := a.Kind
			if len(a.Columns) > 0 {
				n += "(" + strings.Join(a.Columns, ", ") + ")"
			}

			notes = append(notes, n)
		}

		if len(notes) > 0 {
			b.WriteString("design notes: " + strings.Join(notes, "; ") + "\n")
		}
	}

	if len(out.Related) > 0 {
		b.WriteString("\n# related tables\n")

		for _, t := range out.Related {
			cols := make([]string, len(t.Columns))

			for i, c := range t.Columns {
				cols[i] = c.Name + " " + c.Type
				if c.PrimaryKey {
					cols[i] += " PK"
				}
			}

			fmt.Fprintf(&b, "%s(%s)\n", t.Name, strings.Join(cols, ", "))
		}
	}

	return b.String()
}

func docFreshness(at int64, status docStatus) string {
	text := "snapshot generated: " + time.UnixMilli(at).UTC().Format(time.RFC3339) + " (not live; refresh in the documentation UI)\n"
	if status.LastError != "" {
		text += "last refresh failed; showing last successful snapshot\n"
	}
	if status.Refreshing {
		text += "refresh in progress\n"
	}
	return text
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
	// ConnectionID limits the token to one connection.
	ConnectionID string `json:"connection_id"`
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
		"base_url":      s.publicBase(r),
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

	if req.ConnectionID != "" {
		if _, err := s.store.GetGrant(r.Context(), currentUser(r), req.ConnectionID); err != nil {
			writeError(w, http.StatusBadRequest, "you have no access to this connection")

			return
		}
	}

	t := &store.AccessToken{UserID: currentUser(r).ID, Name: req.Name, TokenHash: hash, ReadOnly: req.ReadOnly, ConnectionID: req.ConnectionID}
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
	user := currentUser(r)

	// Revoking a signed-in client also forgets its approval, so it has to
	// ask again.
	if t, err := s.store.GetAccessToken(r.Context(), user.ID, r.PathValue("id")); err == nil && t.ClientID != "" {
		_ = s.store.DeleteOAuthConsent(r.Context(), user.ID, t.ClientID)
	}

	if err := s.store.DeleteAccessToken(r.Context(), user.ID, r.PathValue("id")); err != nil {
		writeStoreError(w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}
