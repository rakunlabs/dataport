package cluster

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/store"
)

// The methods below shadow those of engine.Manager: the ones on a pool run
// on its owner, the others reach every replica where state is kept per
// replica, such as open transactions.

func (r *Router) Schemas(ctx context.Context, c *store.Connection, password string) (out []string, err error) {
	res, err := r.do(ctx, c, password, &callRequest{Op: opSchemas}, func(ctx context.Context) error {
		out, err = r.Manager.Schemas(ctx, c, password)

		return err
	}, nil)
	if res != nil {
		out = res.Strings
	}

	return out, err
}

func (r *Router) Objects(ctx context.Context, c *store.Connection, password, schema string) (out []engine.Object, err error) {
	res, err := r.do(ctx, c, password, &callRequest{Op: opObjects, Schema: schema}, func(ctx context.Context) error {
		out, err = r.Manager.Objects(ctx, c, password, schema)

		return err
	}, nil)
	if res != nil {
		out = res.Objects
	}

	return out, err
}

func (r *Router) Columns(ctx context.Context, c *store.Connection, password, schema, table string) (out []engine.ColumnInfo, err error) {
	res, err := r.do(ctx, c, password, &callRequest{Op: opColumns, Schema: schema, Table: table}, func(ctx context.Context) error {
		out, err = r.Manager.Columns(ctx, c, password, schema, table)

		return err
	}, nil)
	if res != nil {
		out = res.Columns
	}

	return out, err
}

func (r *Router) ResolveTable(ctx context.Context, c *store.Connection, password, table string) (schema string, ok bool, err error) {
	res, err := r.do(ctx, c, password, &callRequest{Op: opResolveTable, Table: table}, func(ctx context.Context) error {
		schema, ok, err = r.Manager.ResolveTable(ctx, c, password, table)

		return err
	}, nil)
	if res != nil {
		schema, ok = res.Text, res.OK
	}

	return schema, ok, err
}

func (r *Router) Structure(ctx context.Context, c *store.Connection, password, schema, table string) (out *engine.TableStructure, err error) {
	res, err := r.do(ctx, c, password, &callRequest{Op: opStructure, Schema: schema, Table: table}, func(ctx context.Context) error {
		out, err = r.Manager.Structure(ctx, c, password, schema, table)

		return err
	}, nil)
	if res != nil {
		out = res.Structure
	}

	return out, err
}

func (r *Router) Routines(ctx context.Context, c *store.Connection, password, schema string) (out []engine.Routine, err error) {
	res, err := r.do(ctx, c, password, &callRequest{Op: opRoutines, Schema: schema}, func(ctx context.Context) error {
		out, err = r.Manager.Routines(ctx, c, password, schema)

		return err
	}, nil)
	if res != nil {
		out = res.Routines
	}

	return out, err
}

func (r *Router) Definition(ctx context.Context, c *store.Connection, password, schema string, rt engine.Routine) (out string, err error) {
	res, err := r.do(ctx, c, password, &callRequest{Op: opDefinition, Schema: schema, Routine: rt}, func(ctx context.Context) error {
		out, err = r.Manager.Definition(ctx, c, password, schema, rt)

		return err
	}, nil)
	if res != nil {
		out = res.Text
	}

	return out, err
}

func (r *Router) Diagram(ctx context.Context, c *store.Connection, password, schema string) (out *engine.Diagram, err error) {
	res, err := r.do(ctx, c, password, &callRequest{Op: opDiagram, Schema: schema}, func(ctx context.Context) error {
		out, err = r.Manager.Diagram(ctx, c, password, schema)

		return err
	}, nil)
	if res != nil {
		out = res.Diagram
	}

	return out, err
}

func (r *Router) SchemaDoc(ctx context.Context, c *store.Connection, password, schema string) (out *engine.SchemaDoc, err error) {
	res, err := r.do(ctx, c, password, &callRequest{Op: opSchemaDoc, Schema: schema}, func(ctx context.Context) error {
		out, err = r.Manager.SchemaDoc(ctx, c, password, schema)

		return err
	}, nil)
	if res != nil {
		out = res.SchemaDoc
	}

	return out, err
}

func (r *Router) Execute(ctx context.Context, c *store.Connection, password string, req engine.ExecRequest) (out *engine.ExecResult, err error) {
	res, err := r.do(ctx, c, password, &callRequest{Op: opExecute, Exec: req}, func(ctx context.Context) error {
		out, err = r.Manager.Execute(ctx, c, password, req)

		return err
	}, nil)
	if res != nil {
		out = res.Exec
	}

	return out, err
}

