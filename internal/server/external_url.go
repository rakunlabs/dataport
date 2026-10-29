package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/rakunlabs/dataport/internal/store"
)

// The external URL is the address dataport is reached at from outside,
// such as https://example.com/tools when a proxy serves it under a path.
// OAuth for MCP clients and SSO redirects use it. Admins set it in the UI;
// until they do, the configured value applies, and with neither the address
// comes from the request and the proxy's forwarded headers.

const externalURLSetting = "external_url"

// externalURL is the address in use: the saved one, else the configured.
func (s *Server) externalURL() string {
	s.settingsMu.RLock()
	defer s.settingsMu.RUnlock()

	return s.external
}

// checkExternalURL tells what is wrong with an address, or "".
func checkExternalURL(raw string) string {
	if raw == "" {
		return ""
	}

	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "the external URL must be an http(s) address such as https://dataport.example.com or https://example.com/tools"
	}

	return ""
}

// savedExternalURL reads the address an admin saved; ok is false when none.
func (s *Server) savedExternalURL(ctx context.Context) (string, bool, error) {
	raw, err := s.store.GetSetting(ctx, externalURLSetting)
	if errors.Is(err, store.ErrNotFound) {
		return "", false, nil
	}

	if err != nil {
		return "", false, err
	}

	return strings.TrimRight(strings.TrimSpace(raw), "/"), true, nil
}

// loadExternalURL puts the saved or configured address to use, here and
// for SSO. A change saved on another replica arrives within a minute.
func (s *Server) loadExternalURL(ctx context.Context) error {
	saved, ok, err := s.savedExternalURL(ctx)
	if err != nil {
		return err
	}

	next := strings.TrimRight(strings.TrimSpace(s.base.Auth.ExternalURL), "/")
	if ok && checkExternalURL(saved) == "" {
		next = saved
	}

	s.settingsMu.Lock()
	changed := next != s.external
	s.external = next
	s.settingsMu.Unlock()

	if changed && s.sso != nil {
		if err := s.sso.SetExternalURL(ctx, next); err != nil {
			slog.Error("apply external URL to single sign-on", "error", err)
		}
	}

	return nil
}

type externalURLView struct {
	// Value is the address in use; Saved is set when an admin saved it,
	// and Config is the configured address it overrides.
	Value  string `json:"value"`
	Saved  bool   `json:"saved"`
	Config string `json:"config"`
	// Detected is what dataport makes of the request without one.
	Detected string `json:"detected"`
}

func (s *Server) externalURLView(r *http.Request) (externalURLView, error) {
	_, saved, err := s.savedExternalURL(r.Context())
	if err != nil {
		return externalURLView{}, err
	}

	scheme := "http"
	if s.isSecure(r) {
		scheme = "https"
	}

	return externalURLView{
		Value: s.externalURL(), Saved: saved, Config: strings.TrimRight(s.base.Auth.ExternalURL, "/"),
		Detected: scheme + "://" + requestHost(r) + forwardedPrefix(r),
	}, nil
}

func (s *Server) adminGetExternalURL(w http.ResponseWriter, r *http.Request) {
	v, err := s.externalURLView(r)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, v)
}

// adminSetExternalURL saves the address; null drops the saved one, so the
// configured address applies again. An empty string means none at all.
func (s *Server) adminSetExternalURL(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Value *string `json:"value"`
	}

	if !readJSON(w, r, &in) {
		return
	}

	var err error

	if in.Value == nil {
		err = s.store.DeleteSetting(r.Context(), externalURLSetting)
	} else {
		v := strings.TrimRight(strings.TrimSpace(*in.Value), "/")
		if msg := checkExternalURL(v); msg != "" {
			writeError(w, http.StatusBadRequest, msg)

			return
		}

		err = s.store.SetSetting(r.Context(), externalURLSetting, v)
	}

	if err != nil {
		writeStoreError(w, err)

		return
	}

	if err := s.loadExternalURL(r.Context()); err != nil {
		writeStoreError(w, err)

		return
	}

	s.adminGetExternalURL(w, r)
}
