package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rakunlabs/dataport/internal/store"
)

// Admin tools let an agent keep up what the push tells search agents about
// the databases: the collection description and the notes of schemas,
// such as the repositories that use them. They are listed only to admins
// whose token is not tied to one connection, as the settings are global,
// and every call checks that again.

var errMCPAdminOnly = errors.New("only an administrator can use this tool, with a token not tied to one connection")

func mcpAdmin(caller *mcpCaller) bool {
	return caller.user.Role == store.RoleAdmin && caller.scope == ""
}

type docExportSettingsOut struct {
	Enabled    bool   `json:"enabled"`
	URL        string `json:"url"`
	Collection string `json:"collection"`
	// Description is what the collection is described with; Default tells
	// whether it is the built-in one.
	Description        string                     `json:"description"`
	DefaultDescription bool                       `json:"default_description"`
	Connections        []docExportSettingsConnOut `json:"connections"`
	Status             docExportStatus            `json:"status"`
	Limits             map[string]int             `json:"limits"`
}

type docExportSettingsConnOut struct {
	Connection string `json:"connection"`
	DBType     string `json:"db_type"`
	AsUser     string `json:"as_user"`
	// Schemas pushed; empty means the default or every chosen one.
	Schemas []string `json:"schemas"`
	// Notes by schema; "" is the note of every schema.
	Notes map[string]string `json:"notes"`
}

type docExportDescriptionIn struct {
	Description string `json:"description" jsonschema:"the collection description; empty restores the default"`
}

type docExportNoteIn struct {
	Connection string `json:"connection" jsonschema:"name of a connection the push already includes"`
	Schema     string `json:"schema,omitempty" jsonschema:"the schema the note is for; empty for every schema of the connection"`
	Note       string `json:"note" jsonschema:"the note; empty removes it"`
}

type docLookupsIn struct {
	Connection string `json:"connection,omitempty" jsonschema:"connection name; empty lists every connection"`
}

type docLookupsOut struct {
	Connections []docLookupsConnOut `json:"connections"`
	Limits      map[string]int      `json:"limits"`
}

type docLookupsConnOut struct {
	Connection string   `json:"connection"`
	DBType     string   `json:"db_type"`
	Tables     []string `json:"tables"`
}

type docSetLookupsIn struct {
	Connection string   `json:"connection" jsonschema:"connection name"`
	Tables     []string `json:"tables" jsonschema:"the whole list, replacing the saved one: schema.table, or a table name for every schema; * matches anything; empty reads no table data"`
}

type docExportChangedOut struct {
	Saved   bool   `json:"saved"`
	Message string `json:"message"`
}