func (r *Router) Explain(ctx context.Context, c *store.Connection, password string, req engine.ExplainRequest) (out *engine.ExplainResult, err error) {
	res, err := r.do(ctx, c, password, &callRequest{Op: opExplain, Explain: req}, func(ctx context.Context) error {
		out, err = r.Manager.Explain(ctx, c, password, req)

		return err
	}, nil)
	if res != nil {
		out = res.Explain
	}

	return out, err
}

func (r *Router) Count(ctx context.Context, c *store.Connection, password string, req engine.CountRequest) (out *engine.CountResult, err error) {
	res, err := r.do(ctx, c, password, &callRequest{Op: opCount, Count: req}, func(ctx context.Context) error {
		out, err = r.Manager.Count(ctx, c, password, req)

		return err
	}, nil)
	if res != nil {
		out = res.CountResult
	}

	return out, err
}

func (r *Router) PreviewEdits(ctx context.Context, c *store.Connection, password string, req engine.EditRequest) (out []string, err error) {
	res, err := r.do(ctx, c, password, &callRequest{Op: opPreviewEdits, Edit: req}, func(ctx context.Context) error {
		out, err = r.Manager.PreviewEdits(ctx, c, password, req)

		return err
	}, nil)
	if res != nil {
		out = res.Strings
	}

	return out, err
}

func (r *Router) ApplyEdits(ctx context.Context, c *store.Connection, password string, req engine.EditRequest) (out *engine.EditResult, err error) {
	res, err := r.do(ctx, c, password, &callRequest{Op: opApplyEdits, Edit: req}, func(ctx context.Context) error {
		out, err = r.Manager.ApplyEdits(ctx, c, password, req)

		return err
	}, nil)
	if res != nil {
		out = res.EditResult
	}

	return out, err
}

func (r *Router) Processes(ctx context.Context, c *store.Connection, password string) (out *engine.ProcessList, err error) {
	res, err := r.do(ctx, c, password, &callRequest{Op: opProcesses}, func(ctx context.Context) error {
		out, err = r.Manager.Processes(ctx, c, password)

		return err
	}, nil)
	if res != nil {
		out = res.Processes
	}

	return out, err
}

func (r *Router) KillProcess(ctx context.Context, c *store.Connection, password, id string, terminate bool) (out string, err error) {
	res, err := r.do(ctx, c, password, &callRequest{Op: opKillProcess, Process: id, Terminate: terminate}, func(ctx context.Context) error {
		out, err = r.Manager.KillProcess(ctx, c, password, id, terminate)

		return err
	}, nil)
	if res != nil {
		out = res.Text
	}

	return out, err
}

func (r *Router) Users(ctx context.Context, c *store.Connection, password string) (out []engine.DBUser, err error) {
	res, err := r.do(ctx, c, password, &callRequest{Op: opUsers}, func(ctx context.Context) error {
		out, err = r.Manager.Users(ctx, c, password)

		return err
	}, nil)
	if res != nil {
		out = res.Users
	}

	return out, err
}

func (r *Router) Grants(ctx context.Context, c *store.Connection, password, name, host string) (out []string, err error) {
	res, err := r.do(ctx, c, password, &callRequest{Op: opGrants, Name: name, Host: host}, func(ctx context.Context) error {
		out, err = r.Manager.Grants(ctx, c, password, name, host)

		return err
	}, nil)
	if res != nil {
		out = res.Strings
	}

	return out, err
}

func (r *Router) Import(ctx context.Context, c *store.Connection, password string, req engine.ImportRequest) (out *engine.ImportResult, err error) {
	res, err := r.do(ctx, c, password, &callRequest{Op: opImport, Import: req}, func(ctx context.Context) error {
		out, err = r.Manager.Import(ctx, c, password, req)

		return err
	}, nil)
	if res != nil {
		out = res.Import
	}

	return out, err
}

