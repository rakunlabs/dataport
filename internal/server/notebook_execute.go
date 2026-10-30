package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/notebook"
	"github.com/rakunlabs/dataport/internal/store"
)

func notebookPublicError(user *store.User, err error) string {
	var ce *engine.ConnError
	if errors.As(err, &ce) && !user.IsAdmin() {
		return "cannot reach the database, contact your administrator"
	}
	if errors.Is(err, errSecret) {
		return "connection secret cannot be read"
	}
	return err.Error()
}

func (s *Server) executeNotebook(ctx context.Context, user *store.User, n *store.Notebook, plan []notebook.Cell, req notebookRunRequest, run *store.NotebookRun, p runPolicy) notebookResult {
	caller := p.caller
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				check, done := context.WithTimeout(context.Background(), 5*time.Second)
				ok, err := s.store.HeartbeatNotebookRun(check, run.ID)
				done()
				// A store error may be transient; a stale run is expired by
				// maintenance and then fences this worker out.
				if err != nil {
					slog.Warn("notebook heartbeat", "run", run.ID, "error", err)
					continue
				}
				if !ok {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { close(stop); <-stopped }()
	start := time.Now()
	out := notebookResult{ID: run.ID, Status: "completed", Cells: []notebookCellResult{}}
	values := map[string]any{}
	outputBytes := 0
	for _, cell := range plan {
		cr := notebookCellResult{Path: cell.Path, Status: "completed"}
		cellStart := time.Now()
		var result *engine.ExecResult
		err := func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			fresh, err := s.store.GetUser(ctx, user.ID)
			if err != nil || fresh.Disabled {
				return errors.New("user is disabled or removed")
			}
			if !s.notebooksAllowed(ctx, fresh) {
				return errNotebooksOff
			}
			if caller != nil {
				// Recheck authentication between cells. Revocation does not
				// retroactively undo a cell already running on the database.
				if caller.token.ExpiresAt > 0 && time.Now().UnixMilli() >= caller.token.ExpiresAt {
					return errors.New("access token expired")
				}
				t, err := s.store.GetAccessTokenByHash(ctx, caller.token.TokenHash)
				if err != nil || t.ID != caller.token.ID {
					return errors.New("access token revoked")
				}
			}
			current, err := s.store.GetNotebook(ctx, fresh, n.ID, false)
			if err != nil {
				return errors.New("notebook access revoked")
			}
			if current.Permission == "view" {
				return errors.New("notebook execution permission revoked")
			}
			access, err := s.cellAccess(ctx, fresh, cell, p, req.Preview)
			if err != nil {
				return err
			}
			g, password := access.src, access.srcPassword
			d, err := engine.DriverFor(g.Connection)
			if err != nil {
				return err
			}
			sql := cell.SQL
			var args []any
			if cell.Template {
				sql, args, err = notebook.RenderContext(ctx, sql, req.Data, values, func(i int) string { return engine.Placeholder(d, i) })
				if err != nil {
					return err
				}
			}
			if _, err := engine.Validate(d, sql, !cell.Write || cell.Transfer != nil || g.ReadOnly); err != nil {
				return err
			}
			if cell.Transfer != nil {
				dst := access.dst
				config, _ := json.Marshal(cell.Transfer)
				cr.Confirmation = confirmationDigest(cell.ConnectionID, sql+string(config), args)
				if req.Preview {
					cr.Status = "preview"
					cr.SQL = sql + "\n-- Transfer target: " + dst.Connection.Name + " / " + cell.Transfer.Schema + " / " + cell.Transfer.Table + "\n-- Settings: " + string(config)
					cr.Args = args
					return nil
				}
				tr, transferErr := s.engine.QueryTransfer(ctx, g.Connection, password, dst.Connection, access.dstPassword, engine.QueryTransferRequest{
					SQL: sql, Args: args, Target: *cell.Transfer,
					SourceTimeout: s.runTimeout(g.Connection, cell.TimeoutSec), Timeout: s.runTimeout(dst.Connection, cell.TimeoutSec),
				})
				var rows, ms int64
				if tr != nil {
					rows, ms = tr.Inserted, tr.DurationMS
				}
				auditCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
				defer done()
				s.log(auditCtx, fresh, g.Connection, engine.Redact(sql), transferErr, rows, ms, run.Source)
				s.log(auditCtx, fresh, dst.Connection, "-- query transfer into "+cell.Transfer.Schema+"."+cell.Transfer.Table, transferErr, rows, ms, run.Source)
				if transferErr != nil {
					return transferErr
				}
				result = &engine.ExecResult{Committed: true, Transactional: true, DurationMS: ms, Results: []*engine.ResultSet{{Kind: "exec", RowsAffected: rows, DurationMS: ms, Statement: "-- transferred rows; skipped: " + strconv.FormatInt(tr.Skipped, 10)}}}
				return nil
			}
			if cell.Write {
				cr.Confirmation = confirmationDigest(cell.ConnectionID, sql, args)
				// UPDATE or DELETE without WHERE asks on every connection,
				// as a production write does; the exact SQL confirmed for it
				// lets it run. The rendered SQL is what is checked.
				production := needsConfirmation(cell, access)
				everyRow := len(engine.Unbounded(d, sql)) > 0
				cr.NeedsConfirmation = production || everyRow
				if req.Preview {
					cr.Status = "preview"
					cr.SQL, cr.Args = sql, args
					return nil
				}
				if cr.NeedsConfirmation && req.Confirmations[cell.Path] != cr.Confirmation {
					cr.Status = "confirmation_required"
					cr.SQL, cr.Args = sql, args
					if production {
						return errors.New("confirm the exact SQL and parameters before running this production cell")
					}
					return errors.New("UPDATE or DELETE without WHERE changes every row; confirm the exact SQL before running this cell")
				}
			}
			result, err = s.execute(ctx, fresh, g, password, execInput{SQL: sql, Args: args, QueryID: run.ID + ":" + cell.ID, Confirm: cell.Write, ReadOnly: !cell.Write, Rows: cell.MaxRows, MaxRows: 10000, TimeoutSec: cell.TimeoutSec, Source: run.Source})
			if err != nil {
				return err
			}
			if msg := result.FirstError(); msg != "" {
				return errors.New(msg)
			}
			// Only the final row result is exposed to dependent cells. Typed
			// values are preserved; output visibility is independent of this.
			rows := []map[string]any{}
			for _, rs := range result.Results {
				if rs.Kind == "rows" {
					rows = []map[string]any{}
					for _, row := range rs.Rows {
						m := map[string]any{}
						for i, col := range rs.Columns {
							if i < len(row) {
								m[col.Name] = row[i]
							}
						}
						rows = append(rows, m)
					}
				}
			}
			values[cell.Path] = rows
			return nil
		}()
		cr.DurationMS = time.Since(cellStart).Milliseconds()
		if cell.Output {
			cr.Result = result
		}
		if result != nil {
			encoded, encodeErr := json.Marshal(result)
			outputBytes += len(encoded)
			if encodeErr != nil || outputBytes > s.notebookCfg().MaxOutputBytes {
				err = errors.New("notebook result budget exceeded; lower row limits or narrow the SQL")
				cr.Result = nil
			}
		}
		if err != nil {
			cr.Error = notebookPublicError(user, err)
			if cr.Status != "confirmation_required" {
				cr.Status = "failed"
			}
			out.Status, out.Error = cr.Status, cr.Error
			if ctx.Err() != nil {
				out.Status, cr.Status = "cancelled", "cancelled"
			}
		}
		out.Cells = append(out.Cells, cr)
		// Persist progress without rows, SQL or bound parameter values.
		meta := notebookRunMetadata(out)
		logCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
		if err := s.store.UpdateNotebookRun(logCtx, run.ID, "running", meta); err != nil {
			slog.Error("save notebook progress", "error", err)
		}
		done()
		if err != nil {
			break
		}
	}
	out.DurationMS = time.Since(start).Milliseconds()
	if req.Preview && out.Status == "completed" {
		out.Status = "preview"
	}
	finish, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	if err := s.store.UpdateNotebookRun(finish, run.ID, out.Status, notebookRunMetadata(out)); err != nil {
		slog.Error("finish notebook run", "error", err)
	}
	return out
}

func notebookRunMetadata(out notebookResult) notebookResult {
	meta := out
	if meta.Error != "" {
		meta.Error = "run failed; database details are available in the query log"
	}
	meta.Cells = make([]notebookCellResult, len(out.Cells))
	for i, c := range out.Cells {
		msg := ""
		if c.Error != "" {
			msg = "cell failed; inspect the foreground response or query log"
		}
		meta.Cells[i] = notebookCellResult{Path: c.Path, Status: c.Status, Error: msg, DurationMS: c.DurationMS}
	}
	return meta
}
