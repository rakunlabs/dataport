package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/rakunlabs/dataport/internal/config"
	"github.com/rakunlabs/dataport/internal/store"
)

// Server settings an admin may change in the UI. They are not configured:
// each starts from its built-in default, and a value saved here wins until
// it is reset. Most apply at once; a few shape the routes and apply after a
// restart.

const runtimeSetting = "server_settings"

type settingKind string

const (
	kindInt      settingKind = "int"
	kindDuration settingKind = "duration"
	kindBool     settingKind = "bool"
)

type settingField struct {
	Key     string      `json:"key"`
	Section string      `json:"section"`
	Kind    settingKind `json:"kind"`
	// Unit is how the UI shows the value: s, m, h or d for durations, which
	// travel as seconds, and MiB for byte counts.
	Unit    string `json:"unit,omitempty"`
	Min     int64  `json:"min"`
	Max     int64  `json:"max"`
	Restart bool   `json:"restart,omitempty"`
	// ptr points at the field in a configuration.
	ptr func(*config.Config) any
}

var settingFields = []settingField{
	{Key: "query.default_max_rows", Section: "query", Kind: kindInt, Min: 1, Max: 10_000_000, ptr: func(c *config.Config) any { return &c.Query.DefaultMaxRows }},
	{Key: "query.max_rows_limit", Section: "query", Kind: kindInt, Min: 0, Max: 10_000_000, ptr: func(c *config.Config) any { return &c.Query.MaxRowsLimit }},
	{Key: "query.default_timeout", Section: "query", Kind: kindDuration, Unit: "s", Min: 0, Max: 7 * 86400, ptr: func(c *config.Config) any { return &c.Query.DefaultTimeout }},
	{Key: "query.max_timeout", Section: "query", Kind: kindDuration, Unit: "s", Min: 0, Max: 7 * 86400, ptr: func(c *config.Config) any { return &c.Query.MaxTimeout }},
	{Key: "query.pool_max_open", Section: "pool", Kind: kindInt, Min: 1, Max: maxPoolOpen, ptr: func(c *config.Config) any { return &c.Query.PoolMaxOpen }},
	{Key: "query.pool_max_idle", Section: "pool", Kind: kindInt, Min: 0, Max: maxPoolOpen, ptr: func(c *config.Config) any { return &c.Query.PoolMaxIdle }},
	{Key: "query.pool_max_lifetime", Section: "pool", Kind: kindDuration, Unit: "m", Min: 0, Max: maxPoolDuration, ptr: func(c *config.Config) any { return &c.Query.PoolMaxLifetime }},
	{Key: "query.pool_max_idle_time", Section: "pool", Kind: kindDuration, Unit: "m", Min: 0, Max: maxPoolDuration, ptr: func(c *config.Config) any { return &c.Query.PoolMaxIdleTime }},
	{Key: "query.pool_idle_timeout", Section: "pool", Kind: kindDuration, Unit: "m", Min: 0, Max: 7 * 86400, ptr: func(c *config.Config) any { return &c.Query.PoolIdleTimeout }},
	{Key: "notebook.max_concurrent", Section: "notebook", Kind: kindInt, Min: 1, Max: 1000, ptr: func(c *config.Config) any { return &c.Notebook.MaxConcurrent }},
	{Key: "notebook.max_duration", Section: "notebook", Kind: kindDuration, Unit: "m", Min: 60, Max: 7 * 86400, ptr: func(c *config.Config) any { return &c.Notebook.MaxDuration }},
	{Key: "notebook.max_output_bytes", Section: "notebook", Kind: kindInt, Unit: "MiB", Min: 1 << 20, Max: 1 << 30, ptr: func(c *config.Config) any { return &c.Notebook.MaxOutputBytes }},
	{Key: "connections.user_create", Section: "connections", Kind: kindBool, ptr: func(c *config.Config) any { return &c.Connections.UserCreate }},
	{Key: "connections.user_files", Section: "connections", Kind: kindBool, ptr: func(c *config.Config) any { return &c.Connections.UserFiles }},
	{Key: "mcp.enabled", Section: "mcp", Kind: kindBool, Restart: true, ptr: func(c *config.Config) any { return &c.MCP.Enabled }},
	{Key: "mcp.stateless", Section: "mcp", Kind: kindBool, Restart: true, ptr: func(c *config.Config) any { return &c.MCP.Stateless }},
	{Key: "mcp.max_rows", Section: "mcp", Kind: kindInt, Min: 1, Max: 100_000, ptr: func(c *config.Config) any { return &c.MCP.MaxRows }},
	{Key: "mcp.max_cell_chars", Section: "mcp", Kind: kindInt, Min: 0, Max: 1_000_000, ptr: func(c *config.Config) any { return &c.MCP.MaxCellChars }},
	{Key: "mcp.max_schema_tables", Section: "mcp", Kind: kindInt, Min: 0, Max: 10_000, ptr: func(c *config.Config) any { return &c.MCP.MaxSchemaTables }},
	{Key: "mcp.token_ttl", Section: "mcp", Kind: kindDuration, Unit: "d", Min: 0, Max: 10 * 365 * 86400, ptr: func(c *config.Config) any { return &c.MCP.TokenTTL }},
}