// Export streams the file from the owner of the pool into w.
func (r *Router) Export(ctx context.Context, c *store.Connection, password string, req engine.ExportRequest, w io.Writer) (out *engine.ExportStats, err error) {
	res, err := r.do(ctx, c, password, &callRequest{Op: opExport, Export: req}, func(ctx context.Context) error {
		out, err = r.Manager.Export(ctx, c, password, req, w)

		return err
	}, func(kind byte, payload []byte) error {
		if kind != frameData {
			return nil
		}

		_, err := w.Write(payload)

		return err
	})
	if res != nil {
		out = res.ExportStats
		if out == nil && err != nil {
			out = &engine.ExportStats{}
		}
	}

	return out, err
}

func (r *Router) GeneratePreview(ctx context.Context, c *store.Connection, password string, req engine.GenerateRequest) (out *engine.GeneratePlan, err error) {
	res, err := r.do(ctx, c, password, &callRequest{Op: opGenPreview, Generate: req}, func(ctx context.Context) error {
		out, err = r.Manager.GeneratePreview(ctx, c, password, req)

		return err
	}, nil)
	if res != nil {
		out = res.Plan
	}

	return out, err
}

func (r *Router) Generate(ctx context.Context, c *store.Connection, password string, req engine.GenerateRequest, progress func(engine.GenerateProgress)) (out int64, err error) {
	res, err := r.do(ctx, c, password, &callRequest{Op: opGenerate, Generate: req}, func(ctx context.Context) error {
		out, err = r.Manager.Generate(ctx, c, password, req, progress)

		return err
	}, func(kind byte, payload []byte) error {
		var p engine.GenerateProgress
		if kind == frameProgress && decode(payload, &p) == nil {
			progress(p)
		}

		return nil
	})
	if res != nil {
		out = res.Count
	}

	return out, err
}

// Transfer runs on the owner of the target pool, which writes; the rows
// come from the owner of the source.
func (r *Router) Transfer(ctx context.Context, src *store.Connection, srcPassword string, dst *store.Connection, dstPassword string,
	req engine.TransferRequest, progress func(engine.TransferProgress),
) error {
	secret, err := r.cipher.Encrypt(srcPassword)
	if err != nil {
		return err
	}

	srcCopy := *src

	_, err = r.do(ctx, dst, dstPassword, &callRequest{Op: opTransfer, Src: &srcCopy, SrcSecret: secret, Transfer: req}, func(ctx context.Context) error {
		return r.TransferFrom(ctx, src, r.tableSource(src, srcPassword, req.Timeout), dst, dstPassword, req, progress)
	}, func(kind byte, payload []byte) error {
		var p engine.TransferProgress
		if kind == frameProgress && decode(payload, &p) == nil {
			progress(p)
		}

		return nil
	})

	return err
}

// QueryTransfer runs on the owner of the target pool, reading the query
// from the owner of the source.
func (r *Router) QueryTransfer(ctx context.Context, src *store.Connection, srcPassword string, dst *store.Connection, dstPassword string,
	req engine.QueryTransferRequest,
) (out *engine.QueryTransferResult, err error) {
	secret, err := r.cipher.Encrypt(srcPassword)
	if err != nil {
		return nil, err
	}

	srcCopy := *src

	res, err := r.do(ctx, dst, dstPassword, &callRequest{Op: opQueryTransfer, Src: &srcCopy, SrcSecret: secret, QueryTransfer: req}, func(ctx context.Context) error {
		out, err = r.QueryTransferFrom(ctx, src, r.querySource(src, srcPassword, req), dst, dstPassword, req)

		return err
	}, nil)
	if res != nil {
		out = res.QueryTransfer
	}

	return out, err
}

// tableSource reads the tables of src where its pool is owned.
func (r *Router) tableSource(src *store.Connection, password string, timeout time.Duration) engine.Source {
	return func(ctx context.Context, fn func(ctx context.Context, s engine.TableSource) error) error {
		key := engine.PoolKey(src)

		addr, done, err := r.owner(ctx, key)
		if err != nil {
			return err
		}

		if addr == nil {
			defer done()

			return r.LocalSource(src, password, timeout)(ctx, fn)
		}

		d, err := engine.DriverFor(src)
		if err != nil {
			return err
		}

		return fn(ctx, &remoteSource{r: r, addr: addr, c: src, password: password, d: d, timeout: timeout})
	}
}

// remoteSource reads the tables of a transfer from another replica, one
// request per table.
type remoteSource struct {
	r        *Router
	addr     *net.UDPAddr
	c        *store.Connection
	password string
	d        engine.Driver
	timeout  time.Duration
}

func (s *remoteSource) Driver() engine.Driver { return s.d }

