// Package cluster lets the replicas of one installation share the target
// databases: each pool is opened on one replica, its owner, which holds a
// lock on it; the others forward their work there over QUIC (alan) and
// receive the results. A replica that needs a pool nobody owns takes it.
package cluster

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/rakunlabs/alan"

	"github.com/rakunlabs/dataport/internal/config"
	"github.com/rakunlabs/dataport/internal/crypto"
	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/store"
)

const (
	lockPrefix = "dataport/pool/"
	// askTimeout bounds the questions replicas ask each other, such as
	// who owns a pool.
	askTimeout = 3 * time.Second
	// lockTimeout covers taking a pool lock, which the first time may
	// wait for every replica to show up (alan's membership wait).
	lockTimeout = 15 * time.Second
	// rowBatch is the number of rows a transfer source sends at once.
	rowBatch = 200
)

// Router is the engine of a replica in a cluster. Operations on a pool run
// where the pool is owned; everything else is the local engine's.
type Router struct {
	*engine.Manager

	t      Transport
	cipher *crypto.Cipher
	self   Node
	alan   *alan.Alan

	ctx    context.Context
	cancel context.CancelFunc

	// lockMu orders taking and giving up pool locks.
	lockMu sync.Mutex

	mu sync.Mutex
	// owned are the pools this replica holds the lock of; active counts
	// the operations running on each.
	owned  map[string]bool
	active map[string]int
	// owners remembers which peer owns a pool.
	owners map[string]string
	// calls are the requests this replica waits on, serving the ones
	// peers sent.
	calls   map[string]*pending
	serving map[string]*served
	// warned keeps the pools opened without a quorum, to warn once.
	warned sync.Map
}

type pending struct {
	addr   string
	stream chan *bufio.Reader
	done   chan struct{}
	gone   chan struct{}
	once   sync.Once
}

type served struct {
	from   string
	cancel context.CancelFunc
}

// New joins the cluster described by cfg. Start runs it.
func New(cfg config.Cluster, m *engine.Manager, ci *crypto.Cipher) (*Router, error) {
	ac := alan.Config{
		DNSAddr:         cfg.DNSAddr,
		BindAddr:        cfg.BindAddr,
		Port:            cfg.Port,
		Replicas:        cfg.Replicas,
		RefreshInterval: cfg.RefreshInterval,
		// Results of any size go through streams; requests stay small.
		MaxMessageSize: 32 << 20,
	}

	if cfg.SecurityKey != "" {
		ac.Security = alan.SecurityConfig{Enabled: true, Key: []byte(cfg.SecurityKey)}
	}

	a, err := alan.New(ac)
	if err != nil {
		return nil, fmt.Errorf("init cluster; %w", err)
	}

	name := cfg.Name
	if name == "" {
		name, _ = os.Hostname()
	}

	r := newRouter(&alanTransport{a: a}, m, ci, name)
	r.alan = a

	return r, nil
}

func newRouter(t Transport, m *engine.Manager, ci *crypto.Cipher, name string) *Router {
	ctx, cancel := context.WithCancel(context.Background())

	r := &Router{
		Manager: m,
		t:       t,
		cipher:  ci,
		self: Node{
			ID: newID(), Name: name,
			Version: config.Build.Version, Commit: config.Build.Commit, StartedAt: time.Now().UnixMilli(),
		},
		ctx: ctx, cancel: cancel,
		owned: map[string]bool{}, active: map[string]int{}, owners: map[string]string{},
		calls: map[string]*pending{}, serving: map[string]*served{},
	}

	t.HandleRequest(typeWho, r.handleWho)
	t.HandleRequest(typeInfo, r.handleInfo)
	t.HandleRequest(typeCall, r.handleCall)
	t.HandleRequest(typeCancel, r.handleCancel)
	t.HandleRequest(typeTxs, r.handleTxs)
	t.HandleRequest(typeEndTx, r.handleEndTx)
	t.HandleRequest(typeRelease, r.handleRelease)
	t.HandleMessage(typeAbort, r.handleAbort)
	t.HandleMessage(typeForget, r.handleForget)
	t.HandleMessage(typeDisconnect, r.handleDisconnect)
	t.HandleStream(typeResult, r.handleResult)
	t.OnPeerLeave(r.peerLeft)

	m.OnClose(r.poolsClosed)

	return r
}

