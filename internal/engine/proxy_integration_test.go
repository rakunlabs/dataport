//go:build integration

package engine

import (
	"context"
	"testing"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

// Postgres from env/compose.yaml, reached only through a SOCKS5 proxy with
// an account: the query runs and the proxy relayed the connection.
func TestIntegrationPostgresThroughProxy(t *testing.T) {
	m := NewManager(store.Limits{MaxOpen: 2}, time.Minute)
	defer m.Close()

	direct := &store.Connection{ID: "pg-direct", DBType: "postgres", Host: "127.0.0.1", Port: 55432, Database: "postgres", Username: "postgres",
		Options: map[string]string{"sslmode": "disable"}, UpdatedAt: 1}
	pw := env("PG_PASSWORD", "pw")

	if err := m.Test(context.Background(), direct, pw); err != nil {
		t.Skip("postgres is not reachable:", err)
	}

	proxy := newSocks(t, "dp", "s3cret")

	c := *direct
	c.ID = "pg-proxy"
	c.ProxyURL = proxy.url("dp", "s3cret")

	if err := m.Test(context.Background(), &c, pw); err != nil {
		t.Fatalf("test through the proxy: %v", err)
	}

	res := run(t, m, &c, pw, "SELECT 41 + 1 AS n", true)
	if got := res.Results[0].Rows[0][0]; got != int32(42) && got != int64(42) {
		t.Errorf("query through the proxy returned %v (%T)", got, got)
	}

	if proxy.relayed.Load() < 2 {
		t.Errorf("the proxy relayed %d connections, want the test and the pool", proxy.relayed.Load())
	}

	bad := c
	bad.ID = "pg-proxy-bad"
	bad.ProxyURL = proxy.url("dp", "wrong")

	if err := m.Test(context.Background(), &bad, pw); err == nil {
		t.Error("a wrong proxy password connected")
	}
}

// The other TCP drivers through the same proxy: each must connect only
// through it.
func TestIntegrationDriversThroughProxy(t *testing.T) {
	for _, tc := range []struct {
		conn store.Connection
		pw   string
		sql  string
	}{
		{store.Connection{ID: "my-p", DBType: "mysql", Host: "127.0.0.1", Port: 53306, Database: "app", Username: "root"}, env("MYSQL_PASSWORD", "pw"), "SELECT 1"},
		{store.Connection{ID: "ch-p", DBType: "clickhouse", Host: "127.0.0.1", Port: 59000, Database: "app", Username: "ch"}, env("CH_PASSWORD", "pw"), "SELECT 1"},
		{store.Connection{ID: "ms-p", DBType: "mssql", Host: "127.0.0.1", Port: 51433, Database: "master", Username: "sa",
			Options: map[string]string{"encrypt": "disable"}}, env("MSSQL_PASSWORD", "Str0ng!Passw0rd"), "SELECT 1"},
	} {
		t.Run(tc.conn.DBType, func(t *testing.T) {
			m := NewManager(store.Limits{MaxOpen: 1}, time.Minute)
			defer m.Close()

			direct := tc.conn
			direct.UpdatedAt = 1

			if err := m.Test(context.Background(), &direct, tc.pw); err != nil {
				t.Skipf("%s is not reachable: %v", tc.conn.DBType, err)
			}

			proxy := newSocks(t, "dp", "s3cret")

			c := direct
			c.ID += "-via"
			c.ProxyURL = proxy.url("dp", "s3cret")

			if err := m.Test(context.Background(), &c, tc.pw); err != nil {
				t.Fatalf("test through the proxy: %v", err)
			}

			run(t, m, &c, tc.pw, tc.sql, true)

			if proxy.relayed.Load() == 0 {
				t.Error("the proxy relayed nothing")
			}
		})
	}
}
