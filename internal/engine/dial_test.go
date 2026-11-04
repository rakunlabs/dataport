package engine

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

// socksServer is a minimal SOCKS5 proxy for tests: CONNECT only, with a
// user and password when set. It counts the connections it relays and
// keeps the last address asked for.
type socksServer struct {
	ln         net.Listener
	user, pass string
	relayed    atomic.Int32
	last       atomic.Value
}

func newSocks(t *testing.T, user, pass string) *socksServer {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	s := &socksServer{ln: ln, user: user, pass: pass}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}

			go s.serve(c)
		}
	}()

	return s
}

func (s *socksServer) url(user, pass string) string {
	if user == "" {
		return "socks5://" + s.ln.Addr().String()
	}

	return "socks5://" + user + ":" + pass + "@" + s.ln.Addr().String()
}

func (s *socksServer) serve(c net.Conn) {
	defer func() { _ = c.Close() }()

	buf := make([]byte, 262)

	// Greeting: version, methods.
	if _, err := io.ReadFull(c, buf[:2]); err != nil {
		return
	}

	if _, err := io.ReadFull(c, buf[:buf[1]]); err != nil {
		return
	}

	if s.user == "" {
		_, _ = c.Write([]byte{5, 0})
	} else {
		_, _ = c.Write([]byte{5, 2})

		// RFC 1929: version, user, password.
		if _, err := io.ReadFull(c, buf[:2]); err != nil {
			return
		}

		user := make([]byte, buf[1])
		_, _ = io.ReadFull(c, user)
		_, _ = io.ReadFull(c, buf[:1])
		pass := make([]byte, buf[0])
		_, _ = io.ReadFull(c, pass)

		if string(user) != s.user || string(pass) != s.pass {
			_, _ = c.Write([]byte{1, 1})

			return
		}

		_, _ = c.Write([]byte{1, 0})
	}

	// Request: version, CONNECT, reserved, address type.
	if _, err := io.ReadFull(c, buf[:4]); err != nil {
		return
	}

	var host string

	switch buf[3] {
	case 1:
		_, _ = io.ReadFull(c, buf[:4])
		host = net.IP(buf[:4]).String()
	case 3:
		_, _ = io.ReadFull(c, buf[:1])
		name := make([]byte, buf[0])
		_, _ = io.ReadFull(c, name)
		host = string(name)
	case 4:
		_, _ = io.ReadFull(c, buf[:16])
		host = net.IP(buf[:16]).String()
	}

	_, _ = io.ReadFull(c, buf[:2])
	addr := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(buf[:2]))))
	s.last.Store(addr)

	target, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		_, _ = c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})

		return
	}
	defer func() { _ = target.Close() }()

	_, _ = c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	s.relayed.Add(1)

	done := make(chan struct{}, 2)

	go func() { _, _ = io.Copy(target, c); done <- struct{}{} }()
	go func() { _, _ = io.Copy(c, target); done <- struct{}{} }()

	<-done
}

func TestParseProxy(t *testing.T) {
	for _, tc := range []struct {
		in, err string
	}{
		{"socks5://proxy:1080", ""},
		{"socks5h://u:p@proxy:1080", ""},
		{"http://proxy:3128", "socks5://"},
		{"socks5://proxy", "host and a port"},
		{"socks5://proxy:1080/x", "only a host"},
		{"socks5://proxy:1080?a=b", "only a host"},
	} {
		_, err := ParseProxy(tc.in)
		if (tc.err == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.err)) {
			t.Errorf("ParseProxy(%q) = %v, want %q", tc.in, err, tc.err)
		}
	}

	if got := RedactProxy("socks5://u:secret@proxy:1080"); strings.Contains(got, "secret") {
		t.Errorf("RedactProxy kept the password: %s", got)
	}
}

// A database type without a dial hook refuses a proxy instead of
// connecting around it.
func TestProxyRefusedWhereUnsupported(t *testing.T) {
	c := &store.Connection{DBType: "sqlite", ProxyURL: "socks5://127.0.0.1:1"}
	if _, err := dialer(c); err == nil {
		t.Fatal("sqlite accepted a proxy")
	}
}

