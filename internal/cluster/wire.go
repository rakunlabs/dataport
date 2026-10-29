package cluster

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/store"
)

// Message types between replicas.
const (
	typeWho        = "dataport.who"
	typeInfo       = "dataport.info"
	typeCall       = "dataport.call"
	typeResult     = "dataport.result"
	typeAbort      = "dataport.abort"
	typeCancel     = "dataport.cancel"
	typeTxs        = "dataport.txs"
	typeEndTx      = "dataport.endtx"
	typeForget     = "dataport.forget"
	typeDisconnect = "dataport.disconnect"
	typeRelease    = "dataport.release"
)

func init() {
	// Values that travel inside []any and map[string]any: bound
	// parameters, cells, edited values and imported rows.
	gob.Register(time.Time{})
	gob.Register(json.Number(""))
	gob.Register(map[string]any{})
	gob.Register([]any{})
}

// Operations a replica runs for another on a pool it owns.
const (
	opSchemas       = "schemas"
	opObjects       = "objects"
	opColumns       = "columns"
	opResolveTable  = "resolve_table"
	opStructure     = "structure"
	opRoutines      = "routines"
	opDefinition    = "definition"
	opDiagram       = "diagram"
	opSchemaDoc     = "schema_doc"
	opExplain       = "explain"
	opCount         = "count"
	opExecute       = "execute"
	opPreviewEdits  = "preview_edits"
	opApplyEdits    = "apply_edits"
	opProcesses     = "processes"
	opKillProcess   = "kill_process"
	opUsers         = "users"
	opGrants        = "grants"
	opImport        = "import"
	opExport        = "export"
	opGenPreview    = "generate_preview"
	opGenerate      = "generate"
	opTransfer      = "transfer"
	opQueryTransfer = "query_transfer"
	// opStream and opQuerySource read the source side of a transfer
	// running on another replica.
	opStream      = "stream"
	opQuerySource = "query_source"
)

// callRequest is one operation on a pool. The connection travels as the
// caller resolved it, with its password encrypted by the shared secret key.
type callRequest struct {
	ID        string
	Op        string
	Key       string
	Conn      store.Connection
	Secret    string
	Src       *store.Connection
	SrcSecret string

	Schema    string
	Table     string
	Name      string
	Host      string
	Process   string
	Terminate bool
	Routine   engine.Routine

	Exec          engine.ExecRequest
	Edit          engine.EditRequest
	Import        engine.ImportRequest
	Export        engine.ExportRequest
	Generate      engine.GenerateRequest
	Transfer      engine.TransferRequest
	QueryTransfer engine.QueryTransferRequest
	Explain       engine.ExplainRequest
	Count         engine.CountRequest
}

// callResult is the answer of an operation; only the field of the
// operation is set.
type callResult struct {
	Err *wireError

	Strings       []string
	Objects       []engine.Object
	Columns       []engine.ColumnInfo
	Text          string
	OK            bool
	Count         int64
	Structure     *engine.TableStructure
	Routines      []engine.Routine
	Diagram       *engine.Diagram
	SchemaDoc     *engine.SchemaDoc
	Exec          *engine.ExecResult
	EditResult    *engine.EditResult
	Processes     *engine.ProcessList
	Users         []engine.DBUser
	Import        *engine.ImportResult
	ExportStats   *engine.ExportStats
	Plan          *engine.GeneratePlan
	QueryTransfer *engine.QueryTransferResult
	Explain       *engine.ExplainResult
	CountResult   *engine.CountResult
}

// Answers of the owner to a call before it runs.
const (
	ackAccepted = 'a'
	ackNotOwner = 'n'
	ackFailed   = 'f'
)

var errNotOwner = errors.New("the replica no longer owns the pool")

// ErrPeerGone means the replica running a request left the cluster before
// it answered.
var ErrPeerGone = errors.New("the replica holding this connection left the cluster; try again")

// sentinels are the errors whose identity the server checks, so they keep
// it across replicas.
var sentinels = map[string]error{
	"unsupported":      engine.ErrUnsupported,
	"read_only":        engine.ErrReadOnly,
	"no_processes":     engine.ErrNoProcesses,
	"tx_lost":          engine.ErrTxLost,
	"tx_busy":          engine.ErrTxBusy,
	"tx_connection":    engine.ErrTxConnection,
	"tx_limit":         engine.ErrTxLimit,
	"bad_edit":         engine.ErrBadEdit,
	"bad_export":       engine.ErrBadExport,
	"files_disabled":   engine.ErrFilesDisabled,
	"file_path":        engine.ErrFilePath,
	"bad_generate":     engine.ErrBadGenerate,
	"bad_import":       engine.ErrBadImport,
	"no_routines":      engine.ErrNoRoutines,
	"no_structure":     engine.ErrNoStructure,
	"no_users":         engine.ErrNoUsers,
	"no_explain":       engine.ErrNoExplain,
	"not_countable":    engine.ErrNotCountable,
	"canceled":         context.Canceled,
	"deadline":         context.DeadlineExceeded,
	"peer_gone":        ErrPeerGone,
	"not_owner":        errNotOwner,
	"message_too_long": errTooLarge,
}

