package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rakunlabs/dataport/internal/store"
)

// The AI assistant of the workspace talks to an OpenAI compatible chat
// completions API an admin configures. The server holds the API key and
// runs the conversation: the model reads schemas, documentation and data
// through the read-only MCP tools, as the signed-in user with their grants
// and in the query log, and proposes SQL for the editor, which the user
// applies. Conversations are kept until the user deletes them.

const (
	aiSetting = "ai_assistant"
	// aiMaxSteps bounds the tool calls of one answer.
	aiMaxSteps = 12
	// aiMaxToolChars shortens tool results sent back to the model.
	aiMaxToolChars = 24000
	aiTimeout      = 5 * time.Minute
	// aiEditorTool is answered by the browser: it puts SQL into the editor.
	aiEditorTool = "set_editor_sql"
)

type aiConfig struct {
	Enabled bool   `json:"enabled"`
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
	KeyEnc  string `json:"key_enc,omitempty"`
	// Prompt is added to the system prompt, for the house rules of an
	// installation.
	Prompt string `json:"prompt,omitempty"`
}

func (s *Server) loadAI(ctx context.Context) (aiConfig, error) {
	raw, err := s.store.GetSetting(ctx, aiSetting)
	if errors.Is(err, store.ErrNotFound) {
		return aiConfig{BaseURL: "https://api.openai.com/v1"}, nil
	}

	if err != nil {
		return aiConfig{}, err
	}

	var v aiConfig
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return aiConfig{}, fmt.Errorf("decode ai settings; %w", err)
	}

	return v, nil
}

// aiReady tells whether the assistant is on and set up.
func (s *Server) aiReady(ctx context.Context) bool {
	v, err := s.loadAI(ctx)

	return err == nil && v.ready()
}

func (v aiConfig) ready() bool {
	return v.Enabled && v.BaseURL != "" && v.Model != ""
}

// ////////////////////////////////////////////
// admin settings

type aiView struct {
	Enabled bool   `json:"enabled"`
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
	HasKey  bool   `json:"has_key"`
	Prompt  string `json:"prompt"`
}

func (s *Server) adminGetAI(w http.ResponseWriter, r *http.Request) {
	v, err := s.loadAI(r.Context())
	if err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, aiView{Enabled: v.Enabled, BaseURL: v.BaseURL, Model: v.Model, HasKey: v.KeyEnc != "", Prompt: v.Prompt})
}

func (s *Server) adminSetAI(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool   `json:"enabled"`
		BaseURL string `json:"base_url"`
		Model   string `json:"model"`
		// Key nil keeps the saved key, "" removes it.
		Key    *string `json:"key"`
		Prompt string  `json:"prompt"`
	}

	if !readJSON(w, r, &in) {
		return
	}

	current, err := s.loadAI(r.Context())
	if err != nil {
		writeStoreError(w, err)

		return
	}

	v := aiConfig{Enabled: in.Enabled, BaseURL: strings.TrimRight(strings.TrimSpace(in.BaseURL), "/"), Model: strings.TrimSpace(in.Model),
		KeyEnc: current.KeyEnc, Prompt: strings.TrimSpace(in.Prompt)}

	if msg := v.check(); msg != "" {
		writeError(w, http.StatusBadRequest, msg)

		return
	}

	if in.Key != nil {
		key := strings.TrimSpace(*in.Key)
		if len(key) > 4096 {
			writeError(w, http.StatusBadRequest, "the API key is too long")

			return
		}

		if v.KeyEnc, err = s.cipher.Encrypt(key); err != nil {
			writeError(w, http.StatusInternalServerError, "the API key cannot be saved")

			return
		}
	}

	raw, err := json.Marshal(v)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	if err := s.store.SetSetting(r.Context(), aiSetting, string(raw)); err != nil {
		writeStoreError(w, err)

		return
	}

	s.adminGetAI(w, r)
}

