package engine

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/rakunlabs/dataport/internal/store"
)

// listSep joins name lists inside a single catalog column. Unit separator
// cannot appear in a sane identifier, unlike a comma.
const listSep = "\x1f"

type IndexInfo struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
	Primary bool     `json:"primary"`
	Method  string   `json:"method,omitempty"`
}

type ForeignKey struct {
	Name       string   `json:"name"`
	Columns    []string `json:"columns"`
	RefSchema  string   `json:"ref_schema"`
	RefTable   string   `json:"ref_table"`
	RefColumns []string `json:"ref_columns"`
	OnUpdate   string   `json:"on_update,omitempty"`
	OnDelete   string   `json:"on_delete,omitempty"`
}

type TableStructure struct {
	Columns     []ColumnInfo `json:"columns"`
	Indexes     []IndexInfo  `json:"indexes"`
	ForeignKeys []ForeignKey `json:"foreign_keys"`
	DDL         string       `json:"ddl"`
	// Comment is the table's comment, and Comments those of its columns,
	// by name, where the database keeps them.
	Comment  string            `json:"comment"`
	Comments map[string]string `json:"comments"`
	Triggers []TriggerInfo     `json:"triggers"`
}

// TriggerInfo is a trigger of a table; Definition is its full source where
// the database gives it.
type TriggerInfo struct {
	Name       string `json:"name"`
	Timing     string `json:"timing"`
	Event      string `json:"event"`
	Enabled    bool   `json:"enabled"`
	Definition string `json:"definition"`
}

// Commenter is implemented by drivers that read table and column comments.
type Commenter interface {
	Comments(ctx context.Context, q Querier, schema, table string) (string, map[string]string, error)
}

// Triggerer is implemented by drivers that list the triggers of a table.
type Triggerer interface {
	Triggers(ctx context.Context, q Querier, schema, table string) ([]TriggerInfo, error)
}

// Structurer reads indexes, foreign keys and the DDL of a table or view.
type Structurer interface {
	Indexes(ctx context.Context, q Querier, schema, table string) ([]IndexInfo, error)
	ForeignKeys(ctx context.Context, q Querier, schema, table string) ([]ForeignKey, error)
	DDL(ctx context.Context, q Querier, schema, table string, t *TableStructure) (string, error)
}

var ErrNoStructure = errors.New("table structure is not available for this database")

