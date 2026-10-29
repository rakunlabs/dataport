package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/sso"
)

// An external URL saved in the UI applies at once to OAuth and SSO, with
// its path, and dropping it falls back to the configured one.
func TestExternalURLSetting(t *testing.T) {
	env := newMCPEnv(t, "/database")
	s := env.srv
	s.base.Auth.ExternalURL = "https://configured.example.com"
	s.sso = sso.New(env.st, s.cipher, "/database", "")

	set := func(body string, want int) {
		t.Helper()
		r := httptest.NewRequest(http.MethodPut, "/admin/external-url", strings.NewReader(body))
		w := httptest.NewRecorder()
		s.adminSetExternalURL(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body)
		}
	}

	set(`{"value":"ftp://x"}`, http.StatusBadRequest)
	set(`{"value":"https://example.com/tools/"}`, http.StatusOK)

	meta := httptest.NewRecorder()
	s.oauthServerMetadata(meta, httptest.NewRequest(http.MethodGet, "/", nil))
	var m map[string]any
	_ = json.Unmarshal(meta.Body.Bytes(), &m)
	if m["registration_endpoint"] != "https://example.com/tools/database/oauth/register" {
		t.Fatalf("metadata: %v", m)
	}
	if got := s.sso.CallbackPath("idp"); got != "/tools/database/api/v1/auth/sso/callback/idp" {
		t.Fatalf("sso callback %q", got)
	}

	set(`{"value":null}`, http.StatusOK)
	if s.externalURL() != "https://configured.example.com" || s.sso.CallbackPath("idp") != "/database/api/v1/auth/sso/callback/idp" {
		t.Fatalf("reset: %q %q", s.externalURL(), s.sso.CallbackPath("idp"))
	}
}

// A user may use a writable connection read-only, everywhere, and turn it
// back off; a connection that is read-only for them stays so.
func TestSelfReadOnly(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")
	s := env.srv
	k := docKeyFor(t, env)

	g, _, err := s.resolveGrant(ctx, env.alice, k.ConnectionID)
	if err != nil || g.ReadOnly || !g.CanWrite {
		t.Fatalf("grant: %v %+v", err, g)
	}

	if err := env.st.SetSelfReadOnly(ctx, env.alice.ID, k.ConnectionID, true); err != nil {
		t.Fatal(err)
	}

	g, pw, _ := s.resolveGrant(ctx, env.alice, k.ConnectionID)
	if !g.ReadOnly || !g.CanWrite || !g.SelfReadOnly {
		t.Fatalf("self read-only grant: %+v", g)
	}
	if _, err := s.execute(ctx, env.alice, g, pw, execInput{SQL: "CREATE TABLE nope (id INTEGER)"}); !errors.Is(err, engine.ErrReadOnly) {
		t.Fatalf("write ran: %v", err)
	}
	grants, _ := env.st.ListGrants(ctx, env.alice)
	for _, gr := range grants {
		if gr.Connection.ID == k.ConnectionID && !gr.ReadOnly {
			t.Fatal("listed grant ignores the choice")
		}
	}

	if err := env.st.SetSelfReadOnly(ctx, env.alice.ID, k.ConnectionID, false); err != nil {
		t.Fatal(err)
	}
	if g, _, _ = s.resolveGrant(ctx, env.alice, k.ConnectionID); g.ReadOnly || g.SelfReadOnly {
		t.Fatalf("turned back on: %+v", g)
	}
}
