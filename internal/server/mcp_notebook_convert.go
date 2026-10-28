package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rakunlabs/dataport/internal/notebook"
	"github.com/rakunlabs/dataport/internal/store"
)

// mcpNotebook finds a notebook by id or path and checks the caller's
// permission on it and the scope of its token or endpoint.
func (s *Server) mcpNotebook(ctx context.Context, req *mcp.CallToolRequest, ref, permission string) (*mcpCaller, *store.Notebook, error) {
	caller, err := callerOf(req)
	if err != nil {
		return nil, nil, err
	}

	if !s.notebooksAllowed(ctx, caller.user) {
		return nil, nil, errNotebooksOff
	}

	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, nil, errors.New("notebook is required; call dataport_list_notebooks")
	}

	n, err := s.lookupNotebook(ctx, caller.user, ref, permission, caller.scope)
	if err != nil {
		var denied *notebookPermissionError
		if errors.As(err, &denied) {
			return nil, nil, fmt.Errorf("your permission on notebook %q is %s; this needs %s", denied.path, denied.have, denied.need)
		}

		if errors.Is(err, store.ErrNotFound) {
			return nil, nil, fmt.Errorf("unknown notebook %q; call dataport_list_notebooks", ref)
		}

		return nil, nil, err
	}

	return caller, n, nil
}

// notebookInScope reports whether every cell of a notebook, draft and
// published, stays on the one connection a scoped caller may use.
func notebookInScope(n *store.Notebook, scope string) bool {
	docs := []notebook.Document{n.Document}
	if n.Published != nil {
		docs = append(docs, *n.Published)
	}

	for _, d := range docs {
		for _, c := range d.Cells {
			if c.ConnectionID != scope || c.Transfer != nil && c.Transfer.ConnectionID != scope {
				return false
			}
		}
	}

	return true
}

func (s *Server) notebookSummary(n *store.Notebook) mcpNotebookSummary {
	out := mcpNotebookSummary{
		ID: n.ID, Name: n.Name, Path: n.Path, Permission: n.Permission,
		Version: n.Version, PublishedVersion: n.PublishedVersion, UpdatedAt: n.UpdatedAt,
	}

	if n.PublishedVersion > 0 {
		out.Endpoint = s.cfg.Server.BasePath + "/api/v1/run/" + n.Path
	}

	return out
}

func (s *Server) toMCPNotebook(ctx context.Context, caller *mcpCaller, n *store.Notebook) (mcpNotebook, error) {
	grants, err := s.store.ListGrants(ctx, caller.user)
	if err != nil {
		return mcpNotebook{}, err
	}

	names := map[string]string{}
	for _, g := range grants {
		names[g.Connection.ID] = g.Connection.Name
	}

	name := func(id string) string {
		if v, ok := names[id]; ok {
			return v
		}

		return id + " (not shared with you)"
	}

	out := mcpNotebook{mcpNotebookSummary: s.notebookSummary(n), Cells: []mcpCell{}, Parameters: []mcpParameter{}}

	for _, c := range n.Document.Cells {
		enabled, output := c.Enabled, c.Output
		mc := mcpCell{
			ID: c.ID, Path: c.Path, Description: c.Description, Connection: name(c.ConnectionID), SQL: c.SQL,
			Enabled: &enabled, Output: &output, Collapsed: c.Collapsed, Template: c.Template, Write: c.Write,
			MaxRows: c.MaxRows, TimeoutSec: c.TimeoutSec, Dependencies: c.Dependencies,
		}

		if t := c.Transfer; t != nil {
			mc.Transfer = &mcpTransfer{
				Connection: name(t.ConnectionID), Schema: t.Schema, Table: t.Table, Wipe: t.Wipe, Batch: t.Batch,
				SkipError: t.SkipError, KeepUnmapped: t.KeepUnmapped,
			}

			for _, m := range t.Mappings {
				mc.Transfer.Mappings = append(mc.Transfer.Mappings, mcpMapping(m))
			}
		}

		out.Cells = append(out.Cells, mc)
	}

	for _, p := range n.Document.Parameters {
		mp := mcpParameter{Name: p.Name, Type: p.Type, Required: p.Required}
		if len(p.Default) > 0 {
			_ = json.Unmarshal(p.Default, &mp.Default)
		}

		out.Parameters = append(out.Parameters, mp)
	}

	if n.Permission == "owner" {
		if out.SharedWith, err = s.notebookShares(ctx, n); err != nil {
			return mcpNotebook{}, err
		}
	}

	return out, nil
}