// Start joins the cluster; it stays joined until Close, so the pools can
// be given up before the peers are told this replica leaves.
func (r *Router) Start(ctx context.Context) error {
	if r.alan == nil {
		return nil
	}

	errs := make(chan error, 1)

	go func() { errs <- r.alan.Start(r.ctx) }()

	select {
	case <-r.alan.Ready():
		slog.Info("joined the cluster", "addr", r.alan.LocalAddr().String(), "replicas", r.alan.Config().Replicas, "secure", r.alan.IsSecure())

		return nil
	case err := <-errs:
		return fmt.Errorf("start cluster; %w", err)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close gives up the pools this replica owns and leaves the cluster.
func (r *Router) Close() {
	r.mu.Lock()
	keys := make([]string, 0, len(r.owned))

	for k := range r.owned {
		keys = append(keys, k)
	}
	r.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), askTimeout)
	defer cancel()

	for _, k := range keys {
		_ = r.t.Unlock(ctx, lockPrefix+k)
	}

	if r.alan != nil {
		_ = r.alan.Stop()
	}

	r.cancel()
}

func newID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)

	return hex.EncodeToString(b)
}

// ////////////////////////////////////////////
// ownership

// owner finds where the pool of key is served. A nil address means this
// replica; done must then be called when the operation ends.
func (r *Router) owner(ctx context.Context, key string) (*net.UDPAddr, func(), error) {
	for attempt := range 6 {
		if done, ok := r.useOwned(key); ok {
			return nil, done, nil
		}

		r.mu.Lock()
		cached := r.owners[key]
		r.mu.Unlock()

		if addr := r.peer(cached); addr != nil {
			return addr, func() {}, nil
		}

		if addr := r.who(ctx, key); addr != nil {
			r.mu.Lock()
			r.owners[key] = addr.String()
			r.mu.Unlock()

			return addr, func() {}, nil
		}

		// Without a quorum no lock can be taken; the work runs here rather
		// than being refused.
		if !r.t.Health().HasQuorum {
			if _, warned := r.warned.LoadOrStore(key, true); !warned {
				slog.Warn("cluster: no quorum, opening a pool on this replica without owning it", "pool", key)
			}

			return nil, func() {}, nil
		}

		if done, ok := r.take(ctx, key); ok {
			return nil, done, nil
		}

		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 100 * time.Millisecond):
		}
	}

	// A holder that does not answer leaves the pool to this replica rather
	// than refusing the work.
	slog.Warn("cluster: no owner for a pool, opening it on this replica", "pool", key)

	return nil, func() {}, nil
}

// useOwned counts an operation on a pool this replica owns.
func (r *Router) useOwned(key string) (func(), bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.owned[key] {
		return nil, false
	}

	r.active[key]++

	return r.doneFunc(key), true
}

func (r *Router) doneFunc(key string) func() {
	var once sync.Once

	return func() {
		once.Do(func() {
			r.mu.Lock()
			r.active[key]--
			if r.active[key] <= 0 {
				delete(r.active, key)
			}
			r.mu.Unlock()

			go r.maybeRelease(key)
		})
	}
}

// take locks the pool of key for this replica.
func (r *Router) take(ctx context.Context, key string) (func(), bool) {
	r.lockMu.Lock()
	defer r.lockMu.Unlock()

	if done, ok := r.useOwned(key); ok {
		return done, true
	}

	lockCtx, cancel := context.WithTimeout(ctx, lockTimeout)
	defer cancel()

	if !r.t.TryLock(lockCtx, lockPrefix+key) {
		return nil, false
	}

	r.mu.Lock()
	r.owned[key] = true
	delete(r.owners, key)
	r.active[key]++
	r.mu.Unlock()

	return r.doneFunc(key), true
}

// maybeRelease gives up a pool that is closed and unused.
func (r *Router) maybeRelease(key string) {
	r.lockMu.Lock()
	defer r.lockMu.Unlock()

	r.mu.Lock()
	idle := r.owned[key] && r.active[key] == 0 && !r.HasPool(key)
	if idle {
		delete(r.owned, key)
	}
	r.mu.Unlock()

	if !idle {
		return
	}

	ctx, cancel := context.WithTimeout(r.ctx, askTimeout)
	defer cancel()

	if err := r.t.Unlock(ctx, lockPrefix+key); err != nil && !errors.Is(err, alan.ErrLockNotHeld) {
		slog.Warn("cluster: release pool lock", "pool", key, "error", err)
	}
}

func (r *Router) poolsClosed(keys []string) {
	for _, k := range keys {
		r.maybeRelease(k)
	}
}

