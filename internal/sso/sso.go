// Package sso signs users in through OAuth2 / OpenID Connect providers.
//
// The protocol work (state, nonce, PKCE, code exchange, id_token
// verification) is done by ada's oauth2 strategy. This package keeps the
// provider settings, decides which dataport user an external identity
// becomes, and hands the result to the session layer.
package sso

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rakunlabs/ada/middleware/auth/cookie"
	"github.com/rakunlabs/ada/middleware/auth/identity"
	"github.com/rakunlabs/ada/middleware/auth/strategy"
	"github.com/rakunlabs/ada/middleware/auth/strategy/oauth2"

	"github.com/rakunlabs/dataport/internal/crypto"
	"github.com/rakunlabs/dataport/internal/store"
)

const settingName = "sso_providers"

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// Provider is one configured identity provider.
type Provider struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Enabled     bool   `json:"enabled"`

	// IssuerURL enables OIDC discovery. Explicit endpoints win over it.
	IssuerURL   string   `json:"issuer_url"`
	AuthURL     string   `json:"auth_url"`
	TokenURL    string   `json:"token_url"`
	UserInfoURL string   `json:"userinfo_url"`
	JWKSURL     string   `json:"jwks_url"`
	ClientID    string   `json:"client_id"`
	Scopes      []string `json:"scopes"`
	// TokenAuthMethod is "", "basic", "post" or "bearer".
	TokenAuthMethod string `json:"token_auth_method"`
	DisablePKCE     bool   `json:"disable_pkce"`

	// ClientSecretEnc is the encrypted secret; it never leaves the server.
	ClientSecretEnc string `json:"client_secret_enc,omitempty"`

	// AutoCreate creates a user on the first sign-in of an unknown identity.
	// Off by default: only users an admin created can sign in.
	AutoCreate bool `json:"auto_create"`
	// LinkByEmail links an unknown identity to the existing user with the
	// same email, when the provider says the email is verified.
	LinkByEmail bool `json:"link_by_email"`
	// AllowedDomains restricts sign-in to these email domains when set.
	AllowedDomains []string `json:"allowed_domains"`

	// RolesClaim lists extra dotted claim paths, comma separated, such as
	// "realm_access.roles, resource_access.*.roles". Their values are added
	// to the flat "roles" claim, which is always read (as in pika). The
	// combined roles are matched against AdminRoles and GroupMapping.
	RolesClaim string `json:"roles_claim"`
	// AdminRoles grant the admin role. When set, the role is synced on each
	// sign-in, so removing it at the provider demotes the user.
	AdminRoles []string `json:"admin_roles"`
	// GroupMapping maps a claim value to a dataport group id. Mapped groups
	// are synced on each sign-in; groups not in the mapping are left alone.
	GroupMapping map[string]string `json:"group_mapping"`
}

// View is what the admin API returns: the provider without the secret.
type View struct {
	Provider
	ClientSecretEnc any    `json:"client_secret_enc,omitempty"`
	HasSecret       bool   `json:"has_secret"`
	CallbackURL     string `json:"callback_url"`
}

type Manager struct {
	store    *store.Store
	cipher   *crypto.Cipher
	basePath string
	// externalURL is the public origin used for redirect URIs, such as
	// "https://dataport.example.com", and prefix the path a proxy serves
	// dataport under before the base path, such as "/tools". Both empty
	// derive the address from the request.
	externalURL string
	prefix      string

	mu         sync.RWMutex
	providers  []Provider
	strategies map[string]*oauth2.Strategy
	// errs keeps why a provider failed to initialise, for the admin page.
	errs    map[string]string
	retried map[string]time.Time
}

func New(st *store.Store, ci *crypto.Cipher, basePath, externalURL string) *Manager {
	m := &Manager{store: st, cipher: ci, basePath: basePath}
	m.externalURL, m.prefix = split(externalURL)

	return m
}

// split cuts the external URL into its origin, which ada takes for the
// callback, and its path, which goes before the base path.
func split(raw string) (string, string) {
	raw = strings.TrimSpace(raw)

	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return strings.TrimSuffix(raw, "/"), ""
	}

	return u.Scheme + "://" + u.Host, strings.TrimRight(u.Path, "/")
}