func (s *remoteSource) Columns(ctx context.Context, schema, table string) ([]engine.ColumnInfo, error) {
	return s.r.Columns(ctx, s.c, s.password, schema, table)
}

func (s *remoteSource) Stream(ctx context.Context, schema, table string, columns func([]engine.Column) error, fn engine.RowFunc) error {
	secret, err := s.r.cipher.Encrypt(s.password)
	if err != nil {
		return err
	}

	req := &callRequest{Op: opStream, Key: engine.PoolKey(s.c), Conn: *s.c, Secret: secret, Schema: schema, Table: table,
		Transfer: engine.TransferRequest{Timeout: s.timeout}}

	_, err = s.r.remote(ctx, s.addr, req, func(kind byte, payload []byte) error {
		switch kind {
		case frameColumns:
			var cols []engine.Column
			if err := decode(payload, &cols); err != nil {
				return err
			}

			return columns(cols)
		case frameRows:
			var rows [][]any
			if err := decode(payload, &rows); err != nil {
				return err
			}

			for _, row := range rows {
				if err := fn(row); err != nil {
					return err
				}
			}
		}

		return nil
	})

	return err
}

// querySource runs the query of a query transfer where the source pool is
// owned.
func (r *Router) querySource(src *store.Connection, password string, req engine.QueryTransferRequest) engine.QueryRows {
	return func(ctx context.Context, fn func(ctx context.Context, targets []string, rows func(engine.RowFunc) error) error) error {
		key := engine.PoolKey(src)

		addr, done, err := r.owner(ctx, key)
		if err != nil {
			return err
		}

		if addr == nil {
			defer done()

			return r.QuerySource(src, password, req)(ctx, fn)
		}

		secret, err := r.cipher.Encrypt(password)
		if err != nil {
			return err
		}

		c, err := r.start(ctx, addr, &callRequest{Op: opQuerySource, Key: key, Conn: *src, Secret: secret, QueryTransfer: req})
		if err != nil {
			return err
		}
		defer c.close()

		kind, payload, err := c.next()
		if err != nil {
			return err
		}

		if kind == frameFinal {
			if err := c.res.Err.decode(); err != nil {
				return err
			}

			return errors.New("the source query returned no columns")
		}

		var targets []string
		if kind != frameTargets || decode(payload, &targets) != nil {
			return errors.New("unexpected answer from the replica")
		}

		err = fn(ctx, targets, func(f engine.RowFunc) error {
			for {
				kind, payload, err := c.next()
				if err != nil {
					return err
				}

				if kind == frameFinal {
					return c.res.Err.decode()
				}

				var rows [][]any
				if err := decode(payload, &rows); err != nil {
					return err
				}

				for _, row := range rows {
					if err := f(row); err != nil {
						return err
					}
				}
			}
		})

		return err
	}
}

// ////////////////////////////////////////////
// owner side

