package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/store"
)

// ////////////////////////////////////////////
// tool types

type mcpConnection struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	DBType      string `json:"db_type" jsonschema:"database type, which decides the SQL dialect"`
	Database    string `json:"database,omitempty"`
	// Access is what you may do there: read, write (data) or ddl (data and
	// schema).
	Access     store.MCPMode `json:"access" jsonschema:"read, write (change data) or ddl (change data and schema)"`
	Writable   bool          `json:"writable"`
	Production bool          `json:"production,omitempty"`
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
	connectionIn
	Schema string `json:"schema,omitempty" jsonschema:"schema (or database) to list; omit for the first one dataport_list_schemas returns"`
}

type listTablesOut struct {
	Connection string          `json:"connection"`
	Schema     string          `json:"schema"`
	Tables     []engine.Object `json:"tables"`
}

type describeTableIn struct {
	connectionIn
	Schema string `json:"schema,omitempty" jsonschema:"schema of the table; omit for the default schema"`
	Table  string `json:"table" jsonschema:"table or view name"`
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
	connectionIn
	Schema string   `json:"schema,omitempty" jsonschema:"schema to describe; omit for the default schema"`
	Tables []string `json:"tables,omitempty" jsonschema:"describe only these tables; omit for every table up to the server cap"`
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
	connectionIn
	Schema string `json:"schema,omitempty" jsonschema:"schema to analyse; omit for the default schema"`
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
	connectionIn
	Schema string   `json:"schema,omitempty" jsonschema:"schema of the tables; omit for the default schema"`
	Tables []string `json:"tables" jsonschema:"tables or views to document, at most 20"`
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
	connectionIn
	SQL     string `json:"sql" jsonschema:"one or more read-only statements (SELECT, WITH, SHOW, EXPLAIN) separated by semicolons"`
	MaxRows int    `json:"max_rows,omitempty" jsonschema:"rows to return per statement; may be lower than the server cap, never higher"`
}

type explainIn struct {
	connectionIn
	SQL     string `json:"sql" jsonschema:"one statement to explain, without EXPLAIN"`
	Analyze bool   `json:"analyze,omitempty" jsonschema:"run the statement to measure the plan; always rolled back, and only for reads unless the connection allows writes"`
	Verbose bool   `json:"verbose,omitempty" jsonschema:"more detail per plan step where the database has it"`
}

type explainOut struct {
	Connection string    `json:"connection"`
	SQL        string    `json:"sql"`
	Analyzed   bool      `json:"analyzed"`
	Plan       mcpResult `json:"plan"`
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

func (s *Server) registerMCPTools(server *mcp.Server, readOnly, notebooks bool) {
	closed := false
	destructive := true

	ro := func(title string) *mcp.ToolAnnotations {
		return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closed}
	}

	mcp.AddTool(server, &mcp.Tool{
		Name:        mcpToolPrefix + "list_connections",
		Annotations: ro("List connections"),
		Description: "List the databases the user lets you use: name, database type (the SQL dialect), your access " +
			"(read, write data, or ddl to also change the schema) and whether it is a production database. " +
			"Call this first; every other tool takes a connection name.",
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
		if len(in.Tables) == 0 || len(in.Tables) > maxTableDocs {
			return nil, tableDocsOut{}, fmt.Errorf("pass 1 to %d tables", maxTableDocs)
		}

		g, _, caller, schema, err := s.mcpDocGrant(ctx, req, in.Connection, in.Schema)
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
		g, _, caller, schema, err := s.mcpDocGrant(ctx, req, in.Connection, in.Schema)
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

	mcp.AddTool(server, &mcp.Tool{
		Name: mcpToolPrefix + "explain",
		Annotations: &mcp.ToolAnnotations{
			Title: "Show the execution plan of a statement", ReadOnlyHint: true, OpenWorldHint: &closed,
		},
		Description: "Show how the database would run one statement: the plan with its estimated costs. With analyze the statement " +
			"runs to measure real times and rows, and is rolled back; prefer the estimate unless timings are needed.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in explainIn) (*mcp.CallToolResult, explainOut, error) {
		return s.mcpExplain(ctx, req, in)
	})

	if notebooks {
		s.registerNotebookReadTools(server)
	}

	// A tool that can never succeed only makes the model try it; read-only
	// tokens do not get one. The engine still enforces the grant either way.
	if readOnly {
		return
	}

	if notebooks {
		s.registerNotebookWriteTools(server)
	}
	s.registerGenerateTool(server)

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