// origin keeps only the scheme and host of the external URL.
func origin(raw string) string {
	o, _ := split(raw)

	return o
}

// SetExternalURL changes the public address, as an admin saved it, and
// builds the providers again with the new redirect URIs.
func (m *Manager) SetExternalURL(ctx context.Context, raw string) error {
	o, p := split(raw)

	m.mu.Lock()
	changed := o != m.externalURL || p != m.prefix
	m.externalURL, m.prefix = o, p
	m.mu.Unlock()

	if !changed {
		return nil
	}

	return m.Load(ctx)
}

// CallbackPath is where providers redirect back to, without the origin.
func (m *Manager) CallbackPath(name string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.prefix + m.basePath + "/api/v1/auth/sso/callback/" + name
}

// Load reads the providers from the store and builds their strategies.
func (m *Manager) Load(ctx context.Context) error {
	providers, err := m.read(ctx)
	if err != nil {
		return err
	}

	strategies := make(map[string]*oauth2.Strategy, len(providers))
	errs := map[string]string{}

	for _, p := range providers {
		if !p.Enabled {
			continue
		}

		s, err := m.build(ctx, p)
		if err != nil {
			slog.Error("sso provider is unavailable", "provider", p.Name, "error", err)
			errs[p.Name] = err.Error()
		}

		if s != nil {
			strategies[p.Name] = s
		}
	}

	m.mu.Lock()
	m.providers = providers
	m.strategies = strategies
	m.errs = errs
	m.mu.Unlock()

	return nil
}

func (m *Manager) read(ctx context.Context) ([]Provider, error) {
	raw, err := m.store.GetSetting(ctx, settingName)
	if errors.Is(err, store.ErrNotFound) {
		return []Provider{}, nil
	}

	if err != nil {
		return nil, err
	}

	var providers []Provider
	if err := json.Unmarshal([]byte(raw), &providers); err != nil {
		return nil, fmt.Errorf("decode sso providers; %w", err)
	}

	return providers, nil
}

func (m *Manager) build(ctx context.Context, p Provider) (*oauth2.Strategy, error) {
	secret, err := m.cipher.Decrypt(p.ClientSecretEnc)
	if err != nil {
		return nil, fmt.Errorf("decrypt client secret; %w", err)
	}

	style, _ := oauth2.ParseAuthHeaderStyle(p.TokenAuthMethod)

	cfg := oauth2.Config{
		ClientID:        p.ClientID,
		ClientSecret:    secret,
		Scopes:          p.Scopes,
		AuthURL:         p.AuthURL,
		TokenURL:        p.TokenURL,
		UserInfoURL:     p.UserInfoURL,
		JWKSURL:         p.JWKSURL,
		DisablePKCE:     p.DisablePKCE,
		AuthHeaderStyle: style,
	}

	// As in pika, explicit endpoints skip OIDC discovery: a discovery
	// document whose issuer differs from the configured one in the
	// slightest (a trailing slash) would disable the provider for good.
	// Discovery is still needed when it is the only source of the user.
	if p.AuthURL == "" || p.TokenURL == "" || (p.UserInfoURL == "" && p.JWKSURL == "") {
		cfg.IssuerURL = p.IssuerURL
	}

	m.mu.RLock()
	external, prefix := m.externalURL, m.prefix
	m.mu.RUnlock()

	opts := oauth2.Options{
		Label: p.DisplayName,
		// The public path: a proxy may serve dataport under a prefix it
		// strips, so the provider redirects back through it.
		CallbackBasePath: prefix + m.basePath + "/api/v1/auth/sso/callback",
		CallbackBaseURL:  external,
		// Without an external URL the redirect URI is built from the request;
		// behind a proxy that is the forwarded host, as elsewhere in dataport.
		UnsafeTrustAllForwardedHeaders: external == "",
		FlowCookie:                     cookie.Options{Path: prefix + m.basePath + "/api/v1/auth/sso/"},
	}

	return oauth2.NewWithContext(ctx, p.Name, cfg, opts)
}

