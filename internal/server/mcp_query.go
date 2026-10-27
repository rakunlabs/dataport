package server

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rakunlabs/dataport/internal/engine"
)

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

	if !readOnly {
		mode, err := s.mcpMode(ctx, caller, g)
		if err != nil {
			return nil, queryOut{}, err
		}

		if err := mcpAllows(mode, g, sqlText); err != nil {
			return nil, queryOut{}, err
		}
	}

	rows := s.mcpCfg().MaxRows
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
		out.Results = append(out.Results, toMCPResult(rs, s.mcpCfg().MaxCellChars))
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

		if connErr.Auth && connErr.UserCredentials {
			return errors.New("the database refused the account; the user has to sign in again in dataport")
		}

		return errors.New("cannot reach the database, contact your administrator")
	}

	return err
}
