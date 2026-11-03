package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/rakunlabs/dataport/internal/store"
)

// fakeAI is an OpenAI compatible chat completions service that answers
// with the next scripted reply and keeps the requests it got.
type fakeAI struct {
	mu       sync.Mutex
	replies  []func(w http.ResponseWriter)
	requests []map[string]any
	auth     []string
}

func (f *fakeAI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)

	f.mu.Lock()
	f.requests = append(f.requests, body)
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	next := f.replies[0]
	f.replies = f.replies[1:]
	f.mu.Unlock()

	next(w)
}

func sse(chunks ...string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, c := range chunks {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", c)
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}
}

func toolCall(id, name, args string) string {
	a, _ := json.Marshal(args)

	return fmt.Sprintf(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":%q,"type":"function","function":{"name":%q,"arguments":%s}}]}}]}`, id, name, a)
}

// The assistant reads the schema through the read-only tools as the user,
// streams its answer, proposes SQL for the editor, and keeps the
// conversation until the user deletes it.
func TestAIAssistant(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")
	s := env.srv

	fake := &fakeAI{replies: []func(http.ResponseWriter){
		// Turn 1: list tables, run a query, then answer with SQL for the editor.
		sse(toolCall("c1", "dataport_list_tables", `{"connection":"shop"}`)),
		sse(toolCall("c2", "dataport_query", `{"connection":"shop","sql":"SELECT count(*) AS n FROM customers"}`)),
		sse(`{"choices":[{"delta":{"content":"There are "}}]}`, `{"choices":[{"delta":{"content":"2 customers."}}]}`,
			toolCall("c3", aiEditorTool, `{"sql":"SELECT * FROM customers","mode":"replace"}`)),
		// Turn 2: a plain answer, then the same question answered again.
		sse(`{"choices":[{"delta":{"content":"Done."}}]}`),
		sse(`{"choices":[{"delta":{"content":"Done again."}}]}`),
	}}
	ai := httptest.NewServer(fake)
	t.Cleanup(ai.Close)

	if s.toMe(ctx, env.alice).AI {
		t.Fatal("the assistant shows before it is set up")
	}

	key, _ := s.cipher.Encrypt("sk-test")
	raw, _ := json.Marshal(aiConfig{Enabled: true, BaseURL: ai.URL, Model: "m", KeyEnc: key})
	if err := env.st.SetSetting(ctx, aiSetting, string(raw)); err != nil {
		t.Fatal(err)
	}
	if !s.toMe(ctx, env.alice).AI {
		t.Fatal("the assistant is set up but not shown")
	}

	chat := func(body map[string]any) []map[string]any {
		t.Helper()

		data, _ := json.Marshal(body)
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(data)))
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, env.alice))
		w := httptest.NewRecorder()
		s.aiChat(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("chat: %d %s", w.Code, w.Body)
		}

		var events []map[string]any
		sc := bufio.NewScanner(w.Body)
		for sc.Scan() {
			var e map[string]any
			_ = json.Unmarshal(sc.Bytes(), &e)
			events = append(events, e)
		}

		return events
	}

	events := chat(map[string]any{"message": "How many customers?", "context": map[string]any{
		"connection": "shop", "db_type": "sqlite", "tab_kind": "query", "production": true,
		"sql": "SELECT 1;\nSELECT nme FROM customers;", "statement": "SELECT nme FROM customers;", "cursor_line": 2,
		"result": map[string]any{"statement": "SELECT 1", "kind": "rows", "columns": []string{"1 INTEGER"}, "rows": [][]any{{1}}, "row_count": 1},
	}})

	var (
		text   string
		convID string
		editor map[string]any
		tools  []string
	)
	for _, e := range events {
		switch e["type"] {
		case "conversation":
			convID = e["conversation"].(map[string]any)["id"].(string)
		case "text":
			text += e["text"].(string)
		case "tool":
			tools = append(tools, e["name"].(string))
		case "tool_done":
			if e["error"] == true {
				t.Fatalf("tool failed: %v", events)
			}
		case "editor":
			editor = e
		case "error":
			t.Fatalf("chat error: %v", e)
		}
	}

	if text != "There are 2 customers." || editor["sql"] != "SELECT * FROM customers" || strings.Join(tools, ",") != "dataport_list_tables,dataport_query" {
		t.Fatalf("answer: %q %v %v", text, editor, tools)
	}
	if last := events[len(events)-1]; last["type"] != "done" {
		t.Fatalf("not done: %v", last)
	}

	// The key went to the service, the editor text and tools with the request,
	// and the query ran as alice, through the log.
	first := fake.requests[0]
	system := fmt.Sprint(first["messages"].([]any)[0].(map[string]any)["content"])
	for _, want := range []string{"cursor is in this statement", "SELECT nme FROM customers;", "line 2", "production", "1 rows in"} {
		if !strings.Contains(system, want) {
			t.Fatalf("system prompt lacks %q:\n%s", want, system)
		}
	}
	if fake.auth[0] != "Bearer sk-test" {
		t.Fatalf("request: %v %v", fake.auth, first)
	}
	if names := fmt.Sprint(first["tools"]); !strings.Contains(names, "dataport_query") || !strings.Contains(names, aiEditorTool) || strings.Contains(names, "dataport_execute") {
		t.Fatalf("tools offered: %s", names)
	}
	if logs, _ := env.st.ListQueryLog(ctx, store.QueryLogFilter{UserID: env.alice.ID, Source: "mcp"}); len(logs) != 1 {
		t.Fatalf("query log: %d", len(logs))
	}

	// The next turn answers the editor proposal first.
	chat(map[string]any{"conversation_id": convID, "message": "Thanks", "editor_results": map[string]string{"c3": "applied"}})
	msgs := fake.requests[3]["messages"].([]any)
	var answered bool
	for _, m := range msgs {
		mm := m.(map[string]any)
		if mm["tool_call_id"] == "c3" && mm["content"] == "applied" {
			answered = true
		}
	}
	if !answered {
		t.Fatalf("editor answer not sent: %v", msgs)
	}

	// A retry drops the last answer and asks the same question once more.
	chat(map[string]any{"conversation_id": convID, "retry": true})
	retried := fake.requests[4]["messages"].([]any)
	thanks := 0
	for _, m := range retried {
		mm := m.(map[string]any)
		if mm["role"] == "user" && strings.HasSuffix(fmt.Sprint(mm["content"]), "] Thanks") {
			thanks++
		}
		if mm["content"] == "Done." {
			t.Fatal("the answer before the retry was sent again")
		}
	}
	if thanks != 1 || !strings.HasSuffix(fmt.Sprint(retried[len(retried)-1].(map[string]any)["content"]), "] Thanks") {
		t.Fatalf("retry history: %v", retried)
	}
	stored, _ := env.st.AIMessages(ctx, convID)
	var lastText string
	_ = json.Unmarshal([]byte(stored[len(stored)-1].Payload), &struct {
		Content *string `json:"content"`
	}{&lastText})
	var firstText string
	_ = json.Unmarshal([]byte(stored[0].Payload), &struct {
		Content *string `json:"content"`
	}{&firstText})
	if firstText != "How many customers?" {
		t.Fatalf("the stored question was changed: %q", firstText)
	}
	if lastText != "Done again." {
		t.Fatalf("stored after retry: %q", lastText)
	}

	// Only alice sees her conversation, and she can delete it.
	if _, err := env.st.GetAIConversation(ctx, env.bob.ID, convID); err == nil {
		t.Fatal("bob reads alice's conversation")
	}
	if err := env.st.DeleteAIConversation(ctx, env.alice.ID, convID); err != nil {
		t.Fatal(err)
	}
	if list, _ := env.st.ListAIConversations(ctx, env.alice.ID); len(list) != 0 {
		t.Fatal("conversation kept after delete")
	}
}

