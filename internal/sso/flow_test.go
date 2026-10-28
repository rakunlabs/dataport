package sso

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// fakeIdP answers the authorization code flow with fixed user claims. Its
// discovery document names a different issuer, as some providers do.
func fakeIdP(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": "https://elsewhere/"})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("code") != "good" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)

			return
		}

		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer"})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"sub": "u1", "email": "a@example.com", "email_verified": true})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv
}

func TestLoginFlow(t *testing.T) {
	ctx := context.Background()
	m, _ := setup(t)
	m.basePath = "/dp"
	m.externalURL = origin("https://dataport.example.com/dp/")

	idp := fakeIdP(t)
	secret := "s"

	// An issuer URL next to explicit endpoints must not run discovery.
	p := Provider{Name: "idp", Enabled: true, ClientID: "c", IssuerURL: idp.URL,
		AuthURL: idp.URL + "/auth", TokenURL: idp.URL + "/token", UserInfoURL: idp.URL + "/userinfo"}
	if err := m.Save(ctx, "", p, &secret); err != nil {
		t.Fatal(err)
	}

	if st := m.Status("idp"); st != "" {
		t.Fatalf("provider unavailable: %s", st)
	}

	// Start: a redirect to the provider with the external callback URL.
	w := httptest.NewRecorder()
	id, _, err := m.Login(w, httptest.NewRequest(http.MethodGet, "/dp/api/v1/auth/sso/idp", nil), "idp")
	if err != nil || id != nil || w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("start: %v %v %d %s", id, err, w.Code, w.Body)
	}

	loc, _ := url.Parse(w.Header().Get("Location"))
	if got := loc.Query().Get("redirect_uri"); got != "https://dataport.example.com/dp/api/v1/auth/sso/callback/idp" {
		t.Fatalf("redirect_uri %q", got)
	}

	cookies := w.Result().Cookies()

	callback := func(code string) (*httptest.ResponseRecorder, error) {
		r := httptest.NewRequest(http.MethodGet, "/dp/api/v1/auth/sso/callback/idp?code="+code+"&state="+loc.Query().Get("state"), nil)
		for _, c := range cookies {
			r.AddCookie(c)
		}

		w := httptest.NewRecorder()
		id, _, err := m.Login(w, r, "idp")
		if err == nil && (id == nil || id.Subject != "u1") {
			t.Fatalf("callback identity %+v", id)
		}

		return w, err
	}

	if _, err := callback("good"); err != nil {
		t.Fatalf("callback: %v", err)
	}

	// A replayed callback fails with a message for the login page, and
	// nothing is written to the browser.
	w, err = callback("good")

	var d *ErrDenied
	if !errors.As(err, &d) || !strings.Contains(d.Reason, "try again") || w.Body.Len() != 0 {
		t.Fatalf("replay: %v %q", err, w.Body)
	}
}
