package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rakunlabs/dataport/internal/config"
	"github.com/rakunlabs/dataport/internal/store"
)

// Tools of the assistant that are not MCP tools: they read the user's own
// things in dataport (help, limits, history, saved queries), or are
// answered by the browser (opening a tab). All of them only read.

const (
	aiHelpTool     = "dataport_help"
	aiLimitsTool   = "my_limits"
	aiHistoryTool  = "my_query_history"
	aiSavedTool    = "my_saved_queries"
	aiNavigateTool = "open_in_workspace"
	// aiAskTool asks the user a question with choices; the turn ends and
	// their answer comes as the next message.
	aiAskTool = "ask_user"
)

func aiFunc(name, description string, params map[string]any) aiTool {
	var t aiTool
	t.Type = "function"
	t.Function.Name, t.Function.Description = name, description

	if params == nil {
		params = map[string]any{"type": "object", "properties": map[string]any{}}
	}

	t.Function.Parameters = params

	return t
}

func aiLocalTools() []aiTool {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

	return []aiTool{
		aiFunc(aiHelpTool, "Read how dataport works: its version, the features open on this server and to the user, keyboard "+
			"shortcuts, and how to do things in the workspace (export, import, transactions, notebooks, documentation, own "+
			"database accounts, MCP). Call it when the user asks about dataport itself.",
			map[string]any{"type": "object", "properties": map[string]any{"topic": str("Optional word to narrow the help, such as export or shortcuts.")}}),
		aiFunc(aiLimitsTool, "Read the user's limits on a connection: rows per statement, time limit, read-only, production, "+
			"manual transactions. Use it to explain cut results or refused writes.",
			map[string]any{"type": "object", "properties": map[string]any{"connection": str("Connection name; empty for all.")}, "required": []string{}}),
		aiFunc(aiHistoryTool, "Search the statements the user ran before, newest first, with their outcome. Only the user's own history.",
			map[string]any{"type": "object", "properties": map[string]any{
				"search":     str("Text the SQL contains."),
				"connection": str("Connection name."),
				"days":       map[string]any{"type": "integer", "description": "Only the last this many days."},
				"limit":      map[string]any{"type": "integer", "description": "At most this many, up to 50; default 20."},
			}}),
		aiFunc(aiSavedTool, "List or search the user's saved queries, with their SQL.",
			map[string]any{"type": "object", "properties": map[string]any{"search": str("Text the name or SQL contains.")}}),
		aiFunc(aiAskTool, "Ask the user one short question when the request is ambiguous and guessing would waste their time "+
			"or touch the wrong data: which connection, schema or table, which time range, update or delete, and the like. Give "+
			"two to four short choices; the user may also answer in their own words. Ask only what you cannot find out with the "+
			"other tools, and at most once per request. After calling it, stop; the answer comes as the next message.",
			map[string]any{"type": "object", "properties": map[string]any{
				"question": str("The question, in the user's language."),
				"choices":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Two to four short answers."},
			}, "required": []string{"question"}}),
		aiFunc(aiNavigateTool, "Offer the user a button that opens something in the workspace: a table's data or structure, a "+
			"schema's documentation or diagram, or a new query tab. Nothing opens until the user clicks.",
			map[string]any{"type": "object", "properties": map[string]any{
				"target":     map[string]any{"type": "string", "enum": []string{"table", "structure", "docs", "diagram", "query"}},
				"connection": str("Connection name."),
				"schema":     str("Schema name."),
				"table":      str("Table name, for table, structure and docs."),
				"sql":        str("For query: the SQL of the new tab."),
				"label":      str("Short text of the button."),
			}, "required": []string{"target", "connection"}}),
	}
}

// aiLocal answers a local tool; ok is false for any other tool.
func (s *Server) aiLocal(ctx context.Context, user *store.User, name, arguments string) (string, bool) {
	args := map[string]any{}
	if strings.TrimSpace(arguments) != "" {
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			return "error: the arguments are not valid JSON", true
		}
	}

	str := func(k string) string { v, _ := args[k].(string); return strings.TrimSpace(v) }
	num := func(k string) int { v, _ := args[k].(float64); return int(v) }

	switch name {
	case aiHelpTool:
		return s.aiHelp(ctx, user, str("topic")), true
	case aiLimitsTool:
		return s.aiLimits(ctx, user, str("connection")), true
	case aiHistoryTool:
		return s.aiHistory(ctx, user, str("search"), str("connection"), num("days"), num("limit")), true
	case aiSavedTool:
		return s.aiSaved(ctx, user, str("search")), true
	}

	return "", false
}