// The dialer reaches a plain TCP server through the proxy, with and
// without an account, and fails with a wrong password.
func TestDialerThroughSocks(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = echo.Close() })

	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}

			go func() { _, _ = io.Copy(c, c); _ = c.Close() }()
		}
	}()

	open := newSocks(t, "", "")
	locked := newSocks(t, "dp", "s3cret")

	try := func(proxyURL string) error {
		dial, err := dialer(&store.Connection{DBType: "postgres", ProxyURL: proxyURL})
		if err != nil {
			return err
		}

		conn, err := dial(context.Background(), "tcp", echo.Addr().String())
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close() }()

		_, _ = conn.Write([]byte("ping"))
		got := make([]byte, 4)

		if _, err := io.ReadFull(conn, got); err != nil || string(got) != "ping" {
			t.Errorf("echo through %s: %q %v", proxyURL, got, err)
		}

		return nil
	}

	if err := try(open.url("", "")); err != nil || open.relayed.Load() != 1 {
		t.Errorf("open proxy: %v, relayed %d", err, open.relayed.Load())
	}

	if err := try(locked.url("dp", "s3cret")); err != nil || locked.relayed.Load() != 1 {
		t.Errorf("proxy with an account: %v, relayed %d", err, locked.relayed.Load())
	}

	if err := try(locked.url("dp", "wrong")); err == nil {
		t.Error("a wrong proxy password connected")
	} else if strings.Contains(err.Error(), "wrong") {
		t.Errorf("the error shows the password: %v", err)
	}

	// socks5h hands the name to the proxy; socks5 resolves it here.
	_ = try("socks5h://" + open.ln.Addr().String())
	if last, _ := open.last.Load().(string); last != echo.Addr().String() {
		t.Errorf("socks5h asked for %s", last)
	}
}

// The engine reads a stored, encrypted proxy and opens the pool through it.
func TestManagerUnsealsProxy(t *testing.T) {
	m := NewManager(store.Limits{MaxOpen: 1}, time.Minute)
	defer m.Close()

	c := &store.Connection{DBType: "postgres", ProxyEnc: "sealed"}

	if _, err := m.withProxy(c); err == nil {
		t.Fatal("a sealed proxy was used without an unseal function")
	}

	m.SetUnseal(func(v string) (string, error) {
		if v != "sealed" {
			t.Errorf("unseal got %q", v)
		}

		return "socks5://127.0.0.1:1080", nil
	})

	out, err := m.withProxy(c)
	if err != nil || out.ProxyURL != "socks5://127.0.0.1:1080" || c.ProxyURL != "" {
		t.Errorf("withProxy = %+v, %v (the stored connection must not change)", out, err)
	}
}

// Without a proxy the drivers keep their own dialers: ClickHouse does TLS
// only with its own, and pgx keeps its connect_timeout and keep-alive.
func TestNoProxyKeepsDriverDialers(t *testing.T) {
	if d, err := dialer(&store.Connection{DBType: "postgres"}); err != nil || d != nil {
		t.Fatalf("dialer without a proxy = %v, %v; want nil", d != nil, err)
	}
}

// socks5h hands the host name to the proxy: pgx must not resolve it first,
// or a name only the proxy knows fails before the proxy is asked.
func TestPostgresSocks5hKeepsTheName(t *testing.T) {
	proxy := newSocks(t, "", "")
	c := &store.Connection{DBType: "postgres", Host: "db.only-the-proxy-knows.invalid", Port: 5432, Database: "x", Username: "u",
		Options: map[string]string{"sslmode": "disable", "connect_timeout": "3"}, ProxyURL: "socks5h://" + proxy.ln.Addr().String()}

	db, err := postgresDriver{}.Open(c, "pw")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = db.PingContext(ctx)

	if last, _ := proxy.last.Load().(string); last != "db.only-the-proxy-knows.invalid:5432" {
		t.Errorf("the proxy was asked for %q; the name must reach it unresolved", last)
	}
}

// A secure ClickHouse connection through the proxy still does TLS: the
// first bytes the server sees are a TLS ClientHello.
func TestClickHouseTLSThroughProxy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	first := make(chan byte, 1)

	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()

		b := make([]byte, 1)
		if _, err := io.ReadFull(c, b); err == nil {
			first <- b[0]
		}
	}()

	proxy := newSocks(t, "", "")
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	p, _ := strconv.Atoi(port)

	c := &store.Connection{DBType: "clickhouse", Host: "127.0.0.1", Port: p, Database: "x", Username: "u",
		Options: map[string]string{"secure": "skip-verify"}, ProxyURL: proxy.url("", "")}

	db, err := clickhouseDriver{}.Open(c, "pw")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() { _ = db.PingContext(ctx) }()

	select {
	case b := <-first:
		// 0x16 opens a TLS handshake record.
		if b != 0x16 {
			t.Fatalf("first byte %#x: the connection is not TLS", b)
		}
	case <-ctx.Done():
		t.Fatal("nothing reached the server through the proxy")
	}
}

// A SQL Server named instance is refused through a proxy: it is found over
// UDP, which SOCKS does not carry.
func TestMSSQLInstanceRefusedThroughProxy(t *testing.T) {
	c := &store.Connection{DBType: "mssql", Options: map[string]string{"instance": "SQLEXPRESS"}, ProxyURL: "socks5://127.0.0.1:1080"}
	if _, err := dialer(c); err == nil || !strings.Contains(err.Error(), "named instance") {
		t.Fatalf("named instance through a proxy: %v", err)
	}
}