func (m *Manager) Structure(ctx context.Context, c *store.Connection, password, schema, table string) (*TableStructure, error) {
	out := &TableStructure{}

	err := m.meta(ctx, c, password, func(ctx context.Context, d Driver, conn *sql.Conn) (err error) {
		s, ok := d.(Structurer)
		if !ok {
			return ErrNoStructure
		}

		if out.Columns, err = d.Columns(ctx, conn, schema, table); err != nil {
			return err
		}

		if out.Indexes, err = s.Indexes(ctx, conn, schema, table); err != nil {
			return err
		}

		if out.ForeignKeys, err = s.ForeignKeys(ctx, conn, schema, table); err != nil {
			return err
		}

		if out.DDL, err = s.DDL(ctx, conn, schema, table, out); err != nil {
			return err
		}

		// Comments and triggers are extras: a database that refuses to show
		// them still shows the rest of the structure.
		out.Comments, out.Triggers = map[string]string{}, []TriggerInfo{}

		if cm, ok := d.(Commenter); ok {
			if tc, cc, err := cm.Comments(ctx, conn, schema, table); err == nil {
				out.Comment, out.Comments = tc, cc
			}
		}

		if tg, ok := d.(Triggerer); ok {
			if list, err := tg.Triggers(ctx, conn, schema, table); err == nil {
				out.Triggers = list
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// ////////////////////////////////////////////

func splitList(s string) []string {
	if s == "" {
		return []string{}
	}

	return strings.Split(s, listSep)
}

func quoteList(d Driver, names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = d.QuoteIdent(n)
	}

	return strings.Join(q, ", ")
}

// buildDDL writes a CREATE TABLE from catalog data, for databases without a
// statement that returns it. Constraints and indexes are complete clauses.
// special replaces the type and default of a column, such as an identity.
func buildDDL(d Driver, qualified string, cols []ColumnInfo, special map[string]string, constraints, indexes []string) string {
	var b strings.Builder

	b.WriteString("CREATE TABLE " + qualified + " (\n")

	lines := make([]string, 0, len(cols)+len(constraints))

	for _, c := range cols {
		line := "  " + d.QuoteIdent(c.Name) + " "

		if def, ok := special[c.Name]; ok {
			line += def
			// Computed columns ("AS expr") take no NOT NULL.
			if !c.Nullable && !strings.HasPrefix(def, "AS ") {
				line += " NOT NULL"
			}
		} else {
			line += c.Type
			if !c.Nullable {
				line += " NOT NULL"
			}

			if c.Default != "" {
				line += " DEFAULT " + c.Default
			}
		}

		lines = append(lines, line)
	}

	for _, c := range constraints {
		lines = append(lines, "  "+c)
	}

	b.WriteString(strings.Join(lines, ",\n"))
	b.WriteString("\n);\n")

	for _, ix := range indexes {
		b.WriteString("\n" + strings.TrimSuffix(ix, ";") + ";")
	}

	return strings.TrimRight(b.String(), "\n") + "\n"
}

func scanIndexes(rows *sql.Rows) ([]IndexInfo, error) {
	defer func() { _ = rows.Close() }()

	list := []IndexInfo{}

	for rows.Next() {
		var (
			ix   IndexInfo
			cols string
		)

		if err := rows.Scan(&ix.Name, &ix.Unique, &ix.Primary, &ix.Method, &cols); err != nil {
			return nil, err
		}

		ix.Columns = splitList(cols)
		list = append(list, ix)
	}

	return list, rows.Err()
}

func scanForeignKeys(rows *sql.Rows) ([]ForeignKey, error) {
	defer func() { _ = rows.Close() }()

	list := []ForeignKey{}

	for rows.Next() {
		var (
			fk         ForeignKey
			cols, refs string
		)

		if err := rows.Scan(&fk.Name, &fk.RefSchema, &fk.RefTable, &cols, &refs, &fk.OnUpdate, &fk.OnDelete); err != nil {
			return nil, err
		}

		fk.Columns = splitList(cols)
		fk.RefColumns = splitList(refs)
		fk.OnUpdate = strings.ReplaceAll(fk.OnUpdate, "_", " ")
		fk.OnDelete = strings.ReplaceAll(fk.OnDelete, "_", " ")
		list = append(list, fk)
	}

	return list, rows.Err()
}

// queryString returns the first column of the first row, or "" without rows.
func queryString(ctx context.Context, q Querier, query string, args ...any) (string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()

	var v sql.NullString
	if rows.Next() {
		if err := rows.Scan(&v); err != nil {
			return "", err
		}
	}

	return v.String, rows.Err()
}

func foreignKeyClause(d Driver, fk ForeignKey) string {
	ref := d.QuoteIdent(fk.RefTable)
	if fk.RefSchema != "" {
		ref = d.QuoteIdent(fk.RefSchema) + "." + ref
	}

	s := "CONSTRAINT " + d.QuoteIdent(fk.Name) + " FOREIGN KEY (" + quoteList(d, fk.Columns) + ") REFERENCES " + ref + " (" + quoteList(d, fk.RefColumns) + ")"

	if fk.OnDelete != "" && fk.OnDelete != "NO ACTION" {
		s += " ON DELETE " + fk.OnDelete
	}

	if fk.OnUpdate != "" && fk.OnUpdate != "NO ACTION" {
		s += " ON UPDATE " + fk.OnUpdate
	}

	return s
}

func scanPairs(rows *sql.Rows) (map[string]string, error) {
	defer func() { _ = rows.Close() }()

	out := map[string]string{}

	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}

		out[k] = v
	}

	return out, rows.Err()
}
