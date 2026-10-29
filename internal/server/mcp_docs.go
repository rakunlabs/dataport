package server

import (
	"fmt"
	"strings"
	"time"

	"github.com/rakunlabs/dataport/internal/engine"
)

// maxTableDocs bounds one table_docs call; related tables come on top.
const maxTableDocs = 20

// tableDocs picks the asked tables out of a schema document, with the
// tables one key away and the anomalies of the asked ones.
func tableDocs(doc *engine.SchemaDoc, names []string, related bool) tableDocsOut {
	out := tableDocsOut{Tables: []engine.DocTable{}}

	byName := map[string]engine.DocTable{}
	lower := map[string]string{}

	for _, t := range doc.Tables {
		byName[t.Name] = t
		lower[strings.ToLower(t.Name)] = t.Name
	}

	asked := map[string]bool{}

	for _, n := range names {
		name, ok := byName[n]
		if !ok {
			// Models often change the case of a name.
			real, found := lower[strings.ToLower(n)]
			if !found {
				out.Missing = append(out.Missing, n)

				continue
			}

			name = byName[real]
		}

		if !asked[name.Name] {
			asked[name.Name] = true
			out.Tables = append(out.Tables, name)
		}
	}

	if related {
		seen := map[string]bool{}

		add := func(n string) {
			if t, ok := byName[n]; ok && !asked[n] && !seen[n] {
				seen[n] = true
				out.Related = append(out.Related, t)
			}
		}

		for _, t := range out.Tables {
			for _, fk := range t.ForeignKeys {
				add(fk.RefTable)
			}

			for _, r := range t.ReferencedBy {
				add(r.Table)
			}
		}

		for _, a := range doc.Anomalies {
			if a.Kind == engine.AnomalyImplied && asked[a.Table] {
				add(a.Ref)
			}
		}
	}

	for _, a := range doc.Anomalies {
		if asked[a.Table] {
			out.Anomalies = append(out.Anomalies, a)
		}
	}

	return out
}

// tableDocsText writes table documentation for a model: compact, with the
// joins spelled out in the connection's quoting.
func tableDocsText(dbType string, out *tableDocsOut) string {
	var b strings.Builder

	fmt.Fprintf(&b, "connection: %s\nschema: %s\n", out.Connection, out.Schema)
	b.WriteString(docFreshness(out.GeneratedAt, out.Status))

	if len(out.Missing) > 0 {
		fmt.Fprintf(&b, "not found: %s (see %slist_tables)\n", strings.Join(out.Missing, ", "), mcpToolPrefix)
	}

	for _, t := range out.Tables {
		b.WriteString("\n# " + tableHeading(t.Name, t) + "\n")
		writeTableDoc(&b, dbType, t, out)
	}

	writeRelatedTables(&b, out.Related)

	return b.String()
}

// tableHeading is the name of a table with its kind and estimated rows.
func tableHeading(name string, t engine.DocTable) string {
	if t.Kind != "table" {
		name += " [" + t.Kind + "]"
	}

	if t.Rows != nil {
		name += fmt.Sprintf(" (~%d rows)", *t.Rows)
	}

	return name
}

func writeRelatedTables(b *strings.Builder, related []engine.DocTable) {
	if len(related) == 0 {
		return
	}

	b.WriteString("\n# related tables\n")

	for _, t := range related {
		cols := make([]string, len(t.Columns))

		for i, c := range t.Columns {
			cols[i] = c.Name + " " + c.Type
			if c.PrimaryKey {
				cols[i] += " PK"
			}
		}

		fmt.Fprintf(b, "%s(%s)\n", t.Name, strings.Join(cols, ", "))
	}
}

