package server

import (
	"context"
	"io"
	"time"

	"github.com/rakunlabs/dataport/internal/cluster"
	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/store"
)

// Engine runs the work on target databases: engine.Manager on a single
// instance, cluster.Router where replicas share the pools.
type Engine interface {
	Limits(c *store.Connection) store.Limits
	SetDefaults(defaults store.Limits, idle time.Duration)
	Test(ctx context.Context, c *store.Connection, password string) error

	Schemas(ctx context.Context, c *store.Connection, password string) ([]string, error)
	Objects(ctx context.Context, c *store.Connection, password, schema string) ([]engine.Object, error)
	Columns(ctx context.Context, c *store.Connection, password, schema, table string) ([]engine.ColumnInfo, error)
	ResolveTable(ctx context.Context, c *store.Connection, password, table string) (string, bool, error)
	Structure(ctx context.Context, c *store.Connection, password, schema, table string) (*engine.TableStructure, error)
	Routines(ctx context.Context, c *store.Connection, password, schema string) ([]engine.Routine, error)
	Definition(ctx context.Context, c *store.Connection, password, schema string, r engine.Routine) (string, error)
	Diagram(ctx context.Context, c *store.Connection, password, schema string) (*engine.Diagram, error)
	SchemaDoc(ctx context.Context, c *store.Connection, password, schema string) (*engine.SchemaDoc, error)

	Execute(ctx context.Context, c *store.Connection, password string, req engine.ExecRequest) (*engine.ExecResult, error)
	Explain(ctx context.Context, c *store.Connection, password string, req engine.ExplainRequest) (*engine.ExplainResult, error)
	Count(ctx context.Context, c *store.Connection, password string, req engine.CountRequest) (*engine.CountResult, error)
	Cancel(ctx context.Context, userID, queryID string) bool
	EndTx(ctx context.Context, userID, txID, connID string, commit bool) (*engine.TxEnd, error)
	Txs(ctx context.Context, userID string) []engine.TxInfo

	PreviewEdits(ctx context.Context, c *store.Connection, password string, req engine.EditRequest) ([]string, error)
	ApplyEdits(ctx context.Context, c *store.Connection, password string, req engine.EditRequest) (*engine.EditResult, error)

	Processes(ctx context.Context, c *store.Connection, password string) (*engine.ProcessList, error)
	KillProcess(ctx context.Context, c *store.Connection, password, id string, terminate bool) (string, error)
	Users(ctx context.Context, c *store.Connection, password string) ([]engine.DBUser, error)
	Grants(ctx context.Context, c *store.Connection, password, name, host string) ([]string, error)
	Disconnect(ctx context.Context, c *store.Connection)

	Import(ctx context.Context, c *store.Connection, password string, req engine.ImportRequest) (*engine.ImportResult, error)
	Export(ctx context.Context, c *store.Connection, password string, req engine.ExportRequest, w io.Writer) (*engine.ExportStats, error)
	GeneratePreview(ctx context.Context, c *store.Connection, password string, req engine.GenerateRequest) (*engine.GeneratePlan, error)
	Generate(ctx context.Context, c *store.Connection, password string, req engine.GenerateRequest, progress func(engine.GenerateProgress)) (int64, error)
	Transfer(ctx context.Context, src *store.Connection, srcPassword string, dst *store.Connection, dstPassword string,
		req engine.TransferRequest, progress func(engine.TransferProgress)) error
	QueryTransfer(ctx context.Context, src *store.Connection, srcPassword string, dst *store.Connection, dstPassword string,
		req engine.QueryTransferRequest) (*engine.QueryTransferResult, error)

	Forget(id string)
	ForgetUser(id, userID string)
}

var (
	_ Engine = (*engine.Manager)(nil)
	_ Engine = (*cluster.Router)(nil)
)
