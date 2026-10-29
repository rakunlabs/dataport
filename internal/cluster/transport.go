package cluster

import (
	"context"
	"errors"
	"io"
	"net"
	"os"

	"github.com/rakunlabs/alan"
)

// Transport is what the router needs from the cluster: messages, streams
// and locks between replicas. alan provides it; tests use one in memory.
type Transport interface {
	// HandleRequest answers requests of a type; the answer goes back to the
	// sender. Handlers run in a goroutine of their own.
	HandleRequest(typ string, fn func(ctx context.Context, from *net.UDPAddr, data []byte) []byte)
	// HandleMessage receives one-way messages of a type.
	HandleMessage(typ string, fn func(ctx context.Context, from *net.UDPAddr, data []byte))
	// HandleStream receives streams of a type; the stream ends when fn
	// returns.
	HandleStream(typ string, fn func(ctx context.Context, from *net.UDPAddr, body io.Reader))
	// Broadcast asks every peer and returns the answers that came back.
	Broadcast(ctx context.Context, typ string, data []byte) []Answer
	// Request asks one peer and returns its answer.
	Request(ctx context.Context, to *net.UDPAddr, typ string, data []byte) ([]byte, error)
	SendTo(ctx context.Context, to *net.UDPAddr, typ string, data []byte) error
	SendStream(ctx context.Context, to *net.UDPAddr, typ string, body io.Reader) error
	Peers() []*net.UDPAddr
	TryLock(ctx context.Context, key string) bool
	Unlock(ctx context.Context, key string) error
	// OnPeerLeave is told about every peer that left.
	OnPeerLeave(fn func(addr *net.UDPAddr))
	Health() Health
}

// Answer is a peer's answer to a broadcast.
type Answer struct {
	From *net.UDPAddr
	Data []byte
}

// Health describes the membership of the cluster as this replica sees it.
type Health struct {
	Addr string `json:"addr"`
	// Replicas is the configured size, this replica included; zero means
	// no quorum is required.
	Replicas   int  `json:"replicas"`
	Peers      int  `json:"peers"`
	QuorumSize int  `json:"quorum_size"`
	HasQuorum  bool `json:"has_quorum"`
	Secure     bool `json:"secure"`
}

// alanTransport is Transport over alan.
type alanTransport struct {
	a *alan.Alan
}

func (t *alanTransport) HandleRequest(typ string, fn func(ctx context.Context, from *net.UDPAddr, data []byte) []byte) {
	t.a.Handle(typ, func(ctx context.Context, msg alan.Message) {
		if !msg.IsRequest() {
			return
		}

		// alan runs the handlers of one peer one after another; a slow
		// answer must not hold up the next message.
		go func() {
			_, _ = t.a.Reply(ctx, msg, fn(ctx, msg.Addr, msg.Data))
		}()
	})
}

func (t *alanTransport) HandleMessage(typ string, fn func(ctx context.Context, from *net.UDPAddr, data []byte)) {
	t.a.Handle(typ, func(ctx context.Context, msg alan.Message) {
		go fn(ctx, msg.Addr, msg.Data)
	})
}

func (t *alanTransport) HandleStream(typ string, fn func(ctx context.Context, from *net.UDPAddr, body io.Reader)) {
	t.a.HandleStream(typ, func(ctx context.Context, msg alan.Message, body io.Reader) error {
		fn(ctx, msg.Addr, body)

		return nil
	})
}

func (t *alanTransport) Broadcast(ctx context.Context, typ string, data []byte) []Answer {
	replies, _ := t.a.SendAndWaitReply(ctx, typ, data)

	out := make([]Answer, 0, len(replies))
	for _, r := range replies {
		out = append(out, Answer{From: r.Addr, Data: r.Data})
	}

	return out
}

func (t *alanTransport) Request(ctx context.Context, to *net.UDPAddr, typ string, data []byte) ([]byte, error) {
	reply, err := t.a.SendToAndWaitReply(ctx, to, typ, data)
	if err != nil {
		if errors.Is(err, alan.ErrMessageTooLarge) {
			return nil, errTooLarge
		}

		return nil, err
	}

	return reply.Data, nil
}

func (t *alanTransport) SendTo(ctx context.Context, to *net.UDPAddr, typ string, data []byte) error {
	_, err := t.a.SendTo(ctx, to, typ, data)

	return err
}

func (t *alanTransport) SendStream(ctx context.Context, to *net.UDPAddr, typ string, body io.Reader) error {
	_, err := t.a.SendToStream(ctx, to, typ, body)

	return err
}

func (t *alanTransport) Peers() []*net.UDPAddr { return t.a.Peers() }

func (t *alanTransport) TryLock(ctx context.Context, key string) bool { return t.a.TryLock(ctx, key) }

func (t *alanTransport) Unlock(ctx context.Context, key string) error { return t.a.Unlock(ctx, key) }

func (t *alanTransport) OnPeerLeave(fn func(addr *net.UDPAddr)) { t.a.OnPeerLeave(fn) }

func (t *alanTransport) Health() Health {
	h := Health{
		Replicas:   t.a.Config().Replicas,
		Peers:      t.a.PeerCount(),
		QuorumSize: t.a.QuorumSize(),
		HasQuorum:  t.a.HasQuorum(),
		Secure:     t.a.IsSecure(),
	}

	if addr, ok := t.a.LocalAddr().(*net.UDPAddr); ok && addr != nil {
		h.Addr = addr.String()

		// A replica listening on every interface reports the address its
		// host name resolves to, as the peers see it.
		if addr.IP.IsUnspecified() {
			if ip := hostIP(); ip != nil {
				h.Addr = (&net.UDPAddr{IP: ip, Port: addr.Port}).String()
			}
		}
	}

	return h
}

func hostIP() net.IP {
	name, err := os.Hostname()
	if err != nil {
		return nil
	}

	ips, err := net.LookupIP(name)
	if err != nil {
		return nil
	}

	for _, ip := range ips {
		if !ip.IsLoopback() {
			return ip
		}
	}

	return nil
}
