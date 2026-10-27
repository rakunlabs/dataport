package server

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

type ctxKey struct{}

func currentUser(r *http.Request) *store.User {
	u, _ := r.Context().Value(ctxKey{}).(*store.User)

	return u
}

func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(cookieName)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "not logged in")

			return
		}

		user, sess, err := s.auth.AuthenticateSession(r.Context(), cookie.Value)
		if err == nil && sess.Remember && time.Since(time.UnixMilli(sess.LastSeenAt)) > time.Hour {
			// A remembered session slides on the server; its cookie follows
			// on the first visit after a pause, so it ends with the session.
			s.setCookieFor(w, r, cookie.Value, time.Until(time.UnixMilli(sess.ExpiresAt)))
		}

		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				slog.Error("authenticate", "error", err)
			}

			s.clearCookie(w, r)
			writeError(w, http.StatusUnauthorized, "session expired")

			return
		}

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, user)))
	})
}

func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u := currentUser(r); u == nil || !u.IsAdmin() {
			writeError(w, http.StatusForbidden, "admin only")

			return
		}

		next.ServeHTTP(w, r)
	})
}

// sameOrigin rejects state-changing requests whose Origin does not match the
// host. Together with the SameSite=Lax cookie this stops cross-site requests.
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if origin := r.Header.Get("Origin"); origin != "" && !originMatches(origin, r) {
				writeError(w, http.StatusForbidden, "cross-origin request")

				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// originMatches reports whether the Origin header names the host the request
// was sent to. Behind a reverse proxy that rewrites Host, the proxy must pass
// the original one in X-Forwarded-Host.
func originMatches(origin string, r *http.Request) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}

	return strings.EqualFold(u.Host, requestHost(r))
}

func limitBody(max int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, max)
			next.ServeHTTP(w, r)
		})
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return host
}