// Public lists the enabled providers for the login page.
type Public struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

func (m *Manager) Public() []Public {
	m.mu.RLock()
	defer m.mu.RUnlock()

	list := []Public{}

	for _, p := range m.providers {
		_, ok := m.strategies[p.Name]
		// A provider that failed to start stays listed: a click tries it
		// again and shows why it fails.
		if ok || (p.Enabled && m.errs[p.Name] != "") {
			label := p.DisplayName
			if label == "" {
				label = p.Name
			}

			list = append(list, Public{Name: p.Name, Label: label})
		}
	}

	return list
}

func (m *Manager) get(ctx context.Context, name string) (*oauth2.Strategy, Provider, bool) {
	m.retry(ctx, name)

	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.strategies[name]
	if !ok || m.errs[name] != "" {
		return nil, Provider{}, false
	}

	for _, p := range m.providers {
		if p.Name == name {
			return s, p, true
		}
	}

	return nil, Provider{}, false
}

// retry builds a provider that failed at start again, such as when the
// identity provider was not reachable yet, at most every half minute.
func (m *Manager) retry(ctx context.Context, name string) {
	m.mu.Lock()
	failed := m.errs[name] != ""
	due := time.Since(m.retried[name]) > 30*time.Second

	var p Provider

	idx := slices.IndexFunc(m.providers, func(x Provider) bool { return x.Name == name && x.Enabled })
	if idx >= 0 {
		p = m.providers[idx]
	}

	if failed && due && idx >= 0 {
		if m.retried == nil {
			m.retried = map[string]time.Time{}
		}

		m.retried[name] = time.Now()
	}
	m.mu.Unlock()

	if !failed || !due || idx < 0 {
		return
	}

	s, err := m.build(ctx, p)

	m.mu.Lock()
	defer m.mu.Unlock()

	if err != nil {
		slog.Error("sso provider is unavailable", "provider", name, "error", err)
		m.errs[name] = err.Error()

		return
	}

	m.strategies[name] = s
	delete(m.errs, name)
}

// Login runs one step of the flow: it redirects to the provider when the
// request starts a login, and returns the identity on the callback. With a
// nil identity and no error the response was written (the redirect to the
// provider). An error carries a message for the login page.
func (m *Manager) Login(w http.ResponseWriter, r *http.Request, name string) (*identity.Identity, Provider, error) {
	s, p, ok := m.get(r.Context(), name)
	if !ok {
		return nil, Provider{}, denied("this sign-in provider is not available")
	}

	// The strategy writes its failures as JSON; they are kept from the
	// browser and shown on the login page instead.
	cw := &captureWriter{ResponseWriter: w}

	id, outcome, err := s.Login(cw, r)
	if err != nil {
		slog.Error("sso login", "provider", name, "error", err)

		return nil, Provider{}, denied("sign-in failed, try again")
	}

	if cw.failed() {
		slog.Warn("sso login failed", "provider", name, "status", cw.status, "response", strings.TrimSpace(cw.body.String()))

		return nil, Provider{}, denied(failureMessage(cw.status, cw.body.Bytes()))
	}

	if outcome != strategy.OutcomeContinue || id == nil {
		return nil, Provider{}, nil
	}

	return id, p, nil
}

func failureMessage(status int, body []byte) string {
	var e struct {
		Error string `json:"error"`
	}

	_ = json.Unmarshal(body, &e)

	switch {
	case e.Error == "state_invalid" || e.Error == "pkce_missing":
		return "the sign-in took too long or was started in another tab; try again"
	case e.Error == "authorization_denied":
		return "the sign-in was cancelled at the provider"
	case e.Error == "provider_configuration":
		return "the sign-in provider is misconfigured; ask an administrator"
	case status >= http.StatusBadGateway:
		return "the sign-in provider could not be reached or rejected the request; ask an administrator"
	}

	return "sign-in failed, try again"
}

// captureWriter passes a successful response through and holds back an
// error response, so the caller can answer with a redirect instead.
type captureWriter struct {
	http.ResponseWriter

	status int
	body   bytes.Buffer
}

