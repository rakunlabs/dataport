package server

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rakunlabs/ada"
	mfolder "github.com/rakunlabs/ada/handler/folder"
	mlog "github.com/rakunlabs/ada/middleware/log"
	mrecover "github.com/rakunlabs/ada/middleware/recover"
	mrequestid "github.com/rakunlabs/ada/middleware/requestid"
	mserver "github.com/rakunlabs/ada/middleware/server"
	mtelemetry "github.com/rakunlabs/ada/middleware/telemetry"

	"github.com/rakunlabs/hardloop"

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
	// Bound scheduled work per instance, including the startup catch-up.
	docDispatch        sync.Mutex
	docJobsMu          sync.Mutex
	docJobs            *hardloop.CronJob
	docJobsSpec        string
	docScheduleChanged chan struct{}
	docRunMu           sync.Mutex
	docRun             docRun
	docRunAverage      int64
	notebookMu         sync.Mutex
	notebookRunning    int
	notebookContext    context.Context
	notebookStop       context.CancelFunc
	notebookWorkers    sync.WaitGroup
	// base is the configuration as loaded; cfg holds it with the settings
	// admins saved over it. settingsMu guards cfg.Query, cfg.MCP,
	// cfg.Notebook and cfg.Connections, which change while running.
	base       config.Config
	settingsMu sync.RWMutex
}

