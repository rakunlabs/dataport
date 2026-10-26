package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/rakunlabs/ada"
	mfolder "github.com/rakunlabs/ada/handler/folder"
	mlog "github.com/rakunlabs/ada/middleware/log"
	mrecover "github.com/rakunlabs/ada/middleware/recover"
	mrequestid "github.com/rakunlabs/ada/middleware/requestid"
	mserver "github.com/rakunlabs/ada/middleware/server"
	mtelemetry "github.com/rakunlabs/ada/middleware/telemetry"

	"github.com/rakunlabs/dataport/internal/auth"
	"github.com/rakunlabs/dataport/internal/config"
	"github.com/rakunlabs/dataport/internal/crypto"
	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/sso"
	"github.com/rakunlabs/dataport/internal/store"
)

//go:embed all:dist
var uiFS embed.FS

const cookieName = "dataport_session"

type Server struct {
	cfg    *config.Config
	store  *store.Store
	auth   *auth.Auth
	cipher *crypto.Cipher
	engine *engine.Manager
	sso    *sso.Manager
	mux    *ada.Server
}

func New(cfg *config.Config, st *store.Store, au *auth.Auth, ci *crypto.Cipher, en *engine.Manager, sm *sso.Manager) (*Server, error) {
	s := &Server{cfg: cfg, store: st, auth: au, cipher: ci, engine: en, sso: sm}

	mux := ada.New()
	mux.Use(
		mrecover.Middleware(),
		mserver.Middleware(config.Service),
		mrequestid.Middleware(),
		mlog.Middleware(),
		mtelemetry.Middleware(),
		securityHeaders,
	)

	base := mux.Group(cfg.Server.BasePath)

	base.GET("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("OK"))
	})

	api := base.Group("/api/v1", limitBody(4<<20), sameOrigin)

	api.GET("/auth/info", s.authInfo)
	api.POST("/auth/login", s.login)
	api.POST("/auth/logout", s.logout)

	// The provider redirects the browser here, so this route is outside the
	// same-origin check; ada's strategy validates state, nonce and PKCE.
	base.GET("/api/v1/auth/sso/{name}", s.ssoLogin)
	base.GET("/api/v1/auth/sso/callback/{name}", s.ssoLogin)

	user := api.Group("", s.requireUser)
	user.GET("/me", s.me)
	user.GET("/about", s.about)
	user.POST("/me/password", s.changePassword)
	user.GET("/connections", s.listMyConnections)
	user.GET("/connections/{id}/schemas", s.schemas)
	user.GET("/connections/{id}/objects", s.objects)
	user.GET("/connections/{id}/columns", s.columns)
	user.GET("/connections/{id}/structure", s.structure)
	user.GET("/connections/{id}/routines", s.routines)
	user.GET("/connections/{id}/definition", s.definition)
	user.GET("/connections/{id}/diagram", s.diagram)
	user.PUT("/connections/{id}/credentials", s.setCredential)
	user.DELETE("/connections/{id}/credentials", s.deleteCredential)
	user.POST("/connections/{id}/query", s.runQuery)
	user.POST("/connections/{id}/disconnect", s.disconnect)
	user.GET("/connections/{id}/processes", s.processes)
	user.POST("/connections/{id}/processes/kill", s.killProcess)
	user.GET("/connections/{id}/users", s.dbUsers)
	user.GET("/connections/{id}/grants", s.dbGrants)
	user.GET("/connections/{id}/export", s.export)
	user.POST("/connections/{id}/import", s.importRows)
	user.POST("/connections/{id}/transfer", s.transfer)
	user.POST("/connections/{id}/edits", s.applyEdits)
	user.POST("/connections/{id}/edits/preview", s.previewEdits)
	user.POST("/queries/{queryID}/cancel", s.cancelQuery)
	user.GET("/tokens", s.listTokens)
	user.POST("/tokens", s.createToken)
	user.DELETE("/tokens/{id}", s.deleteToken)
	user.GET("/history", s.myHistory)
	user.GET("/saved", s.listSaved)
	user.POST("/saved", s.createSaved)
	user.PUT("/saved/{id}", s.updateSaved)
	user.DELETE("/saved/{id}", s.deleteSaved)

	admin := api.Group("/admin", s.requireUser, s.requireAdmin)
	admin.GET("/drivers", s.drivers)
	admin.GET("/pool-defaults", s.poolDefaults)
	admin.GET("/users", s.listUsers)
	admin.POST("/users", s.createUser)
	admin.PUT("/users/{id}", s.updateUser)
	admin.DELETE("/users/{id}", s.deleteUser)
	admin.GET("/groups", s.listGroups)
	admin.POST("/groups", s.createGroup)
	admin.PUT("/groups/{id}", s.updateGroup)
	admin.DELETE("/groups/{id}", s.deleteGroup)
	admin.GET("/connections", s.adminListConnections)
	admin.POST("/connections", s.adminCreateConnection)
	admin.POST("/connections/test", s.adminTestConnection)
	admin.GET("/connections/{id}", s.adminGetConnection)
	admin.PUT("/connections/{id}", s.adminUpdateConnection)
	admin.DELETE("/connections/{id}", s.adminDeleteConnection)
	admin.GET("/logs", s.adminLogs)
	admin.GET("/sso", s.adminListSSO)
	admin.POST("/sso", s.adminSaveSSO)
	admin.PUT("/sso/{name}", s.adminSaveSSO)
	admin.DELETE("/sso/{name}", s.adminDeleteSSO)
	admin.DELETE("/users/{id}/identities", s.adminUnlinkSSO)

	if cfg.MCP.Enabled {
		base.Handle("/mcp", s.mcpHandler())
	}

	sub, err := fs.Sub(uiFS, "dist")
	if err != nil {
		return nil, err
	}

	folder, err := mfolder.New(&mfolder.Config{
		BasePath:       cfg.Server.BasePath,
		PrefixPath:     cfg.Server.BasePath,
		Index:          true,
		StripIndexName: true,
		SPA:            true,
		CacheRegex: []*mfolder.RegexCacheStore{
			{Regex: `^index\.html$`, CacheControl: "no-store"},
		},
	})
	if err != nil {
		return nil, err
	}

	folder.SetFs(http.FS(sub))
	base.Handle("/*", folder)

	if cfg.Server.BasePath != "" {
		target := cfg.Server.BasePath + "/"
		base.Handle("", http.RedirectHandler(target, http.StatusMovedPermanently))
	}

	s.mux = mux

	return s, nil
}

