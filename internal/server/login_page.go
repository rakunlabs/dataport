package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/rakunlabs/dataport/internal/store"
)

const loginPageSetting = "login_page"

// LoginPage is what admins set for the sign-in page. It is read from the
// store on each use, so every replica shows a change at once.
type LoginPage struct {
	// Subtitle names this instance, such as "sandbox", so users see where
	// they sign in. It is shown on the sign-in card and in the top bar.
	Subtitle string `json:"subtitle"`
	// CollapseLocalLogin folds the username and password form behind a
	// "Local login" button, as pika's "Collapse local login form".
	CollapseLocalLogin bool `json:"collapse_local_login"`
}

const maxSubtitle = 64

func (s *Server) loginPage(ctx context.Context) LoginPage {
	var p LoginPage

	raw, err := s.store.GetSetting(ctx, loginPageSetting)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			slog.Error("read login page settings", "error", err)
		}

		return p
	}

	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		slog.Warn("ignoring stored login page settings", "error", err)

		return LoginPage{}
	}

	return p
}

func (s *Server) adminGetLoginPage(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.loginPage(r.Context()))
}

func (s *Server) adminSetLoginPage(w http.ResponseWriter, r *http.Request) {
	var in LoginPage
	if !readJSON(w, r, &in) {
		return
	}

	in.Subtitle = strings.TrimSpace(in.Subtitle)
	if utf8.RuneCountInString(in.Subtitle) > maxSubtitle {
		writeError(w, http.StatusBadRequest, "the subtitle may have at most 64 characters")

		return
	}

	data, err := json.Marshal(in)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	if err := s.store.SetSetting(r.Context(), loginPageSetting, string(data)); err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, in)
}
