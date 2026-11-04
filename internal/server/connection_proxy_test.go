package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/dataport/internal/auth"
	"github.com/rakunlabs/dataport/internal/store"
)

// An admin saves a connection with a SOCKS5 proxy: it is stored encrypted,
// shown with its password masked, kept when the form sends it back masked,
// refused for a database that cannot use one, and sealed again when the
// secret key is rotated.
func TestConnectionProxy(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")
	s := env.srv
	s.base.Auth.SecretKey = "k"

	hash, _ := auth.HashPassword("x-password")
	admin := &store.User{Username: "root", PasswordHash: hash, Role: store.RoleAdmin}
	if err := env.st.CreateUser(ctx, admin); err != nil {
		t.Fatal(err)
	}

	session, _ := s.auth.CreateSession(ctx, admin.ID, "", "", false)
	call := func(method, path string, body any) (int, map[string]any) {
		t.Helper()

		data, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(string(data)))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: cookieName, Value: session})
		w := httptest.NewRecorder()
		s.mux.ServeHTTP(w, r)

		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)

		return w.Code, out
	}

	form := func(proxy any) map[string]any {
		return map[string]any{"name": "pg-via-proxy", "db_type": "postgres", "host": "db.internal", "port": 5432, "database": "app",
			"username": "u", "password": "p", "proxy": proxy, "access": []any{}}
	}

	// Refused: not SOCKS5, and a database type that cannot use a proxy.
	if code, out := call("POST", "/admin/connections", form("http://proxy:3128")); code != http.StatusBadRequest || !strings.Contains(out["message"].(string), "socks5://") {
		t.Fatalf("http proxy: %d %v", code, out)
	}

	sqlite := map[string]any{"name": "file", "db_type": "sqlite", "database": "shop.db", "proxy": "socks5://proxy:1080", "access": []any{}}
	if code, out := call("POST", "/admin/connections", sqlite); code != http.StatusBadRequest || !strings.Contains(out["message"].(string), "cannot use a proxy") {
		t.Fatalf("sqlite with a proxy: %d %v", code, out)
	}

	code, created := call("POST", "/admin/connections", form("socks5://dp:s3cret@proxy.internal:1080"))
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, created)
	}

	id := created["id"].(string)
	if created["proxy"] != "socks5://dp:xxxxx@proxy.internal:1080" {
		t.Fatalf("shown proxy: %v", created["proxy"])
	}

	stored, _ := env.st.GetConnection(ctx, id)
	if strings.Contains(stored.ProxyEnc, "s3cret") || stored.ProxyEnc == "" {
		t.Fatalf("the proxy is not encrypted: %q", stored.ProxyEnc)
	}

	readBack := func() string {
		c, _ := env.st.GetConnection(ctx, id)
		raw, err := s.cipher.Decrypt(c.ProxyEnc)
		if err != nil {
			t.Fatal(err)
		}

		return raw
	}

	// The list shows it masked too.
	_, _ = call("GET", "/admin/connections", nil)
	code, got := call("GET", "/admin/connections/"+id, nil)
	if code != http.StatusOK || got["proxy"] != "socks5://dp:xxxxx@proxy.internal:1080" {
		t.Fatalf("get: %d %v", code, got["proxy"])
	}

	// Sent back masked, the password is kept; nil keeps it all.
	if code, out := call("PUT", "/admin/connections/"+id, form("socks5://dp:xxxxx@proxy.internal:1080")); code != http.StatusOK {
		t.Fatalf("update masked: %d %v", code, out)
	}

	if got := readBack(); got != "socks5://dp:s3cret@proxy.internal:1080" {
		t.Fatalf("masked update changed the proxy: %s", got)
	}

	if code, _ := call("PUT", "/admin/connections/"+id, form(nil)); code != http.StatusOK || readBack() != "socks5://dp:s3cret@proxy.internal:1080" {
		t.Fatal("a form without the proxy changed it")
	}

	// A masked password is not carried over to another host: it could send
	// the saved password to a proxy of anyone's choosing.
	if code, out := call("PUT", "/admin/connections/"+id, form("socks5://dp:xxxxx@evil.example:1080")); code != http.StatusBadRequest {
		t.Fatalf("masked password to another host: %d %v", code, out)
	}

	// A test reuses the saved database password only through the saved
	// proxy: through another one, that proxy would see the connection.
	test := func(proxy any) (int, map[string]any) {
		body := form(proxy)
		delete(body, "password")
		body["id"] = id

		return call("POST", "/admin/connections/test", body)
	}

	if code, out := test("socks5://dp:s3cret@other-proxy:1080"); code != http.StatusBadRequest || !strings.Contains(out["message"].(string), "changed proxy") {
		t.Fatalf("test through another proxy with the saved password: %d %v", code, out)
	}

	if code, out := test(""); code != http.StatusBadRequest || !strings.Contains(out["message"].(string), "changed proxy") {
		t.Fatalf("test without the saved proxy, with the saved password: %d %v", code, out)
	}

	// The saved proxy, masked or left out, is fine (the database itself is
	// not there, so the answer is a failed connection, not a refusal).
	for _, p := range []any{"socks5://dp:xxxxx@proxy.internal:1080", nil} {
		if code, out := test(p); code != http.StatusOK {
			t.Fatalf("test through the saved proxy %v: %d %v", p, code, out)
		}
	}

	// Rotating the secret key seals the proxy again.
	r := httptest.NewRequest(http.MethodPost, "/api/v1/admin/secret-key/rotate", strings.NewReader(`{"current":"k","key":"a-new-key-of-enough-length"}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: cookieName, Value: session})
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("rotate: %d %s", w.Code, w.Body)
	}

	c, _ := env.st.GetConnection(ctx, id)
	if !s.cipher.Current(c.ProxyEnc) || readBack() != "socks5://dp:s3cret@proxy.internal:1080" {
		t.Fatal("the proxy was not sealed with the new key")
	}

	// Cleared with "".
	if code, out := call("PUT", "/admin/connections/"+id, form("")); code != http.StatusOK || out["proxy"] != nil {
		t.Fatalf("clear: %d %v", code, out["proxy"])
	}

	if c, _ := env.st.GetConnection(ctx, id); c.ProxyEnc != "" {
		t.Fatal("the proxy was not removed")
	}
}