// exec runs a forwarded operation on this replica.
func (r *Router) exec(ctx context.Context, req *callRequest, fw *frameWriter) *callResult {
	res := &callResult{}

	err := func() error {
		password, err := r.cipher.Decrypt(req.Secret)
		if err != nil {
			return errors.New("the replicas do not share the same secret key")
		}

		c := &req.Conn
		m := r.Manager

		switch req.Op {
		case opSchemas:
			res.Strings, err = m.Schemas(ctx, c, password)
		case opObjects:
			res.Objects, err = m.Objects(ctx, c, password, req.Schema)
		case opColumns:
			res.Columns, err = m.Columns(ctx, c, password, req.Schema, req.Table)
		case opResolveTable:
			res.Text, res.OK, err = m.ResolveTable(ctx, c, password, req.Table)
		case opStructure:
			res.Structure, err = m.Structure(ctx, c, password, req.Schema, req.Table)
		case opRoutines:
			res.Routines, err = m.Routines(ctx, c, password, req.Schema)
		case opDefinition:
			res.Text, err = m.Definition(ctx, c, password, req.Schema, req.Routine)
		case opDiagram:
			res.Diagram, err = m.Diagram(ctx, c, password, req.Schema)
		case opSchemaDoc:
			res.SchemaDoc, err = m.SchemaDoc(ctx, c, password, req.Schema)
		case opExecute:
			res.Exec, err = m.Execute(ctx, c, password, req.Exec)
		case opExplain:
			res.Explain, err = m.Explain(ctx, c, password, req.Explain)
		case opCount:
			res.CountResult, err = m.Count(ctx, c, password, req.Count)
		case opPreviewEdits:
			res.Strings, err = m.PreviewEdits(ctx, c, password, req.Edit)
		case opApplyEdits:
			res.EditResult, err = m.ApplyEdits(ctx, c, password, req.Edit)
		case opProcesses:
			res.Processes, err = m.Processes(ctx, c, password)
		case opKillProcess:
			res.Text, err = m.KillProcess(ctx, c, password, req.Process, req.Terminate)
		case opUsers:
			res.Users, err = m.Users(ctx, c, password)
		case opGrants:
			res.Strings, err = m.Grants(ctx, c, password, req.Name, req.Host)
		case opImport:
			res.Import, err = m.Import(ctx, c, password, req.Import)
		case opExport:
			res.ExportStats, err = m.Export(ctx, c, password, req.Export, fw)
		case opGenPreview:
			res.Plan, err = m.GeneratePreview(ctx, c, password, req.Generate)
		case opGenerate:
			res.Count, err = m.Generate(ctx, c, password, req.Generate, func(p engine.GenerateProgress) {
				_ = fw.value(frameProgress, p)
			})
		case opTransfer, opQueryTransfer:
			src := req.Src
			if src == nil {
				return errors.New("transfer without a source")
			}

			srcPassword, err := r.cipher.Decrypt(req.SrcSecret)
			if err != nil {
				return errors.New("the replicas do not share the same secret key")
			}

			if req.Op == opTransfer {
				return r.TransferFrom(ctx, src, r.tableSource(src, srcPassword, req.Transfer.Timeout), c, password, req.Transfer, func(p engine.TransferProgress) {
					_ = fw.value(frameProgress, p)
				})
			}

			res.QueryTransfer, err = r.QueryTransferFrom(ctx, src, r.querySource(src, srcPassword, req.QueryTransfer), c, password, req.QueryTransfer)

			return err
		case opStream:
			return r.serveStream(ctx, c, password, req, fw)
		case opQuerySource:
			return r.serveQuerySource(ctx, c, password, req, fw)
		default:
			return fmt.Errorf("unknown operation %q; are all replicas on the same version?", req.Op)
		}

		return err
	}()

	res.Err = encodeError(err)

	return res
}

func (r *Router) serveStream(ctx context.Context, c *store.Connection, password string, req *callRequest, fw *frameWriter) error {
	return r.LocalSource(c, password, req.Transfer.Timeout)(ctx, func(ctx context.Context, s engine.TableSource) error {
		batch := make([][]any, 0, rowBatch)

		flush := func() error {
			if len(batch) == 0 {
				return nil
			}

			err := fw.value(frameRows, batch)
			batch = batch[:0]

			return err
		}

		err := s.Stream(ctx, req.Schema, req.Table, func(cols []engine.Column) error {
			return fw.value(frameColumns, cols)
		}, func(row []any) error {
			batch = append(batch, append([]any(nil), row...))
			if len(batch) >= rowBatch {
				return flush()
			}

			return nil
		})
		if err != nil {
			return err
		}

		return flush()
	})
}

func (r *Router) serveQuerySource(ctx context.Context, c *store.Connection, password string, req *callRequest, fw *frameWriter) error {
	return r.QuerySource(c, password, req.QueryTransfer)(ctx, func(ctx context.Context, targets []string, rows func(engine.RowFunc) error) error {
		if err := fw.value(frameTargets, targets); err != nil {
			return err
		}

		batch := make([][]any, 0, rowBatch)

		err := rows(func(row []any) error {
			// Drivers may scan into types of their own, which only this
			// replica's build is sure to know.
			for i, v := range row {
				row[i] = engine.Portable(v)
			}

			batch = append(batch, row)
			if len(batch) < rowBatch {
				return nil
			}

			err := fw.value(frameRows, batch)
			batch = batch[:0]

			return err
		})
		if err != nil {
			return err
		}

		if len(batch) > 0 {
			return fw.value(frameRows, batch)
		}

		return nil
	})
}

// ////////////////////////////////////////////
// state kept per replica

type cancelRequest struct{ UserID, QueryID string }