func settingFieldFor(key string) *settingField {
	for i := range settingFields {
		if settingFields[i].Key == key {
			return &settingFields[i]
		}
	}

	return nil
}

// get returns the value of the field in c as JSON travels it: seconds for
// durations.
func (f *settingField) get(c *config.Config) any {
	switch p := f.ptr(c).(type) {
	case *int:
		return int64(*p)
	case *time.Duration:
		return int64(*p / time.Second)
	case *bool:
		return *p
	}

	return nil
}

// set checks a value from JSON and writes it into c.
func (f *settingField) set(c *config.Config, raw json.RawMessage) error {
	if f.Kind == kindBool {
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return fmt.Errorf("%s must be true or false", f.Key)
		}

		*f.ptr(c).(*bool) = b

		return nil
	}

	var n int64
	if err := json.Unmarshal(raw, &n); err != nil {
		return fmt.Errorf("%s must be a whole number", f.Key)
	}

	if n < f.Min || n > f.Max {
		return fmt.Errorf("%s must be between %d and %d", f.Key, f.Min, f.Max)
	}

	switch p := f.ptr(c).(type) {
	case *int:
		*p = int(n)
	case *time.Duration:
		*p = time.Duration(n) * time.Second
	}

	return nil
}

// apply writes the stored values over a copy of the configuration. Values
// that no longer fit are skipped, so a bad row cannot stop the server.
func applySettings(base *config.Config, saved map[string]json.RawMessage) *config.Config {
	out := *base

	for key, raw := range saved {
		f := settingFieldFor(key)
		if f == nil {
			continue
		}

		if err := f.set(&out, raw); err != nil {
			slog.Warn("ignoring a stored server setting", "key", key, "error", err)
		}
	}

	out.Notebook.Normalize()

	return &out
}

func (s *Server) savedSettings(ctx context.Context) (map[string]json.RawMessage, error) {
	raw, err := s.store.GetSetting(ctx, runtimeSetting)
	if errors.Is(err, store.ErrNotFound) {
		return map[string]json.RawMessage{}, nil
	}

	if err != nil {
		return nil, err
	}

	saved := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(raw), &saved); err != nil {
		return nil, fmt.Errorf("decode server settings; %w", err)
	}

	return saved, nil
}

// loadSettings reads the stored values into use. Values that need a
// restart are taken only at start.
func (s *Server) loadSettings(ctx context.Context, start bool) error {
	saved, err := s.savedSettings(ctx)
	if err != nil {
		return err
	}

	s.useSettings(applySettings(&s.base, saved), start)

	return nil
}

