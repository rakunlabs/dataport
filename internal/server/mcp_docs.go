package server

import (
	"fmt"
	"regexp"
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
		writeLookupValues(b, t.Values, "  ")
	}
}

// writeLookupValues lists the rows of a lookup table, one per line.
func writeLookupValues(b *strings.Builder, v *engine.LookupValues, indent string) {
	if v == nil || len(v.Rows) == 0 {
		return
	}

	fmt.Fprintf(b, "%svalues (%s):\n", indent, strings.Join(v.Columns, " | "))

	for _, r := range v.Rows {
		b.WriteString(indent + "  " + strings.Join(r, " | ") + "\n")
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

	if p := t.Partition; p != nil {
		fmt.Fprintf(b, "partitioned by %s, %d partitions", p.Key, p.Count)

		if p.First != "" && p.First != p.Last {
			fmt.Fprintf(b, " (%s … %s)", p.First, p.Last)
		}

		b.WriteString("; query the parent table, partitions are not listed\n")
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
			// A partial unique index is an upsert target only with its
			// predicate repeated in ON CONFLICT.
			if ix.Where == "" {
				uniques = append(uniques, "("+strings.Join(ix.Columns, ", ")+")")
			}
		}

		fmt.Fprintf(b, "%s %s (%s)", kind, ix.Name, strings.Join(ix.Columns, ", "))

		// B-tree is the default nearly everywhere; others change which
		// operators the index serves, such as GIN for jsonb @>.
		if m := strings.ToLower(ix.Method); m != "" && m != "btree" {
			b.WriteString(" using " + m)
		}

		// A partial index serves only queries whose filter implies it.
		if ix.Where != "" {
			b.WriteString(" where " + ix.Where)
		}

		b.WriteString("\n")
	}

	if len(uniques) > 0 {
		b.WriteString("unique keys (upsert targets): " + strings.Join(uniques, ", ") + "\n")
	}

	shownJoins := map[string]bool{}

	for _, fk := range t.ForeignKeys {
		line := "references: " + join(t.Name, fk.Columns, fk.RefTable, fk.RefColumns)
		if fk.OnDelete != "" && fk.OnDelete != "NO ACTION" {
			line += "  (on delete " + fk.OnDelete + ")"
		}

		if !shownJoins[line] {
			shownJoins[line] = true
			b.WriteString(line + "\n")
		}
	}

	for _, r := range t.ReferencedBy {
		line := "referenced by: " + join(t.Name, r.RefColumns, r.Table, r.Columns)
		if !shownJoins[line] {
			shownJoins[line] = true
			b.WriteString(line + "\n")
		}
	}

	for _, a := range out.Anomalies {
		if a.Table == t.Name && a.Kind == engine.AnomalyImplied {
			fmt.Fprintf(b, "probably references (no foreign key): %s\n", join(t.Name, a.Columns, a.Ref, a.RefCols))
		}
	}

	for _, tr := range out.Triggers[t.Name] {
		fmt.Fprintf(b, "trigger %s %s %s", tr.Name, tr.Timing, tr.Event)

		if fn := triggerFunction(tr.Definition); fn != "" {
			b.WriteString(" -> " + fn)
		}

		if !tr.Enabled {
			b.WriteString(" (disabled)")
		}

		b.WriteString("\n")

		if tr.Function != "" {
			b.WriteString("```sql\n" + tr.Function + "\n```\n")
		}
	}

	if t.Definition != "" {
		b.WriteString("definition:\n" + t.Definition + "\n")
	}

	writeLookupValues(b, t.Values, "")

	var notes []string

	for _, a := range out.Anomalies {
		if a.Table != t.Name || a.Kind == engine.AnomalyImplied {
			continue
		}

		n := anomalyLabel(a.Kind)
		if len(a.Columns) > 0 {
			n += "(" + strings.Join(a.Columns, ", ") + ")"
		}

		notes = append(notes, n)
	}

	if len(notes) > 0 {
		b.WriteString("design notes: " + strings.Join(notes, "; ") + "\n")
	}
}

// anomalyLabel names an anomaly kind in text for models. "orphan" reads
// as unused, while it only means no key links the table to another.
func anomalyLabel(kind string) string {
	if kind == engine.AnomalyOrphan {
		return "no_fk_relations"
	}

	return kind
}

var triggerFunctionRe = regexp.MustCompile(`(?i)\bEXECUTE\s+(?:FUNCTION|PROCEDURE)\s+([^\s(]+)`)

// triggerFunction is the function a PostgreSQL trigger runs, from its
// definition; "" where the definition names none.
func triggerFunction(def string) string {
	if m := triggerFunctionRe.FindStringSubmatch(def); m != nil {
		return m[1] + "()"
	}

	return ""
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
