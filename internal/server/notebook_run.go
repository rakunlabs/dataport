package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/notebook"
	"github.com/rakunlabs/dataport/internal/store"
)

// Run history keeps a run alive while its worker heartbeats, and deletes
// finished runs after the retention window.
const (
	notebookRunStaleAfter = 2 * time.Minute
	notebookRunRetention  = 14 * 24 * time.Hour
)

func (s *Server) expireNotebookRuns(ctx context.Context) error {
	now := time.Now()
	return s.store.MaintainNotebookRuns(ctx, now.Add(-notebookRunStaleAfter).UnixMilli(), now.Add(-notebookRunRetention).UnixMilli())
}

// ownNotebookRun returns a run of the notebook started by the user; admins
// may reach every run. Other runs are not found.
func (s *Server) ownNotebookRun(ctx context.Context, user *store.User, n *store.Notebook, id string) (*store.NotebookRun, error) {
	run, err := s.store.GetNotebookRun(ctx, strings.TrimSpace(id))
	if err != nil {
		return nil, err
	}
	if run.NotebookID != n.ID || run.UserID != user.ID && !user.IsAdmin() {
		return nil, store.ErrNotFound
	}
	return run, nil
}

type notebookRunRequest struct {
	Version    int            `json:"version"`
	Cell       string         `json:"cell"`
	Data       map[string]any `json:"data"`
	Preview    bool           `json:"preview"`
	Background bool           `json:"background"`
	// A confirmation binds the exact SQL, bound values and connection.
	Confirmations map[string]string `json:"confirmations"`
}

type notebookCellResult struct {
	Path         string             `json:"path"`
	Status       string             `json:"status"`
	Error        string             `json:"error,omitempty"`
	DurationMS   int64              `json:"duration_ms"`
	Result       *engine.ExecResult `json:"result,omitempty"`
	SQL          string             `json:"sql,omitempty"`
	Args         []any              `json:"args,omitempty"`
	Confirmation string             `json:"confirmation,omitempty"`
}

type notebookResult struct {
	ID         string               `json:"id"`
	Status     string               `json:"status"`
	Error      string               `json:"error,omitempty"`
	Cells      []notebookCellResult `json:"cells"`
	DurationMS int64                `json:"duration_ms"`
}

func confirmationDigest(connection, sql string, args []any) string {
	b, _ := json.Marshal([]any{connection, sql, args})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (s *Server) runNotebook(w http.ResponseWriter, r *http.Request) {
	n := s.notebookFor(w, r, "run")
	if n == nil {
		return
	}
	var req notebookRunRequest
	if !readJSON(w, r, &req) {
		return
	}
	if req.Version != n.Version {
		writeError(w, http.StatusConflict, "notebook changed; reload before running")
		return
	}
	s.startNotebookRun(w, r, n, n.Document, n.Version, req, nil)
}

// Published endpoints accept bearer tokens only, not ambient browser cookies.
// GET is always read-only; POST preserves token and grant restrictions. Writes
// to production are refused (there is no interactive confirmation here).
func (s *Server) publishedNotebook(w http.ResponseWriter, r *http.Request) {
	caller, status, msg := s.mcpAuthenticate(r)
	if caller == nil {
		writeError(w, status, msg)
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, caller.user))
	if !s.notebooksAllowed(r.Context(), caller.user) {
		writeError(w, http.StatusForbidden, errNotebooksOff.Error())
		return
	}
	n, err := s.store.GetNotebook(r.Context(), caller.user, r.PathValue("path"), true)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeStoreError(w, err)
		return
	}
	if err != nil || n.Published == nil {
		writeError(w, http.StatusNotFound, "published notebook not found")
		return
	}
	if n.Permission == "view" {
		writeError(w, http.StatusForbidden, "notebook permission denied")
		return
	}
	data := map[string]any{}
	if r.Method == http.MethodPost {
		if !readJSON(w, r, &data) {
			return
		}
	} else {
		for k, values := range r.URL.Query() {
			if len(values) != 1 {
				writeError(w, http.StatusBadRequest, "duplicate query parameter")
				return
			}
			var p *notebook.Parameter
			for i := range n.Published.Parameters {
				if n.Published.Parameters[i].Name == k {
					p = &n.Published.Parameters[i]
					break
				}
			}
			var v any = values[0]
			if p != nil && p.Type != "string" {
				if err := json.Unmarshal([]byte(values[0]), &v); err != nil {
					writeError(w, http.StatusBadRequest, "invalid query parameter "+k)
					return
				}
			}
			data[k] = v
		}
	}
	s.startNotebookRun(w, r, n, *n.Published, n.PublishedVersion, notebookRunRequest{Cell: r.PathValue("cell"), Data: data}, caller)
}