func (s *Server) useSettings(next *config.Config, start bool) {
	s.settingsMu.Lock()

	if !start {
		for _, f := range settingFields {
			if f.Restart {
				// Keep what the server runs with.
				switch p := f.ptr(next).(type) {
				case *bool:
					*p = *f.ptr(s.cfg).(*bool)
				}
			}
		}
	}

	s.cfg.Query, s.cfg.MCP, s.cfg.Notebook, s.cfg.Connections = next.Query, next.MCP, next.Notebook, next.Connections
	q := s.cfg.Query
	s.settingsMu.Unlock()

	s.store.SetPersonalConnections(next.Connections.UserCreate)

	if s.local != nil {
		s.engine.SetDefaults(store.Limits{
			MaxOpen:        q.PoolMaxOpen,
			MaxIdle:        q.PoolMaxIdle,
			MaxLifetimeSec: int(q.PoolMaxLifetime.Seconds()),
			MaxIdleTimeSec: int(q.PoolMaxIdleTime.Seconds()),
		}, q.PoolIdleTimeout)
	}
}

// reloadSettings picks up values saved on another replica.
func (s *Server) reloadSettings(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.loadSettings(ctx, false); err != nil && ctx.Err() == nil {
				slog.Error("reload server settings", "error", err)
			}

			if err := s.loadExternalURL(ctx); err != nil && ctx.Err() == nil {
				slog.Error("reload external URL", "error", err)
			}
		}
	}
}

func (s *Server) queryCfg() config.Query {
	s.settingsMu.RLock()
	defer s.settingsMu.RUnlock()

	return s.cfg.Query
}

func (s *Server) mcpCfg() config.MCP {
	s.settingsMu.RLock()
	defer s.settingsMu.RUnlock()

	return s.cfg.MCP
}

func (s *Server) connectionsCfg() config.Connections {
	s.settingsMu.RLock()
	defer s.settingsMu.RUnlock()

	return s.cfg.Connections
}

func (s *Server) notebookCfg() config.Notebook {
	s.settingsMu.RLock()
	defer s.settingsMu.RUnlock()

	return s.cfg.Notebook
}

// ////////////////////////////////////////////
// handlers

type settingView struct {
	settingField
	// Value is what is saved, or the starting value when nothing is.
	Value any `json:"value"`
	// Config is the built-in default.
	Config any `json:"config"`
	// Saved is set when an admin saved a value.
	Saved bool `json:"saved"`
	// Running is what the server runs with, for settings that need a restart.
	Running any `json:"running,omitempty"`
}

func (s *Server) settingsView(saved map[string]json.RawMessage) []settingView {
	effective := applySettings(&s.base, saved)

	s.settingsMu.RLock()
	running := *s.cfg
	s.settingsMu.RUnlock()

	out := make([]settingView, 0, len(settingFields))

	for _, f := range settingFields {
		_, ok := saved[f.Key]
		v := settingView{settingField: f, Value: f.get(effective), Config: f.get(&s.base), Saved: ok}

		if f.Restart {
			v.Running = f.get(&running)
		}

		out = append(out, v)
	}

	return out
}

func (s *Server) adminGetSettings(w http.ResponseWriter, r *http.Request) {
	saved, err := s.savedSettings(r.Context())
	if err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, s.settingsView(saved))
}

// adminSetSettings saves the values given; null drops a saved value, so
// the configuration's applies again.
func (s *Server) adminSetSettings(w http.ResponseWriter, r *http.Request) {
	var in map[string]json.RawMessage
	if !readJSON(w, r, &in) {
		return
	}

	saved, err := s.savedSettings(r.Context())
	if err != nil {
		writeStoreError(w, err)

		return
	}

	check := s.base

	for key, raw := range in {
		f := settingFieldFor(key)
		if f == nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown setting %q", key))

			return
		}

		if string(raw) == "null" {
			delete(saved, key)

			continue
		}

		if err := f.set(&check, raw); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())

			return
		}

		saved[key] = raw
	}

	data, err := json.Marshal(saved)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	if err := s.store.SetSetting(r.Context(), runtimeSetting, string(data)); err != nil {
		writeStoreError(w, err)

		return
	}

	s.useSettings(applySettings(&s.base, saved), false)

	writeJSON(w, http.StatusOK, s.settingsView(saved))
}