func (s *Server) registerMCPAdminTools(server *mcp.Server, readOnly bool) {
	closed := false

	mcp.AddTool(server, &mcp.Tool{
		Name: mcpToolPrefix + "docs_export_settings",
		Annotations: &mcp.ToolAnnotations{
			Title: "Search index push settings", ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closed,
		},
		Description: "Admin only. Show how schema documentation is pushed to the search index (such as krabby): the " +
			"collection, its description, every pushed connection with its schemas and the notes written into their " +
			"documents, and how the last push went. Search agents decide from the description whether to look in the " +
			"collection, and the notes tell them what a schema is for, such as the repositories or services that use it.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, docExportSettingsOut, error) {
		if _, err := mcpAdminCaller(req); err != nil {
			return nil, docExportSettingsOut{}, err
		}

		out, err := s.docExportSettings(ctx)

		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: mcpToolPrefix + "docs_lookup_tables",
		Annotations: &mcp.ToolAnnotations{
			Title: "Tables whose rows the documentation shows", ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closed,
		},
		Description: "Admin only. List, per connection, the tables whose rows the documentation shows (schema.table " +
			"patterns). The documentation reads no other table data.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in docLookupsIn) (*mcp.CallToolResult, docLookupsOut, error) {
		if _, err := mcpAdminCaller(req); err != nil {
			return nil, docLookupsOut{}, err
		}

		out, err := s.docLookupSettings(ctx, in.Connection)

		return nil, out, err
	})

	if readOnly {
		return
	}

	mcp.AddTool(server, &mcp.Tool{
		Name:        mcpToolPrefix + "docs_export_set_description",
		Annotations: &mcp.ToolAnnotations{Title: "Describe the search index collection", IdempotentHint: true, OpenWorldHint: &closed},
		Description: "Admin only. Set the description of the search index collection the documentation is pushed to. " +
			"Say which databases and schemas it holds and which services or repositories use them, so search agents " +
			"know when to look there. Empty restores the default. Sent to the search index on the next push.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in docExportDescriptionIn) (*mcp.CallToolResult, docExportChangedOut, error) {
		caller, err := mcpAdminCaller(req)
		if err != nil {
			return nil, docExportChangedOut{}, err
		}

		err = s.changeDocExport(ctx, caller, "description", func(in2 *docExportIn) error {
			in2.Description = in.Description

			return nil
		})
		if err != nil {
			return nil, docExportChangedOut{}, err
		}

		return nil, docExportChangedOut{Saved: true, Message: "saved; push to send it to the search index"}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        mcpToolPrefix + "docs_export_set_note",
		Annotations: &mcp.ToolAnnotations{Title: "Note a schema for search agents", IdempotentHint: true, OpenWorldHint: &closed},
		Description: "Admin only. Set the note of a schema of a pushed connection, or of all its schemas with an " +
			"empty schema. The note is written into the overview and every table document of the schema, so a " +
			"table found alone still tells what it belongs to. Write facts an agent cannot read from the catalog: " +
			"the repositories or services that own, write or read the schema, and what it holds. Keep it short. " +
			"An empty note removes it. The connection must already be pushed; add connections in the admin UI.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in docExportNoteIn) (*mcp.CallToolResult, docExportChangedOut, error) {
		caller, err := mcpAdminCaller(req)
		if err != nil {
			return nil, docExportChangedOut{}, err
		}

		err = s.changeDocExport(ctx, caller, "note", func(cfg *docExportIn) error {
			i, err := s.docExportTargetByName(ctx, cfg.Targets, in.Connection)
			if err != nil {
				return err
			}

			t := &cfg.Targets[i]
			schema := strings.TrimSpace(in.Schema)

			if t.Notes == nil {
				t.Notes = map[string]string{}
			}

			if strings.TrimSpace(in.Note) == "" {
				delete(t.Notes, schema)
			} else {
				t.Notes[schema] = in.Note
			}

			return nil
		})
		if err != nil {
			return nil, docExportChangedOut{}, err
		}

		return nil, docExportChangedOut{Saved: true, Message: "saved; push to send it to the search index"}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        mcpToolPrefix + "docs_set_lookup_tables",
		Annotations: &mcp.ToolAnnotations{Title: "Choose tables whose rows the documentation shows", IdempotentHint: true, OpenWorldHint: &closed},
		Description: "Admin only. Replace the list of tables of a connection whose rows the documentation shows, such as " +
			"code tables whose values other columns hold (status_type: NEW = New). No other table data is read. " +
			"Only tables of at most 50 rows and 6 columns are read. The rows reach everyone who reads the documentation " +
			"and the search index, which often has no access control: never list tables of accounts, secrets or personal " +
			"data. Show the user the list and get their approval before saving. Saving rebuilds the connection's saved " +
			"documentation in the background and pushes it.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in docSetLookupsIn) (*mcp.CallToolResult, docExportChangedOut, error) {
		caller, err := mcpAdminCaller(req)
		if err != nil {
			return nil, docExportChangedOut{}, err
		}

		c, err := s.adminConnectionByName(ctx, in.Connection)
		if err != nil {
			return nil, docExportChangedOut{}, err
		}

		tables, msg := checkDocPatterns(in.Tables)
		if msg != "" {
			return nil, docExportChangedOut{}, errors.New(strings.ReplaceAll(msg, "schema pattern", "table pattern"))
		}

		dc, err := s.store.GetDocConnection(ctx, c.ID)
		if errors.Is(err, store.ErrNotFound) {
			dc, err = &store.DocConnection{ConnectionID: c.ID, Include: []string{}, Exclude: []string{}}, nil
		}

		if err != nil {
			return nil, docExportChangedOut{}, err
		}

		if slices.Equal(dc.LookupTables, tables) {
			return nil, docExportChangedOut{Saved: true, Message: "unchanged"}, nil
		}

		dc.LookupTables = tables
		if err := s.store.SaveDocConnection(ctx, dc); err != nil {
			return nil, docExportChangedOut{}, err
		}

		slog.Info("documentation tables to read changed over MCP", "connection", c.Name, "user", caller.user.Username, "token", caller.token.ID)
		s.refreshConnectionDocs(c.ID)

		return nil, docExportChangedOut{Saved: true, Message: "saved; the connection's documentation is rebuilt and pushed in the background"}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        mcpToolPrefix + "docs_export_push",
		Annotations: &mcp.ToolAnnotations{Title: "Push documentation now", OpenWorldHint: &closed},
		Description: "Admin only. Push the documentation to the search index now, in the background, sending every " +
			"item again. Check its outcome with docs_export_settings.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, docExportChangedOut, error) {
		caller, err := mcpAdminCaller(req)
		if err != nil {
			return nil, docExportChangedOut{}, err
		}

		cfg, err := s.loadDocExport(ctx)
		if err != nil {
			return nil, docExportChangedOut{}, err
		}

		if !cfg.Enabled || cfg.URL == "" {
			return nil, docExportChangedOut{}, errors.New("the push is off or has no URL; turn it on in the admin UI")
		}

		if !s.exportDocsInBackground() {
			return nil, docExportChangedOut{}, errors.New("the documentation is already being pushed")
		}

		slog.Info("documentation push started over MCP", "user", caller.user.Username, "token", caller.token.ID)

		return nil, docExportChangedOut{Message: "pushing in the background; check docs_export_settings for the outcome"}, nil
	})
}

