package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// MCPMode is what MCP agents may do on a connection, below the user's grant.
type MCPMode string

const (
	MCPOff   MCPMode = "off"
	MCPRead  MCPMode = "read"
	MCPWrite MCPMode = "write"
	// MCPDDL also allows changing the schema.
	MCPDDL MCPMode = "ddl"
)

func (m MCPMode) Valid() bool {
	switch m {
	case MCPOff, MCPRead, MCPWrite, MCPDDL:
		return true
	}

	return false
}

func (m MCPMode) rank() int {
	switch m {
	case MCPRead:
		return 1
	case MCPWrite:
		return 2
	case MCPDDL:
		return 3
	}

	return 0
}

// AtLeast reports whether m allows what o allows.
func (m MCPMode) AtLeast(o MCPMode) bool {
	return m.rank() >= o.rank()
}

// MCPPolicy is a user's choice of what MCP agents may reach.
type MCPPolicy struct {
	// DefaultMode applies to connections without their own mode, such as
	// ones shared later.
	DefaultMode MCPMode            `json:"default_mode"`
	Connections map[string]MCPMode `json:"connections"`
}

// Mode is the mode of a connection under the policy.
func (p *MCPPolicy) Mode(connectionID string) MCPMode {
	if m, ok := p.Connections[connectionID]; ok {
		return m
	}

	return p.DefaultMode
}

// GetMCPPolicy returns the user's policy, or nil when they never saved one.
func (s *Store) GetMCPPolicy(ctx context.Context, userID string) (*MCPPolicy, error) {
	p := &MCPPolicy{Connections: map[string]MCPMode{}}

	err := s.queryRow(ctx, "SELECT default_mode FROM "+s.t("mcp_policies")+" WHERE user_id = ?", userID).Scan(&p.DefaultMode)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	rows, err := s.query(ctx, "SELECT connection_id, mode FROM "+s.t("mcp_access")+" WHERE user_id = ?", userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			id   string
			mode MCPMode
		)

		if err := rows.Scan(&id, &mode); err != nil {
			return nil, err
		}

		p.Connections[id] = mode
	}

	return p, rows.Err()
}

// SetMCPPolicy replaces the user's policy.
func (s *Store) SetMCPPolicy(ctx context.Context, userID string, p *MCPPolicy) error {
	if !p.DefaultMode.Valid() {
		return fmt.Errorf("unknown mode %q", p.DefaultMode)
	}

	for _, m := range p.Connections {
		if !m.Valid() {
			return fmt.Errorf("unknown mode %q", m)
		}
	}

	return wrapWriteErr(s.inTx(ctx, func(tx *sql.Tx) error {
		ts := now()

		res, err := tx.ExecContext(ctx, s.q("UPDATE "+s.t("mcp_policies")+" SET default_mode = ?, updated_at = ? WHERE user_id = ?"), p.DefaultMode, ts, userID)
		if err != nil {
			return err
		}

		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			if _, err := tx.ExecContext(ctx, s.q("INSERT INTO "+s.t("mcp_policies")+" (user_id, default_mode, updated_at) VALUES (?, ?, ?)"), userID, p.DefaultMode, ts); err != nil {
				return err
			}
		}

		if _, err := tx.ExecContext(ctx, s.q("DELETE FROM "+s.t("mcp_access")+" WHERE user_id = ?"), userID); err != nil {
			return err
		}

		for id, m := range p.Connections {
			if _, err := tx.ExecContext(ctx, s.q("INSERT INTO "+s.t("mcp_access")+" (user_id, connection_id, mode) VALUES (?, ?, ?)"), userID, id, m); err != nil {
				return err
			}
		}

		return nil
	}))
}
