package engine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"

	"github.com/rakunlabs/dataport/internal/store"
)

// A connection may be opened through a SOCKS5 proxy. The drivers take a
// dial function, so the proxy covers every database that talks TCP; a file
// database or ODBC, where another library opens the connection, refuses it.

// dialTimeout bounds reaching the proxy and, through it, the database.
const dialTimeout = 10 * time.Second

// DialFunc opens a TCP connection to an address.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// proxyDrivers are the types that can connect through a proxy.
var proxyDrivers = map[string]bool{
	"postgres": true, "mysql": true, "mariadb": true, "mssql": true,
	"oracle": true, "clickhouse": true, "trino": true,
}

// SupportsProxy tells whether a database type can connect through a proxy.
func SupportsProxy(dbType string) bool { return proxyDrivers[dbType] }

// ParseProxy reads a proxy URL: socks5://[user:password@]host:port, or
// socks5h:// to let the proxy resolve the database's host name.
func ParseProxy(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, errors.New("the proxy is not a valid URL")
	}

	if u.Scheme != "socks5" && u.Scheme != "socks5h" {
		return nil, errors.New("the proxy must be a socks5:// URL, such as socks5://user:password@proxy.example.com:1080")
	}

	if u.Hostname() == "" || u.Port() == "" {
		return nil, errors.New("the proxy needs a host and a port, such as socks5://proxy.example.com:1080")
	}

	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("the proxy URL takes only a host, a port and an account")
	}

	return u, nil
}

// RedactProxy is a proxy URL without its password, for showing.
func RedactProxy(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || raw == "" {
		return ""
	}

	if u.User != nil {
		if _, ok := u.User.Password(); ok {
			u.User = url.UserPassword(u.User.Username(), "xxxxx")
		}
	}

	return u.String()
}

// dialer is how a connection reaches its database through the proxy in
// c.ProxyURL. Without a proxy it is nil: the drivers keep their own dialers,
// with their own timeouts, keep-alives and TLS.
func dialer(c *store.Connection) (DialFunc, error) {
	if c.ProxyURL == "" {
		return nil, nil
	}

	direct := &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}

	if !SupportsProxy(c.DBType) {
		return nil, fmt.Errorf("%s connections cannot use a proxy", c.DBType)
	}

	if c.DBType == "mssql" && strings.TrimSpace(c.Options["instance"]) != "" {
		return nil, errors.New("a SQL Server named instance cannot be found through a proxy; enter its port instead of the instance name")
	}

	u, err := ParseProxy(c.ProxyURL)
	if err != nil {
		return nil, err
	}

	var auth *proxy.Auth
	if u.User != nil {
		pw, _ := u.User.Password()
		auth = &proxy.Auth{User: u.User.Username(), Password: pw}
	}

	d, err := proxy.SOCKS5("tcp", u.Host, auth, direct)
	if err != nil {
		return nil, fmt.Errorf("proxy; %w", err)
	}

	cd, ok := d.(proxy.ContextDialer)
	if !ok {
		return nil, errors.New("the proxy dialer cannot be cancelled")
	}

	// socks5 resolves the database's host here, socks5h on the proxy.
	resolveHere := u.Scheme == "socks5"

	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(ctx, dialTimeout)
		defer cancel()

		if resolveHere {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}

			if net.ParseIP(host) == nil {
				ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
				if err != nil {
					return nil, err
				}

				if len(ips) == 0 {
					return nil, fmt.Errorf("no address for %s", host)
				}

				addr = net.JoinHostPort(ips[0].IP.String(), port)
			}
		}

		conn, err := cd.DialContext(ctx, network, addr)
		if err != nil {
			return nil, fmt.Errorf("through the proxy %s; %w", u.Host, err)
		}

		return conn, nil
	}, nil
}

// httpClient is the HTTP client of a driver that talks HTTP, through the
// connection's dialer.
func httpClient(dial DialFunc) *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	t.DialContext = dial

	return &http.Client{Transport: t}
}

// dialFunc is a DialFunc as the Dialer interface some drivers take.
type dialFunc DialFunc

func (f dialFunc) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return f(ctx, network, addr)
}

// hostDialFunc is a dialFunc that also names the host, so SQL Server's
// driver hands the name to it instead of resolving it first: socks5h then
// leaves the name to the proxy, and socks5 resolves it in the dialer.
type hostDialFunc struct {
	dialFunc
	host string
}

func (d hostDialFunc) HostName() string { return d.host }

// proxyResolves tells whether the connection's proxy resolves host names
// itself (socks5h).
func proxyResolves(c *store.Connection) bool {
	u, err := url.Parse(c.ProxyURL)

	return err == nil && u.Scheme == "socks5h"
}