func (c *captureWriter) WriteHeader(status int) {
	c.status = status
	if status < http.StatusBadRequest {
		c.ResponseWriter.WriteHeader(status)
	}
}

func (c *captureWriter) Write(b []byte) (int, error) {
	if c.status == 0 {
		c.WriteHeader(http.StatusOK)
	}

	if c.failed() {
		return c.body.Write(b)
	}

	return c.ResponseWriter.Write(b)
}

func (c *captureWriter) failed() bool { return c.status >= http.StatusBadRequest }

// ////////////////////////////////////////////
// admin

func (m *Manager) Views() []View {
	m.mu.RLock()
	defer m.mu.RUnlock()

	list := make([]View, 0, len(m.providers))
	for _, p := range m.providers {
		list = append(list, m.view(p))
	}

	return list
}

func (m *Manager) view(p Provider) View {
	v := View{Provider: p, HasSecret: p.ClientSecretEnc != "", CallbackURL: m.externalURL + m.prefix + m.basePath + "/api/v1/auth/sso/callback/" + p.Name}
	v.ClientSecretEnc = nil

	return v
}

// Status reports whether an enabled provider failed to initialise, for the
// admin page.
func (m *Manager) Status(name string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, p := range m.providers {
		if p.Name == name && p.Enabled {
			if e := m.errs[name]; e != "" {
				return e
			}

			if _, ok := m.strategies[name]; !ok {
				return "unavailable, check the server log"
			}
		}
	}

	return ""
}

// Save validates and stores a provider. A nil secret keeps the stored one.
func (m *Manager) Save(ctx context.Context, original string, p Provider, secret *string) error {
	p.Name = strings.ToLower(strings.TrimSpace(p.Name))
	if !namePattern.MatchString(p.Name) {
		return errors.New("name must be lowercase letters, digits, - or _, up to 32 characters")
	}

	if p.ClientID == "" {
		return errors.New("client id is required")
	}

	if p.IssuerURL == "" && (p.AuthURL == "" || p.TokenURL == "") {
		return errors.New("set the issuer URL, or both the authorization and token URLs")
	}

	if p.IssuerURL == "" && p.UserInfoURL == "" && p.JWKSURL == "" {
		return errors.New("without an issuer URL, set the userinfo URL or the JWKS URL to read the user")
	}

	if len(p.Scopes) == 0 {
		p.Scopes = []string{"openid", "email", "profile"}
	}

	for i, d := range p.AllowedDomains {
		p.AllowedDomains[i] = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(d), "@"))
	}

	p.AllowedDomains = slices.DeleteFunc(p.AllowedDomains, func(s string) bool { return s == "" })

	providers, err := m.read(ctx)
	if err != nil {
		return err
	}

	idx := slices.IndexFunc(providers, func(x Provider) bool { return x.Name == original })
	if original != p.Name && slices.ContainsFunc(providers, func(x Provider) bool { return x.Name == p.Name }) {
		return store.ErrConflict
	}

	switch {
	case secret != nil:
		if p.ClientSecretEnc, err = m.cipher.Encrypt(*secret); err != nil {
			return err
		}
	case idx >= 0:
		p.ClientSecretEnc = providers[idx].ClientSecretEnc
	default:
		p.ClientSecretEnc = ""
	}

	if idx >= 0 {
		providers[idx] = p
	} else {
		providers = append(providers, p)
	}

	return m.write(ctx, providers)
}

func (m *Manager) Delete(ctx context.Context, name string) error {
	providers, err := m.read(ctx)
	if err != nil {
		return err
	}

	n := len(providers)
	providers = slices.DeleteFunc(providers, func(p Provider) bool { return p.Name == name })

	if len(providers) == n {
		return store.ErrNotFound
	}

	return m.write(ctx, providers)
}

func (m *Manager) write(ctx context.Context, providers []Provider) error {
	raw, err := json.Marshal(providers)
	if err != nil {
		return err
	}

	if err := m.store.SetSetting(ctx, settingName, string(raw)); err != nil {
		return err
	}

	return m.Load(ctx)
}
