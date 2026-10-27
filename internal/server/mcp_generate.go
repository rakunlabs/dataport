package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/store"
)

type generateRowsIn struct {
	connectionIn
	Schema  string             `json:"schema,omitempty" jsonschema:"schema of the table; omit for the default schema"`
	Table   string             `json:"table" jsonschema:"table to fill with made-up rows"`
	Rows    int                `json:"rows" jsonschema:"number of rows to insert, at most 1000000; 0 only previews"`
	Columns []engine.GenColumn `json:"columns,omitempty" jsonschema:"generators for some columns; the others are guessed from their name and type. generator is one of the names dataport returns in generators"`
	Seed    int64              `json:"seed,omitempty" jsonschema:"repeat the same values with the same seed"`
	Truncate bool `json:"truncate,omitempty" jsonschema:"delete every row of the table first; only when the user asked"`
}

type generateRowsOut struct {
	Connection string             `json:"connection"`
	Table      string             `json:"table"`
	Inserted   int64              `json:"inserted"`
	Seed       int64              `json:"seed"`
	Columns    []engine.GenColumn `json:"columns"`
	Sample     [][]any            `json:"sample"`
	Generators []string           `json:"generators"`
	DurationMS int64              `json:"duration_ms"`
}

func (s *Server) registerGenerateTool(server *mcp.Server) {
	closed := false
	destructive := true

	mcp.AddTool(server, &mcp.Tool{
		Name: mcpToolPrefix + "generate_rows",
		Annotations: &mcp.ToolAnnotations{
			Title: "Fill a table with test data", DestructiveHint: &destructive, OpenWorldHint: &closed,
		},
		Description: "Insert made-up rows (names, emails, addresses, dates, numbers…) into a table for testing. Each column gets a generator " +
			"guessed from its name and type; override some with columns. Foreign keys pick existing values of the referenced table, " +
			"identity and computed columns and those with a default are left to the database. Call with rows 0 first to see the " +
			"generators and sample rows without writing. Special generators: skip, null, constant and list (values), sequence (min is the start), " +
			"fk (ref), template (a mugo template; .row holds the other values of the row, .n its number, fake \"email\" makes a value, " +
			"randInt and oneOf pick). The rows go in one transaction where the database has them. Only fill tables the user asked for.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in generateRowsIn) (*mcp.CallToolResult, generateRowsOut, error) {
		return s.mcpGenerate(ctx, req, in)
	})
}

func (s *Server) mcpGenerate(ctx context.Context, req *mcp.CallToolRequest, in generateRowsIn) (*mcp.CallToolResult, generateRowsOut, error) {
	if strings.TrimSpace(in.Table) == "" {
		return nil, generateRowsOut{}, errors.New("table is required")
	}

	if in.Rows < 0 || in.Rows > engine.MaxGenerateRows {
		return nil, generateRowsOut{}, fmt.Errorf("rows must be between 0 and %d", engine.MaxGenerateRows)
	}

	g, password, caller, err := s.mcpGrant(ctx, req, in.Connection)
	if err != nil {
		return nil, generateRowsOut{}, err
	}

	if in.Schema, err = s.mcpSchema(ctx, g, password, in.Schema); err != nil {
		return nil, generateRowsOut{}, err
	}

	ereq := engine.GenerateRequest{
		Schema: in.Schema, Table: in.Table, Columns: in.Columns, Rows: max(in.Rows, 1), Seed: in.Seed,
		Truncate: in.Truncate, Timeout: s.runTimeout(g.Connection, nil),
	}

	plan, err := s.engine.GeneratePreview(ctx, g.Connection, password, ereq)
	if err != nil {
		return nil, generateRowsOut{}, mcpEngineError(err)
	}

	out := generateRowsOut{
		Connection: g.Connection.Name, Table: in.Table, Seed: plan.Seed,
		Columns: plan.Columns, Sample: plan.Rows[:min(len(plan.Rows), 5)], Generators: plan.Generators,
	}

	if in.Rows == 0 {
		return nil, out, nil
	}

	mode, err := s.mcpMode(ctx, caller, g)
	if err != nil {
		return nil, generateRowsOut{}, err
	}

	// Inserting rows changes data, never the schema: the write mode is enough.
	if mode == store.MCPRead {
		return nil, generateRowsOut{}, mcpAllows(mode, g, "")
	}

	// The seed of the preview makes the rows the user is shown.
	ereq.Seed, ereq.Rows = plan.Seed, in.Rows
	summary := generateSummary(generateIn{Schema: in.Schema, Table: in.Table, Columns: plan.Columns, Rows: in.Rows, Seed: plan.Seed, Truncate: in.Truncate})

	if g.Connection.Production {
		if err := confirmProduction(ctx, req, g.Connection, summary, engine.RiskWrite); err != nil {
			return nil, generateRowsOut{}, err
		}
	}

	start := time.Now()
	n, err := s.engine.Generate(ctx, g.Connection, password, ereq, func(engine.GenerateProgress) {})
	out.Inserted, out.DurationMS = n, time.Since(start).Milliseconds()

	s.log(ctx, caller.user, g.Connection, summary, err, n, out.DurationMS, "mcp")

	if err != nil {
		return nil, generateRowsOut{}, mcpEngineError(err)
	}

	slog.InfoContext(ctx, "mcp generate", "user", caller.user.Username, "connection", g.Connection.Name, "table", in.Table, "rows", n)

	return nil, out, nil
}