// writeTableDoc writes what follows a table's heading: comment, columns,
// keys, joins, triggers, definition and design notes.
func writeTableDoc(b *strings.Builder, dbType string, t engine.DocTable, out *tableDocsOut) {
	d, _ := engine.GetDriver(dbType)
	q := func(s string) string {
		if d == nil {
			return s
		}

		return d.QuoteIdent(s)
	}

	join := func(from string, fromCols []string, to string, toCols []string) string {
		conds := make([]string, 0, len(fromCols))
		for i, c := range fromCols {
			if i < len(toCols) {
				conds = append(conds, q(from)+"."+q(c)+" = "+q(to)+"."+q(toCols[i]))
			}
		}

		return "JOIN " + q(to) + " ON " + strings.Join(conds, " AND ")
	}

	if t.Comment != "" {
		b.WriteString(t.Comment + "\n")
	}

	refs := map[string]string{}

	for _, fk := range t.ForeignKeys {
		for i, c := range fk.Columns {
			if i < len(fk.RefColumns) {
				refs[c] = fk.RefTable + "." + fk.RefColumns[i]
			}
		}
	}

	b.WriteString("columns:\n")

	for _, c := range t.Columns {
		fmt.Fprintf(b, "  %s %s", c.Name, c.Type)

		if c.PrimaryKey {
			b.WriteString(" PK")
		}

		if !c.Nullable {
			b.WriteString(" NOT NULL")
		}

		if c.Extra != "" {
			b.WriteString(" " + c.Extra)
		}

		if c.Default != "" {
			b.WriteString(" DEFAULT " + c.Default)
		}

		if r := refs[c.Name]; r != "" {
			b.WriteString(" -> " + r)
		}

		if c.Comment != "" {
			b.WriteString("  -- " + strings.ReplaceAll(c.Comment, "\n", " "))
		}

		b.WriteString("\n")
	}

	var uniques []string

	for _, ix := range t.Indexes {
		kind := "index"

		switch {
		case ix.Primary:
			kind = "primary key"
		case ix.Unique:
			kind = "unique"
			uniques = append(uniques, "("+strings.Join(ix.Columns, ", ")+")")
		}

		fmt.Fprintf(b, "%s %s (%s)\n", kind, ix.Name, strings.Join(ix.Columns, ", "))
	}

	if len(uniques) > 0 {
		b.WriteString("unique keys (upsert targets): " + strings.Join(uniques, ", ") + "\n")
	}

	for _, fk := range t.ForeignKeys {
		fmt.Fprintf(b, "references: %s", join(t.Name, fk.Columns, fk.RefTable, fk.RefColumns))

		if fk.OnDelete != "" && fk.OnDelete != "NO ACTION" {
			b.WriteString("  (on delete " + fk.OnDelete + ")")
		}

		b.WriteString("\n")
	}

	for _, r := range t.ReferencedBy {
		fmt.Fprintf(b, "referenced by: %s\n", join(t.Name, r.RefColumns, r.Table, r.Columns))
	}

	for _, a := range out.Anomalies {
		if a.Table == t.Name && a.Kind == engine.AnomalyImplied {
			fmt.Fprintf(b, "probably references (no foreign key): %s\n", join(t.Name, a.Columns, a.Ref, a.RefCols))
		}
	}

	for _, tr := range out.Triggers[t.Name] {
		fmt.Fprintf(b, "trigger %s %s %s", tr.Name, tr.Timing, tr.Event)

		if !tr.Enabled {
			b.WriteString(" (disabled)")
		}

		b.WriteString("\n")
	}

	if t.Definition != "" {
		b.WriteString("definition:\n" + t.Definition + "\n")
	}

	var notes []string

	for _, a := range out.Anomalies {
		if a.Table != t.Name || a.Kind == engine.AnomalyImplied {
			continue
		}

		n := a.Kind
		if len(a.Columns) > 0 {
			n += "(" + strings.Join(a.Columns, ", ") + ")"
		}

		notes = append(notes, n)
	}

	if len(notes) > 0 {
		b.WriteString("design notes: " + strings.Join(notes, "; ") + "\n")
	}
}

func docFreshness(at int64, status docStatus) string {
	text := "snapshot generated: " + time.UnixMilli(at).UTC().Format(time.RFC3339) + " (not live; refresh in the documentation UI)\n"
	if status.LastError != "" {
		text += "last refresh failed; showing last successful snapshot\n"
	}
	if status.Refreshing {
		text += "refresh in progress\n"
	}
	return text
}
