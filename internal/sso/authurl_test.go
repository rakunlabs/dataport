package sso

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// Parameters written into the authorization URL, such as a login page
// title some providers read, reach the provider next to the flow's own.
func TestAuthURLKeepsQuery(t *testing.T) {
	ctx := context.Background()
	idp := fakeIdP(t)
	secret := "s"

	for name, p := range map[string]Provider{
		"explicit": {AuthURL: idp.URL + "/auth?title=DataPort&kc_idp_hint=corp", TokenURL: idp.URL + "/token", UserInfoURL: idp.URL + "/userinfo"},
		// With an issuer and no userinfo or JWKS URL, discovery runs; the
		// written authorization URL still wins over the discovered one.
		"issuer": {IssuerURL: idp.URL, AuthURL: idp.URL + "/auth?title=DataPort&kc_idp_hint=corp", TokenURL: idp.URL + "/token", UserInfoURL: idp.URL + "/userinfo"},
	} {
		t.Run(name, func(t *testing.T) {
			m, _ := setup(t)
			p.Name, p.Enabled, p.ClientID = "idp", true, "c"
			if err := m.Save(ctx, "", p, &secret); err != nil {
				t.Fatal(err)
			}

			w := httptest.NewRecorder()
			if _, _, err := m.Login(w, httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/idp", nil), "idp"); err != nil || w.Code != http.StatusTemporaryRedirect {
				t.Fatalf("start: %v %d", err, w.Code)
			}

			loc, err := url.Parse(w.Header().Get("Location"))
			if err != nil {
				t.Fatal(err)
			}

			q := loc.Query()
			if loc.Path != "/auth" || q.Get("title") != "DataPort" || q.Get("kc_idp_hint") != "corp" || q.Get("client_id") != "c" || q.Get("state") == "" {
				t.Fatalf("authorization URL lost its parameters: %s", loc)
			}
		})
	}
}
