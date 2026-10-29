package cluster

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
)

// memNet joins in-memory transports, for tests of the router.
type memNet struct {
	mu    sync.Mutex
	nodes map[string]*memTransport
	locks map[string]string
}

func newMemNet() *memNet {
	return &memNet{nodes: map[string]*memTransport{}, locks: map[string]string{}}
}

type memTransport struct {
	net  *memNet
	addr *net.UDPAddr

	mu       sync.Mutex
	requests map[string]func(ctx context.Context, from *net.UDPAddr, data []byte) []byte
	messages map[string]func(ctx context.Context, from *net.UDPAddr, data []byte)
	streams  map[string]func(ctx context.Context, from *net.UDPAddr, body io.Reader)
	leave    func(addr *net.UDPAddr)
	ctx      context.Context
	cancel   context.CancelFunc
}

func (n *memNet) join(port int) *memTransport {
	ctx, cancel := context.WithCancel(context.Background())
	t := &memTransport{
		net: n, addr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port},
		requests: map[string]func(ctx context.Context, from *net.UDPAddr, data []byte) []byte{},
		messages: map[string]func(ctx context.Context, from *net.UDPAddr, data []byte){},
		streams:  map[string]func(ctx context.Context, from *net.UDPAddr, body io.Reader){},
		ctx:      ctx, cancel: cancel,
	}

	n.mu.Lock()
	n.nodes[t.addr.String()] = t
	n.mu.Unlock()

	return t
}

// drop takes a node out, as when its replica stops: its locks are freed
// and the others are told.
func (n *memNet) drop(t *memTransport) {
	n.mu.Lock()
	delete(n.nodes, t.addr.String())

	for k, holder := range n.locks {
		if holder == t.addr.String() {
			delete(n.locks, k)
		}
	}

	rest := make([]*memTransport, 0, len(n.nodes))
	for _, o := range n.nodes {
		rest = append(rest, o)
	}
	n.mu.Unlock()

	t.cancel()

	for _, o := range rest {
		o.mu.Lock()
		fn := o.leave
		o.mu.Unlock()

		if fn != nil {
			fn(t.addr)
		}
	}
}

func (n *memNet) node(addr *net.UDPAddr) *memTransport {
	n.mu.Lock()
	defer n.mu.Unlock()

	return n.nodes[addr.String()]
}

func (t *memTransport) HandleRequest(typ string, fn func(ctx context.Context, from *net.UDPAddr, data []byte) []byte) {
	t.mu.Lock()
	t.requests[typ] = fn
	t.mu.Unlock()
}

func (t *memTransport) HandleMessage(typ string, fn func(ctx context.Context, from *net.UDPAddr, data []byte)) {
	t.mu.Lock()
	t.messages[typ] = fn
	t.mu.Unlock()
}

func (t *memTransport) HandleStream(typ string, fn func(ctx context.Context, from *net.UDPAddr, body io.Reader)) {
	t.mu.Lock()
	t.streams[typ] = fn
	t.mu.Unlock()
}

func (t *memTransport) Request(ctx context.Context, to *net.UDPAddr, typ string, data []byte) ([]byte, error) {
	o := t.net.node(to)
	if o == nil {
		return nil, errors.New("no such peer")
	}

	o.mu.Lock()
	fn := o.requests[typ]
	o.mu.Unlock()

	if fn == nil {
		return nil, fmt.Errorf("no handler for %s", typ)
	}

	done := make(chan []byte, 1)

	go func() { done <- fn(o.ctx, t.addr, append([]byte(nil), data...)) }()

	select {
	case b := <-done:
		return b, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (t *memTransport) Broadcast(ctx context.Context, typ string, data []byte) []Answer {
	var out []Answer

	for _, p := range t.Peers() {
		if b, err := t.Request(ctx, p, typ, data); err == nil {
			out = append(out, Answer{From: p, Data: b})
		}
	}

	return out
}

func (t *memTransport) SendTo(_ context.Context, to *net.UDPAddr, typ string, data []byte) error {
	o := t.net.node(to)
	if o == nil {
		return errors.New("no such peer")
	}

	o.mu.Lock()
	fn := o.messages[typ]
	o.mu.Unlock()

	if fn != nil {
		go fn(o.ctx, t.addr, append([]byte(nil), data...))
	}

	return nil
}

func (t *memTransport) SendStream(ctx context.Context, to *net.UDPAddr, typ string, body io.Reader) error {
	o := t.net.node(to)
	if o == nil {
		return errors.New("no such peer")
	}

	o.mu.Lock()
	fn := o.streams[typ]
	o.mu.Unlock()

	if fn == nil {
		return fmt.Errorf("no stream handler for %s", typ)
	}

	pr, pw := io.Pipe()
	done := make(chan struct{})

	go func() {
		defer close(done)
		fn(o.ctx, t.addr, pr)
		_ = pr.Close()
	}()

	_, err := io.Copy(pw, body)
	_ = pw.CloseWithError(err)
	<-done

	return err
}

func (t *memTransport) Peers() []*net.UDPAddr {
	t.net.mu.Lock()
	defer t.net.mu.Unlock()

	var out []*net.UDPAddr

	for k, o := range t.net.nodes {
		if k != t.addr.String() {
			out = append(out, o.addr)
		}
	}

	return out
}

func (t *memTransport) TryLock(_ context.Context, key string) bool {
	t.net.mu.Lock()
	defer t.net.mu.Unlock()

	if holder, ok := t.net.locks[key]; ok {
		return holder == t.addr.String()
	}

	t.net.locks[key] = t.addr.String()

	return true
}

func (t *memTransport) Unlock(_ context.Context, key string) error {
	t.net.mu.Lock()
	defer t.net.mu.Unlock()

	if t.net.locks[key] != t.addr.String() {
		return errors.New("lock not held")
	}

	delete(t.net.locks, key)

	return nil
}

func (t *memTransport) holds(key string) bool {
	t.net.mu.Lock()
	defer t.net.mu.Unlock()

	return t.net.locks[key] == t.addr.String()
}

func (t *memTransport) OnPeerLeave(fn func(addr *net.UDPAddr)) {
	t.mu.Lock()
	t.leave = fn
	t.mu.Unlock()
}

func (t *memTransport) Health() Health {
	return Health{Addr: t.addr.String(), Peers: len(t.Peers()), HasQuorum: true}
}
