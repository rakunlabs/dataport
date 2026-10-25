package config

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/rakunlabs/chu"
	"github.com/rakunlabs/chu/loader/loaderenv"
	"github.com/rakunlabs/logi"
	"github.com/rakunlabs/tell"

	_ "github.com/rakunlabs/chu/loader/external/loadervault"
)

var (
	ServiceName = "dataport"
	Service     = ServiceName
)

type Config struct {
	LogLevel string `cfg:"log_level,noprefix" default:"info"`

	// DataDir holds the SQLite database and the generated secret key.
	DataDir string `cfg:"data_dir" default:"./data"`

	Server    Server      `cfg:"server"`
	Store     Store       `cfg:"store"`
	Auth      Auth        `cfg:"auth"`
	Query     Query       `cfg:"query"`
	Telemetry tell.Config `cfg:"telemetry,noprefix"`
}

type Server struct {
	Host string `cfg:"host"`
	Port string `cfg:"port" default:"8080"`
	// BasePath serves the application under a sub path such as "/dataport".
	BasePath string `cfg:"base_path"`
}

type Store struct {
	// Type is "sqlite" or "postgres".
	Type string `cfg:"type" default:"sqlite"`
	// DSN is the connection string. For sqlite an empty value means
	// <data_dir>/dataport.db.
	DSN string `cfg:"dsn" log:"-"`
	// TablePrefix is prepended to every table name of the application store.
	TablePrefix string `cfg:"table_prefix" default:"dp_"`
}

type Auth struct {
	// SecretKey encrypts the stored connection passwords. When empty a key is
	// generated once into <data_dir>/secret.key.
	SecretKey string `cfg:"secret_key" log:"-"`

	// AdminUsername and AdminPassword create the first admin when the user
	// table is empty. An empty password generates one and logs it once.
	AdminUsername string `cfg:"admin_username" default:"admin"`
	AdminPassword string `cfg:"admin_password" log:"-"`

	SessionTTL time.Duration `cfg:"session_ttl" default:"12h"`
	// DisablePasswordLogin hides the username and password form, leaving
	// only SSO. Keep it off until an SSO admin can sign in.
	DisablePasswordLogin bool `cfg:"disable_password_login"`
	// ExternalURL is the public origin, such as https://dataport.example.com.
	// SSO uses it for redirect URIs; empty derives it from the request.
	ExternalURL string `cfg:"external_url"`

	// SecureCookie forces the Secure flag on the session cookie. It is also
	// set automatically when the request arrived over TLS or through a proxy
	// that reports X-Forwarded-Proto: https.
	SecureCookie bool `cfg:"secure_cookie"`
}

type Query struct {
	DefaultMaxRows int           `cfg:"default_max_rows" default:"1000"`
	DefaultTimeout time.Duration `cfg:"default_timeout" default:"30s"`
	// MaxRowsLimit caps any per-connection override the admin sets.
	MaxRowsLimit int `cfg:"max_rows_limit" default:"100000"`
	// Pool defaults for connections that leave a limit empty. Each
	// connection, and each user of an own-account connection, has its own
	// pool.
	PoolMaxOpen     int           `cfg:"pool_max_open" default:"5"`
	PoolMaxIdle     int           `cfg:"pool_max_idle" default:"5"`
	PoolMaxLifetime time.Duration `cfg:"pool_max_lifetime" default:"1h"`
	PoolMaxIdleTime time.Duration `cfg:"pool_max_idle_time" default:"5m"`
	// PoolIdleTimeout closes idle target pools after this duration.
	PoolIdleTimeout time.Duration `cfg:"pool_idle_timeout" default:"10m"`
}

func Load(ctx context.Context) (*Config, error) {
	var cfg Config
	if err := chu.Load(ctx, ServiceName, &cfg,
		chu.WithLoaderOption(loaderenv.New(loaderenv.WithPrefix("DATAPORT_"))),
	); err != nil {
		return nil, fmt.Errorf("load config; %w", err)
	}

	cfg.Server.BasePath = normalizeBasePath(cfg.Server.BasePath)

	if err := logi.SetLogLevel(cfg.LogLevel); err != nil {
		return nil, fmt.Errorf("set log level %s; %w", cfg.LogLevel, err)
	}

	slog.Info("loaded configuration", "config", chu.MarshalMap(cfg))

	return &cfg, nil
}

func normalizeBasePath(p string) string {
	p = strings.Trim(strings.TrimSpace(p), "/")
	if p == "" {
		return ""
	}

	return "/" + p
}