func (r *Router) who(ctx context.Context, key string) *net.UDPAddr {
	ctx, cancel := context.WithTimeout(ctx, askTimeout)
	defer cancel()

	for _, a := range r.t.Broadcast(ctx, typeWho, []byte(key)) {
		if len(a.Data) == 1 && a.Data[0] == 'y' {
			return a.From
		}
	}

	return nil
}

func (r *Router) handleWho(_ context.Context, _ *net.UDPAddr, data []byte) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.owned[string(data)] {
		return []byte{'y'}
	}

	return []byte{'n'}
}

// peer returns the connected peer with an address, or nil.
func (r *Router) peer(addr string) *net.UDPAddr {
	if addr == "" {
		return nil
	}

	for _, p := range r.t.Peers() {
		if p.String() == addr {
			return p
		}
	}

	return nil
}

func (r *Router) peerLeft(addr *net.UDPAddr) {
	a := addr.String()

	r.mu.Lock()
	defer r.mu.Unlock()

	for k, v := range r.owners {
		if v == a {
			delete(r.owners, k)
		}
	}

	for _, p := range r.calls {
		if p.addr == a {
			p.once.Do(func() { close(p.gone) })
		}
	}

	for _, s := range r.serving {
		if s.from == a {
			s.cancel()
		}
	}
}

// ////////////////////////////////////////////
// forwarding

// do runs an operation on the pool of c: local where this replica owns it,
// otherwise on the owner, handing the frames of the answer to onFrame.
func (r *Router) do(ctx context.Context, c *store.Connection, password string, req *callRequest,
	local func(ctx context.Context) error, onFrame func(kind byte, payload []byte) error,
) (*callResult, error) {
	key := engine.PoolKey(c)

	for range 3 {
		addr, done, err := r.owner(ctx, key)
		if err != nil {
			return nil, err
		}

		if addr == nil {
			defer done()

			return nil, local(ctx)
		}

		secret, err := r.cipher.Encrypt(password)
		if err != nil {
			return nil, err
		}

		req.Key, req.Conn, req.Secret = key, *c, secret

		res, err := r.remote(ctx, addr, req, onFrame)
		if errors.Is(err, errNotOwner) {
			r.mu.Lock()
			delete(r.owners, key)
			r.mu.Unlock()

			continue
		}

		return res, err
	}

	return nil, errors.New("the replicas could not agree on which one serves this connection; try again")
}

// remote sends an operation to its owner and reads the answer.
func (r *Router) remote(ctx context.Context, addr *net.UDPAddr, req *callRequest, onFrame func(kind byte, payload []byte) error) (*callResult, error) {
	c, err := r.start(ctx, addr, req)
	if err != nil {
		return nil, err
	}
	defer c.close()

	for {
		kind, payload, err := c.next()
		if err != nil {
			return nil, err
		}

		if kind == frameFinal {
			return c.res, c.res.Err.decode()
		}

		if onFrame == nil {
			continue
		}

		if err := onFrame(kind, payload); err != nil {
			return nil, err
		}
	}
}

type call struct {
	r        *Router
	addr     *net.UDPAddr
	id       string
	p        *pending
	ctx      context.Context
	rd       *bufio.Reader
	res      *callResult
	finished bool
	stop     func() bool
}

func (r *Router) start(ctx context.Context, addr *net.UDPAddr, req *callRequest) (*call, error) {
	req.ID = newID()

	data, err := encode(req)
	if err != nil {
		return nil, fmt.Errorf("encode request; %w", err)
	}

	p := &pending{addr: addr.String(), stream: make(chan *bufio.Reader), done: make(chan struct{}), gone: make(chan struct{})}

	r.mu.Lock()
	r.calls[req.ID] = p
	r.mu.Unlock()

	c := &call{r: r, addr: addr, id: req.ID, p: p, ctx: ctx}

	ackCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	ack, err := r.t.Request(ackCtx, addr, typeCall, data)

	cancel()

	if err != nil {
		c.forget()
		// The owner may have started without its answer arriving.
		go r.abort(addr, req.ID)

		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		if errors.Is(err, errTooLarge) {
			return nil, err
		}

		return nil, fmt.Errorf("%w (%v)", ErrPeerGone, err)
	}

	switch {
	case len(ack) == 1 && ack[0] == ackAccepted:
	case len(ack) == 1 && ack[0] == ackNotOwner:
		c.forget()

		return nil, errNotOwner
	default:
		c.forget()

		var w wireError
		if len(ack) > 1 && ack[0] == ackFailed && decode(ack[1:], &w) == nil {
			return nil, w.decode()
		}

		return nil, errors.New("unexpected answer from the replica")
	}

	// A stopped request tells the owner, which stops the work and ends the
	// stream.
	c.stop = context.AfterFunc(ctx, func() { r.abort(addr, req.ID) })

	select {
	case c.rd = <-p.stream:
		return c, nil
	case <-p.gone:
		c.close()

		return nil, ErrPeerGone
	case <-ctx.Done():
		c.close()

		return nil, ctx.Err()
	}
}

