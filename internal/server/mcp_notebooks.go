package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rakunlabs/dataport/internal/store"
)

// Notebook tools let an agent manage the notebooks of its user: read, write
// and run them, follow background runs, publish and share. They act exactly
// as the user does in the workspace: notebook permissions, database grants,
// the token's scope and read-only flag, and production confirmation apply.

// ////////////////////////////////////////////
// tool types

type mcpNotebookRef struct {
	Notebook string `json:"notebook" jsonschema:"notebook id or path, from dataport_list_notebooks"`
}

type mcpNotebookSummary struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Path             string `json:"path"`
	Permission       string `json:"permission" jsonschema:"your permission: view, run, edit or owner"`
	Version          int    `json:"version" jsonschema:"saved draft version; pass it back when saving or running"`
	PublishedVersion int    `json:"published_version,omitempty" jsonschema:"version served by the endpoint; absent when unpublished"`
	Endpoint         string `json:"endpoint,omitempty" jsonschema:"path of the published HTTP endpoint"`
	UpdatedAt        int64  `json:"updated_at"`
}

type mcpMapping struct {
	Source   string `json:"source" jsonschema:"column of the source query"`
	Target   string `json:"target" jsonschema:"column of the target table"`
	Type     string `json:"type,omitempty" jsonschema:"convert to string, number or date; omit to keep the value"`
	Nullable bool   `json:"nullable,omitempty" jsonschema:"allow NULL"`
	Template string `json:"template,omitempty" jsonschema:"value template with mugo functions, the value at '.', for example {{ . | trim | upper }}"`
	Encoding string `json:"encoding,omitempty" jsonschema:"ISO-8859-1 to decode bytes; omit for UTF-8"`
}

type mcpTransfer struct {
	Connection   string       `json:"connection" jsonschema:"target connection name; writable, not production, and different from the source"`
	Schema       string       `json:"schema,omitempty"`
	Table        string       `json:"table" jsonschema:"existing target table"`
	Wipe         bool         `json:"wipe,omitempty" jsonschema:"DELETE the target rows first, in the same transaction"`
	Batch        int          `json:"batch,omitempty" jsonschema:"rows per insert, 1-500; default 100"`
	SkipError    string       `json:"skip_error,omitempty" jsonschema:"skip rows whose insert error contains this text (PostgreSQL, MySQL, SQLite)"`
	KeepUnmapped bool         `json:"keep_unmapped,omitempty" jsonschema:"also copy source columns without a mapping, under their own name"`
	Mappings     []mcpMapping `json:"mappings,omitempty" jsonschema:"omit to copy every source column under its own name"`
}

type mcpCell struct {
	ID           string       `json:"id,omitempty" jsonschema:"keep a cell's id when updating it; omit for a new cell"`
	Path         string       `json:"path" jsonschema:"unique cell name: a letter, then letters, digits, _ or -; other cells read its rows as .cells.<path>"`
	Description  string       `json:"description,omitempty"`
	Connection   string       `json:"connection,omitempty" jsonschema:"connection name from dataport_list_connections; may be omitted on an endpoint for one connection"`
	SQL          string       `json:"sql"`
	Enabled      *bool        `json:"enabled,omitempty" jsonschema:"default true; disabled cells are skipped"`
	Output       *bool        `json:"output,omitempty" jsonschema:"return the rows; default true"`
	Collapsed    bool         `json:"collapsed,omitempty"`
	Template     bool         `json:"template,omitempty" jsonschema:"render the SQL as a template: bind values with {{ param .data.x }} and read dependencies as .cells.<path>"`
	Write        bool         `json:"write,omitempty" jsonschema:"the cell changes data; cells read only by default"`
	MaxRows      int          `json:"max_rows,omitempty" jsonschema:"rows per statement: 0 the connection default, -1 the notebook maximum"`
	TimeoutSec   *int         `json:"timeout_sec,omitempty"`
	Dependencies []string     `json:"dependencies,omitempty" jsonschema:"paths of read-only cells to run first"`
	Transfer     *mcpTransfer `json:"transfer,omitempty" jsonschema:"stream this cell's query result into a table; needs write"`
}

type mcpParameter struct {
	Name     string `json:"name"`
	Type     string `json:"type" jsonschema:"string, number, boolean or json"`
	Required bool   `json:"required,omitempty"`
	Default  any    `json:"default,omitempty"`
}

type mcpShare struct {
	User       string `json:"user,omitempty" jsonschema:"username; give user or group"`
	Group      string `json:"group,omitempty" jsonschema:"group name"`
	Permission string `json:"permission" jsonschema:"view, run (view and run) or edit (view, run and save)"`
}

