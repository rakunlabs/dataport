package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rakunlabs/dataport/internal/notebook"
	"github.com/rakunlabs/dataport/internal/store"
)

func (s *Server) mcpRunNotebook(ctx context.Context, req *mcp.CallToolRequest, in runNotebookIn) (*mcp.CallToolResult, runNotebookOut, error) {
	caller, n, err := s.mcpNotebook(ctx, req, in.Notebook, "run")
	if err != nil {
		return nil, runNotebookOut{}, err
	}

	doc, version := n.Document, n.Version

	if in.Published {
		if n.Published == nil {
			return nil, runNotebookOut{}, fmt.Errorf("notebook %q is not published", n.Path)
		}

		doc, version = *n.Published, n.PublishedVersion
	} else if in.Version > 0 && in.Version != n.Version {
		return nil, runNotebookOut{}, fmt.Errorf("notebook %q is now at version %d, not %d; call dataport_get_notebook", n.Path, n.Version, in.Version)
	}

	if in.Preview && in.Background {
		return nil, runNotebookOut{}, errors.New("preview cannot run in the background")
	}

	run := notebookRunRequest{Version: version, Cell: in.Cell, Data: in.Inputs, Preview: in.Preview, Background: in.Background}
	if run.Data == nil {
		run.Data = map[string]any{}
	}

	policy := runPolicy{caller: caller, readOnly: caller.token.ReadOnly, noProductionWrites: !canElicit(req), source: "mcp"}

	// Production writes need the user's confirmation of the exact SQL and
	// values. A preview renders them without running any write; the real
	// run then only proceeds for the same SQL and values.
	if !in.Preview && !policy.readOnly && !policy.noProductionWrites {
		prod, err := s.planWritesProduction(ctx, caller.user, doc, in.Cell, policy)
		if err != nil {
			return nil, runNotebookOut{}, mcpNotebookError(err)
		}

		if prod {
			preview := run
			preview.Preview, preview.Background = true, false

			out, _, err := s.beginNotebookRun(ctx, caller.user, n, doc, version, preview, policy)
			if err != nil {
				return nil, runNotebookOut{}, mcpNotebookError(err)
			}

			if out.Status != "preview" {
				res, mout := s.mcpRunResult(n, out, in.MaxRows)

				return res, mout, nil
			}

			if run.Confirmations, err = s.confirmNotebookWrites(ctx, req, caller.user, n, doc, out, policy); err != nil {
				return nil, runNotebookOut{}, err
			}
		}
	}

	out, record, err := s.beginNotebookRun(ctx, caller.user, n, doc, version, run, policy)
	if err != nil {
		return nil, runNotebookOut{}, mcpNotebookError(err)
	}

	if out == nil {
		return nil, runNotebookOut{RunID: record.ID, Notebook: n.Path, Status: "running", Cells: []mcpCellRun{}}, nil
	}

	res, mout := s.mcpRunResult(n, out, in.MaxRows)

	return res, mout, nil
}

// planWritesProduction reports whether a run would write to a production
// connection.
func (s *Server) planWritesProduction(ctx context.Context, user *store.User, doc notebook.Document, cell string, p runPolicy) (bool, error) {
	plan, err := doc.Plan(cell, true)
	if err != nil {
		return false, err
	}

	for _, c := range plan {
		g, err := s.cellAccess(ctx, user, c, p, false)
		if err != nil {
			return false, err
		}

		if needsConfirmation(c, g) || changesEveryRow(c, g) {
			return true, nil
		}
	}

	return false, nil
}

