package engine

import (
	"regexp"
	"slices"
	"strings"
)

var trailingDigits = regexp.MustCompile(`^(.*?)(\d+)$`)

func findAnomalies(tables []DocTable, keys bool, schema string) []Anomaly {
	list := []Anomaly{}
	pks := primaryKeys(tables)

	// Tables joined by a key, declared or implied; without a single link in
	// the schema every table would be an orphan, which tells nothing.
	linked := map[string]bool{}
	implied := map[string][]Anomaly{}

	for _, t := range tables {
		for _, fk := range t.ForeignKeys {
			if sameSchema(fk, schema) {
				linked[t.Name], linked[fk.RefTable] = true, true
			}
		}

		if keys && t.Kind == "table" {
			implied[t.Name] = impliedRelationships(t, pks)
			for _, a := range implied[t.Name] {
				linked[t.Name], linked[a.Ref] = true, true
			}
		}
	}

	for _, t := range tables {
		if t.Kind != "table" {
			continue
		}

		if keys {
			if countPK(t) == 0 {
				list = append(list, Anomaly{Kind: AnomalyNoPrimaryKey, Table: t.Name})
			}

			list = append(list, unindexedFKs(t)...)
			list = append(list, implied[t.Name]...)
			list = append(list, nullableUnique(t)...)
			list = append(list, duplicateIndexes(t)...)

			if len(linked) > 0 && !linked[t.Name] {
				list = append(list, Anomaly{Kind: AnomalyOrphan, Table: t.Name})
			}
		}

		if len(t.Columns) == 1 {
			list = append(list, Anomaly{Kind: AnomalySingleColumn, Table: t.Name, Columns: []string{t.Columns[0].Name}})
		}

		for _, c := range t.Columns {
			if isNullString(c.Default) {
				list = append(list, Anomaly{Kind: AnomalyNullDefault, Table: t.Name, Columns: []string{c.Name}})
			}
		}

		list = append(list, incrementingColumns(t)...)
	}

	return list
}

// unindexedFKs finds foreign keys whose columns do not lead any index, so
// a delete on the referenced table scans this one.
func unindexedFKs(t DocTable) []Anomaly {
	var list []Anomaly

	for _, fk := range t.ForeignKeys {
		covered := slices.ContainsFunc(t.Indexes, func(ix IndexInfo) bool {
			return len(ix.Columns) >= len(fk.Columns) && sameSet(ix.Columns[:len(fk.Columns)], fk.Columns)
		})

		if !covered {
			list = append(list, Anomaly{Kind: AnomalyUnindexedFK, Table: t.Name, Columns: fk.Columns, Ref: fk.RefTable})
		}
	}

	return list
}

type pkInfo struct {
	table, column, typ string
}

// primaryKeys indexes the tables with a single-column primary key by their
// lowercased name and the singular forms a column may use for them.
func primaryKeys(tables []DocTable) map[string]pkInfo {
	out := map[string]pkInfo{}

	for _, t := range tables {
		if t.Kind != "table" {
			continue
		}

		var pk []DocColumn

		for _, c := range t.Columns {
			if c.PrimaryKey {
				pk = append(pk, c)
			}
		}

		if len(pk) != 1 {
			continue
		}

		info := pkInfo{table: t.Name, column: pk[0].Name, typ: baseType(pk[0].Type)}

		for _, n := range singulars(strings.ToLower(t.Name)) {
			if _, taken := out[n]; !taken {
				out[n] = info
			}
		}
	}

	return out
}

func singulars(name string) []string {
	list := []string{name}

	switch {
	case strings.HasSuffix(name, "ies"):
		list = append(list, strings.TrimSuffix(name, "ies")+"y")
	case strings.HasSuffix(name, "ses"), strings.HasSuffix(name, "xes"), strings.HasSuffix(name, "ches"), strings.HasSuffix(name, "shes"):
		list = append(list, strings.TrimSuffix(name, "es"))
	case strings.HasSuffix(name, "s") && !strings.HasSuffix(name, "ss"):
		list = append(list, strings.TrimSuffix(name, "s"))
	}

	return list
}