func (s *Server) aiGrant(ctx context.Context, user *store.User, name string) (*store.Grant, []*store.Grant, error) {
	grants, err := s.store.ListGrants(ctx, user)
	if err != nil {
		return nil, nil, err
	}

	visible := []*store.Grant{}
	for _, g := range grants {
		if !g.Connection.HideWorkspace && !g.Connection.NotebookOnly {
			visible = append(visible, g)
		}
	}

	if name == "" {
		return nil, visible, nil
	}

	g := findGrant(visible, name)
	if g == nil {
		return nil, visible, fmt.Errorf("unknown connection %q", name)
	}

	return g, visible, nil
}

func (s *Server) aiLimits(ctx context.Context, user *store.User, name string) string {
	g, list, err := s.aiGrant(ctx, user, name)
	if err != nil {
		return "error: " + err.Error()
	}

	if g != nil {
		list = []*store.Grant{g}
	}

	q := s.queryCfg()

	var b strings.Builder

	for _, g := range list {
		c := g.Connection

		rows := c.MaxRows
		if rows <= 0 {
			rows = q.DefaultMaxRows
		}

		timeout := "none"
		if t := s.timeout(c); t > 0 {
			timeout = t.String()
		}

		fmt.Fprintf(&b, "- %s (%s): rows per statement %d (the user may ask for up to %s), time limit %s", c.Name, c.DBType, rows,
			limitText(q.MaxRowsLimit), timeout)

		switch {
		case g.ReadOnly:
			b.WriteString(", read-only")
		default:
			b.WriteString(", may write")
		}

		if c.Production {
			b.WriteString(", production: writes need confirmation with the exact SQL")
		}

		if c.UserCredentials {
			b.WriteString(", the user signs in with their own database account")
		}

		b.WriteString("\n")
	}

	if b.Len() == 0 {
		return "no connections are shared with the user"
	}

	return b.String()
}

func limitText(n int) string {
	if n <= 0 {
		return "any number"
	}

	return fmt.Sprint(n)
}

func (s *Server) aiHistory(ctx context.Context, user *store.User, search, name string, days, limit int) string {
	f := store.QueryLogFilter{UserID: user.ID, Search: search, Limit: 20}
	if limit > 0 && limit <= 50 {
		f.Limit = limit
	}

	if days > 0 {
		f.From = time.Now().AddDate(0, 0, -days).UnixMilli()
	}

	if name != "" {
		g, _, err := s.aiGrant(ctx, user, name)
		if err != nil {
			return "error: " + err.Error()
		}

		f.ConnectionID = g.Connection.ID
	}

	list, err := s.store.ListQueryLog(ctx, f)
	if err != nil {
		return "error: cannot read the history"
	}

	if len(list) == 0 {
		return "no statements match"
	}

	var b strings.Builder

	for _, l := range list {
		fmt.Fprintf(&b, "- %s on %s, %s, %d rows, %d ms", time.UnixMilli(l.CreatedAt).Format(time.RFC3339), l.ConnectionName, l.Status, l.RowCount, l.DurationMS)

		if l.Source == "mcp" {
			b.WriteString(", by an agent")
		}

		fmt.Fprintf(&b, "\n```sql\n%s\n```\n", aiClip(l.SQL, 2000))

		if l.Error != "" {
			fmt.Fprintf(&b, "error: %s\n", aiClip(l.Error, 500))
		}
	}

	return aiClip(b.String(), aiMaxToolChars)
}

