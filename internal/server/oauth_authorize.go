package server

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	dpauth "github.com/rakunlabs/dataport/internal/auth"
	"github.com/rakunlabs/dataport/internal/store"
)

// oauthAuthorizeRedirect sends the browser to the approval page of the app,
// which signs the user in first when needed and comes back to it. The path
// is the public one, so a proxy that strips a prefix keeps working.
func (s *Server) oauthAuthorizeRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, s.publicPath(r)+"/#/oauth/authorize?"+r.URL.RawQuery, http.StatusFound)
}

type authorizeParams struct {
	ClientID            string `json:"client_id"`
	RedirectURI         string `json:"redirect_uri"`
	ResponseType        string `json:"response_type"`
	State               string `json:"state"`
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
	Resource            string `json:"resource"`
	Scope               string `json:"scope"`
}

// authorizeRequest is a checked authorization request.
type authorizeRequest struct {
	params authorizeParams
	client *store.OAuthClient
	// connection is the one connection the client asked for through its
	// resource (an endpoint /mcp/<name>); nil for all of them.
	connection *store.Grant
}

// errRedirect is an error the client hears about through its redirect URI;
// others are shown to the user, as the redirect cannot be trusted.
type errRedirect struct{ code, desc string }

func (e *errRedirect) Error() string { return e.desc }

func (s *Server) checkAuthorize(r *http.Request, user *store.User, p authorizeParams) (*authorizeRequest, error) {
	client, err := s.store.GetOAuthClient(r.Context(), p.ClientID)
	if err != nil {
		return nil, errors.New("unknown client; register it again")
	}

	if !slices.Contains(client.RedirectURIs, p.RedirectURI) {
		return nil, errors.New("the redirect URI was not registered by this client")
	}

	out := &authorizeRequest{params: p, client: client}

	switch {
	case p.ResponseType != "code":
		return out, &errRedirect{"unsupported_response_type", "only the code flow is supported"}
	case p.CodeChallenge == "" || p.CodeChallengeMethod != "S256":
		return out, &errRedirect{"invalid_request", "PKCE with S256 is required"}
	}

	if conn := s.resourceConnection(r, p.Resource); conn != "" {
		g, err := s.grantByName(r, user, conn)
		if err != nil {
			return out, &errRedirect{"invalid_target", "you have no access to connection " + conn}
		}

		out.connection = g
	}

	return out, nil
}

// resourceConnection returns the connection named by a resource URL such
// as https://host/base/mcp/shop; empty for the whole /mcp endpoint.
func (s *Server) resourceConnection(r *http.Request, resource string) string {
	if resource == "" {
		return ""
	}

	u, err := url.Parse(resource)
	if err != nil {
		return ""
	}

	rest, ok := strings.CutPrefix(u.Path, s.cfg.Server.BasePath+"/mcp/")
	if !ok {
		return ""
	}

	return strings.Trim(rest, "/")
}

// grantByName finds a connection of the user by name or id.
func (s *Server) grantByName(r *http.Request, user *store.User, name string) (*store.Grant, error) {
	grants, err := s.store.ListGrants(r.Context(), user)
	if err != nil {
		return nil, err
	}

	if g := findGrant(grants, name); g != nil {
		return g, nil
	}

	return nil, store.ErrNotFound
}

func findGrant(grants []*store.Grant, name string) *store.Grant {
	for _, g := range grants {
		if g.Connection.Name == name || g.Connection.ID == name {
			return g
		}
	}

	for _, g := range grants {
		if strings.EqualFold(g.Connection.Name, name) {
			return g
		}
	}

	return nil
}

func authorizeParamsOf(q url.Values) authorizeParams {
	return authorizeParams{
		ClientID: q.Get("client_id"), RedirectURI: q.Get("redirect_uri"), ResponseType: q.Get("response_type"),
		State: q.Get("state"), CodeChallenge: q.Get("code_challenge"), CodeChallengeMethod: q.Get("code_challenge_method"),
		Resource: q.Get("resource"), Scope: q.Get("scope"),
	}
}

// oauthAuthorizeInfo tells the approval page who asks for what.
func (s *Server) oauthAuthorizeInfo(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)

	req, err := s.checkAuthorize(r, user, authorizeParamsOf(r.URL.Query()))

	var redirect *errRedirect

	switch {
	case req == nil:
		writeError(w, http.StatusBadRequest, err.Error())

		return
	case errors.As(err, &redirect):
		writeJSON(w, http.StatusOK, map[string]any{"error": redirect.desc, "redirect": redirectWith(req.params, url.Values{
			"error": {redirect.code}, "error_description": {redirect.desc},
		})})

		return
	}

	// A client the user chose to remember is approved again at once, with
	// the same choices, while they still hold.
	if next := s.rememberedApproval(r, user, req); next != "" {
		writeJSON(w, http.StatusOK, map[string]any{"redirect": next, "remembered": true})

		return
	}

	grants, err := s.store.ListGrants(r.Context(), user)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	conns := make([]map[string]any, 0, len(grants))
	for _, g := range grants {
		if g.Connection.HideMCP || g.Connection.NotebookOnly {
			continue
		}

		conns = append(conns, map[string]any{"id": g.Connection.ID, "name": g.Connection.Name, "db_type": g.Connection.DBType, "writable": !g.ReadOnly})
	}

	out := map[string]any{
		"client":       req.client.Name,
		"redirect_uri": req.params.RedirectURI,
		"connections":  conns,
	}

	if req.connection != nil {
		out["connection"] = req.connection.Connection.ID
	}

	writeJSON(w, http.StatusOK, out)
}

