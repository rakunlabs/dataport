// Package sso signs users in through OAuth2 / OpenID Connect providers.
//
// The protocol work (state, nonce, PKCE, code exchange, id_token
// verification) is done by ada's oauth2 strategy. This package keeps the
// provider settings, decides which dataport user an external identity
// becomes, and hands the result to the session layer.
package sso

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"

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
	// "https://dataport.example.com". Empty derives it from the request.
	externalURL string

	mu         sync.RWMutex
	providers  []Provider
	strategies map[string]*oauth2.Strategy
}

func New(st *store.Store, ci *crypto.Cipher, basePath, externalURL string) *Manager {
	return &Manager{store: st, cipher: ci, basePath: basePath, externalURL: strings.TrimSuffix(externalURL, "/")}
}

// CallbackPath is where providers redirect back to, without the origin.
func (m *Manager) CallbackPath(name string) string {
	return m.basePath + "/api/v1/auth/sso/callback/" + name
}

// Load reads the providers from the store and builds their strategies.
func (m *Manager) Load(ctx context.Context) error {
	providers, err := m.read(ctx)
	if err != nil {
		return err
	}

	strategies := make(map[string]*oauth2.Strategy, len(providers))

	for _, p := range providers {
		if !p.Enabled {
			continue
		}

		s, err := m.build(ctx, p)
		if err != nil {
			slog.Error("sso provider is unavailable", "provider", p.Name, "error", err)
		}

		if s != nil {
			strategies[p.Name] = s
		}
	}

	m.mu.Lock()
	m.providers = providers
	m.strategies = strategies
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
		IssuerURL:       p.IssuerURL,
		DisablePKCE:     p.DisablePKCE,
		AuthHeaderStyle: style,
	}

	opts := oauth2.Options{
		Label:            p.DisplayName,
		CallbackBasePath: m.basePath + "/api/v1/auth/sso/callback",
		CallbackBaseURL:  m.externalURL,
		FlowCookie:       cookie.Options{Path: m.basePath + "/api/v1/auth/sso/"},
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
		if _, ok := m.strategies[p.Name]; ok {
			label := p.DisplayName
			if label == "" {
				label = p.Name
			}

			list = append(list, Public{Name: p.Name, Label: label})
		}
	}

	return list
}

func (m *Manager) get(name string) (*oauth2.Strategy, Provider, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.strategies[name]
	if !ok {
		return nil, Provider{}, false
	}

	for _, p := range m.providers {
		if p.Name == name {
			return s, p, true
		}
	}

	return nil, Provider{}, false
}

// Login runs one step of the flow: it redirects to the provider when the
// request starts a login, and returns the identity on the callback. It
// returns nil when the strategy already wrote the response.
func (m *Manager) Login(w http.ResponseWriter, r *http.Request, name string) (*identity.Identity, Provider, bool) {
	s, p, ok := m.get(name)
	if !ok {
		http.Error(w, "unknown sign-in provider", http.StatusNotFound)

		return nil, Provider{}, false
	}

	id, outcome, err := s.Login(w, r)
	if err != nil {
		slog.Error("sso login", "provider", name, "error", err)
		http.Error(w, "sign-in failed", http.StatusInternalServerError)

		return nil, Provider{}, false
	}

	if outcome != strategy.OutcomeContinue || id == nil {
		return nil, Provider{}, false
	}

	return id, p, true
}

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
	v := View{Provider: p, HasSecret: p.ClientSecretEnc != "", CallbackURL: m.externalURL + m.CallbackPath(p.Name)}
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