// confirmNotebookWrites shows the user every production write of a preview
// and returns the confirmations of the ones they accepted.
func (s *Server) confirmNotebookWrites(ctx context.Context, req *mcp.CallToolRequest, user *store.User, n *store.Notebook, doc notebook.Document, preview *notebookResult, p runPolicy) (map[string]string, error) {
	cells := map[string]notebook.Cell{}
	for _, c := range doc.Cells {
		cells[c.Path] = c
	}

	var (
		b             strings.Builder
		confirmations = map[string]string{}
	)

	for _, c := range preview.Cells {
		if c.Confirmation == "" {
			continue
		}

		cell := cells[c.Path]

		g, err := s.cellAccess(ctx, user, cell, p, false)
		if err != nil || !c.NeedsConfirmation {
			continue
		}

		why := "production database"
		if !needsConfirmation(cell, g) {
			why = "UPDATE or DELETE without WHERE, changes every row"
		}

		confirmations[c.Path] = c.Confirmation
		fmt.Fprintf(&b, "\n\ncell %s on %q (%s):\n%s", c.Path, g.src.Connection.Name, why, c.SQL)

		if len(c.Args) > 0 {
			args, _ := json.Marshal(c.Args)
			fmt.Fprintf(&b, "\nvalues: %s", args)
		}
	}

	shown := b.String()
	if len(shown) > 6000 {
		shown = shown[:6000] + "\n…"
	}

	if len(confirmations) == 0 {
		return confirmations, nil
	}

	msg := fmt.Sprintf("Notebook %q has writes that need your confirmation. Run these statements?%s", n.Name, shown)
	if err := elicitConfirmation(ctx, req, msg, "Run these statements"); err != nil {
		return nil, err
	}

	return confirmations, nil
}

func (s *Server) mcpRunResult(n *store.Notebook, out *notebookResult, maxRows int) (*mcp.CallToolResult, runNotebookOut) {
	rows := s.mcpCfg().MaxRows
	if maxRows > 0 && (rows <= 0 || maxRows < rows) {
		rows = maxRows
	}

	mout := runNotebookOut{RunID: out.ID, Notebook: n.Path, Status: out.Status, Error: out.Error, DurationMS: out.DurationMS, Cells: []mcpCellRun{}}

	for _, c := range out.Cells {
		cr := mcpCellRun{Path: c.Path, Status: c.Status, Error: c.Error, DurationMS: c.DurationMS, SQL: c.SQL, Args: c.Args}

		if c.Result != nil {
			cr.Committed = c.Result.Committed

			for _, rs := range c.Result.Results {
				capped := *rs
				if rows > 0 && len(capped.Rows) > rows {
					capped.Rows, capped.Truncated = capped.Rows[:rows], true
				}

				cr.Results = append(cr.Results, toMCPResult(&capped, s.mcpCfg().MaxCellChars))
			}
		}

		mout.Cells = append(mout.Cells, cr)
	}

	switch out.Status {
	case "failed", "cancelled", "confirmation_required":
		msg := "notebook run " + out.Status
		if out.Error != "" {
			msg += ": " + out.Error
		}

		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}, mout
	}

	return nil, mout
}

// mcpOwnRun returns a run of the notebook started by the caller; admins may
// reach every run.
func (s *Server) mcpOwnRun(ctx context.Context, caller *mcpCaller, n *store.Notebook, id string) (*store.NotebookRun, error) {
	run, err := s.ownNotebookRun(ctx, caller.user, n, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("unknown run %q for notebook %q", id, n.Path)
	}

	return run, err
}

func toMCPRunInfo(run *store.NotebookRun) mcpRunInfo {
	out := mcpRunInfo{ID: run.ID, Status: run.Status, Version: run.Version, Source: run.Source, CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt}

	var info notebookResult
	if err := json.Unmarshal(run.Info, &info); err == nil {
		out.Error, out.DurationMS = info.Error, info.DurationMS

		for _, c := range info.Cells {
			out.Cells = append(out.Cells, mcpCellRun{Path: c.Path, Status: c.Status, Error: c.Error, DurationMS: c.DurationMS})
		}
	}

	if run.CancelRequested && run.Status == "running" {
		out.Status = "stopping"
	}

	return out
}
