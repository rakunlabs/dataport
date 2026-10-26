package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/dataport/internal/config"
	"github.com/rakunlabs/dataport/internal/store"
)

func TestSameOrigin(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := sameOrigin(next)

	tests := []struct {
		name      string
		method    string
		host      string
		forwarded string
		origin    string
		want      int
	}{
		{"no origin", http.MethodPost, "app.example", "", "", http.StatusNoContent},
		{"same host", http.MethodPost, "localhost:3000", "", "http://localhost:3000", http.StatusNoContent},
		{"case insensitive", http.MethodPost, "App.Example", "", "https://app.example", http.StatusNoContent},
		{"other host", http.MethodPost, "app.example", "", "https://evil.example", http.StatusForbidden},
		{"suffix trick", http.MethodPost, "app.example", "", "https://evil-app.example", http.StatusForbidden},
		{"other port", http.MethodPost, "localhost:8080", "", "http://localhost:3000", http.StatusForbidden},
		{"proxy forwarded host", http.MethodPost, "dataport:8080", "app.example", "https://app.example", http.StatusNoContent},
		{"proxy list", http.MethodPost, "dataport:8080", "app.example, inner", "https://app.example", http.StatusNoContent},
		{"null origin", http.MethodPost, "app.example", "", "null", http.StatusForbidden},
		{"get is not checked", http.MethodGet, "app.example", "", "https://evil.example", http.StatusNoContent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, "/api/v1/auth/login", nil)
			r.Host = tt.host
			if tt.forwarded != "" {
				r.Header.Set("X-Forwarded-Host", tt.forwarded)
			}
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}

			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)

			if w.Code != tt.want {
				t.Fatalf("status = %d, want %d", w.Code, tt.want)
			}
		})
	}
}

func TestTimeouts(t *testing.T) {
	s := &Server{cfg: &config.Config{}}
	none, ten := 0, 10
	c := &store.Connection{}

	if got := s.timeout(c); got != 0 {
		t.Fatalf("no limit by default, got %v", got)
	}

	c.TimeoutMS = 30000
	if got := s.timeout(c); got != 30*time.Second {
		t.Fatalf("connection limit: %v", got)
	}

	if got := s.runTimeout(c, &none); got != 0 {
		t.Fatalf("user asked for none: %v", got)
	}

	if got := s.runTimeout(c, nil); got != 30*time.Second {
		t.Fatalf("connection limit kept: %v", got)
	}

	s.cfg.Query.MaxTimeout = 5 * time.Second
	if got := s.runTimeout(c, &none); got != 5*time.Second {
		t.Fatalf("cap applies to no limit: %v", got)
	}

	if got := s.runTimeout(c, &ten); got != 5*time.Second {
		t.Fatalf("cap applies: %v", got)
	}
}

func TestAbout(t *testing.T) {
	config.Build = config.BuildInfo{Version: "v1.2.3", OS: "linux", Arch: "amd64", Repository: config.Repository}

	w := httptest.NewRecorder()
	(&Server{}).about(w, httptest.NewRequest(http.MethodGet, "/api/v1/about", nil))

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"version":"v1.2.3"`) ||
		!strings.Contains(w.Body.String(), `"repository":"https://github.com/rakunlabs/dataport"`) {
		t.Fatalf("about: %d %s", w.Code, w.Body.String())
	}
}