func (v aiConfig) check() string {
	if v.BaseURL != "" {
		u, err := url.Parse(v.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Fragment != "" {
			return "the API address must be an http(s) URL such as https://api.openai.com/v1"
		}
	}

	switch {
	case v.Enabled && (v.BaseURL == "" || v.Model == ""):
		return "the API address and the model are required while the assistant is on"
	case len(v.Model) > 200:
		return "the model name is too long"
	case len(v.Prompt) > 8000:
		return "the instructions may be at most 8000 characters"
	}

	return ""
}

// adminTestAI asks the model for a one word answer with the saved settings.
func (s *Server) adminTestAI(w http.ResponseWriter, r *http.Request) {
	v, err := s.loadAI(r.Context())
	if err != nil {
		writeStoreError(w, err)

		return
	}

	if v.BaseURL == "" || v.Model == "" {
		writeError(w, http.StatusBadRequest, "save the API address and the model first")

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	start := time.Now()

	msg, err := s.aiComplete(ctx, v, []aiMessage{{Role: "user", Content: aiText("Answer with the single word: ok")}}, nil, nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"reply": msg.text(), "ms": time.Since(start).Milliseconds()})
}

// ////////////////////////////////////////////
// chat completions

// aiMessage is a chat message as the API sends and takes it.
type aiMessage struct {
	Role       string       `json:"role"`
	Content    *string      `json:"content"`
	ToolCalls  []aiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
	Name       string       `json:"name,omitempty"`
}

type aiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func aiText(s string) *string { return &s }

func (m aiMessage) text() string {
	if m.Content == nil {
		return ""
	}

	return *m.Content
}

type aiTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Parameters  any    `json:"parameters"`
	} `json:"function"`
}