type mcpNotebook struct {
	mcpNotebookSummary
	Cells      []mcpCell      `json:"cells"`
	Parameters []mcpParameter `json:"parameters"`
	SharedWith []mcpShare     `json:"shared_with,omitempty" jsonschema:"only shown to the owner"`
}

type listNotebooksOut struct {
	Notebooks []mcpNotebookSummary `json:"notebooks"`
}

type saveNotebookIn struct {
	Notebook   string         `json:"notebook,omitempty" jsonschema:"id or path of the notebook to update; omit to create one"`
	Name       string         `json:"name"`
	Path       string         `json:"path" jsonschema:"notebook path, also the endpoint path: a letter, then letters, digits, _ or -"`
	Version    int            `json:"version,omitempty" jsonschema:"version from dataport_get_notebook; required when updating, so you never overwrite someone else's change"`
	Cells      []mcpCell      `json:"cells" jsonschema:"every cell, in order; this replaces the whole document"`
	Parameters []mcpParameter `json:"parameters,omitempty"`
}

type runNotebookIn struct {
	Notebook   string         `json:"notebook" jsonschema:"notebook id or path"`
	Cell       string         `json:"cell,omitempty" jsonschema:"run only this cell and its dependencies; omit for every enabled cell"`
	Inputs     map[string]any `json:"inputs,omitempty" jsonschema:"values of the notebook parameters"`
	Version    int            `json:"version,omitempty" jsonschema:"refuse to run if the saved draft is no longer this version"`
	Published  bool           `json:"published,omitempty" jsonschema:"run the published snapshot instead of the saved draft"`
	Preview    bool           `json:"preview,omitempty" jsonschema:"run the read-only cells and show the rendered SQL of write cells without running them"`
	Background bool           `json:"background,omitempty" jsonschema:"return a run id at once; follow it with dataport_notebook_runs. Background results are not kept"`
	MaxRows    int            `json:"max_rows,omitempty" jsonschema:"rows to return per statement; may be lower than the server cap, never higher"`
}

type mcpCellRun struct {
	Path       string      `json:"path"`
	Status     string      `json:"status"`
	Error      string      `json:"error,omitempty"`
	DurationMS int64       `json:"duration_ms"`
	Committed  bool        `json:"committed,omitempty" jsonschema:"the cell's changes were saved"`
	Results    []mcpResult `json:"results,omitempty"`
	SQL        string      `json:"sql,omitempty" jsonschema:"rendered SQL of a write cell in a preview"`
	Args       []any       `json:"args,omitempty" jsonschema:"values bound to the rendered SQL"`
}

type runNotebookOut struct {
	RunID      string       `json:"run_id"`
	Notebook   string       `json:"notebook"`
	Status     string       `json:"status" jsonschema:"completed, failed, cancelled, preview or running (background)"`
	Error      string       `json:"error,omitempty"`
	DurationMS int64        `json:"duration_ms"`
	Cells      []mcpCellRun `json:"cells"`
}

type notebookRunsIn struct {
	Notebook string `json:"notebook"`
	Run      string `json:"run,omitempty" jsonschema:"one run id; omit for the latest runs"`
	Limit    int    `json:"limit,omitempty" jsonschema:"runs to list, at most 50; default 10"`
}

type mcpRunInfo struct {
	ID         string       `json:"id"`
	Status     string       `json:"status"`
	Version    int          `json:"version"`
	Source     string       `json:"source"`
	Error      string       `json:"error,omitempty"`
	DurationMS int64        `json:"duration_ms,omitempty"`
	Cells      []mcpCellRun `json:"cells,omitempty"`
	CreatedAt  int64        `json:"created_at"`
	UpdatedAt  int64        `json:"updated_at"`
}

type notebookRunsOut struct {
	Runs []mcpRunInfo `json:"runs"`
}

type cancelRunIn struct {
	Notebook string `json:"notebook"`
	Run      string `json:"run" jsonschema:"run id"`
}

type publishNotebookIn struct {
	Notebook string `json:"notebook"`
	Version  int    `json:"version" jsonschema:"saved version to publish, from dataport_get_notebook"`
	Publish  *bool  `json:"publish,omitempty" jsonschema:"false takes the endpoint down; default true"`
}

type shareNotebookIn struct {
	Notebook string     `json:"notebook"`
	Shares   []mcpShare `json:"shares" jsonschema:"who may use the notebook besides its owner; this replaces the list, an empty list unshares"`
}

type shareNotebookOut struct {
	Shares []mcpShare `json:"shares"`
}

type deleteNotebookOut struct {
	Deleted string `json:"deleted"`
}

type cancelRunOut struct {
	Cancelled string `json:"cancelled"`
}

// ////////////////////////////////////////////
// tools