func (s *Server) Start(ctx context.Context) error {
	addr := net.JoinHostPort(s.cfg.Server.Host, s.cfg.Server.Port)
	slog.Info("starting server", "addr", addr, "base_path", s.cfg.Server.BasePath)

	return s.mux.StartWithContext(ctx, addr)
}

// ////////////////////////////////////////////
// middleware

type ctxKey struct{}

func currentUser(r *http.Request) *store.User {
	u, _ := r.Context().Value(ctxKey{}).(*store.User)

	return u
}

func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(cookieName)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "not logged in")

			return
		}

		user, err := s.auth.Authenticate(r.Context(), cookie.Value)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				slog.Error("authenticate", "error", err)
			}

			s.clearCookie(w, r)
			writeError(w, http.StatusUnauthorized, "session expired")

			return
		}

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, user)))
	})
}

func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u := currentUser(r); u == nil || !u.IsAdmin() {
			writeError(w, http.StatusForbidden, "admin only")

			return
		}

		next.ServeHTTP(w, r)
	})
}

// sameOrigin rejects state-changing requests whose Origin does not match the
// host. Together with the SameSite=Lax cookie this stops cross-site requests.
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if origin := r.Header.Get("Origin"); origin != "" && !originMatches(origin, r) {
				writeError(w, http.StatusForbidden, "cross-origin request")

				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// originMatches reports whether the Origin header names the host the request
// was sent to. Behind a reverse proxy that rewrites Host, the proxy must pass
// the original one in X-Forwarded-Host.
func originMatches(origin string, r *http.Request) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}

	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}

	// X-Forwarded-Host may list several hosts; the first is the client's.
	host, _, _ = strings.Cut(host, ",")

	return strings.EqualFold(u.Host, strings.TrimSpace(host))
}

func limitBody(max int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, max)
			next.ServeHTTP(w, r)
		})
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// ////////////////////////////////////////////
// helpers

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"message": msg})
}

// writeStoreError maps store errors to HTTP responses and hides internal
// details behind a generic message.
func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "name already exists")
	default:
		slog.Error("request failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(v); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "request too large")

			return false
		}

		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))

		return false
	}

	if dec.More() {
		writeError(w, http.StatusBadRequest, "invalid request: trailing data")

		return false
	}

	_, _ = io.Copy(io.Discard, r.Body)

	return true
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return host
}

func (s *Server) isSecure(r *http.Request) bool {
	return s.cfg.Auth.SecureCookie || r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (s *Server) cookiePath() string {
	if s.cfg.Server.BasePath == "" {
		return "/"
	}

	return s.cfg.Server.BasePath + "/"
}

func (s *Server) setCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     s.cookiePath(),
		HttpOnly: true,
		Secure:   s.isSecure(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(s.auth.TTL().Seconds()),
	})
}

func (s *Server) clearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     s.cookiePath(),
		HttpOnly: true,
		Secure:   s.isSecure(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}