// aiComplete runs one streamed completion. Text arrives through onText as
// it is written; the whole message is returned.
func (s *Server) aiComplete(ctx context.Context, v aiConfig, messages []aiMessage, tools []aiTool, onText func(string)) (*aiMessage, error) {
	key, err := s.cipher.Decrypt(v.KeyEnc)
	if err != nil {
		return nil, errors.New("the saved API key cannot be read; enter it again in the settings")
	}

	body := map[string]any{"model": v.Model, "messages": messages, "stream": true}
	if len(tools) > 0 {
		body["tools"] = tools
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.BaseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach the AI service: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

		return nil, fmt.Errorf("the AI service answered %d: %s", resp.StatusCode, aiErrorText(data))
	}

	// A service that ignores stream answers with one JSON body.
	if !strings.Contains(resp.Header.Get("Content-Type"), "event-stream") {
		var out struct {
			Choices []struct {
				Message aiMessage `json:"message"`
			} `json:"choices"`
		}

		if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out); err != nil || len(out.Choices) == 0 {
			return nil, errors.New("the AI service sent no answer")
		}

		m := out.Choices[0].Message
		m.Role = "assistant"
		m.ToolCalls = cleanToolCalls(m.ToolCalls)

		if onText != nil && m.text() != "" {
			onText(m.text())
		}

		return &m, nil
	}

	var (
		text  strings.Builder
		calls []aiToolCall
	)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)

	for scanner.Scan() {
		line, ok := strings.CutPrefix(scanner.Text(), "data:")
		if !ok {
			continue
		}

		line = strings.TrimSpace(line)
		if line == "[DONE]" {
			break
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    *int   `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}

		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			continue
		}

		if chunk.Error != nil {
			return nil, errors.New("the AI service failed: " + chunk.Error.Message)
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		d := chunk.Choices[0].Delta
		if d.Content != "" {
			text.WriteString(d.Content)

			if onText != nil {
				onText(d.Content)
			}
		}

		for _, tc := range d.ToolCalls {
			// Services that leave out index start a call with its id and
			// continue it without one.
			i := len(calls) - 1
			switch {
			case tc.Index != nil && *tc.Index >= 0 && *tc.Index < 64:
				i = *tc.Index
			case tc.ID != "" || i < 0:
				i = len(calls)
			}

			for len(calls) <= i {
				calls = append(calls, aiToolCall{Type: "function"})
			}

			c := &calls[i]
			if tc.ID != "" && c.ID == "" {
				c.ID = tc.ID
			}

			c.Function.Name += tc.Function.Name
			c.Function.Arguments += tc.Function.Arguments
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading the AI answer: %w", err)
	}

	calls = cleanToolCalls(calls)

	m := &aiMessage{Role: "assistant", ToolCalls: calls}
	if text.Len() > 0 || len(calls) == 0 {
		m.Content = aiText(text.String())
	}

	return m, nil
}

// cleanToolCalls drops calls without a name and gives every call an id of
// its own: the answers name them, and some services send none or the same
// one twice.
func cleanToolCalls(calls []aiToolCall) []aiToolCall {
	seen := map[string]bool{}
	kept := []aiToolCall{}

	for i, c := range calls {
		if c.Function.Name == "" {
			continue
		}

		if c.ID == "" || seen[c.ID] {
			c.ID = fmt.Sprintf("call_%d_%d", time.Now().UnixNano(), i)
		}

		c.Type = "function"
		seen[c.ID] = true
		kept = append(kept, c)
	}

	if len(kept) == 0 {
		return nil
	}

	return kept
}

func aiErrorText(data []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	if json.Unmarshal(data, &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}

	return strings.TrimSpace(string(data))
}

// ////////////////////////////////////////////
// tools

// aiSession is an MCP session of the user's read-only tools, in memory.
type aiSession struct {
	client *mcp.ClientSession
	tools  []aiTool
	// allowed are the MCP tools offered to the model.
	allowed map[string]bool
}

func (s *Server) newAISession(ctx context.Context, user *store.User) (*aiSession, error) {
	// A read-only caller: the assistant reads and proposes, the user runs.
	caller := &mcpCaller{user: user, token: &store.AccessToken{UserID: user.ID, ReadOnly: true}}

	server := s.newMCPServer(caller)
	// Tool calls carry the caller, as the HTTP endpoint's do.
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if r, ok := req.(*mcp.CallToolRequest); ok {
				if r.Extra == nil {
					r.Extra = &mcp.RequestExtra{}
				}

				r.Extra.TokenInfo = &auth.TokenInfo{UserID: user.ID, Extra: map[string]any{"caller": caller}}
			}

			return next(ctx, method, req)
		}
	})

	st, ct := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		return nil, err
	}

	client, err := mcp.NewClient(&mcp.Implementation{Name: "dataport-assistant", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		return nil, err
	}

	list, err := client.ListTools(ctx, nil)
	if err != nil {
		_ = client.Close()

		return nil, err
	}

	out := &aiSession{client: client, allowed: map[string]bool{}}

	for _, t := range list.Tools {
		// The assistant only reads: tools that change anything are not
		// offered, also those a read-only token still gets.
		if t.Annotations == nil || !t.Annotations.ReadOnlyHint {
			continue
		}

		out.allowed[t.Name] = true

		var tool aiTool
		tool.Type = "function"
		tool.Function.Name, tool.Function.Description, tool.Function.Parameters = t.Name, t.Description, t.InputSchema
		out.tools = append(out.tools, tool)
	}

	var editor aiTool
	editor.Type = "function"
	editor.Function.Name = aiEditorTool
	editor.Function.Description = "Propose SQL for the user's editor. The user sees it and applies it with one click. " +
		"Use it whenever you write SQL the user should run or change; the tool does not run anything. " +
		"mode statement replaces the statement the user selected or has the cursor in (use it to fix or rewrite \"this query\"), " +
		"replace replaces the whole editor text, insert adds at the cursor."
	editor.Function.Parameters = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"sql":         map[string]any{"type": "string", "description": "The SQL, in the dialect of the tab's connection."},
			"mode":        map[string]any{"type": "string", "enum": []string{"statement", "replace", "insert"}, "description": "statement: replace the selected statement or the one under the cursor; replace: the whole text; insert: at the cursor"},
			"explanation": map[string]any{"type": "string", "description": "One short sentence on what it does."},
		},
		"required": []string{"sql"},
	}
	out.tools = append(out.tools, editor)
	out.tools = append(out.tools, aiLocalTools()...)

	return out, nil
}

func (a *aiSession) call(ctx context.Context, tc aiToolCall) string {
	if !a.allowed[tc.Function.Name] {
		return "error: no such tool " + tc.Function.Name
	}

	args := map[string]any{}
	if strings.TrimSpace(tc.Function.Arguments) != "" {
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return "error: the arguments are not valid JSON"
		}
	}

	res, err := a.client.CallTool(ctx, &mcp.CallToolParams{Name: tc.Function.Name, Arguments: args})
	if err != nil {
		return "error: " + err.Error()
	}

	var b strings.Builder
	if res.IsError {
		b.WriteString("error: ")
	}

	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			b.WriteString(t.Text)
			b.WriteString("\n")
		}
	}

	if b.Len() == 0 && res.StructuredContent != nil {
		data, _ := json.Marshal(res.StructuredContent)
		b.Write(data)
	}

	return aiClip(b.String(), aiMaxToolChars)
}

// ////////////////////////////////////////////
// conversations

// aiContext is what the workspace tells about the tab the user works in.
type aiContext struct {
	Connection string `json:"connection"`
	DBType     string `json:"db_type"`
	Database   string `json:"database"`
	ReadOnly   bool   `json:"read_only"`
	Production bool   `json:"production"`
	Schema     string `json:"schema"`
	Table      string `json:"table"`
	TabKind    string `json:"tab_kind"`
	TabTitle   string `json:"tab_title"`
	SQL        string `json:"sql"`
	// Selection is the selected text; Statement, without a selection, the
	// statement under the cursor, which a run would take.
	Selection    string    `json:"selection"`
	Statement    string    `json:"statement"`
	CursorLine   int       `json:"cursor_line"`
	Where        string    `json:"where"`
	OrderBy      string    `json:"order_by"`
	TxOpen       bool      `json:"tx_open"`
	TxStatements int       `json:"tx_statements"`
	Error        string    `json:"error"`
	Result       *aiResult `json:"result"`
	Locale       string    `json:"locale"`
	// CellPath and CellConnection name the notebook cell the user works in.
	CellPath       string `json:"cell_path"`
	CellConnection string `json:"cell_connection"`
	// TimeZone is the browser's IANA zone, such as Europe/Istanbul, and
	// UTCOffset its offset in minutes east of UTC.
	TimeZone  string `json:"time_zone"`
	UTCOffset int    `json:"utc_offset"`
}

// aiZone is the user's time zone, from the browser; UTC when unknown.
func (c aiContext) zone() *time.Location {
	if c.TimeZone != "" {
		if loc, err := time.LoadLocation(c.TimeZone); err == nil {
			return loc
		}
	}

	if c.UTCOffset != 0 {
		return time.FixedZone("", c.UTCOffset*60)
	}

	return time.UTC
}

// aiStamp writes a time as the model reads it, in the user's zone.
func aiStamp(ms int64, loc *time.Location) string {
	return time.UnixMilli(ms).In(loc).Format("2006-01-02 15:04 Mon -07:00")
}

// aiResult is the result the user looks at: its columns and first rows.
type aiResult struct {
	Statement    string   `json:"statement"`
	Kind         string   `json:"kind"`
	Columns      []string `json:"columns"`
	Rows         [][]any  `json:"rows"`
	RowCount     int      `json:"row_count"`
	RowsAffected int64    `json:"rows_affected"`
	Truncated    bool     `json:"truncated"`
	DurationMS   int64    `json:"duration_ms"`
	Error        string   `json:"error"`
	// Selected is what the user selected in the grid.
	Selected *aiSelection `json:"selected"`
}

type aiSelection struct {
	RowNumbers []int    `json:"row_numbers"`
	Columns    []string `json:"columns"`
	Rows       [][]any  `json:"rows"`
	TotalRows  int      `json:"total_rows"`
}

func aiCells(row []any, n int) string {
	cells := make([]string, len(row))
	for i, v := range row {
		if v == nil {
			cells[i] = "NULL"
		} else {
			cells[i] = aiClip(fmt.Sprint(v), n)
		}
	}

	return strings.Join(cells, " | ")
}

// text writes the result as the model reads it, a small table.
func (r *aiResult) text() string {
	var b strings.Builder

	fmt.Fprintf(&b, "statement:\n```sql\n%s\n```\n", aiClip(r.Statement, 4000))

	switch {
	case r.Error != "":
		fmt.Fprintf(&b, "failed: %s\n", aiClip(r.Error, 2000))
	case r.Kind == "exec":
		fmt.Fprintf(&b, "ran in %d ms, %d rows affected\n", r.DurationMS, r.RowsAffected)
	default:
		more := ""
		if r.Truncated {
			more = " (cut at the row limit)"
		}

		fmt.Fprintf(&b, "%d rows in %d ms%s; the first %d:\n", r.RowCount, r.DurationMS, more, len(r.Rows))
		b.WriteString(strings.Join(r.Columns, " | "))
		b.WriteString("\n")

		for _, row := range r.Rows {
			cells := make([]string, len(row))
			for i, v := range row {
				if v == nil {
					cells[i] = "NULL"
				} else {
					cells[i] = aiClip(fmt.Sprint(v), 200)
				}
			}

			b.WriteString(strings.Join(cells, " | "))
			b.WriteString("\n")
		}
	}

	if sel := r.Selected; sel != nil && len(sel.Rows) > 0 {
		fmt.Fprintf(&b, "the user selected %d row(s) of it, rows %v; \"these\" or \"this value\" mean them:\n", sel.TotalRows, sel.RowNumbers)
		b.WriteString(strings.Join(sel.Columns, " | "))
		b.WriteString("\n")

		for _, row := range sel.Rows {
			b.WriteString(aiCells(row, 300))
			b.WriteString("\n")
		}
	}

	return aiClip(b.String(), 16000)
}

func (s *Server) aiSystemPrompt(v aiConfig, c aiContext) string {
	var b strings.Builder

	loc := c.zone()
	now := time.Now().In(loc)
	fmt.Fprintf(&b, "Now it is %s (%s) for the user; their time zone is %s. Use it for \"today\", \"last week\" and the like, "+
		"and keep in mind the database's own clock and time zone may differ: check with a query when it matters. "+
		"Messages of the user start with the time they were written, in brackets.\n\n",
		now.Format("2006-01-02 15:04 Monday -07:00"), now.UTC().Format("2006-01-02 15:04 UTC"), nonEmpty(c.TimeZone, now.Format("-07:00")))

	b.WriteString("You are the assistant of dataport, a web SQL workspace. You help the user understand their databases, " +
		"write and fix SQL and read results. Be brief and concrete; use Markdown, and fenced ```sql blocks for SQL.\n\n")
	b.WriteString("You have read-only tools: list connections, schemas and tables, describe tables, read the saved schema " +
		"documentation and run read-only queries as the user, within their grants. Inspect the schema before writing SQL and " +
		"never invent names. You cannot change data yourself: write changes as SQL and give it to the user with " + aiEditorTool +
		", which puts it into their editor for them to review and run.\n" +
		"When the user says \"this query\", \"this\" or \"here\", they mean the selected text, or else the statement under the cursor, " +
		"both given below. To fix or improve it, propose the new statement with mode statement. When a query failed, read the error, " +
		"check the names against the schema, and say what was wrong in one sentence before the fix. For slow queries look at the plan " +
		"with the explain tool. Prefer the dialect's own functions and keep the user's formatting and aliases.\n" +
		"When a request is ambiguous and you cannot settle it with your tools, ask with " + aiAskTool + " instead of guessing, " +
		"above all before proposing a change to data. Do not ask what you can look up.\n\n")
	b.WriteString(mcpInstructions(s.mcpCfg(), true, false))

	if c.Locale != "" {
		fmt.Fprintf(&b, "\nAnswer in the language of the user's messages; their interface language is %q.\n", c.Locale)
	}

	b.WriteString("\nThe user's current tab:\n")
	fmt.Fprintf(&b, "- kind: %s\n", nonEmpty(c.TabKind, "none"))

	if c.TabTitle != "" {
		fmt.Fprintf(&b, "- title: %s\n", c.TabTitle)
	}

	if c.Connection != "" {
		fmt.Fprintf(&b, "- connection: %s (%s)", c.Connection, c.DBType)

		if c.Database != "" {
			fmt.Fprintf(&b, ", database %s", c.Database)
		}

		var flags []string
		if c.Production {
			flags = append(flags, "production: every write needs the user's confirmation, be careful with changes")
		}

		if c.ReadOnly {
			flags = append(flags, "read-only for the user: do not propose writes")
		}

		if len(flags) > 0 {
			fmt.Fprintf(&b, " — %s", strings.Join(flags, "; "))
		}

		b.WriteString("\n")
	}

	if c.TxOpen {
		fmt.Fprintf(&b, "- a manual transaction is open with %d statement(s); the user commits or rolls it back\n", c.TxStatements)
	}

	if c.Schema != "" {
		fmt.Fprintf(&b, "- schema: %s\n", c.Schema)
	}

	if c.Table != "" {
		fmt.Fprintf(&b, "- table: %s\n", c.Table)
	}

	if c.Where != "" || c.OrderBy != "" {
		fmt.Fprintf(&b, "- table filter: WHERE %s ORDER BY %s\n", nonEmpty(c.Where, "-"), nonEmpty(c.OrderBy, "-"))
	}

	if c.CellPath != "" {
		fmt.Fprintf(&b, "- the user works in notebook cell %q on connection %s; the editor text below is that cell\n", c.CellPath, nonEmpty(c.CellConnection, "not chosen yet"))
	}

	if c.SQL != "" {
		fmt.Fprintf(&b, "- editor text (cursor on line %d):\n```sql\n%s\n```\n", c.CursorLine, aiClip(c.SQL, 20000))
	}

	switch {
	case c.Selection != "":
		fmt.Fprintf(&b, "- the user selected this; \"this query\" means it:\n```sql\n%s\n```\n", aiClip(c.Selection, 8000))
	case c.Statement != "" && c.Statement != c.SQL:
		fmt.Fprintf(&b, "- the cursor is in this statement; \"this query\" means it:\n```sql\n%s\n```\n", aiClip(c.Statement, 8000))
	}

	if c.Result != nil {
		b.WriteString("- the result the user looks at:\n")
		b.WriteString(c.Result.text())
	}

	if c.Error != "" {
		fmt.Fprintf(&b, "- last error: %s\n", aiClip(c.Error, 2000))
	}

	if v.Prompt != "" {
		b.WriteString("\nInstructions of this installation:\n")
		b.WriteString(v.Prompt)
		b.WriteString("\n")
	}

	return b.String()
}

func nonEmpty(v, fallback string) string {
	if v == "" {
		return fallback
	}

	return v
}

// aiClip shortens long text for the model, saying so.
func aiClip(v string, n int) string {
	if len(v) <= n {
		return v
	}

	return clip(v, n) + "\n… (shortened)"
}

func (s *Server) aiInfo(w http.ResponseWriter, r *http.Request) {
	v, err := s.loadAI(r.Context())
	if err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"enabled": v.ready(), "model": v.Model})
}

