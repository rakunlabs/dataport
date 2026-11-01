package server

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/rakunlabs/dataport/internal/engine"
)

// The schema overview pushed to a search index opens with a map: tables
// grouped by what joins them, and the joins as a Mermaid ER diagram, which
// models read well and a text index keeps whole.

// maxDiagramEdges keeps the diagram of a large schema readable.
const maxDiagramEdges = 200

var migrationTableRe = regexp.MustCompile(`(?i)(^|_)(migrations?|schema_version|flyway_schema_history|goose_db_version|databasechangelog(lock)?)(_|$)`)

type tableGroup struct {
	Name   string
	Tables []string
}

// schemaGroups puts tables joined by foreign keys together, then tables
// that share a name prefix with a group or with each other. Migration
// bookkeeping gets a group of its own, so it is not mistaken for data.
func schemaGroups(doc *engine.SchemaDoc) []tableGroup {
	parent := map[string]string{}

	var find func(string) string
	find = func(n string) string {
		if parent[n] != n {
			parent[n] = find(parent[n])
		}

		return parent[n]
	}

	union := func(a, b string) {
		if ra, rb := find(a), find(b); ra != rb {
			parent[rb] = ra
		}
	}

	var (
		migrations []string
		names      []string
	)

	degree := map[string]int{}

	for _, t := range doc.Tables {
		if migrationTableRe.MatchString(t.Name) {
			migrations = append(migrations, t.Name)

			continue
		}

		parent[t.Name] = t.Name
		names = append(names, t.Name)
	}

	for _, t := range doc.Tables {
		for _, fk := range t.ForeignKeys {
			if _, ok := parent[t.Name]; !ok {
				continue
			}

			if _, ok := parent[fk.RefTable]; !ok || fk.RefTable == t.Name {
				continue
			}

			union(t.Name, fk.RefTable)
			degree[t.Name]++
			degree[fk.RefTable]++
		}
	}

	// Tables no key joins go with the tables sharing their prefix.
	prefix := func(n string) string {
		if i := strings.IndexByte(n, '_'); i > 0 {
			return n[:i]
		}

		return n
	}

	byPrefix := map[string][]string{}

	for _, n := range names {
		byPrefix[prefix(n)] = append(byPrefix[prefix(n)], n)
	}

	for _, n := range names {
		if degree[n] > 0 {
			continue
		}

		for _, m := range byPrefix[prefix(n)] {
			if m != n && prefix(n) != n {
				union(m, n)

				break
			}
		}
	}

	members := map[string][]string{}

	var roots []string

	for _, n := range names {
		r := find(n)
		if _, ok := members[r]; !ok {
			roots = append(roots, r)
		}

		members[r] = append(members[r], n)
	}

	var (
		out   []tableGroup
		other []string
	)

	for _, r := range roots {
		list := members[r]
		if len(list) == 1 {
			other = append(other, list[0])

			continue
		}

		out = append(out, tableGroup{Name: groupName(list, degree, prefix), Tables: list})
	}

	// Larger groups first: they are the core of the schema.
	slices.SortStableFunc(out, func(a, b tableGroup) int { return len(b.Tables) - len(a.Tables) })

	if len(migrations) > 0 {
		out = append(out, tableGroup{Name: "migration bookkeeping", Tables: migrations})
	}

	if len(other) > 0 {
		out = append(out, tableGroup{Name: "standalone", Tables: other})
	}

	return out
}

// groupName is the shared prefix of a group when there is one, else its
// most joined table.
func groupName(list []string, degree map[string]int, prefix func(string) string) string {
	p := prefix(list[0])
	shared := p != list[0]

	for _, n := range list[1:] {
		if prefix(n) != p {
			shared = false

			break
		}
	}

	if shared {
		return p + "_*"
	}

	best := list[0]

	for _, n := range list[1:] {
		if degree[n] > degree[best] {
			best = n
		}
	}

	return best
}

func writeSchemaGroups(b *strings.Builder, groups []tableGroup) {
	if len(groups) == 0 {
		return
	}

	b.WriteString("\n## groups\n")

	for _, g := range groups {
		fmt.Fprintf(b, "%s: %s\n", g.Name, strings.Join(g.Tables, ", "))
	}
}

var mermaidUnsafe = regexp.MustCompile(`[^A-Za-z0-9_]`)

// writeSchemaDiagram writes the foreign keys of a schema as a Mermaid ER
// diagram: referenced table, one to many, referencing table, by column.
func writeSchemaDiagram(b *strings.Builder, doc *engine.SchemaDoc) {
	type edge struct{ from, to, label string }

	var (
		edges []edge
		seen  = map[edge]bool{}
	)

	for _, t := range doc.Tables {
		for _, fk := range t.ForeignKeys {
			if fk.RefSchema != "" && fk.RefSchema != doc.Schema {
				continue
			}

			e := edge{from: fk.RefTable, to: t.Name, label: strings.Join(fk.Columns, ",")}
			if !seen[e] {
				seen[e] = true
				edges = append(edges, e)
			}
		}
	}

	if len(edges) == 0 {
		return
	}

	b.WriteString("\n## relationships\n```mermaid\nerDiagram\n")

	for i, e := range edges {
		if i == maxDiagramEdges {
			fmt.Fprintf(b, "  %%%% %d more relationships left out\n", len(edges)-i)

			break
		}

		fmt.Fprintf(b, "  %s ||--o{ %s : %q\n", mermaidUnsafe.ReplaceAllString(e.from, "_"), mermaidUnsafe.ReplaceAllString(e.to, "_"), e.label)
	}

	b.WriteString("```\n")
}
