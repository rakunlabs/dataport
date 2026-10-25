package sso

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rakunlabs/ada/middleware/auth/identity"

	"github.com/rakunlabs/dataport/internal/config"
	"github.com/rakunlabs/dataport/internal/crypto"
	"github.com/rakunlabs/dataport/internal/store"
)

func setup(t *testing.T) (*Manager, *store.Store) {
	t.Helper()

	st, err := store.New(context.Background(), config.Store{Type: "sqlite", TablePrefix: "dp_",
		DSN: "file:" + filepath.Join(t.TempDir(), "t.db")}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = st.Close() })

	ci, _ := crypto.New("k")

	return New(st, ci, "", ""), st
}

func TestResolve(t *testing.T) {
	ctx := context.Background()
	m, st := setup(t)

	admin := &store.User{Username: "root", PasswordHash: "x", Role: store.RoleAdmin}
	_ = st.CreateUser(ctx, admin)

	ops := &store.Group{Name: "ops"}
	_ = st.CreateGroup(ctx, ops)

	manual := &store.Group{Name: "manual"}
	_ = st.CreateGroup(ctx, manual)

	known := &store.User{Username: "ayse", Email: "ayse@example.com", PasswordHash: "x", GroupIDs: []string{manual.ID}}
	_ = st.CreateUser(ctx, known)

	p := Provider{Name: "idp"}

	id := func(sub, email string, verified bool, claims map[string]any) *identity.Identity {
		return &identity.Identity{Subject: sub, Email: email, EmailVerified: verified, Name: "N", Claims: claims}
	}

	var denied *ErrDenied

	// Unknown identity, nothing enabled.
	if _, err := m.Resolve(ctx, p, id("s1", "ayse@example.com", true, nil)); !errors.As(err, &denied) {
		t.Fatalf("expected denial, got %v", err)
	}

	// Link by email requires a verified address.
	p.LinkByEmail = true
	if _, err := m.Resolve(ctx, p, id("s1", "ayse@example.com", false, nil)); !errors.As(err, &denied) {
		t.Fatalf("unverified email must not link, got %v", err)
	}

	u, err := m.Resolve(ctx, p, id("s1", "AYSE@example.com", true, nil))
	if err != nil || u.ID != known.ID {
		t.Fatalf("link by email: %v %v", u, err)
	}

	// The link now works on its own, even if the email changes at the IdP.
	p.LinkByEmail = false
	if u, err := m.Resolve(ctx, p, id("s1", "other@example.com", true, nil)); err != nil || u.ID != known.ID {
		t.Fatalf("linked identity: %v %v", u, err)
	}

	// Allowed domains.
	p.AllowedDomains = []string{"corp.example"}
	if _, err := m.Resolve(ctx, p, id("s1", "ayse@example.com", true, nil)); !errors.As(err, &denied) {
		t.Fatalf("domain must be refused, got %v", err)
	}

	p.AllowedDomains = nil

	// Auto create picks a free username.
	p.AutoCreate = true
	n1, err := m.Resolve(ctx, p, id("s2", "new@example.com", true, nil))
	if err != nil || n1.Username != "new@example.com" || n1.IsAdmin() {
		t.Fatalf("auto create: %+v %v", n1, err)
	}

	n2, err := m.Resolve(ctx, p, id("s3", "new@example.com", false, map[string]any{"preferred_username": "new@example.com"}))
	if err != nil || n2.Username != "new@example.com-2" || n2.Email != "" {
		t.Fatalf("second auto create: %+v %v", n2, err)
	}

	// Roles: admin role and group mapping are synced; manual groups stay.
	p.RolesClaim = "realm_access.roles"
	p.AdminRoles = []string{"dp-admin"}
	p.GroupMapping = map[string]string{"dp-ops": ops.ID}

	claims := map[string]any{"realm_access": map[string]any{"roles": []any{"dp-admin", "dp-ops"}}}

	u, err = m.Resolve(ctx, p, id("s1", "ayse@example.com", true, claims))
	if err != nil || !u.IsAdmin() || !slices.Contains(u.GroupIDs, ops.ID) || !slices.Contains(u.GroupIDs, manual.ID) {
		t.Fatalf("role sync: %+v %v", u, err)
	}

	u, err = m.Resolve(ctx, p, id("s1", "ayse@example.com", true, map[string]any{}))
	if err != nil || u.IsAdmin() || slices.Contains(u.GroupIDs, ops.ID) || !slices.Contains(u.GroupIDs, manual.ID) {
		t.Fatalf("role removal: %+v %v", u, err)
	}

	// The last admin is never demoted by SSO.
	_ = st.DeleteUser(ctx, admin.ID)
	_, _ = m.Resolve(ctx, p, id("s1", "ayse@example.com", true, claims))

	if u, _ = m.Resolve(ctx, p, id("s1", "ayse@example.com", true, map[string]any{})); !u.IsAdmin() {
		t.Fatal("last admin was demoted")
	}

	// Disabled users are refused.
	u.Disabled = true
	u.GroupIDs = nil
	_ = st.UpdateUser(ctx, u)

	if _, err := m.Resolve(ctx, p, id("s1", "", false, nil)); !errors.As(err, &denied) {
		t.Fatalf("disabled user must be refused, got %v", err)
	}
}

