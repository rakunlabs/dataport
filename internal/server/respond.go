package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/store"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"message": msg})
}

// writeStoreError maps store errors to HTTP responses and hides internal
// details behind a generic message.
func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "name already exists")
	default:
		slog.Error("request failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

// writeEngineError reports an engine failure. A failure to reach the target
// database can name its host, so regular users get a generic message and the
// detail goes to the log.
func writeEngineError(w http.ResponseWriter, r *http.Request, err error) {
	var connErr *engine.ConnError

	switch {
	case errors.As(err, &connErr):
		slog.Warn("target database unreachable", "error", err)

		msg := "cannot reach the database, contact your administrator"
		if u := currentUser(r); u != nil && u.IsAdmin() {
			msg = err.Error()
		}

		// With their own account, a refused sign-in is the user's to fix.
		if connErr.Auth && connErr.UserCredentials {
			writeCredentialsRequired(w, "the database refused your username or password")

			return
		}

		writeError(w, http.StatusBadGateway, msg)
	case errors.Is(err, engine.ErrReadOnly):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, engine.ErrTxLost):
		writeJSON(w, http.StatusGone, map[string]any{"message": err.Error(), "tx_lost": true})
	case errors.Is(err, engine.ErrTxBusy), errors.Is(err, engine.ErrTxConnection), errors.Is(err, engine.ErrTxLimit):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(v); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "request too large")

			return false
		}

		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))

		return false
	}

	if dec.More() {
		writeError(w, http.StatusBadRequest, "invalid request: trailing data")

		return false
	}

	_, _ = io.Copy(io.Discard, r.Body)

	return true
}

// writeConfirmationRequired asks the user to confirm changes on a production
// connection.
func writeConfirmationRequired(w http.ResponseWriter, risk engine.Risk) {
	writeJSON(w, http.StatusConflict, map[string]any{
		"message":               "this is a production database; confirm to run the changes",
		"confirmation_required": true,
		"risk":                  risk,
	})
}

// writeGrantError answers a failure to load a grant. notFound names the
// connection in the 404 message.
func writeGrantError(w http.ResponseWriter, err error, notFound string) {
	var credErr *credentialsError

	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, notFound)
	case errors.As(err, &credErr):
		writeCredentialsRequired(w, credErr.msg)
	case errors.Is(err, errSecret):
		writeError(w, http.StatusInternalServerError, "connection secret cannot be read")
	case errors.Is(err, errNotebookOnly):
		writeError(w, http.StatusForbidden, err.Error())
	default:
		writeStoreError(w, err)
	}
}
