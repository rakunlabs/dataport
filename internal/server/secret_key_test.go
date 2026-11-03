package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/dataport/internal/auth"
	"github.com/rakunlabs/dataport/internal/crypto"
	"github.com/rakunlabs/dataport/internal/store"
)

// Rotating the secret key seals every stored secret again: connection
// passwords and encrypted values inside settings. Documentation built
// before stays current, and the old key no longer reads the store.
func TestRotateSecretKey(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")
	s := env.srv
	s.base.Auth.SecretKey = "k"

	k := docKeyFor(t, env)
	c, _ := env.st.GetConnection(ctx, k.ConnectionID)
	c.PasswordEnc, _ = s.cipher.Encrypt("db-password")
	if err := env.st.UpdateConnection(ctx, c); err != nil {
		t.Fatal(err)
	}

	token, _ := s.cipher.Encrypt("push-token")
	raw, _ := json.Marshal(map[string]any{"enabled": false, "url": "https://x", "token_enc": token, "targets": []any{}})
	if err := env.st.SetSetting(ctx, docExportSetting, string(raw)); err != nil {
		t.Fatal(err)
	}

	built, err := s.documentation(ctx, k, false)
	if err != nil {
		t.Fatal(err)
	}

	hash, _ := auth.HashPassword("x-password")
	admin := &store.User{Username: "root", PasswordHash: hash, Role: store.RoleAdmin}
	if err := env.st.CreateUser(ctx, admin); err != nil {
		t.Fatal(err)
	}

	session, _ := s.auth.CreateSession(ctx, admin.ID, "", "", false)
	rotate := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/admin/secret-key/rotate", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: cookieName, Value: session})
		w := httptest.NewRecorder()
		s.mux.ServeHTTP(w, r)

		return w
	}

	if w := rotate(`{"current":"wrong","key":"a-new-key-of-enough-length"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("wrong current key: %d %s", w.Code, w.Body)
	}
	if w := rotate(`{"current":"k","key":"short"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("short key: %d %s", w.Code, w.Body)
	}

	w := rotate(`{"current":"k","key":"a-new-key-of-enough-length"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("rotate: %d %s", w.Code, w.Body)
	}

	var out struct {
		Rewritten  int       `json:"rewritten"`
		Unreadable int       `json:"unreadable"`
		Status     keyStatus `json:"status"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Rewritten != 2 || out.Unreadable != 0 || !out.Status.Rotated || out.Status.Unreadable != 0 {
		t.Fatalf("rotate result: %s", w.Body)
	}

	// Everything is sealed with the new key only.
	c, _ = env.st.GetConnection(ctx, k.ConnectionID)
	cfg, _ := s.loadDocExport(ctx)
	for _, enc := range []string{c.PasswordEnc, cfg.TokenEnc} {
		if !s.cipher.Current(enc) {
			t.Fatalf("not sealed with the new key: %q", enc)
		}
	}
	if pw, err := s.cipher.Decrypt(c.PasswordEnc); err != nil || pw != "db-password" {
		t.Fatalf("password: %q %v", pw, err)
	}

	// The documentation built before is served as it was.
	got, err := s.documentation(ctx, k, false)
	if err != nil || got.Document.GeneratedAt != built.Document.GeneratedAt {
		t.Fatalf("documentation rebuilt after the rotation: %v", err)
	}

	// A value sealed with the old key after the rotation (a save that
	// encrypted just before it) is sealed again by the next rotation pass,
	// and is counted until then.
	old, _ := crypto.New("k")
	late, _ := old.Encrypt("late")
	c.PasswordEnc = late
	_ = env.st.UpdateConnection(ctx, c)
	if st, _ := s.keyStatus(ctx); st.Unreadable != 1 {
		t.Fatalf("old-key value not counted: %+v", st)
	}
	if got, err := s.cipher.Decrypt(late); err != nil || got != "late" {
		t.Fatal("the old key no longer reads")
	}

}
