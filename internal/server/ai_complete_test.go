package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Inline completion asks the service in the form its kind takes, with the
// tables before the text, and gives the editor only what goes at the cursor.
func TestAIInlineComplete(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")
	s := env.srv

	var (
		paths  []string
		bodies []map[string]any
	)
	reply := map[string]string{
		"/completions":      `{"choices":[{"text":"active = true\n\nSELECT 2"}]}`,
		"/fim/completions":  `{"choices":[{"message":{"content":"active = true"}}]}`,
		"/chat/completions": "{\"choices\":[{\"message\":{\"content\":\"```sql\\nWHERE active = true\\n```\"}}]}",
	}
	ai := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		paths = append(paths, r.URL.Path)
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, reply[r.URL.Path])
	}))
	t.Cleanup(ai.Close)

	complete := func() (int, string) {
		t.Helper()

		data, _ := json.Marshal(map[string]any{"prefix": "SELECT name FROM users\nWHERE ", "suffix": ";", "db_type": "postgres", "tables": []string{"users(id integer, active boolean)"}})
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(data)))
		r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, env.alice))
		w := httptest.NewRecorder()
		s.aiInlineComplete(w, r)

		var out struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &out)

		return w.Code, out.Text
	}

	if code, _ := complete(); code != http.StatusForbidden || s.toMe(ctx, env.alice).AIComplete {
		t.Fatalf("completion before it is set up: %d", code)
	}

	// Shared takes the assistant's address and key.
	key, _ := s.cipher.Encrypt("sk-chat")
	raw, _ := json.Marshal(aiConfig{BaseURL: ai.URL, Model: "big", KeyEnc: key})
	_ = env.st.SetSetting(ctx, aiSetting, string(raw))

	for _, mode := range []string{aiModeFIM, aiModeCodestral, aiModeChat} {
		raw, _ := json.Marshal(aiCompleteConfig{Enabled: true, Mode: mode, Shared: true, Model: "small"})
		_ = env.st.SetSetting(ctx, aiCompleteSetting, string(raw))

		if !s.toMe(ctx, env.alice).AIComplete {
			t.Fatalf("%s: set up but not shown", mode)
		}

		code, text := complete()
		if code != http.StatusOK || text != "active = true" {
			t.Fatalf("%s: %d %q", mode, code, text)
		}
	}

	if strings.Join(paths, ",") != "/completions,/fim/completions,/chat/completions" {
		t.Fatalf("paths: %v", paths)
	}

	fim := bodies[0]
	if fim["model"] != "small" || fim["suffix"] != ";" || !strings.Contains(fmt.Sprint(fim["prompt"]), "-- users(id integer, active boolean)\n\nSELECT name") {
		t.Fatalf("fim request: %v", fim)
	}

	chat := fmt.Sprint(bodies[2]["messages"])
	if !strings.Contains(chat, "WHERE "+aiCursor+";") || !strings.Contains(chat, "postgres SQL") {
		t.Fatalf("chat request: %s", chat)
	}
}

// The endpoint path may be set: a proxy's base path stays in the address,
// queries are joined, and Ollama's /api/generate answers with response.
func TestAIInlineCompletePath(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")
	s := env.srv

	var got []string
	ai := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		got = append(got, r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/api/generate") {
			if body["options"] == nil || body["suffix"] != ";" {
				t.Errorf("ollama body: %v", body)
			}
			_, _ = fmt.Fprint(w, `{"response":"id = 1","done":true}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"content":"id = 2"}`)
	}))
	t.Cleanup(ai.Close)

	for _, c := range []struct{ base, path, want, text string }{
		{ai.URL + "/proxy/ollama/", "/api/generate", "/proxy/ollama/api/generate", "id = 1"},
		{ai.URL + "/llm?team=a", "/infill?x=1", "/llm/infill?team=a&x=1", "id = 2"},
	} {
		got = nil
		text, err := s.aiInfill(ctx, aiRoute{Mode: aiModeFIM, BaseURL: c.base, Path: c.path, Model: "m"}, aiInfillInput{Prefix: "WHERE ", Suffix: ";"})
		if err != nil || text != c.text || len(got) != 1 || got[0] != c.want {
			t.Fatalf("%s%s: %q %v %v", c.base, c.path, text, err, got)
		}
	}

	for p, ok := range map[string]bool{"": true, "/v1/completions": true, "/x?api-version=1": true, "completions": false, "//evil.com/x": false, "/a b": false} {
		if aiPathValid(p) != ok {
			t.Errorf("path %q valid=%v", p, !ok)
		}
	}
}

func TestAICleanCompletion(t *testing.T) {
	for _, c := range []struct {
		text, prefix, suffix string
		chat                 bool
		want                 string
	}{
		{"id = 1)", "WHERE (", ")", false, "id = 1"},
		{"<think>hmm</think>\n  id = 1", "WHERE ", "", true, "id = 1"},
		{"SELECT * FROM t WHERE id = 1", "SELECT * FROM t WHERE ", "", true, "id = 1"},
		{"a,\n  b\n\nother", "SELECT ", "", false, "a,\n  b"},
		{"   \n", "x", "", false, ""},
	} {
		if got := aiCleanCompletion(c.text, c.prefix, c.suffix, c.chat); got != c.want {
			t.Errorf("%q: %q, want %q", c.text, got, c.want)
		}
	}
}