func redirectWith(p authorizeParams, v url.Values) string {
	if p.State != "" {
		v.Set("state", p.State)
	}

	sep := "?"
	if strings.Contains(p.RedirectURI, "?") {
		sep = "&"
	}

	return p.RedirectURI + sep + v.Encode()
}

// oauthApprove answers the approval page: it makes a code for the client,
// or tells it the user said no, and returns where the browser goes next.
func (s *Server) oauthApprove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		authorizeParams
		Approve      bool   `json:"approve"`
		ConnectionID string `json:"connection_id"`
		// ReadOnly is sent by pages of earlier versions and ignored: what
		// the client may do on each connection (read, write, schema) is the
		// user's MCP access setting, not part of the approval.
		ReadOnly bool `json:"read_only"`
		// Remember approves this client again without asking.
		Remember bool `json:"remember"`
	}

	if !readJSON(w, r, &req) {
		return
	}

	user := currentUser(r)

	ar, err := s.checkAuthorize(r, user, req.authorizeParams)
	if ar == nil {
		writeError(w, http.StatusBadRequest, err.Error())

		return
	}

	var redirect *errRedirect
	if errors.As(err, &redirect) {
		writeJSON(w, http.StatusOK, map[string]string{"redirect": redirectWith(req.authorizeParams, url.Values{
			"error": {redirect.code}, "error_description": {redirect.desc},
		})})

		return
	}

	if !req.Approve {
		writeJSON(w, http.StatusOK, map[string]string{"redirect": redirectWith(req.authorizeParams, url.Values{
			"error": {"access_denied"}, "error_description": {"the user declined"},
		})})

		return
	}

	// A client that asked for one connection gets only that one.
	connID := req.ConnectionID
	if ar.connection != nil {
		connID = ar.connection.Connection.ID
	}

	if connID != "" {
		if _, err := s.store.GetGrant(r.Context(), user, connID); err != nil {
			writeError(w, http.StatusBadRequest, "you have no access to this connection")

			return
		}
	}

	if req.Remember {
		if err := s.store.SaveOAuthConsent(r.Context(), &store.OAuthConsent{
			UserID: user.ID, ClientID: ar.client.ID, ConnectionID: connID,
		}); err != nil {
			writeStoreError(w, err)

			return
		}
	}

	next, err := s.issueCode(r, user, ar, connID)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"redirect": next})
}

// issueCode makes an authorization code and returns the redirect that
// hands it to the client.
func (s *Server) issueCode(r *http.Request, user *store.User, ar *authorizeRequest, connID string) (string, error) {
	code, err := randomSecret("")
	if err != nil {
		return "", err
	}

	if err := s.store.CreateOAuthCode(r.Context(), &store.OAuthCode{
		CodeHash: dpauth.HashSecret(code), ClientID: ar.client.ID, UserID: user.ID, RedirectURI: ar.params.RedirectURI,
		CodeChallenge: ar.params.CodeChallenge, ConnectionID: connID,
		ExpiresAt: time.Now().Add(oauthCodeTTL).UnixMilli(),
	}); err != nil {
		return "", err
	}

	v := url.Values{"code": {code}}
	v.Set("iss", s.publicBase(r))

	return redirectWith(ar.params, v), nil
}

// rememberedApproval approves a request again from a remembered consent.
// It returns "" to ask the user: nothing remembered, another connection
// asked for, or the grant behind it gone.
func (s *Server) rememberedApproval(r *http.Request, user *store.User, ar *authorizeRequest) string {
	c, err := s.store.GetOAuthConsent(r.Context(), user.ID, ar.client.ID)
	if err != nil {
		return ""
	}

	if ar.connection != nil && ar.connection.Connection.ID != c.ConnectionID {
		return ""
	}

	// A remembered connection the user can no longer reach is asked again.
	if c.ConnectionID != "" {
		if _, err := s.store.GetGrant(r.Context(), user, c.ConnectionID); err != nil {
			return ""
		}
	}

	redirect, err := s.issueCode(r, user, ar, c.ConnectionID)
	if err != nil {
		slog.Error("oauth remembered approval", "error", err)

		return ""
	}

	return redirect
}
