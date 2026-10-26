package engine

import (
	"context"
	"database/sql"

	"github.com/rakunlabs/dataport/internal/store"
)

// DiagramTable is one table of a schema diagram: its columns and the keys
// that reference other tables.
type DiagramTable struct {
	Name        string       `json:"name"`
	Kind        string       `json:"kind"`
	Columns     []ColumnInfo `json:"columns"`
	ForeignKeys []ForeignKey `json:"foreign_keys"`
}

type Diagram struct {
	Tables []DiagramTable `json:"tables"`
	// Truncated is set when the schema has more tables than the diagram
	// shows.
	Truncated bool `json:"truncated"`
}

// MaxDiagramTables keeps a diagram readable and its catalog queries short.
const MaxDiagramTables = 150

// Diagram reads the tables of a schema with their columns and foreign keys,
// on one pooled connection.
func (m *Manager) Diagram(ctx context.Context, c *store.Connection, password, schema string) (*Diagram, error) {
	out := &Diagram{Tables: []DiagramTable{}}

	err := m.meta(ctx, c, password, func(ctx context.Context, d Driver, conn *sql.Conn) error {
		objects, err := d.Objects(ctx, conn, schema)
		if err != nil {
			return err
		}

		tables := objects[:0:0]

		for _, o := range objects {
			if o.Kind == "table" || o.Kind == "view" || o.Kind == "materialized_view" {
				tables = append(tables, o)
			}
		}

		if len(tables) > MaxDiagramTables {
			tables = tables[:MaxDiagramTables]
			out.Truncated = true
		}

		s, _ := d.(Structurer)

		for _, o := range tables {
			cols, err := d.Columns(ctx, conn, schema, o.Name)
			if err != nil {
				return err
			}

			t := DiagramTable{Name: o.Name, Kind: o.Kind, Columns: cols, ForeignKeys: []ForeignKey{}}

			// Views have no keys; a restricted account may not see the
			// constraint catalog, which leaves the diagram without lines.
			if s != nil && o.Kind == "table" {
				if fks, err := s.ForeignKeys(ctx, conn, schema, o.Name); err == nil {
					t.ForeignKeys = fks
				}
			}

			out.Tables = append(out.Tables, t)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}