func (r *Router) abort(addr *net.UDPAddr, id string) {
	ctx, cancel := context.WithTimeout(r.ctx, askTimeout)
	defer cancel()

	_ = r.t.SendTo(ctx, addr, typeAbort, []byte(id))
}

// next reads the next frame; the final one fills c.res.
func (c *call) next() (byte, []byte, error) {
	kind, payload, err := readFrame(c.rd)
	if err != nil {
		if c.ctx.Err() != nil {
			return 0, nil, c.ctx.Err()
		}

		return 0, nil, ErrPeerGone
	}

	if kind == frameFinal {
		c.finished = true
		c.res = &callResult{}

		if err := decode(payload, c.res); err != nil {
			return 0, nil, fmt.Errorf("decode result; %w", err)
		}

		fillEmpty(reflect.ValueOf(c.res))
	}

	return kind, payload, nil
}

func (c *call) close() {
	if c.stop != nil {
		c.stop()
	}

	if !c.finished && c.ctx.Err() == nil {
		c.r.abort(c.addr, c.id)
	}

	c.forget()
}

func (c *call) forget() {
	c.r.mu.Lock()
	delete(c.r.calls, c.id)
	c.r.mu.Unlock()

	select {
	case <-c.p.done:
	default:
		close(c.p.done)
	}
}

// handleResult hands the stream of an answer to the request waiting on it.
func (r *Router) handleResult(ctx context.Context, _ *net.UDPAddr, body io.Reader) {
	rd := bufio.NewReaderSize(body, 64<<10)

	id, err := readHeader(rd)
	if err != nil {
		return
	}

	r.mu.Lock()
	p := r.calls[id]
	r.mu.Unlock()

	if p == nil {
		return
	}

	select {
	case p.stream <- rd:
		// The stream stays open until the request has read it.
		select {
		case <-p.done:
		case <-ctx.Done():
		}
	case <-p.done:
	case <-ctx.Done():
	}

	// What the request left unread is drained, so the owner, told to stop,
	// can finish writing.
	_, _ = io.Copy(io.Discard, rd)
}

// handleCall runs an operation a peer forwarded, when this replica owns
// the pool, and streams the answer back.
func (r *Router) handleCall(_ context.Context, from *net.UDPAddr, data []byte) []byte {
	var req callRequest
	if err := decode(data, &req); err != nil {
		return failAck(fmt.Errorf("decode request; %w", err))
	}

	done, ok := r.useOwned(req.Key)
	if !ok {
		return []byte{ackNotOwner}
	}

	// The work ends when the caller stops it or leaves; the clocks of the
	// replicas may differ, so its deadline does not travel.
	ctx, cancel := context.WithCancel(r.ctx)

	r.mu.Lock()
	r.serving[req.ID] = &served{from: from.String(), cancel: cancel}
	r.mu.Unlock()

	go func() {
		defer done()
		defer cancel()
		defer func() {
			r.mu.Lock()
			delete(r.serving, req.ID)
			r.mu.Unlock()
		}()

		r.serve(ctx, from, &req, cancel)
	}()

	return []byte{ackAccepted}
}

func failAck(err error) []byte {
	b, _ := encode(encodeError(err))

	return append([]byte{ackFailed}, b...)
}

func (r *Router) serve(ctx context.Context, from *net.UDPAddr, req *callRequest, cancel context.CancelFunc) {
	pr, pw := io.Pipe()

	go func() {
		fw := &frameWriter{w: bufio.NewWriterSize(pw, 64<<10)}

		err := writeHeader(fw.w, req.ID)
		if err == nil {
			res := r.exec(ctx, req, fw)
			err = fw.final(res)
		}

		_ = pw.CloseWithError(err)
	}()

	// The answer goes back even after the work was stopped, so the
	// request learns how it ended.
	if err := r.t.SendStream(r.ctx, from, typeResult, pr); err != nil {
		cancel()
		_ = pr.CloseWithError(err)
	}
}

