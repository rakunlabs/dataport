package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/store"
)

// The user's MCP policy decides, per connection, what agents may do there:
// nothing, read, write data, or also change the schema. It is read on every
// call, so a change applies at once to every token and signed-in client of
// the user. The grant, the token's read-only flag and production
// confirmation stay upper limits.

// mcpMode is the most an agent of caller may do on a connection.
func (s *Server) mcpMode(ctx context.Context, caller *mcpCaller, g *store.Grant) (store.MCPMode, error) {
	p, err := s.store.GetMCPPolicy(ctx, caller.user.ID)
	if err != nil {
		return store.MCPOff, err
	}

	return capMCPMode(p, caller, g), nil
}

func capMCPMode(p *store.MCPPolicy, caller *mcpCaller, g *store.Grant) store.MCPMode {
	// Without a saved policy, agents may only read; the user allows more
	// per connection under Settings → MCP access.
	mode := store.MCPRead
	if p != nil {
		mode = p.Mode(g.Connection.ID)
	}

	if mode != store.MCPOff && (g.ReadOnly || caller.token.ReadOnly) {
		mode = store.MCPRead
	}

	return mode
}

// mcpAllows refuses a script the mode does not allow on g.
func mcpAllows(mode store.MCPMode, g *store.Grant, sqlText string) error {
	name := g.Connection.Name

	switch mode {
	case store.MCPOff:
		return fmt.Errorf("connection %q is not available to agents; the user can allow it under Settings → MCP access", name)
	case store.MCPRead:
		return fmt.Errorf("agents may only read on connection %q", name)
	case store.MCPWrite:
		d, err := engine.DriverFor(g.Connection)
		if err != nil {
			return err
		}

		if engine.Classify(d, sqlText) == engine.RiskDDL {
			return fmt.Errorf("agents may change data but not the schema on connection %q", name)
		}
	}

	return nil
}

// ////////////////////////////////////////////
// settings page

type mcpAccessConnection struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	DBType   string `json:"db_type"`
	Color    string `json:"color"`
	ReadOnly bool   `json:"read_only"`
	// Production connections ask the user to confirm every agent write.
	Production bool          `json:"production"`
	Mode       store.MCPMode `json:"mode"`
}

type mcpAccessView struct {
	// Saved is false until the user saves a policy; agents may then only
	// read, on every connection the user can reach.
	Saved       bool                  `json:"saved"`
	DefaultMode store.MCPMode         `json:"default_mode"`
	Connections []mcpAccessConnection `json:"connections"`
}

func (s *Server) mcpAccessView(ctx context.Context, user *store.User) (*mcpAccessView, error) {
	grants, err := s.store.ListGrants(ctx, user)
	if err != nil {
		return nil, err
	}

	p, err := s.store.GetMCPPolicy(ctx, user.ID)
	if err != nil {
		return nil, err
	}

	out := &mcpAccessView{Saved: p != nil, DefaultMode: store.MCPRead, Connections: make([]mcpAccessConnection, 0, len(grants))}
	if p != nil {
		out.DefaultMode = p.DefaultMode
	}

	for _, g := range grants {
		c := g.Connection
		if c.HideMCP || c.NotebookOnly {
			continue
		}

		mode := store.MCPRead
		if p != nil {
			mode = p.Mode(c.ID)
		}

		if g.ReadOnly && mode.AtLeast(store.MCPWrite) {
			mode = store.MCPRead
		}

		out.Connections = append(out.Connections, mcpAccessConnection{
			ID: c.ID, Name: c.Name, DBType: c.DBType, Color: c.Color, ReadOnly: g.ReadOnly, Production: c.Production, Mode: mode,
		})
	}

	return out, nil
}

func (s *Server) getMCPAccess(w http.ResponseWriter, r *http.Request) {
	out, err := s.mcpAccessView(r.Context(), currentUser(r))
	if err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, out)
}

func (s *Server) setMCPAccess(w http.ResponseWriter, r *http.Request) {
	var in store.MCPPolicy
	if !readJSON(w, r, &in) {
		return
	}

	user := currentUser(r)

	grants, err := s.store.ListGrants(r.Context(), user)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	shared := map[string]*store.Grant{}
	for _, g := range grants {
		shared[g.Connection.ID] = g
	}

	if in.DefaultMode == store.MCPWrite || in.DefaultMode == store.MCPDDL {
		writeError(w, http.StatusBadRequest, "connections shared later may start at most read-only")

		return
	}

	policy := &store.MCPPolicy{DefaultMode: in.DefaultMode, Connections: map[string]store.MCPMode{}}

	for id, mode := range in.Connections {
		g := shared[id]
		if g == nil {
			// A connection no longer shared is simply dropped.
			continue
		}

		if g.ReadOnly && mode.AtLeast(store.MCPWrite) {
			writeError(w, http.StatusBadRequest, "connection "+g.Connection.Name+" is read-only for you")

			return
		}

		policy.Connections[id] = mode
	}

	if err := s.store.SetMCPPolicy(r.Context(), user.ID, policy); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())

		return
	}

	s.getMCPAccess(w, r)
}