// Cancel stops a request of the user on whichever replica runs it.
func (r *Router) Cancel(ctx context.Context, userID, queryID string) bool {
	found := r.Manager.Cancel(ctx, userID, queryID)

	data, _ := encode(cancelRequest{userID, queryID})

	ctx, cancel := context.WithTimeout(ctx, askTimeout)
	defer cancel()

	for _, a := range r.t.Broadcast(ctx, typeCancel, data) {
		if len(a.Data) == 1 && a.Data[0] == 'y' {
			found = true
		}
	}

	return found
}

func (r *Router) handleCancel(ctx context.Context, _ *net.UDPAddr, data []byte) []byte {
	var req cancelRequest
	if decode(data, &req) != nil || !r.Manager.Cancel(ctx, req.UserID, req.QueryID) {
		return []byte{'n'}
	}

	return []byte{'y'}
}

// Txs lists the open transactions of a user on every replica.
func (r *Router) Txs(ctx context.Context, userID string) []engine.TxInfo {
	out := r.Manager.Txs(ctx, userID)

	ctx, cancel := context.WithTimeout(ctx, askTimeout)
	defer cancel()

	for _, a := range r.t.Broadcast(ctx, typeTxs, []byte(userID)) {
		var list []engine.TxInfo
		if decode(a.Data, &list) == nil {
			out = append(out, list...)
		}
	}

	return out
}

func (r *Router) handleTxs(ctx context.Context, _ *net.UDPAddr, data []byte) []byte {
	b, _ := encode(r.Manager.Txs(ctx, string(data)))

	return b
}

type endTxRequest struct {
	UserID, TxID, ConnID string
	Commit               bool
}

type endTxResult struct {
	End *engine.TxEnd
	Err *wireError
}

// EndTx commits or rolls back a transaction on the replica that holds it.
func (r *Router) EndTx(ctx context.Context, userID, txID, connID string, commit bool) (*engine.TxEnd, error) {
	end, err := r.Manager.EndTx(ctx, userID, txID, connID, commit)
	if !errors.Is(err, engine.ErrTxLost) {
		return end, err
	}

	data, _ := encode(endTxRequest{userID, txID, connID, commit})

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	for _, a := range r.t.Broadcast(ctx, typeEndTx, data) {
		var res endTxResult
		if decode(a.Data, &res) != nil {
			continue
		}

		if e := res.Err.decode(); !errors.Is(e, engine.ErrTxLost) {
			return res.End, e
		}
	}

	return nil, engine.ErrTxLost
}

func (r *Router) handleEndTx(ctx context.Context, _ *net.UDPAddr, data []byte) []byte {
	var req endTxRequest
	if err := decode(data, &req); err != nil {
		b, _ := encode(endTxResult{Err: encodeError(engine.ErrTxLost)})

		return b
	}

	end, err := r.Manager.EndTx(ctx, req.UserID, req.TxID, req.ConnID, req.Commit)
	b, _ := encode(endTxResult{End: end, Err: encodeError(err)})

	return b
}

type forgetRequest struct{ ID, UserID string }

// Forget closes the pools of an edited or deleted connection everywhere.
func (r *Router) Forget(id string) {
	r.Manager.Forget(id)
	r.tell(typeForget, forgetRequest{ID: id})
}

// ForgetUser closes the pool of a user's own account everywhere.
func (r *Router) ForgetUser(id, userID string) {
	r.Manager.ForgetUser(id, userID)
	r.tell(typeForget, forgetRequest{ID: id, UserID: userID})
}

func (r *Router) handleForget(_ context.Context, _ *net.UDPAddr, data []byte) {
	var req forgetRequest
	if decode(data, &req) != nil {
		return
	}

	if req.UserID != "" {
		r.Manager.ForgetUser(req.ID, req.UserID)
	} else {
		r.Manager.Forget(req.ID)
	}
}

// Disconnect lets go of what any replica holds open on a connection.
func (r *Router) Disconnect(ctx context.Context, c *store.Connection) {
	r.Manager.Disconnect(ctx, c)
	r.tell(typeDisconnect, *c)
}

func (r *Router) handleDisconnect(ctx context.Context, _ *net.UDPAddr, data []byte) {
	var c store.Connection
	if decode(data, &c) == nil {
		r.Manager.Disconnect(ctx, &c)
	}
}

// tell sends a message to every peer, without waiting for answers.
func (r *Router) tell(typ string, v any) {
	data, err := encode(v)
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(r.ctx, askTimeout)
	defer cancel()

	for _, p := range r.t.Peers() {
		_ = r.t.SendTo(ctx, p, typ, data)
	}
}