func (r *Router) handleAbort(_ context.Context, from *net.UDPAddr, data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if s := r.serving[string(data)]; s != nil && s.from == from.String() {
		s.cancel()
	}
}

// frameWriter writes the frames of an answer; progress callbacks may come
// from other goroutines.
type frameWriter struct {
	mu sync.Mutex
	w  *bufio.Writer
}

func (f *frameWriter) frame(kind byte, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := writeFrame(f.w, kind, payload); err != nil {
		return err
	}

	return f.w.Flush()
}

func (f *frameWriter) value(kind byte, v any) error {
	b, err := encode(v)
	if err != nil {
		return err
	}

	return f.frame(kind, b)
}

func (f *frameWriter) Write(p []byte) (int, error) {
	if err := f.frame(frameData, p); err != nil {
		return 0, err
	}

	return len(p), nil
}

func (f *frameWriter) final(res *callResult) error {
	b, err := encode(res)
	if err != nil {
		b, _ = encode(&callResult{Err: encodeError(fmt.Errorf("encode result; %w", err))})
	}

	return f.frame(frameFinal, b)
}

// ////////////////////////////////////////////
// replicas

// Node describes a replica.
type Node struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Addr      string            `json:"addr"`
	Self      bool              `json:"self"`
	Version   string            `json:"version"`
	Commit    string            `json:"commit"`
	StartedAt int64             `json:"started_at"`
	Pools     []engine.PoolInfo `json:"pools"`
	// Owned are the pools this replica holds the lock of.
	Owned []string `json:"owned"`
}

// Overview is the cluster as one replica sees it.
type Overview struct {
	Enabled bool   `json:"enabled"`
	Health  Health `json:"health"`
	Nodes   []Node `json:"nodes"`
	// Unreachable are peers that did not answer.
	Unreachable []string `json:"unreachable"`
}

func (r *Router) node() Node {
	n := r.self
	n.Pools = r.Pools()

	r.mu.Lock()
	n.Owned = make([]string, 0, len(r.owned))

	for k := range r.owned {
		n.Owned = append(n.Owned, k)
	}
	r.mu.Unlock()

	sort.Strings(n.Owned)

	return n
}

func (r *Router) handleInfo(_ context.Context, _ *net.UDPAddr, _ []byte) []byte {
	b, _ := encode(r.node())

	return b
}

// Overview asks every replica about itself.
func (r *Router) Overview(ctx context.Context) Overview {
	self := r.node()
	self.Self = true

	h := r.t.Health()
	self.Addr = h.Addr

	out := Overview{Enabled: true, Health: h, Nodes: []Node{self}, Unreachable: []string{}}

	ctx, cancel := context.WithTimeout(ctx, askTimeout)
	defer cancel()

	answered := map[string]bool{}

	for _, a := range r.t.Broadcast(ctx, typeInfo, nil) {
		var n Node
		if decode(a.Data, &n) != nil {
			continue
		}

		fillEmpty(reflect.ValueOf(&n))
		n.Addr = a.From.String()
		answered[n.Addr] = true
		out.Nodes = append(out.Nodes, n)
	}

	for _, p := range r.t.Peers() {
		if !answered[p.String()] {
			out.Unreachable = append(out.Unreachable, p.String())
		}
	}

	sort.SliceStable(out.Nodes[1:], func(i, j int) bool { return out.Nodes[i+1].Name < out.Nodes[j+1].Name })

	return out
}

// Standalone describes a single instance without a cluster.
func Standalone(m *engine.Manager) Overview {
	name, _ := os.Hostname()

	return Overview{
		Nodes: []Node{{
			Name: name, Self: true, Version: config.Build.Version, Commit: config.Build.Commit,
			Pools: m.Pools(), Owned: []string{},
		}},
		Unreachable: []string{},
	}
}

// ReleasePool closes a pool wherever it is open, so its owner gives it up.
func (r *Router) ReleasePool(ctx context.Context, key string) bool {
	released := r.Release(key)

	ctx, cancel := context.WithTimeout(ctx, askTimeout)
	defer cancel()

	for _, a := range r.t.Broadcast(ctx, typeRelease, []byte(key)) {
		if len(a.Data) == 1 && a.Data[0] == 'y' {
			released = true
		}
	}

	return released
}

func (r *Router) handleRelease(_ context.Context, _ *net.UDPAddr, data []byte) []byte {
	if r.Release(string(data)) {
		return []byte{'y'}
	}

	return []byte{'n'}
}
