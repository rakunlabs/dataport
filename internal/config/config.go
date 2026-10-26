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

// Repository is where dataport's source lives.
const Repository = "https://github.com/rakunlabs/dataport"

// BuildInfo describes the running binary. The release build sets version,
// commit and date; others fall back to what Go recorded.
type BuildInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	// Cgo is set in builds with cgo, which have the ODBC and DuckDB drivers.
	Cgo        bool   `json:"cgo"`
	Repository string `json:"repository"`
}

// Build is filled in by main before the server starts.
var Build = BuildInfo{Version: "v0.0.0", Repository: Repository}

type Config struct {
	LogLevel string `cfg:"log_level,noprefix" default:"info"`

	// DataDir holds the SQLite database and the generated secret key.
	DataDir string `cfg:"data_dir" default:"./data"`

	Server    Server      `cfg:"server"`
	Store     Store       `cfg:"store"`
	Auth      Auth        `cfg:"auth"`
	Query     Query       `cfg:"query"`
	MCP       MCP         `cfg:"mcp"`
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
	// DSN is the connection string. For sqlite it is the database file, a
	// path or a file: URI; empty means <data_dir>/dataport.db.
	DSN string `cfg:"dsn" log:"-"`
	// MigrateDSN, for postgres, is a separate connection used only to create
	// and migrate the tables, for environments where the migration account
	// differs from the one the application runs with. Empty uses DSN.
	MigrateDSN string `cfg:"migrate_dsn" log:"-"`
	// TablePrefix is prepended to every table name of the application store,
	// so dataport's tables do not mix with others in a shared schema.
	TablePrefix string `cfg:"table_prefix" default:"dataport_"`
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
	DefaultMaxRows int `cfg:"default_max_rows" default:"200"`
	// DefaultTimeout limits a run when the connection sets no timeout; zero
	// means no limit. Users may still stop a run, or choose a limit per tab.
	DefaultTimeout time.Duration `cfg:"default_timeout" default:"0s"`
	// MaxTimeout caps any timeout a connection or a user chooses; zero
	// means no cap.
	MaxTimeout time.Duration `cfg:"max_timeout" default:"0s"`
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
	// FileDir is the directory file databases (SQLite, DuckDB) are opened
	// from. Empty disables them. The data directory is always refused.
	FileDir string `cfg:"file_dir"`
}

// MCP serves the Model Context Protocol at <base_path>/mcp. Clients
// authenticate with a personal access token and see what their user sees.
type MCP struct {
	Enabled bool `cfg:"enabled" default:"true"`
	// Stateless keeps no MCP session on the server, for several replicas
	// behind a load balancer without sticky sessions. Production writes
	// then cannot be confirmed through the client and are refused.
	Stateless bool `cfg:"stateless"`
	// MaxRows caps rows returned to a model per statement, below the
	// connection's own cap.
	MaxRows int `cfg:"max_rows" default:"200"`
	// MaxCellChars shortens long text values in results.
	MaxCellChars int `cfg:"max_cell_chars" default:"2000"`
	// MaxSchemaTables caps the tables described by schema_context.
	MaxSchemaTables int `cfg:"max_schema_tables" default:"100"`
	// TokenTTL is the longest lifetime a new access token may have; zero
	// allows tokens that never expire.
	TokenTTL time.Duration `cfg:"token_ttl" default:"2160h"`
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