func mcpAdminCaller(req *mcp.CallToolRequest) (*mcpCaller, error) {
	caller, err := callerOf(req)
	if err != nil {
		return nil, err
	}

	if !mcpAdmin(caller) {
		return nil, errMCPAdminOnly
	}

	return caller, nil
}

func (s *Server) docExportSettings(ctx context.Context) (docExportSettingsOut, error) {
	cfg, err := s.loadDocExport(ctx)
	if err != nil {
		return docExportSettingsOut{}, err
	}

	out := docExportSettingsOut{
		Enabled: cfg.Enabled, URL: cfg.URL, Collection: cfg.Collection,
		Description: cfg.description(), DefaultDescription: cfg.Description == "",
		Connections: []docExportSettingsConnOut{}, Status: s.docExportStatusNow(ctx),
		Limits: map[string]int{"description": docExportMaxDescription, "note": docExportMaxNote},
	}

	for _, t := range cfg.Targets {
		c := docExportSettingsConnOut{Connection: t.ConnectionID, Schemas: t.Schemas, Notes: t.Notes}

		if conn, err := s.store.GetConnection(ctx, t.ConnectionID); err == nil {
			c.Connection, c.DBType = conn.Name, conn.DBType
		}

		if u, err := s.store.GetUser(ctx, t.UserID); err == nil {
			c.AsUser = u.Username
		}

		if c.Notes == nil {
			c.Notes = map[string]string{}
		}

		out.Connections = append(out.Connections, c)
	}

	return out, nil
}

// docExportTargetByName finds a pushed connection by its name, in any case.
func (s *Server) docExportTargetByName(ctx context.Context, targets []docExportTarget, name string) (int, error) {
	name = strings.TrimSpace(name)

	i := slices.IndexFunc(targets, func(t docExportTarget) bool {
		c, err := s.store.GetConnection(ctx, t.ConnectionID)

		return err == nil && strings.EqualFold(c.Name, name)
	})
	if i < 0 {
		return 0, errors.New("the push does not include a connection named " + name + "; see docs_export_settings, and add connections in the admin UI")
	}

	return i, nil
}

// changeDocExport applies a change to the saved push settings, checked as
// the admin UI checks them. The token stays as it is.
func (s *Server) changeDocExport(ctx context.Context, caller *mcpCaller, what string, change func(*docExportIn) error) error {
	s.docExportSave.Lock()
	defer s.docExportSave.Unlock()

	current, err := s.loadDocExport(ctx)
	if err != nil {
		return err
	}

	in := docExportIn{Enabled: current.Enabled, URL: current.URL, Collection: current.Collection, Description: current.Description, Targets: []docExportTarget{}}

	for _, t := range current.Targets {
		t.Schemas = slices.Clone(t.Schemas)

		notes := make(map[string]string, len(t.Notes))
		for k, v := range t.Notes {
			notes[k] = v
		}

		t.Notes = notes
		in.Targets = append(in.Targets, t)
	}

	if err := change(&in); err != nil {
		return err
	}

	next, msg := s.checkDocExport(ctx, in, current)
	if msg != "" {
		return errors.New(msg)
	}

	raw, err := json.Marshal(next)
	if err != nil {
		return err
	}

	if err := s.store.SetSetting(ctx, docExportSetting, string(raw)); err != nil {
		return err
	}

	slog.Info("documentation push settings changed over MCP", "change", what, "user", caller.user.Username, "token", caller.token.ID)

	return nil
}

// adminConnectionByName finds a connection an admin defined, by name in
// any case; personal connections belong to their owners.
func (s *Server) adminConnectionByName(ctx context.Context, name string) (*store.Connection, error) {
	list, err := s.store.ListConnections(ctx)
	if err != nil {
		return nil, err
	}

	for _, c := range list {
		if c.OwnerID == "" && strings.EqualFold(c.Name, strings.TrimSpace(name)) {
			return c, nil
		}
	}

	return nil, errors.New("no connection named " + name)
}

func (s *Server) docLookupSettings(ctx context.Context, name string) (docLookupsOut, error) {
	out := docLookupsOut{Connections: []docLookupsConnOut{}, Limits: map[string]int{"rows": 50, "columns": 6}}

	list, err := s.store.ListConnections(ctx)
	if err != nil {
		return out, err
	}

	found := false

	for _, c := range list {
		if c.OwnerID != "" || (name != "" && !strings.EqualFold(c.Name, strings.TrimSpace(name))) {
			continue
		}

		found = true
		tables := []string{}

		if dc, err := s.store.GetDocConnection(ctx, c.ID); err == nil {
			tables = dc.LookupTables
		}

		out.Connections = append(out.Connections, docLookupsConnOut{Connection: c.Name, DBType: c.DBType, Tables: tables})
	}

	if name != "" && !found {
		return out, errors.New("no connection named " + name)
	}

	return out, nil
}