func New(cfg *config.Config, st *store.Store, au *auth.Auth, ci *crypto.Cipher, en *engine.Manager, sm *sso.Manager) (*Server, error) {
	s := &Server{cfg: cfg, base: *cfg, store: st, auth: au, cipher: ci, engine: en, sso: sm, docScheduleChanged: make(chan struct{}, 1)}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := s.loadSettings(ctx, true); err != nil {
		return nil, fmt.Errorf("load server settings; %w", err)
	}

	s.notebookContext, s.notebookStop = context.WithCancel(context.Background())

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
	user.GET("/my-connections", s.listPersonalConnections)
	user.POST("/my-connections", s.createPersonalConnection)
	user.POST("/my-connections/test", s.testPersonalConnection)
	user.PUT("/my-connections/{id}", s.updatePersonalConnection)
	user.DELETE("/my-connections/{id}", s.deletePersonalConnection)
	user.GET("/my-connections/drivers", s.personalDrivers)
	user.GET("/connections/{id}/schemas", s.schemas)
	user.GET("/connections/{id}/objects", s.objects)
	user.GET("/connections/{id}/columns", s.columns)
	user.GET("/connections/{id}/structure", s.structure)
	user.GET("/connections/{id}/routines", s.routines)
	user.GET("/connections/{id}/definition", s.definition)
	user.GET("/connections/{id}/diagram", s.diagram)
	user.GET("/connections/{id}/docs", s.serveDoc)
	user.POST("/connections/{id}/docs/refresh", s.serveDoc)
	user.GET("/docs", s.listDocs)
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
	user.POST("/connections/{id}/generate", s.generate)
	user.POST("/connections/{id}/generate/preview", s.generatePreview)
	user.POST("/connections/{id}/edits", s.applyEdits)
	user.POST("/connections/{id}/edits/preview", s.previewEdits)
	user.POST("/queries/{queryID}/cancel", s.cancelQuery)
	user.POST("/connections/{id}/tx", s.endTx)
	user.GET("/tx", s.listTxs)
	user.GET("/mcp/access", s.getMCPAccess)
	user.PUT("/mcp/access", s.setMCPAccess)
	user.GET("/tokens", s.listTokens)
	user.POST("/tokens", s.createToken)
	user.DELETE("/tokens/{id}", s.deleteToken)
	user.GET("/history", s.myHistory)
	user.GET("/saved", s.listSaved)
	user.POST("/saved", s.createSaved)
	user.PUT("/saved/{id}", s.updateSaved)
	user.DELETE("/saved/{id}", s.deleteSaved)
	user.GET("/notebooks", s.listNotebooks)
	user.POST("/notebooks", s.saveNotebook)
	user.GET("/notebooks/{note}", s.getNotebook)
	user.PUT("/notebooks/{note}", s.saveNotebook)
	user.DELETE("/notebooks/{note}", s.deleteNotebook)
	user.POST("/notebooks/{note}/publish", s.publishNotebook)
	user.GET("/notebooks/{note}/access", s.notebookSharing)
	user.PUT("/notebooks/{note}/access", s.notebookSharing)
	user.GET("/notebooks/{note}/subjects", s.notebookSubjects)
	user.POST("/notebooks/{note}/run", s.runNotebook)
	user.GET("/notebooks/{note}/runs", s.notebookRuns)
	user.GET("/notebooks/{note}/runs/{run}", s.notebookRunAction)
	user.POST("/notebooks/{note}/runs/{run}/cancel", s.notebookRunAction)
	api.GET("/run/{path}", s.publishedNotebook)
	api.POST("/run/{path}", s.publishedNotebook)
	api.GET("/run/{path}/{cell}", s.publishedNotebook)
	api.POST("/run/{path}/{cell}", s.publishedNotebook)

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
	admin.GET("/settings", s.adminGetSettings)
	admin.PUT("/settings", s.adminSetSettings)
	admin.GET("/sessions/lifetime", s.adminGetSessionLifetime)
	admin.PUT("/sessions/lifetime", s.adminSetSessionLifetime)
	admin.GET("/docs/schedule", s.adminGetDocSchedule)
	admin.PUT("/docs/schedule", s.adminSetDocSchedule)
	admin.POST("/docs/run", s.adminRunDocs)
	admin.GET("/sso", s.adminListSSO)
	admin.POST("/sso", s.adminSaveSSO)
	admin.PUT("/sso/{name}", s.adminSaveSSO)
	admin.DELETE("/sso/{name}", s.adminDeleteSSO)
	admin.DELETE("/users/{id}/identities", s.adminUnlinkSSO)

	s.mux = mux

	if cfg.MCP.Enabled {
		h := s.mcpHandler()
		base.Handle("/mcp", h)
		// One endpoint per connection, for clients set up for one database.
		base.Handle("/mcp/{conn}", h)

		s.registerOAuth()

		user.GET("/oauth/authorize", s.oauthAuthorizeInfo)
		user.POST("/oauth/authorize", s.oauthApprove)
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
	defer func() { s.notebookStop(); s.notebookWorkers.Wait() }()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	maintenanceDone := make(chan struct{})
	go func() { defer close(maintenanceDone); s.maintainNotebookRuns(ctx) }()
	defer func() { cancel(); <-maintenanceDone }()
	docsDone := make(chan struct{})
	go func() { defer close(docsDone); s.runDocJobs(ctx) }()
	defer func() { cancel(); <-docsDone }()
	settingsDone := make(chan struct{})
	go func() { defer close(settingsDone); s.reloadSettings(ctx) }()
	defer func() { cancel(); <-settingsDone }()

	addr := net.JoinHostPort(s.cfg.Server.Host, s.cfg.Server.Port)
	slog.Info("starting server", "addr", addr, "base_path", s.cfg.Server.BasePath)

	return s.mux.StartWithContext(ctx, addr)
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

// setCookie sets the cookie of a new session. A remembered session's cookie
// lasts its idle window and is renewed while used; any other lives until the
// browser closes.
func (s *Server) setCookie(w http.ResponseWriter, r *http.Request, token string, remember bool) {
	s.setCookieFor(w, r, token, s.auth.Lifetime().CookieMaxAge(remember))
}

// setCookieFor sets the session cookie for maxAge; zero ends it with the
// browser.
func (s *Server) setCookieFor(w http.ResponseWriter, r *http.Request, token string, maxAge time.Duration) {
	c := &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     s.cookiePath(),
		HttpOnly: true,
		Secure:   s.isSecure(r),
		SameSite: http.SameSiteLaxMode,
	}

	if maxAge > 0 {
		c.MaxAge = max(1, int(maxAge.Seconds()))
	}

	http.SetCookie(w, c)
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
