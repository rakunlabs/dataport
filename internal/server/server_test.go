package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
