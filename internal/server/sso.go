package server

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/rakunlabs/dataport/internal/sso"
	"github.com/rakunlabs/dataport/internal/store"
)

// authInfo tells the login page which sign-in methods exist.
func (s *Server) authInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"password":  !s.cfg.Auth.DisablePasswordLogin,
		"providers": s.sso.Public(),
	})
}

// ssoLogin starts the flow (redirect to the provider) and finishes it on the
// callback, where it opens a dataport session and returns to the app.
func (s *Server) ssoLogin(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	id, provider, ok := s.sso.Login(w, r, name)
	if !ok {
		return
	}

	user, err := s.sso.Resolve(r.Context(), provider, id)
	if err != nil {
		var denied *sso.ErrDenied
		if !errors.As(err, &denied) {
			slog.Error("sso resolve", "provider", name, "error", err)
			s.redirectLogin(w, r, "sign-in failed, try again")

			return
		}

		slog.Info("sso sign-in refused", "provider", name, "subject", id.Subject, "reason", denied.Reason)
		s.redirectLogin(w, r, denied.Reason)

		return
	}

	token, err := s.auth.CreateSession(r.Context(), user.ID, clientIP(r), r.UserAgent())
	if err != nil {
		slog.Error("sso session", "error", err)
		s.redirectLogin(w, r, "sign-in failed, try again")

		return
	}

	_ = s.store.TouchUserLogin(r.Context(), user.ID)

	s.setCookie(w, r, token)
	http.Redirect(w, r, s.cookiePath(), http.StatusSeeOther)
}

func (s *Server) redirectLogin(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, s.cookiePath()+"#/login?error="+url.QueryEscape(msg), http.StatusSeeOther)
}

// ////////////////////////////////////////////
// admin

func (s *Server) adminListSSO(w http.ResponseWriter, _ *http.Request) {
	views := s.sso.Views()

	out := make([]map[string]any, 0, len(views))
	for _, v := range views {
		out = append(out, map[string]any{"provider": v, "status": s.sso.Status(v.Name)})
	}

	writeJSON(w, http.StatusOK, out)
}

type ssoRequest struct {
	sso.Provider
	// ClientSecret nil keeps the stored secret.
	ClientSecret *string `json:"client_secret"`
	// The admin form echoes these read-only fields back; they are ignored.
	HasSecret   bool   `json:"has_secret"`
	CallbackURL string `json:"callback_url"`
}

func (s *Server) adminSaveSSO(w http.ResponseWriter, r *http.Request) {
	var req ssoRequest
	if !readJSON(w, r, &req) {
		return
	}

	req.ClientSecretEnc = ""

	if err := s.sso.Save(r.Context(), r.PathValue("name"), req.Provider, req.ClientSecret); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "a provider with this name already exists")

			return
		}

		writeError(w, http.StatusBadRequest, err.Error())

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminDeleteSSO(w http.ResponseWriter, r *http.Request) {
	if err := s.sso.Delete(r.Context(), r.PathValue("name")); err != nil {
		writeStoreError(w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminUnlinkSSO(w http.ResponseWriter, r *http.Request) {
	if err := s.store.UnlinkIdentities(r.Context(), r.PathValue("id")); err != nil {
		writeStoreError(w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}