func (s *Server) listAIConversations(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListAIConversations(r.Context(), currentUser(r).ID)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, list)
}

// aiShown is a stored message as the side panel shows it.
type aiShown struct {
	Seq       int          `json:"seq"`
	Role      string       `json:"role"`
	Content   string       `json:"content"`
	ToolCalls []aiToolCall `json:"tool_calls,omitempty"`
	ToolName  string       `json:"tool_name,omitempty"`
	ToolCall  string       `json:"tool_call_id,omitempty"`
	CreatedAt int64        `json:"created_at"`
}

func (s *Server) getAIConversation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	c, err := s.store.GetAIConversation(ctx, currentUser(r).ID, r.PathValue("conv"))
	if err != nil {
		writeStoreError(w, err)

		return
	}

	msgs, err := s.store.AIMessages(ctx, c.ID)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	out := make([]aiShown, 0, len(msgs))

	for _, m := range msgs {
		var am aiMessage
		if json.Unmarshal([]byte(m.Payload), &am) != nil {
			continue
		}

		out = append(out, aiShown{Seq: m.Seq, Role: am.Role, Content: am.text(), ToolCalls: am.ToolCalls, ToolName: am.Name, ToolCall: am.ToolCallID, CreatedAt: m.CreatedAt})
	}

	writeJSON(w, http.StatusOK, map[string]any{"conversation": c, "messages": out})
}