func TestRolesUnion(t *testing.T) {
	// ada fills Identity.Roles from the flat "roles" claim; extra paths add
	// to it, as in pika.
	id := &identity.Identity{
		Roles:  []string{"dp-admin", "x"},
		Claims: map[string]any{"realm_access": map[string]any{"roles": []any{"x", "dp-ops"}}},
	}

	if got := Roles(id, ""); !slices.Equal(got, []string{"dp-admin", "x"}) {
		t.Fatalf("flat roles only: %v", got)
	}

	if got := Roles(id, "realm_access.roles"); !slices.Equal(got, []string{"dp-admin", "x", "dp-ops"}) {
		t.Fatalf("union: %v", got)
	}
}

func TestFlatRolesClaimSync(t *testing.T) {
	ctx := context.Background()
	m, st := setup(t)

	_ = st.CreateUser(ctx, &store.User{Username: "root", PasswordHash: "x", Role: store.RoleAdmin})
	u := &store.User{Username: "u", Email: "u@example.com", PasswordHash: "x"}
	_ = st.CreateUser(ctx, u)

	p := Provider{Name: "idp", LinkByEmail: true, AdminRoles: []string{"dp-admin"}}

	got, err := m.Resolve(ctx, p, &identity.Identity{Subject: "s", Email: "u@example.com", EmailVerified: true, Roles: []string{"dp-admin"}})
	if err != nil || !got.IsAdmin() {
		t.Fatalf("flat roles claim must grant admin without a roles path: %+v %v", got, err)
	}
}

func TestHarvestRoles(t *testing.T) {
	claims := map[string]any{
		"groups":          "a b",
		"realm_access":    map[string]any{"roles": []any{"x", "y"}},
		"resource_access": map[string]any{"c1": map[string]any{"roles": []any{"z", "x"}}},
	}

	got := HarvestRoles(claims, "groups, realm_access.roles, resource_access.*.roles, missing.path")
	if !slices.Equal(got, []string{"a", "b", "x", "y", "z"}) {
		t.Fatalf("got %v", got)
	}
}

func TestViewHidesSecret(t *testing.T) {
	m, _ := setup(t)
	secret := "top-secret"

	if err := m.Save(context.Background(), "", Provider{Name: "idp", ClientID: "c", AuthURL: "https://a", TokenURL: "https://t", UserInfoURL: "https://u"}, &secret); err != nil {
		t.Fatal(err)
	}

	data, err := json.Marshal(m.Views())
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(data), "client_secret_enc") || strings.Contains(string(data), "v1:") || !strings.Contains(string(data), `"has_secret":true`) {
		t.Fatalf("view leaks or misreports the secret: %s", data)
	}

	// Saving without a secret keeps the stored one.
	if err := m.Save(context.Background(), "idp", Provider{Name: "idp", ClientID: "c2", AuthURL: "https://a", TokenURL: "https://t", UserInfoURL: "https://u"}, nil); err != nil {
		t.Fatal(err)
	}

	if !m.Views()[0].HasSecret {
		t.Fatal("secret was dropped on save")
	}
}