func (s *Server) startNotebookRun(w http.ResponseWriter, r *http.Request, n *store.Notebook, doc notebook.Document, version int, req notebookRunRequest, caller *mcpCaller) {
	policy := runPolicy{source: "notebook"}
	if caller != nil {
		// Published endpoints cannot ask anyone to confirm a production
		// write, and GET never changes data.
		policy = runPolicy{caller: caller, readOnly: caller.token.ReadOnly || r.Method == http.MethodGet, noProductionWrites: true, source: "endpoint"}
	}

	out, run, err := s.beginNotebookRun(r.Context(), currentUser(r), n, doc, version, req, policy)
	if err != nil {
		writeNotebookError(w, err)
		return
	}

	if out == nil {
		writeJSON(w, http.StatusAccepted, run)

		return
	}

	status := http.StatusOK
	if out.Status == "failed" {
		status = http.StatusUnprocessableEntity
	}
	if out.Status == "confirmation_required" {
		status = http.StatusConflict
	}
	writeJSON(w, status, out)
}

// runPolicy says who asks for a run and what they may do with it.
type runPolicy struct {
	// caller is set for bearer-token callers (published endpoints and MCP):
	// its connection scope applies and the token is rechecked between cells.
	caller *mcpCaller
	// readOnly refuses plans with write or transfer cells.
	readOnly bool
	// noProductionWrites refuses writes on production connections, for
	// callers that cannot show the user the SQL to confirm.
	noProductionWrites bool
	source             string
}

// runFailure is a refusal before anything ran, with its HTTP status.
type runFailure struct {
	status int
	msg    string
}

func (e *runFailure) Error() string { return e.msg }

// takeNotebookSlot counts a run in, unless max runs are going already.
func (s *Server) takeNotebookSlot(max int) bool {
	s.notebookMu.Lock()
	defer s.notebookMu.Unlock()
	if s.notebookRunning >= max {
		return false
	}
	s.notebookRunning++
	return true
}

func (s *Server) releaseNotebookSlot() {
	s.notebookMu.Lock()
	s.notebookRunning--
	s.notebookMu.Unlock()
}

func refuse(status int, msg string) error { return &runFailure{status: status, msg: msg} }

// beginNotebookRun checks a plan against the caller's access and runs it. A
// foreground run returns its result; a background run returns the run
// record at once and keeps going after ctx ends.
func (s *Server) beginNotebookRun(ctx context.Context, user *store.User, n *store.Notebook, doc notebook.Document, version int, req notebookRunRequest, p runPolicy) (*notebookResult, *store.NotebookRun, error) {
	data, err := doc.Inputs(req.Data)
	if err != nil {
		return nil, nil, refuse(http.StatusBadRequest, err.Error())
	}
	req.Data = data
	plan, err := doc.Plan(req.Cell, true)
	if err != nil {
		return nil, nil, refuse(http.StatusBadRequest, err.Error())
	}
	if len(plan) == 0 {
		return nil, nil, refuse(http.StatusBadRequest, "no enabled cells")
	}
	// Resolve the entire plan before the first SQL statement. Repeat before
	// every cell too, so background work does not retain revoked access.
	for _, c := range plan {
		if _, err := s.cellAccess(ctx, user, c, p, req.Preview); err != nil {
			return nil, nil, err
		}
	}
	if req.Preview && req.Background {
		return nil, nil, refuse(http.StatusBadRequest, "preview cannot run in background")
	}
	limits := s.notebookCfg()
	if !s.takeNotebookSlot(limits.MaxConcurrent) {
		return nil, nil, refuse(http.StatusTooManyRequests, "too many notebook runs; try again later")
	}
	run := &store.NotebookRun{NotebookID: n.ID, UserID: user.ID, Version: version, Source: p.source}
	if err := s.store.CreateNotebookRun(ctx, run); err != nil {
		s.releaseNotebookSlot()
		return nil, nil, err
	}
	if req.Background {
		ctx = context.WithoutCancel(ctx)
	}
	// Bound whole-notebook resource use even where query timeouts are disabled.
	ctx, cancel := context.WithTimeout(ctx, limits.MaxDuration)
	stopShutdown := context.AfterFunc(s.notebookContext, cancel)
	s.notebookWorkers.Add(1)
	work := func() notebookResult {
		defer s.notebookWorkers.Done()
		defer s.releaseNotebookSlot()
		defer stopShutdown()
		defer cancel()
		return s.executeNotebook(ctx, user, n, plan, req, run, p)
	}
	if req.Background {
		go work()
		return nil, run, nil
	}
	out := work()
	return &out, run, nil
}

// cellGrants are the connections one cell runs on.
type cellGrants struct {
	src         *store.Grant
	srcPassword string
	// dst and dstPassword are set for transfer cells.
	dst         *store.Grant
	dstPassword string
}

// needsConfirmation reports whether a cell writes to a production database
// and so runs only with the user's confirmation of the exact SQL. Transfers
// only read their source, and never write to production targets.
func needsConfirmation(c notebook.Cell, g cellGrants) bool {
	return c.Write && c.Transfer == nil && g.src.Connection.Production
}

