package main

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"runtime/debug"

	"github.com/rakunlabs/into"
	"github.com/rakunlabs/logi"
	"github.com/rakunlabs/tell"

	"github.com/rakunlabs/dataport/internal/auth"
	"github.com/rakunlabs/dataport/internal/config"
	"github.com/rakunlabs/dataport/internal/crypto"
	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/server"
	"github.com/rakunlabs/dataport/internal/sso"
	"github.com/rakunlabs/dataport/internal/store"
)

var (
	name    = "dataport"
	version = "v0.0.0"
	commit  = "-"
	date    = "-"
)

func main() {
	config.Service = name + "/" + version
	config.Build = buildInfo()

	into.Init(run,
		into.WithLogger(logi.InitializeLog(logi.WithCaller(false))),
		into.WithMsgf("%s [%s] commit=%s date=%s", name, version, commit, date),
	)
}

func run(ctx context.Context) error {
	cfg, err := config.Load(ctx)
	if err != nil {
		return err
	}

	collector, err := tell.New(ctx, cfg.Telemetry)
	if err != nil {
		return fmt.Errorf("init telemetry; %w", err)
	}
	defer func() { _ = collector.Shutdown() }()

	// A generated key lives in the local data directory. Replicas sharing a
	// PostgreSQL store would each make their own and fail to read the
	// passwords the others stored.
	if cfg.Auth.SecretKey == "" && cfg.Store.Type != "sqlite" && cfg.Store.Type != "" {
		slog.Warn("DATAPORT_AUTH_SECRET_KEY is not set; set the same key on every replica that shares the store")
	}

	key, err := crypto.LoadOrCreateKey(cfg.Auth.SecretKey, cfg.DataDir)
	if err != nil {
		return err
	}

	cipher, err := crypto.New(key)
	if err != nil {
		return fmt.Errorf("init cipher; %w", err)
	}

	st, err := store.New(ctx, cfg.Store, cfg.DataDir)
	if err != nil {
		return fmt.Errorf("init store; %w", err)
	}
	defer func() { _ = st.Close() }()

	if err := bootstrapAdmin(ctx, st, cfg.Auth); err != nil {
		return fmt.Errorf("bootstrap admin; %w", err)
	}

	au := auth.New(st, cfg.Auth.SessionTTL)
	go au.Janitor(ctx)

	if err := engine.ConfigureFiles(cfg.Query.FileDir, cfg.DataDir); err != nil {
		return err
	}

	en := engine.NewManager(store.Limits{
		MaxOpen:        cfg.Query.PoolMaxOpen,
		MaxIdle:        cfg.Query.PoolMaxIdle,
		MaxLifetimeSec: int(cfg.Query.PoolMaxLifetime.Seconds()),
		MaxIdleTimeSec: int(cfg.Query.PoolMaxIdleTime.Seconds()),
	}, cfg.Query.PoolIdleTimeout)
	defer en.Close()

	go en.Janitor(ctx)

	sm := sso.New(st, cipher, cfg.Server.BasePath, cfg.Auth.ExternalURL)
	if err := sm.Load(ctx); err != nil {
		return fmt.Errorf("load sso providers; %w", err)
	}

	srv, err := server.New(cfg, st, au, cipher, en, sm)
	if err != nil {
		return fmt.Errorf("init server; %w", err)
	}

	return srv.Start(ctx)
}

// bootstrapAdmin creates the first admin when there are no users yet.
func bootstrapAdmin(ctx context.Context, st *store.Store, cfg config.Auth) error {
	n, err := st.CountUsers(ctx)
	if err != nil {
		return err
	}

	if n > 0 {
		return nil
	}

	password := cfg.AdminPassword
	generated := password == ""

	if generated {
		password = auth.RandomPassword()
	} else if err := auth.ValidatePassword(password); err != nil {
		return err
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}

	if err := st.CreateUser(ctx, &store.User{
		Username:     cfg.AdminUsername,
		DisplayName:  "Administrator",
		PasswordHash: hash,
		Role:         store.RoleAdmin,
	}); err != nil {
		return err
	}

	if generated {
		slog.Warn("created the first admin with a generated password, change it after logging in",
			"username", cfg.AdminUsername, "password", password)
	} else {
		slog.Info("created the first admin", "username", cfg.AdminUsername)
	}

	return nil
}

// buildInfo reports the release values set at link time, or what Go
// recorded for a local build such as go run.
func buildInfo() config.BuildInfo {
	b := config.BuildInfo{
		Version:    version,
		Commit:     commit,
		Date:       date,
		GoVersion:  runtime.Version(),
		OS:         runtime.GOOS,
		Arch:       runtime.GOARCH,
		Repository: config.Repository,
	}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return b
	}

	// A release sets the commit at link time; only a local build reads it,
	// and whether the tree had changes, from what Go recorded.
	local := b.Commit == "" || b.Commit == "-"
	dirty := false

	for _, s := range info.Settings {
		switch s.Key {
		case "CGO_ENABLED":
			b.Cgo = s.Value == "1"
		case "vcs.revision":
			if local {
				b.Commit = s.Value[:min(len(s.Value), 7)]
			}
		case "vcs.time":
			if b.Date == "" || b.Date == "-" {
				b.Date = s.Value
			}
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}

	if local && dirty && b.Commit != "-" {
		b.Commit += "-dirty"
	}

	return b
}