// fromMCPDocument builds a document from the cells an agent sent. Cells keep
// their id when the agent sends it or, failing that, when a cell of the same
// path existed, so the workspace keeps its state for them.
func (s *Server) fromMCPDocument(ctx context.Context, caller *mcpCaller, existing *store.Notebook, cells []mcpCell, params []mcpParameter) (notebook.Document, error) {
	grants, err := s.store.ListGrants(ctx, caller.user)
	if err != nil {
		return notebook.Document{}, err
	}

	resolve := func(name, what string) (string, error) {
		name = strings.TrimSpace(name)
		if name == "" {
			if caller.scope != "" {
				return caller.scope, nil
			}

			return "", fmt.Errorf("%s needs a connection; call dataport_list_connections", what)
		}

		// Agents may not put a notebook-only connection into a cell, which
		// would let them run any SQL on it.
		g := findGrant(mcpVisible(grants), name)
		if g == nil || g.Connection.NotebookOnly || g.Connection.HideNotebooks {
			return "", fmt.Errorf("%s: unknown connection %q; call dataport_list_connections", what, name)
		}

		if caller.scope != "" && g.Connection.ID != caller.scope {
			return "", fmt.Errorf("%s: this endpoint only serves one connection", what)
		}

		return g.Connection.ID, nil
	}

	ids := map[string]string{}
	if existing != nil {
		for _, c := range existing.Document.Cells {
			ids[c.Path] = c.ID
		}
	}

	doc := notebook.Document{Cells: []notebook.Cell{}, Parameters: []notebook.Parameter{}}

	for _, mc := range cells {
		c := notebook.Cell{
			ID: strings.TrimSpace(mc.ID), Path: strings.TrimSpace(mc.Path), Description: mc.Description, SQL: mc.SQL,
			Enabled: mc.Enabled == nil || *mc.Enabled, Output: mc.Output == nil || *mc.Output,
			Collapsed: mc.Collapsed, Template: mc.Template, Write: mc.Write, MaxRows: mc.MaxRows,
			TimeoutSec: mc.TimeoutSec, Dependencies: mc.Dependencies,
		}

		if c.ID == "" {
			c.ID = ids[c.Path]
		}

		if c.ID == "" {
			c.ID = store.NewID()
		}

		if c.ConnectionID, err = resolve(mc.Connection, "cell "+c.Path); err != nil {
			return notebook.Document{}, err
		}

		if t := mc.Transfer; t != nil {
			c.Write = true
			c.Transfer = &notebook.Transfer{
				Schema: t.Schema, Table: t.Table, Wipe: t.Wipe, Batch: t.Batch, SkipError: t.SkipError,
				KeepUnmapped: t.KeepUnmapped, Mappings: []notebook.Mapping{},
			}

			if c.Transfer.Batch == 0 {
				c.Transfer.Batch = 100
			}

			if c.Transfer.ConnectionID, err = resolve(t.Connection, "transfer of cell "+c.Path); err != nil {
				return notebook.Document{}, err
			}

			for _, m := range t.Mappings {
				c.Transfer.Mappings = append(c.Transfer.Mappings, notebook.Mapping(m))
			}
		}

		doc.Cells = append(doc.Cells, c)
	}

	for _, mp := range params {
		p := notebook.Parameter{Name: mp.Name, Type: mp.Type, Required: mp.Required}

		if mp.Default != nil {
			if p.Default, err = json.Marshal(mp.Default); err != nil {
				return notebook.Document{}, err
			}
		}

		doc.Parameters = append(doc.Parameters, p)
	}

	doc.Normalize()

	return doc, nil
}

func (s *Server) resolveShares(ctx context.Context, shares []mcpShare) ([]store.NotebookAccess, error) {
	if len(shares) == 0 {
		return []store.NotebookAccess{}, nil
	}

	users, err := s.store.ListUsers(ctx)
	if err != nil {
		return nil, err
	}

	groups, err := s.store.ListGroups(ctx)
	if err != nil {
		return nil, err
	}

	access := make([]store.NotebookAccess, 0, len(shares))

	for _, sh := range shares {
		a := store.NotebookAccess{Permission: sh.Permission}

		switch {
		case sh.User != "" && sh.Group == "":
			for _, u := range users {
				if strings.EqualFold(u.Username, sh.User) && !u.Disabled {
					a.SubjectType, a.SubjectID = store.SubjectUser, u.ID
				}
			}
		case sh.Group != "" && sh.User == "":
			for _, g := range groups {
				if strings.EqualFold(g.Name, sh.Group) {
					a.SubjectType, a.SubjectID = store.SubjectGroup, g.ID
				}
			}
		default:
			return nil, errors.New("each share names exactly one user or group")
		}

		if a.SubjectID == "" {
			return nil, fmt.Errorf("unknown user or group %q", sh.User+sh.Group)
		}

		access = append(access, a)
	}

	return access, nil
}

func (s *Server) notebookShares(ctx context.Context, n *store.Notebook) ([]mcpShare, error) {
	access, err := s.store.NotebookAccess(ctx, n.ID)
	if err != nil {
		return nil, err
	}

	out := []mcpShare{}

	for _, a := range access {
		sh := mcpShare{Permission: a.Permission}

		switch a.SubjectType {
		case store.SubjectUser:
			if u, err := s.store.GetUser(ctx, a.SubjectID); err == nil {
				sh.User = u.Username
			}
		case store.SubjectGroup:
			if g, err := s.store.GetGroup(ctx, a.SubjectID); err == nil {
				sh.Group = g.Name
			}
		}

		if sh.User != "" || sh.Group != "" {
			out = append(out, sh)
		}
	}

	return out, nil
}

// mcpNotebookError turns a refusal into a plain message for the model and
// hides connection details as the other tools do.
func mcpNotebookError(err error) error {
	var failure *runFailure
	if errors.As(err, &failure) {
		if failure.status == http.StatusTooManyRequests {
			return errors.New(failure.msg + "; wait a little or use background")
		}

		return errors.New(failure.msg)
	}

	return mcpEngineError(err)
}