func (s *Server) renameAIConversation(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title string `json:"title"`
	}

	if !readJSON(w, r, &in) {
		return
	}

	if err := s.store.RenameAIConversation(r.Context(), currentUser(r).ID, r.PathValue("conv"), clip(strings.TrimSpace(in.Title), 200)); err != nil {
		writeStoreError(w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteAIConversation(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteAIConversation(r.Context(), currentUser(r).ID, r.PathValue("conv")); err != nil {
		writeStoreError(w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteAIConversations(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteAIConversations(r.Context(), currentUser(r).ID); err != nil {
		writeStoreError(w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// aiChat adds the user's message to a conversation (a new one without an
// id) and streams the answer as newline-delimited JSON events: the
// conversation, text as it is written, each tool call and its outcome, SQL
// proposed for the editor, and done or error.
func (s *Server) aiChat(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ConversationID string    `json:"conversation_id"`
		Message        string    `json:"message"`
		Context        aiContext `json:"context"`
		// EditorResults answer set_editor_sql calls of the last turn,
		// keyed by call id: applied, or the user's reason not to.
		EditorResults map[string]string `json:"editor_results"`
		// Retry answers the last question of the conversation again: what
		// came after it is dropped, and message may be left empty.
		Retry bool `json:"retry"`
	}

	if !readJSON(w, r, &in) {
		return
	}

	in.Message = strings.TrimSpace(in.Message)
	if in.Retry && in.ConversationID == "" {
		writeError(w, http.StatusBadRequest, "there is nothing to answer again")

		return
	}

	if (!in.Retry && in.Message == "") || len(in.Message) > 32000 {
		writeError(w, http.StatusBadRequest, "write a message of at most 32000 characters")

		return
	}

	user := currentUser(r)

	v, err := s.loadAI(r.Context())
	if err != nil {
		writeStoreError(w, err)

		return
	}

	if !v.ready() {
		writeError(w, http.StatusForbidden, "the AI assistant is not set up on this server; ask an administrator")

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), aiTimeout)
	defer cancel()

	var conv *store.AIConversation

	if in.ConversationID != "" {
		if conv, err = s.store.GetAIConversation(ctx, user.ID, in.ConversationID); err != nil {
			writeStoreError(w, err)

			return
		}
	} else {
		conv = &store.AIConversation{UserID: user.ID, Title: clip(strings.Join(strings.Fields(in.Message), " "), 80)}
		if err := s.store.CreateAIConversation(ctx, conv); err != nil {
			writeStoreError(w, err)

			return
		}
	}

	stored, err := s.store.AIMessages(ctx, conv.ID)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	// A retry drops the last question with all that answered it, and asks
	// it again, as it was unless the user changed it.
	if in.Retry {
		last := -1

		for i, m := range stored {
			var am aiMessage
			if json.Unmarshal([]byte(m.Payload), &am) == nil && am.Role == "user" {
				last = i
			}
		}

		var lastText string

		if last >= 0 {
			var am aiMessage
			_ = json.Unmarshal([]byte(stored[last].Payload), &am)
			lastText = am.text()
		}

		if in.Message == "" {
			in.Message = lastText
		}

		// Only the question the user sees as the last one is dropped: one
		// the server never saved (its send failed) is just asked now.
		if last >= 0 && strings.TrimSpace(lastText) == in.Message {
			if err := s.store.DeleteAIMessagesFrom(ctx, conv.ID, stored[last].Seq); err != nil {
				writeStoreError(w, err)

				return
			}

			stored = stored[:last]
		}

		if in.Message == "" {
			writeError(w, http.StatusBadRequest, "there is nothing to answer again")

			return
		}
	}

	history := make([]aiMessage, 0, len(stored)+2)
	// When each message of history was written, for the model.
	written := make([]int64, 0, len(stored)+2)

	for _, m := range stored {
		var am aiMessage
		if json.Unmarshal([]byte(m.Payload), &am) == nil {
			history = append(history, am)
			written = append(written, m.CreatedAt)
		}
	}

	save := func(m aiMessage) {
		data, err := json.Marshal(m)
		if err == nil {
			_, err = s.store.AppendAIMessage(context.WithoutCancel(ctx), conv.ID, string(data))
		}

		if err != nil {
			slog.Error("save ai message", "error", err)
		}

		history = append(history, m)
		written = append(written, time.Now().UnixMilli())
	}

	// dated is the history as the model gets it: each user message starts
	// with when it was written, so "a moment ago" and "yesterday" make
	// sense; the stored text stays as the user wrote it.
	loc := in.Context.zone()
	dated := func() []aiMessage {
		out := make([]aiMessage, len(history))
		for i, m := range history {
			if m.Role == "user" && i < len(written) {
				m.Content = aiText("[" + aiStamp(written[i], loc) + "] " + m.text())
			}

			out[i] = m
		}

		return out
	}

	// Every tool call of the history gets an answer before the next
	// message, so the history stays well formed: editor proposals get the
	// user's choice, calls a cut-off turn left open get a note.
	answered := map[string]bool{}
	for _, m := range history {
		if m.Role == "tool" {
			answered[m.ToolCallID] = true
		}
	}

	var open []aiToolCall

	for _, m := range history {
		for _, tc := range m.ToolCalls {
			if !answered[tc.ID] {
				open = append(open, tc)
			}
		}
	}

	for _, tc := range open {
		answer := "not run: the turn was interrupted"
		if tc.Function.Name == aiEditorTool {
			if answer = in.EditorResults[tc.ID]; answer == "" {
				answer = "shown to the user; not applied yet"
			}
		}

		save(aiMessage{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: aiText(answer)})
	}

	save(aiMessage{Role: "user", Content: aiText(in.Message)})

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	rc := http.NewResponseController(w)
	enc := json.NewEncoder(w)
	emit := func(v map[string]any) {
		_ = enc.Encode(v)
		_ = rc.Flush()
	}

	emit(map[string]any{"type": "conversation", "conversation": conv, "at": time.Now().UnixMilli()})

	session, err := s.newAISession(ctx, user)
	if err != nil {
		emit(map[string]any{"type": "error", "error": "cannot start the assistant's tools: " + err.Error()})

		return
	}
	defer func() { _ = session.client.Close() }()

	system := aiMessage{Role: "system", Content: aiText(s.aiSystemPrompt(v, in.Context))}

	for step := 0; ; step++ {
		tools := session.tools
		if step >= aiMaxSteps {
			// The last step must answer in words.
			tools = nil
		}

		msg, err := s.aiComplete(ctx, v, append([]aiMessage{system}, dated()...), tools, func(text string) {
			emit(map[string]any{"type": "text", "text": text})
		})
		if err != nil {
			emit(map[string]any{"type": "error", "error": err.Error()})

			return
		}

		// The last step answers in words; calls a service makes anyway are
		// dropped, so the turn ends.
		if step >= aiMaxSteps && len(msg.ToolCalls) > 0 {
			msg.ToolCalls = nil
			if msg.text() == "" {
				msg.Content = aiText("(stopped: too many steps for one answer)")
			}
		}

		save(*msg)

		if len(msg.ToolCalls) == 0 {
			break
		}

		editor := false

		for _, tc := range msg.ToolCalls {
			if tc.Function.Name == aiEditorTool {
				var args struct {
					SQL         string `json:"sql"`
					Mode        string `json:"mode"`
					Explanation string `json:"explanation"`
				}

				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
				emit(map[string]any{"type": "editor", "id": tc.ID, "sql": args.SQL, "mode": args.Mode, "explanation": args.Explanation})

				editor = true

				continue
			}

			// A question for the user: the turn ends with it, the answer is
			// their next message.
			if tc.Function.Name == aiAskTool {
				var args struct {
					Question string   `json:"question"`
					Choices  []string `json:"choices"`
				}

				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)

				if len(args.Choices) > 6 {
					args.Choices = args.Choices[:6]
				}

				emit(map[string]any{"type": "question", "id": tc.ID, "question": args.Question, "choices": args.Choices})
				save(aiMessage{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: aiText("asked; the user answers in the next message")})

				editor = true

				continue
			}

			// A button the user clicks to open something; nothing opens by itself.
			if tc.Function.Name == aiNavigateTool {
				var args map[string]any
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
				emit(map[string]any{"type": "navigate", "id": tc.ID, "target": args})
				save(aiMessage{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: aiText("shown to the user as a button")})

				continue
			}

			emit(map[string]any{"type": "tool", "id": tc.ID, "name": tc.Function.Name, "arguments": tc.Function.Arguments})

			out, local := s.aiLocal(ctx, user, tc.Function.Name, tc.Function.Arguments)
			if !local {
				out = session.call(ctx, tc)
			}
			save(aiMessage{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: aiText(out)})
			emit(map[string]any{"type": "tool_done", "id": tc.ID, "error": strings.HasPrefix(out, "error: ")})
		}

		// The user answers editor proposals and questions; the turn ends with them.
		if editor {
			break
		}
	}

	emit(map[string]any{"type": "done", "at": time.Now().UnixMilli()})
}