var errTooLarge = errors.New("the request is too large to forward to another replica")

type wireError struct {
	Code            string
	Msg             string
	Conn            bool
	Auth            bool
	UserCredentials bool
}

func encodeError(err error) *wireError {
	if err == nil {
		return nil
	}

	w := &wireError{Msg: err.Error()}

	var connErr *engine.ConnError
	if errors.As(err, &connErr) {
		w.Conn, w.Auth, w.UserCredentials = true, connErr.Auth, connErr.UserCredentials

		return w
	}

	for code, s := range sentinels {
		if errors.Is(err, s) {
			w.Code = code

			break
		}
	}

	return w
}

// remoteError keeps the text of an error from another replica and the
// identity of the sentinel it wrapped.
type remoteError struct {
	msg string
	is  error
}

func (e *remoteError) Error() string { return e.msg }
func (e *remoteError) Unwrap() error { return e.is }

func (w *wireError) decode() error {
	if w == nil {
		return nil
	}

	if w.Conn {
		return &engine.ConnError{Err: errors.New(w.Msg), Auth: w.Auth, UserCredentials: w.UserCredentials}
	}

	if s, ok := sentinels[w.Code]; ok {
		if s.Error() == w.Msg {
			return s
		}

		return &remoteError{msg: w.Msg, is: s}
	}

	return errors.New(w.Msg)
}

// fillEmpty turns nil slices and maps that JSON writes as such into empty
// ones: gob sends empty ones as nil, and the UI expects [] where the engine
// returned it.
func fillEmpty(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			fillEmpty(v.Elem())
		}
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Interface {
			return
		}

		for i := range v.Len() {
			fillEmpty(v.Index(i))
		}
	case reflect.Struct:
		t := v.Type()

		for i := range t.NumField() {
			f, fv := t.Field(i), v.Field(i)
			if !f.IsExported() {
				continue
			}

			tag := f.Tag.Get("json")
			if tag == "-" {
				continue
			}

			if fv.Kind() == reflect.Slice || fv.Kind() == reflect.Map {
				if fv.IsNil() && !strings.Contains(tag, "omitempty") && fv.CanSet() {
					if fv.Kind() == reflect.Slice {
						fv.Set(reflect.MakeSlice(fv.Type(), 0, 0))
					} else {
						fv.Set(reflect.MakeMap(fv.Type()))
					}
				}
			}

			fillEmpty(fv)
		}
	}
}

func encode(v any) ([]byte, error) {
	var b bytes.Buffer
	if err := gob.NewEncoder(&b).Encode(v); err != nil {
		return nil, err
	}

	return b.Bytes(), nil
}

func decode(data []byte, v any) error {
	return gob.NewDecoder(bytes.NewReader(data)).Decode(v)
}

// ////////////////////////////////////////////
// result streams

// A result stream starts with the call id and carries frames: a kind, a
// length and the payload. The final frame holds the callResult.
const (
	frameData     = 'd'
	frameProgress = 'p'
	frameColumns  = 'c'
	frameTargets  = 't'
	frameRows     = 'r'
	frameFinal    = 'f'
)

// maxFrame bounds one frame: a whole query result is one.
const maxFrame = 1 << 30

func writeHeader(w io.Writer, id string) error {
	if len(id) > 255 {
		return errors.New("call id too long")
	}

	_, err := w.Write(append([]byte{byte(len(id))}, id...))

	return err
}

func readHeader(r *bufio.Reader) (string, error) {
	n, err := r.ReadByte()
	if err != nil {
		return "", err
	}

	id := make([]byte, n)
	if _, err := io.ReadFull(r, id); err != nil {
		return "", err
	}

	return string(id), nil
}

func writeFrame(w io.Writer, kind byte, payload []byte) error {
	head := make([]byte, 1, 1+binary.MaxVarintLen64)
	head[0] = kind
	head = binary.AppendUvarint(head, uint64(len(payload)))

	if _, err := w.Write(head); err != nil {
		return err
	}

	_, err := w.Write(payload)

	return err
}

func readFrame(r *bufio.Reader) (byte, []byte, error) {
	kind, err := r.ReadByte()
	if err != nil {
		return 0, nil, err
	}

	n, err := binary.ReadUvarint(r)
	if err != nil {
		return 0, nil, err
	}

	if n > maxFrame {
		return 0, nil, fmt.Errorf("frame of %d bytes is too large", n)
	}

	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}

	return kind, payload, nil
}
