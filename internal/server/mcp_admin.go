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