func (s *Server) aiSaved(ctx context.Context, user *store.User, search string) string {
	list, err := s.store.ListSavedQueries(ctx, user.ID)
	if err != nil {
		return "error: cannot read the saved queries"
	}

	_, grants, _ := s.aiGrant(ctx, user, "")
	names := map[string]string{}
	for _, g := range grants {
		names[g.Connection.ID] = g.Connection.Name
	}

	q := strings.ToLower(search)

	var b strings.Builder

	n := 0

	for _, sq := range list {
		if q != "" && !strings.Contains(strings.ToLower(sq.Name), q) && !strings.Contains(strings.ToLower(sq.SQL), q) {
			continue
		}

		if n++; n > 30 {
			b.WriteString("… more; search to narrow\n")

			break
		}

		fmt.Fprintf(&b, "- %q on %s\n```sql\n%s\n```\n", sq.Name, nonEmpty(names[sq.ConnectionID], "a connection no longer shared"), aiClip(sq.SQL, 3000))
	}

	if n == 0 {
		return "no saved queries match"
	}

	return aiClip(b.String(), aiMaxToolChars)
}

func (s *Server) aiHelp(ctx context.Context, user *store.User, topic string) string {
	cfg := s.mcpCfg()

	features := []string{}
	add := func(on bool, name string) {
		state := "off"
		if on {
			state = "on"
		}

		features = append(features, name+": "+state)
	}

	add(s.notebooksAllowed(ctx, user), "notebooks")
	add(!s.docsOff(), "schema documentation")
	add(!s.docsOff() && s.docsBuildAllowed(ctx, user), "building documentation")
	add(cfg.Enabled, "MCP endpoint for AI agents")
	add(s.connectionsCfg().UserCreate, "own connections")

	sections := []struct{ key, text string }{
		{"about", fmt.Sprintf("dataport %s: a multi-user web SQL workspace. People work on the databases an admin shared with them, "+
			"without seeing hosts or shared passwords. Statements are logged per user; production databases ask for confirmation "+
			"with the exact SQL before a write. Features on this server for the user: %s.", config.Build.Version, strings.Join(features, ", "))},
		{"shortcuts", "Ctrl+Enter runs the selection or the statement under the cursor (the whole editor if it holds one statement). " +
			"Ctrl+\\ runs into a new result tab. Ctrl+Shift+Enter runs the statement under the cursor. Alt+X runs the whole script, " +
			"Alt+S selects the statement, Alt+Z wraps lines (Ctrl+Option on macOS). Ctrl+S saves the query. Ctrl+Shift+F formats. " +
			"Ctrl+Shift+E explains. In grids Enter or F2 edits a cell. Ctrl+Space suggests in WHERE and ORDER BY."},
		{"run", "Each run is one transaction unless the tab uses a manual transaction (TX button), which keeps it open until " +
			"Commit or Rollback. The sliders button above the editor sets rows per statement and the time limit for the tab. " +
			"The green triangle next to a statement runs it alone."},
		{"edit", "In a table tab or a result read from one table, double click a cell to edit, add or delete rows; the SQL " +
			"preview shows the statements before they are saved."},
		{"export", "Right click a table or schema in the tree: Export writes CSV, JSON, SQL and more; Import reads a file into a table; " +
			"Transfer copies tables to another connection. Results copy as TSV, CSV, JSON, Markdown or SQL from the grid menu."},
		{"generate", "Right click a table, Generate data, to fill it with made-up rows; foreign keys use existing rows."},
		{"notebooks", "Notebooks hold SQL cells across connections with parameters, can be shared with users or groups, and " +
			"published as endpoints. Open them from the Notebooks button in the top bar."},
		{"docs", "The Docs button shows schema documentation: tables, columns, keys, indexes, triggers, a diagram and design notes. " +
			"Right click a schema or table, View documentation."},
		{"accounts", "Some connections make each user sign in with their own database account: the key pill next to the " +
			"connection in the tree asks for it, and the account can be changed or forgotten there."},
		{"readonly", "The RW pill on a connection the user may write switches read-only on for them; the lock shows it is read-only."},
		{"mcp", "Settings → MCP access shows how to add dataport to Claude Code, OpenCode and other agents, with browser sign-in " +
			"or an access token, and which databases agents may use."},
		{"history", "The clock button in the top bar shows the user's history; the bookmark button their saved queries."},
	}

	q := strings.ToLower(topic)

	var b strings.Builder

	for _, sec := range sections {
		if q == "" || sec.key == "about" || strings.Contains(sec.key, q) || strings.Contains(strings.ToLower(sec.text), q) {
			fmt.Fprintf(&b, "%s: %s\n\n", sec.key, sec.text)
		}
	}

	return b.String()
}