func (s *Server) registerNotebookReadTools(server *mcp.Server) {
	closed := false

	mcp.AddTool(server, &mcp.Tool{
		Name:        mcpToolPrefix + "list_notebooks",
		Annotations: &mcp.ToolAnnotations{Title: "List notebooks", ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closed},
		Description: "List the notebooks you own or that are shared with you: SQL cells across connections that run in order, " +
			"pass rows to each other and can be published as an HTTP endpoint. Shows your permission and versions.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listNotebooksOut, error) {
		caller, err := callerOf(req)
		if err != nil {
			return nil, listNotebooksOut{}, err
		}

		list, err := s.store.ListNotebooks(ctx, caller.user)
		if err != nil {
			return nil, listNotebooksOut{}, err
		}

		out := listNotebooksOut{Notebooks: []mcpNotebookSummary{}}

		for _, n := range list {
			if caller.scope != "" {
				// The listing carries no document; a scoped caller only
				// sees notebooks that stay within its connection.
				full, err := s.store.GetNotebook(ctx, caller.user, n.ID, false)
				if err != nil || !notebookInScope(full, caller.scope) {
					continue
				}
			}

			out.Notebooks = append(out.Notebooks, s.notebookSummary(n))
		}

		return nil, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        mcpToolPrefix + "get_notebook",
		Annotations: &mcp.ToolAnnotations{Title: "Read a notebook", ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closed},
		Description: "Return a notebook's saved draft: its cells (connection, SQL, flags, dependencies, transfer settings), " +
			"parameters and version. Read it before changing or running a notebook.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in mcpNotebookRef) (*mcp.CallToolResult, mcpNotebook, error) {
		caller, n, err := s.mcpNotebook(ctx, req, in.Notebook, "view")
		if err != nil {
			return nil, mcpNotebook{}, err
		}

		out, err := s.toMCPNotebook(ctx, caller, n)

		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        mcpToolPrefix + "run_notebook",
		Annotations: &mcp.ToolAnnotations{Title: "Run a notebook", OpenWorldHint: &closed},
		Description: "Run a saved notebook, or one cell with its dependencies, with values for its parameters. Each cell is its own " +
			"transaction: when a later cell fails, earlier changes stay. Write cells need a writable grant and token; on production " +
			"connections the user is asked to confirm the exact SQL and values first. Use preview to see what write cells would run.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in runNotebookIn) (*mcp.CallToolResult, runNotebookOut, error) {
		return s.mcpRunNotebook(ctx, req, in)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        mcpToolPrefix + "notebook_runs",
		Annotations: &mcp.ToolAnnotations{Title: "Notebook runs", ReadOnlyHint: true, OpenWorldHint: &closed},
		Description: "Show your recent runs of a notebook, or one run, with the status and timing of each cell. " +
			"Use it to follow background runs. Runs keep no rows or input values.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in notebookRunsIn) (*mcp.CallToolResult, notebookRunsOut, error) {
		caller, n, err := s.mcpNotebook(ctx, req, in.Notebook, "view")
		if err != nil {
			return nil, notebookRunsOut{}, err
		}

		out := notebookRunsOut{Runs: []mcpRunInfo{}}

		if in.Run != "" {
			run, err := s.mcpOwnRun(ctx, caller, n, in.Run)
			if err != nil {
				return nil, notebookRunsOut{}, err
			}

			out.Runs = append(out.Runs, toMCPRunInfo(run))

			return nil, out, nil
		}

		limit := in.Limit
		if limit <= 0 {
			limit = 10
		}

		userID := caller.user.ID
		if caller.user.IsAdmin() {
			userID = ""
		}

		runs, err := s.store.ListNotebookRuns(ctx, n.ID, userID, min(limit, 50), 0)
		if err != nil {
			return nil, notebookRunsOut{}, err
		}

		for _, run := range runs {
			out.Runs = append(out.Runs, toMCPRunInfo(run))
		}

		return nil, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        mcpToolPrefix + "cancel_notebook_run",
		Annotations: &mcp.ToolAnnotations{Title: "Stop a notebook run", IdempotentHint: true, OpenWorldHint: &closed},
		Description: "Stop one of your running notebook runs. Cells that already finished keep their changes.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in cancelRunIn) (*mcp.CallToolResult, cancelRunOut, error) {
		caller, n, err := s.mcpNotebook(ctx, req, in.Notebook, "run")
		if err != nil {
			return nil, cancelRunOut{}, err
		}

		run, err := s.mcpOwnRun(ctx, caller, n, in.Run)
		if err != nil {
			return nil, cancelRunOut{}, err
		}

		if run.Status != "running" {
			return nil, cancelRunOut{}, fmt.Errorf("run %s is already %s", run.ID, run.Status)
		}

		if err := s.store.CancelNotebookRun(ctx, run.ID); err != nil {
			return nil, cancelRunOut{}, err
		}

		return nil, cancelRunOut{Cancelled: run.ID}, nil
	})
}