// cellAccess checks one cell against the user's grants and the run policy,
// before a run and again before the cell runs. A preview renders write
// cells without running them, so it needs neither write access nor a
// production confirmation.
func (s *Server) cellAccess(ctx context.Context, user *store.User, c notebook.Cell, p runPolicy, preview bool) (cellGrants, error) {
	var g cellGrants
	var err error
	if p.caller != nil && p.caller.scope != "" && (p.caller.scope != c.ConnectionID || c.Transfer != nil && p.caller.scope != c.Transfer.ConnectionID) {
		return g, refuse(http.StatusForbidden, "notebook uses a connection outside this token's scope")
	}
	if g.src, g.srcPassword, err = s.resolveNotebookGrant(ctx, user, c.ConnectionID); err != nil {
		return g, refuse(http.StatusForbidden, "cell "+c.Path+": connection unavailable or database sign-in required")
	}
	if g.src.Connection.HideNotebooks {
		return g, refuse(http.StatusForbidden, "cell "+c.Path+": connection is not available in notebooks")
	}
	if c.Write && c.Transfer == nil && g.src.ReadOnly {
		return g, refuse(http.StatusForbidden, "cell "+c.Path+": connection is read-only")
	}
	if c.Write && p.readOnly && !preview {
		return g, refuse(http.StatusForbidden, "cell "+c.Path+" writes, and this run may only read")
	}
	if p.noProductionWrites && needsConfirmation(c, g) && !preview {
		return g, refuse(http.StatusForbidden, "cell "+c.Path+" writes to a production database, which needs a confirmation this caller cannot give")
	}
	if p.source == "mcp" && p.caller != nil {
		if err := s.mcpCellAccess(ctx, p.caller, c, g.src, false); err != nil {
			return g, err
		}
	}
	if c.Transfer != nil {
		g.dst, g.dstPassword, err = s.resolveNotebookGrant(ctx, user, c.Transfer.ConnectionID)
		if err != nil || g.dst.ReadOnly || g.dst.Connection.HideNotebooks {
			return g, refuse(http.StatusForbidden, "cell "+c.Path+": transfer target unavailable or read-only")
		}
		if p.source == "mcp" && p.caller != nil {
			if err := s.mcpCellAccess(ctx, p.caller, c, g.dst, true); err != nil {
				return g, err
			}
		}
		// An unattended transfer never accepts a blanket production flag.
		// Production transfer approval belongs to the existing transfer UI.
		if g.dst.Connection.Production {
			return g, refuse(http.StatusForbidden, "cell "+c.Path+": use the connection transfer tool for production targets")
		}
	}
	return g, nil
}

// mcpCellAccess applies the user's MCP policy to a cell an agent runs. Every
// connection a cell touches must be open to agents; a writing cell needs a
// mode that allows its statements, and a transfer target needs write.
func (s *Server) mcpCellAccess(ctx context.Context, caller *mcpCaller, c notebook.Cell, g *store.Grant, target bool) error {
	if g.Connection.HideMCP {
		return refuse(http.StatusForbidden, "cell "+c.Path+": connection "+g.Connection.Name+" is not available to agents")
	}
	mode, err := s.mcpMode(ctx, caller, g)
	if err != nil {
		return err
	}
	switch {
	case mode == store.MCPOff:
		err = mcpAllows(mode, g, "")
	case target && !mode.AtLeast(store.MCPWrite):
		err = mcpAllows(store.MCPRead, g, "")
	case !target && c.Write && c.Transfer == nil:
		err = mcpAllows(mode, g, c.SQL)
	}
	if err != nil {
		return refuse(http.StatusForbidden, "cell "+c.Path+": "+err.Error())
	}
	return nil
}

func (s *Server) notebookRuns(w http.ResponseWriter, r *http.Request) {
	n := s.notebookFor(w, r, "view")
	if n == nil {
		return
	}
	// Expire abandoned jobs on reads as well as on server maintenance.
	if err := s.expireNotebookRuns(r.Context()); err != nil {
		slog.Warn("expire notebook runs", "error", err)
	}
	userID := currentUser(r).ID
	if currentUser(r).IsAdmin() {
		userID = ""
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit = min(100, max(1, limit))
	if r.URL.Query().Get("limit") == "" {
		limit = 25
	}
	list, err := s.store.ListNotebookRuns(r.Context(), n.ID, userID, limit, max(0, offset))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// notebookRunAction shows a run to whoever may view the notebook's runs,
// and cancels it for whoever may run the notebook; either way only the
// user's own runs, or every run for an admin.
func (s *Server) notebookRunAction(w http.ResponseWriter, r *http.Request) {
	permission := "view"
	if r.Method == http.MethodPost {
		permission = "run"
	}
	n := s.notebookFor(w, r, permission)
	if n == nil {
		return
	}
	run, err := s.ownNotebookRun(r.Context(), currentUser(r), n, r.PathValue("run"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if r.Method == http.MethodPost {
		if err := s.store.CancelNotebookRun(r.Context(), run.ID); err != nil {
			writeStoreError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) maintainNotebookRuns(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := s.expireNotebookRuns(ctx); err != nil && ctx.Err() == nil {
			slog.Error("notebook maintenance", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