// The assistant's own tools read the user's help, limits, history and
// saved queries; a navigation button goes to the browser; the model is told
// the time and when each message was written.
func TestAIAssistantLocalTools(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")
	s := env.srv

	if err := env.st.AddQueryLog(ctx, &store.QueryLog{UserID: env.alice.ID, Username: "alice", ConnectionName: "shop", SQL: "SELECT name FROM customers WHERE id = 7", Status: "ok", Source: "web"}); err != nil {
		t.Fatal(err)
	}
	if err := env.st.AddQueryLog(ctx, &store.QueryLog{UserID: env.bob.ID, Username: "bob", ConnectionName: "hidden", SQL: "SELECT secret FROM bobs", Status: "ok", Source: "web"}); err != nil {
		t.Fatal(err)
	}
	if err := env.st.CreateSavedQuery(ctx, &store.SavedQuery{UserID: env.alice.ID, Name: "Monthly report", SQL: "SELECT 42"}); err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]string{
		aiHelpTool:    "Ctrl+Enter",
		aiLimitsTool:  "shop (sqlite): rows per statement",
		aiHistoryTool: "WHERE id = 7",
		aiSavedTool:   "Monthly report",
	} {
		out, ok := s.aiLocal(ctx, env.alice, name, `{}`)
		if !ok || !strings.Contains(out, want) {
			t.Fatalf("%s: %q", name, out)
		}
	}
	if out, _ := s.aiLocal(ctx, env.alice, aiHistoryTool, `{}`); strings.Contains(out, "bobs") {
		t.Fatal("history shows another user's statements")
	}
	if out, _ := s.aiLocal(ctx, env.alice, aiLimitsTool, `{"connection":"hidden"}`); !strings.HasPrefix(out, "error:") {
		t.Fatalf("limits of a connection not shared: %q", out)
	}

	fake := &fakeAI{replies: []func(http.ResponseWriter){
		sse(toolCall("h1", aiHelpTool, `{"topic":"shortcuts"}`)),
		sse(toolCall("n1", aiNavigateTool, `{"target":"table","connection":"shop","schema":"main","table":"customers"}`)),
		sse(`{"choices":[{"delta":{"content":"Here."}}]}`),
		// Turn 2: an ambiguous request gets a question that ends the turn.
		sse(toolCall("q1", aiAskTool, `{"question":"Which table?","choices":["customers","orders"]}`)),
		// Turn 3: the answer arrives as the next message.
		sse(`{"choices":[{"delta":{"content":"Orders it is."}}]}`),
	}}
	ai := httptest.NewServer(fake)
	t.Cleanup(ai.Close)

	key, _ := s.cipher.Encrypt("k")
	raw, _ := json.Marshal(aiConfig{Enabled: true, BaseURL: ai.URL, Model: "m", KeyEnc: key})
	_ = env.st.SetSetting(ctx, aiSetting, string(raw))

	data, _ := json.Marshal(map[string]any{"message": "open customers", "context": map[string]any{"time_zone": "Europe/Istanbul"}})
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(data)))
	r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, env.alice))
	w := httptest.NewRecorder()
	s.aiChat(w, r)

	body := w.Body.String()
	if !strings.Contains(body, `"type":"navigate"`) || !strings.Contains(body, `"table":"customers"`) || !strings.Contains(body, `"type":"done","at"`) && !strings.Contains(body, `"at":`) {
		t.Fatalf("events: %s", body)
	}

	ask := func(text string) string {
		data, _ := json.Marshal(map[string]any{"conversation_id": convOf(body), "message": text})
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(data)))
		r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, env.alice))
		w := httptest.NewRecorder()
		s.aiChat(w, r)

		return w.Body.String()
	}
	if out := ask("delete the old rows"); !strings.Contains(out, `"type":"question"`) || !strings.Contains(out, `"choices":["customers","orders"]`) || len(fake.requests) != 4 {
		t.Fatalf("question: %s (%d requests)", out, len(fake.requests))
	}
	if out := ask("orders"); !strings.Contains(out, "Orders it is.") {
		t.Fatalf("answer: %s", out)
	}

	msgs := fake.requests[0]["messages"].([]any)
	system := fmt.Sprint(msgs[0].(map[string]any)["content"])
	user := fmt.Sprint(msgs[1].(map[string]any)["content"])
	if !strings.Contains(system, "Europe/Istanbul") || !strings.Contains(system, "Now it is") || !strings.HasPrefix(user, "[") || !strings.HasSuffix(user, "] open customers") {
		t.Fatalf("time: %q / %q", system[:200], user)
	}
}

func convOf(body string) string {
	for _, line := range strings.Split(body, "\n") {
		var e struct {
			Conversation struct {
				ID string `json:"id"`
			} `json:"conversation"`
		}
		if json.Unmarshal([]byte(line), &e) == nil && e.Conversation.ID != "" {
			return e.Conversation.ID
		}
	}

	return ""
}

// Services that send no ids, the same id twice or nameless calls still give
// a well formed history.
func TestCleanToolCalls(t *testing.T) {
	call := func(id, name string) aiToolCall {
		c := aiToolCall{ID: id}
		c.Function.Name = name

		return c
	}

	got := cleanToolCalls([]aiToolCall{call("", "a"), call("x", "b"), call("x", "c"), call("y", "")})
	if len(got) != 3 || got[0].ID == "" || got[1].ID != "x" || got[2].ID == "x" || got[0].ID == got[2].ID {
		t.Fatalf("calls: %+v", got)
	}
	if cleanToolCalls([]aiToolCall{call("z", "")}) != nil {
		t.Fatal("a call without a name was kept")
	}
}
