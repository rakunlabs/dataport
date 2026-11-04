package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAIDoRetries(t *testing.T) {
	for _, tc := range []struct {
		name     string
		statuses []int
		retries  int
		wantTry  int32
		wantErr  string
	}{
		{name: "ok first", statuses: []int{200}, retries: 2, wantTry: 1},
		{name: "429 then ok", statuses: []int{429, 200}, retries: 2, wantTry: 2},
		{name: "503 twice then ok", statuses: []int{503, 503, 200}, retries: 2, wantTry: 3},
		{name: "gives up", statuses: []int{502, 502, 502, 502}, retries: 2, wantTry: 3, wantErr: "after 3 tries"},
		{name: "no retries", statuses: []int{500, 200}, retries: 0, wantTry: 1, wantErr: "answered 500"},
		{name: "4xx is final", statuses: []int{401, 200}, retries: 2, wantTry: 1, wantErr: "answered 401"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var tries atomic.Int32

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if string(body) != `{"x":1}` {
					t.Errorf("try %d sent body %q", tries.Load()+1, body)
				}

				n := tries.Add(1)
				status := tc.statuses[n-1]
				if status != 200 {
					// The wait is asked for, so the test does not sleep.
					w.Header().Set("Retry-After", "0")
				}

				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":{"message":"busy"}}`)
			}))
			defer srv.Close()

			body := []byte(`{"x":1}`)
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, strings.NewReader(string(body)))

			resp, err := aiDo(context.Background(), aiClient(""), req, body, tc.retries)
			if resp != nil {
				_ = resp.Body.Close()
			}

			if got := tries.Load(); got != tc.wantTry {
				t.Errorf("tries = %d, want %d", got, tc.wantTry)
			}

			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("error = %v, want one with %q", err, tc.wantErr)
			}
		})
	}
}

// A cancelled request stops waiting for the next try.
func TestAIDoStopsOnCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "10")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, nil)
	start := time.Now()

	if _, err := aiDo(ctx, aiClient(""), req, nil, 3); err == nil {
		t.Fatal("expected an error")
	}

	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %v; the wait should end with the context", d)
	}
}

// The proxy setting sends the request through the proxy.
func TestAIClientProxy(t *testing.T) {
	var seen atomic.Value

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A forward proxy gets the absolute URL of the target.
		seen.Store(r.URL.String())
		w.WriteHeader(http.StatusOK)
	}))
	defer proxy.Close()

	req, _ := http.NewRequest(http.MethodGet, "http://ai.invalid/v1/chat/completions", nil)

	resp, err := aiDo(context.Background(), aiClient(proxy.URL), req, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	_ = resp.Body.Close()

	if got, _ := seen.Load().(string); got != "http://ai.invalid/v1/chat/completions" {
		t.Errorf("proxy saw %q", got)
	}
}

func TestAIConfigCheck(t *testing.T) {
	n := func(i int) *int { return &i }

	for _, tc := range []struct {
		v    aiConfig
		want string
	}{
		{aiConfig{Proxy: "http://proxy:3128"}, ""},
		{aiConfig{Proxy: "socks5://proxy:1080"}, "proxy must be"},
		{aiConfig{Proxy: "http://proxy:3128/path"}, "proxy must be"},
		{aiConfig{MaxSteps: n(0)}, "between 1 and"},
		{aiConfig{MaxSteps: n(aiMaxStepsLimit + 1)}, "between 1 and"},
		{aiConfig{MaxSteps: n(30)}, ""},
		{aiConfig{Retries: n(-1)}, "retries must be"},
		{aiConfig{Retries: n(aiRetriesLimit + 1)}, "retries must be"},
		{aiConfig{Retries: n(0)}, ""},
	} {
		got := tc.v.check()
		if (tc.want == "") != (got == "") || (tc.want != "" && !strings.Contains(got, tc.want)) {
			t.Errorf("check(%+v) = %q, want %q", tc.v, got, tc.want)
		}
	}
}

func TestRetryAfter(t *testing.T) {
	if got := retryAfter("3"); got != 3*time.Second {
		t.Errorf("seconds: %v", got)
	}

	if got := retryAfter(""); got != 0 {
		t.Errorf("empty: %v", got)
	}

	if got := retryAfter(time.Now().Add(-time.Minute).UTC().Format(http.TimeFormat)); got != 0 {
		t.Errorf("past date: %v", got)
	}
}