// impliedRelationships finds columns named after another table's key, such
// as customer_id next to customers(id), that no foreign key declares.
func impliedRelationships(t DocTable, pks map[string]pkInfo) []Anomaly {
	declared := map[string]bool{}

	for _, fk := range t.ForeignKeys {
		for _, c := range fk.Columns {
			declared[c] = true
		}
	}

	var list []Anomaly

	for _, c := range t.Columns {
		// A table's own single key is not a reference to another table.
		if declared[c.Name] || (c.PrimaryKey && countPK(t) == 1) {
			continue
		}

		name := strings.ToLower(c.Name)

		var target string

		for _, suffix := range []string{"_id", "id"} {
			if strings.HasSuffix(name, suffix) && len(name) > len(suffix) {
				target = strings.TrimSuffix(strings.TrimSuffix(name, suffix), "_")

				break
			}
		}

		if target == "" {
			continue
		}

		pk, ok := pks[target]
		if !ok || pk.table == t.Name || !compatibleTypes(pk.typ, baseType(c.Type)) {
			continue
		}

		list = append(list, Anomaly{Kind: AnomalyImplied, Table: t.Name, Columns: []string{c.Name}, Ref: pk.table, RefCols: []string{pk.column}})
	}

	return list
}

func countPK(t DocTable) int {
	n := 0

	for _, c := range t.Columns {
		if c.PrimaryKey {
			n++
		}
	}

	return n
}

// nullableUnique finds columns a unique index covers alone while they
// still accept NULL, which most databases allow more than once.
func nullableUnique(t DocTable) []Anomaly {
	nullable := map[string]bool{}

	for _, c := range t.Columns {
		nullable[c.Name] = c.Nullable && !c.PrimaryKey
	}

	var list []Anomaly

	for _, ix := range t.Indexes {
		if ix.Unique && !ix.Primary && len(ix.Columns) == 1 && nullable[ix.Columns[0]] {
			list = append(list, Anomaly{Kind: AnomalyNullableUnique, Table: t.Name, Columns: ix.Columns, Ref: ix.Name})
		}
	}

	return list
}

// duplicateIndexes finds indexes on the same columns, in the same order,
// as another one of the table.
func duplicateIndexes(t DocTable) []Anomaly {
	seen := map[string]string{}

	var list []Anomaly

	for _, ix := range t.Indexes {
		if len(ix.Columns) == 0 {
			continue
		}

		key := strings.Join(ix.Columns, "\x1f")
		if first, ok := seen[key]; ok {
			list = append(list, Anomaly{Kind: AnomalyDuplicateIndex, Table: t.Name, Columns: ix.Columns, Ref: first + " / " + ix.Name})

			continue
		}

		seen[key] = ix.Name
	}

	return list
}

// incrementingColumns finds runs like phone1, phone2, phone3: a repeating
// group that usually belongs in a table of its own.
func incrementingColumns(t DocTable) []Anomaly {
	groups := map[string][]string{}

	var order []string

	for _, c := range t.Columns {
		m := trailingDigits.FindStringSubmatch(c.Name)
		if m == nil || m[1] == "" {
			continue
		}

		base := strings.TrimRight(strings.ToLower(m[1]), "_")
		if _, ok := groups[base]; !ok {
			order = append(order, base)
		}

		groups[base] = append(groups[base], c.Name)
	}

	var list []Anomaly

	for _, base := range order {
		if len(groups[base]) >= 2 {
			list = append(list, Anomaly{Kind: AnomalyIncrementing, Table: t.Name, Columns: groups[base]})
		}
	}

	return list
}

// isNullString tells a default of the text 'null', almost always meant as
// NULL, from NULL itself.
func isNullString(def string) bool {
	d := strings.ToLower(strings.TrimSpace(def))
	if i := strings.Index(d, "::"); i > 0 {
		d = d[:i]
	}

	return d == "'null'" || d == `"null"` || d == "n'null'"
}

func baseType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	if i := strings.IndexAny(t, "( "); i > 0 {
		t = t[:i]
	}

	return t
}

var typeFamilies = map[string]string{
	"int": "int", "int2": "int", "int4": "int", "int8": "int", "integer": "int", "bigint": "int", "smallint": "int",
	"tinyint": "int", "mediumint": "int", "serial": "int", "bigserial": "int", "number": "int", "numeric": "int", "decimal": "int",
	"int32": "int", "int64": "int", "uint32": "int", "uint64": "int", "hugeint": "int", "ubigint": "int", "uinteger": "int",
	"varchar": "text", "char": "text", "text": "text", "character": "text", "nvarchar": "text", "nchar": "text",
	"varchar2": "text", "nvarchar2": "text", "string": "text", "uuid": "uuid", "uniqueidentifier": "uuid",
}

func compatibleTypes(a, b string) bool {
	if a == b {
		return true
	}

	fa, fb := typeFamilies[a], typeFamilies[b]

	return fa != "" && fa == fb
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)

	return slices.Equal(x, y)
}