func (s *Server) registerNotebookWriteTools(server *mcp.Server) {
	closed := false
	destructive := true
	notDestructive := false

	mcp.AddTool(server, &mcp.Tool{
		Name:        mcpToolPrefix + "save_notebook",
		Annotations: &mcp.ToolAnnotations{Title: "Create or update a notebook", DestructiveHint: &notDestructive, OpenWorldHint: &closed},
		Description: "Create a notebook, or replace the saved draft of one you may edit. Send every cell in order. Updates need the " +
			"version from dataport_get_notebook and fail if someone saved since. Saving never changes a published endpoint until it is published again. " +
			"In template cells bind every value with {{ param ... }}, never quoted or printed into the SQL; mugo functions shape values first, " +
			"as in {{ .data.name | trim | upper | param }}.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in saveNotebookIn) (*mcp.CallToolResult, mcpNotebook, error) {
		caller, err := callerOf(req)
		if err != nil {
			return nil, mcpNotebook{}, err
		}

		var existing *store.Notebook

		if in.Notebook != "" {
			if _, existing, err = s.mcpNotebook(ctx, req, in.Notebook, "edit"); err != nil {
				return nil, mcpNotebook{}, err
			}

			if in.Version == 0 {
				return nil, mcpNotebook{}, errors.New("version is required when updating; call dataport_get_notebook")
			}
		}

		doc, err := s.fromMCPDocument(ctx, caller, existing, in.Cells, in.Parameters)
		if err != nil {
			return nil, mcpNotebook{}, err
		}

		n, err := s.storeNotebook(ctx, caller.user, existing, saveNotebookRequest{Name: in.Name, Path: in.Path, Document: doc, Version: in.Version})
		if err != nil {
			return nil, mcpNotebook{}, mcpNotebookError(err)
		}

		out, err := s.toMCPNotebook(ctx, caller, n)

		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        mcpToolPrefix + "publish_notebook",
		Annotations: &mcp.ToolAnnotations{Title: "Publish a notebook", IdempotentHint: true, OpenWorldHint: &closed},
		Description: "Serve a saved version of a notebook you own at its HTTP endpoint, or take the endpoint down. Callers of the " +
			"endpoint use their own access token and database grants. Only publish when the user asked for it.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in publishNotebookIn) (*mcp.CallToolResult, mcpNotebookSummary, error) {
		caller, n, err := s.mcpNotebook(ctx, req, in.Notebook, "owner")
		if err != nil {
			return nil, mcpNotebookSummary{}, err
		}

		n, err = s.setPublished(ctx, caller.user, n, in.Version, in.Publish == nil || *in.Publish)
		if err != nil {
			return nil, mcpNotebookSummary{}, mcpNotebookError(err)
		}

		return nil, s.notebookSummary(n), nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        mcpToolPrefix + "share_notebook",
		Annotations: &mcp.ToolAnnotations{Title: "Share a notebook", IdempotentHint: true, OpenWorldHint: &closed},
		Description: "Set who may view, run or edit a notebook you own, by username or group name. This replaces the whole list. " +
			"Sharing never grants database access: people need their own grants to run it.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in shareNotebookIn) (*mcp.CallToolResult, shareNotebookOut, error) {
		_, n, err := s.mcpNotebook(ctx, req, in.Notebook, "owner")
		if err != nil {
			return nil, shareNotebookOut{}, err
		}

		access, err := s.resolveShares(ctx, in.Shares)
		if err != nil {
			return nil, shareNotebookOut{}, err
		}

		if err := s.setNotebookAccess(ctx, n, access); err != nil {
			return nil, shareNotebookOut{}, mcpNotebookError(err)
		}

		shares, err := s.notebookShares(ctx, n)

		return nil, shareNotebookOut{Shares: shares}, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        mcpToolPrefix + "delete_notebook",
		Annotations: &mcp.ToolAnnotations{Title: "Delete a notebook", DestructiveHint: &destructive, OpenWorldHint: &closed},
		Description: "Delete a notebook you own with its endpoint and run history. It cannot be undone; only delete when the user asked for it.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in mcpNotebookRef) (*mcp.CallToolResult, deleteNotebookOut, error) {
		_, n, err := s.mcpNotebook(ctx, req, in.Notebook, "owner")
		if err != nil {
			return nil, deleteNotebookOut{}, err
		}

		if err := s.store.DeleteNotebook(ctx, n.ID); err != nil {
			return nil, deleteNotebookOut{}, err
		}

		return nil, deleteNotebookOut{Deleted: n.Path}, nil
	})
}