// The admin's step limit ends a turn: after it the model is asked once
// more without tools and must answer in words.
func TestAIAssistantStepLimit(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")
	s := env.srv

	call := sse(toolCall("c", "dataport_list_tables", `{"connection":"shop"}`))
	fake := &fakeAI{replies: []func(http.ResponseWriter){call, call, sse(`{"choices":[{"delta":{"content":"Enough."}}]}`)}}
	ai := httptest.NewServer(fake)
	t.Cleanup(ai.Close)

	steps := 2
	raw, _ := json.Marshal(aiConfig{Enabled: true, BaseURL: ai.URL, Model: "m", MaxSteps: &steps})
	if err := env.st.SetSetting(ctx, aiSetting, string(raw)); err != nil {
		t.Fatal(err)
	}

	data, _ := json.Marshal(map[string]any{"message": "list everything"})
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(data)))
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, env.alice))
	w := httptest.NewRecorder()
	s.aiChat(w, r)

	if len(fake.requests) != 3 {
		t.Fatalf("the model was asked %d times, want 3 (2 steps and the answer)", len(fake.requests))
	}

	if _, ok := fake.requests[2]["tools"]; ok {
		t.Error("the last request still offered tools")
	}

	if _, ok := fake.requests[1]["tools"]; !ok {
		t.Error("a request within the limit had no tools")
	}

	if !strings.Contains(w.Body.String(), "Enough.") {
		t.Errorf("answer: %s", w.Body)
	}
}

// The AI proxy may carry an account: it is stored encrypted, shown masked,
// and a masked password is kept only for the same host and user.
func TestAIProxySealed(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")
	s := env.srv

	set := func(proxy string) (int, map[string]any) {
		t.Helper()

		data, _ := json.Marshal(map[string]any{"enabled": false, "base_url": "https://api.example.com/v1", "model": "", "proxy": proxy})
		r := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(string(data)))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.adminSetAI(w, r)

		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)

		return w.Code, out
	}

	stored := func() (string, string) {
		raw, _ := env.st.GetSetting(ctx, aiSetting)
		v, err := s.loadAI(ctx)
		if err != nil {
			t.Fatal(err)
		}

		return raw, v.Proxy
	}

	code, out := set("http://bob:hunter2@proxy.corp:3128")
	if code != http.StatusOK || out["proxy"] != "http://bob:xxxxx@proxy.corp:3128" {
		t.Fatalf("set: %d %v", code, out)
	}

	raw, proxy := stored()
	if strings.Contains(raw, "hunter2") || strings.Contains(raw, "proxy.corp") {
		t.Fatalf("the proxy is stored in clear: %s", raw)
	}

	if proxy != "http://bob:hunter2@proxy.corp:3128" {
		t.Fatalf("read back %q", proxy)
	}

	// Sent back masked, it keeps the password.
	if code, _ := set("http://bob:xxxxx@proxy.corp:3128"); code != http.StatusOK {
		t.Fatal("masked save refused")
	}

	if _, proxy := stored(); proxy != "http://bob:hunter2@proxy.corp:3128" {
		t.Fatalf("masked save changed the proxy: %q", proxy)
	}

	// Masked to another host, it is refused.
	if code, out := set("http://bob:xxxxx@evil.example:3128"); code != http.StatusBadRequest {
		t.Fatalf("masked password to another host: %d %v", code, out)
	}

	// Rotating the secret key reseals it with the other *_enc settings.
	rewritten, err := env.st.RewriteSecrets(ctx, func(v string) (string, error) {
		plain, err := s.cipher.Decrypt(v)
		if err != nil {
			return "", err
		}

		return "resealed:" + plain, nil
	})
	if err != nil || rewritten.Rewritten == 0 {
		t.Fatalf("rewrite: %+v %v", rewritten, err)
	}

	if raw, _ := env.st.GetSetting(ctx, aiSetting); !strings.Contains(raw, `"proxy_enc":"resealed:http://bob:hunter2@proxy.corp:3128"`) {
		t.Fatalf("the AI proxy was not resealed: %s", raw)
	}

	// A proxy the cipher cannot read stops the AI rather than connect
	// around it; saving the settings again replaces it.
	if _, err := s.loadAI(ctx); err == nil || !strings.Contains(err.Error(), "proxy cannot be read") {
		t.Fatalf("unreadable proxy: %v", err)
	}

	if code, _ := set("http://bob:hunter2@proxy.corp:3128"); code != http.StatusOK {
		t.Fatal("the admin cannot save over an unreadable proxy")
	}

	if _, proxy := stored(); proxy != "http://bob:hunter2@proxy.corp:3128" {
		t.Fatalf("after saving again: %q", proxy)
	}
}
