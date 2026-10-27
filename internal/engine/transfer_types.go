package engine

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	typeArgs    = regexp.MustCompile(`\(([^)]*)\)`)
	numericArgs = regexp.MustCompile(`^\s*(\d+|max)\s*(,\s*\d+\s*)?$`)
)

// MapType translates a column type of one database into one the target
// understands. The same database keeps the type as it is.
func MapType(from, to string, typ string) string {
	if from == to {
		return typ
	}

	t := strings.ToLower(strings.TrimSpace(typ))

	// ClickHouse wraps types: Nullable(Int32), LowCardinality(String).
	for _, w := range []string{"nullable(", "lowcardinality("} {
		for strings.HasPrefix(t, w) && strings.HasSuffix(t, ")") {
			t = strings.TrimSpace(t[len(w) : len(t)-1])
		}
	}

	args := ""

	// Only lengths and precisions carry over; anything else in the
	// parentheses (an enum list, a time zone) does not fit the target.
	if m := typeArgs.FindStringSubmatch(t); m != nil && numericArgs.MatchString(m[1]) {
		args = strings.ReplaceAll(m[1], " ", "")
	}

	base := strings.TrimSpace(typeArgs.ReplaceAllString(t, ""))

	// Arrays (text[], _int4) have no common form; their text goes over.
	if strings.HasSuffix(base, "]") || strings.HasPrefix(base, "_") || strings.HasPrefix(t, "array(") {
		return targetType(to, "text", "")
	}

	family := typeFamily(base)

	return targetType(to, family, args)
}

func typeFamily(t string) string {
	switch {
	case strings.Contains(t, "bigint") || t == "int8" || t == "int64" || t == "bigserial" || t == "uint32" || t == "uint64" || t == "long" || t == "hugeint":
		return "bigint"
	case t == "smallint" || t == "int2" || t == "int16" || t == "uint8" || t == "uint16" || t == "smallserial" || t == "tinyint" || t == "year":
		return "smallint"
	case isIntType(t):
		return "int"
	case strings.HasPrefix(t, "numeric") || strings.HasPrefix(t, "decimal") || t == "number" || t == "money" || t == "smallmoney":
		return "decimal"
	case t == "real" || t == "float4" || t == "float32" || t == "binary_float":
		return "real"
	case strings.Contains(t, "double") || t == "float" || t == "float8" || t == "float64" || t == "binary_double":
		return "double"
	case t == "bool" || t == "boolean" || t == "bit":
		return "bool"
	case strings.Contains(t, "timestamp") && (strings.Contains(t, "zone") || strings.Contains(t, "tz")) || t == "datetimeoffset" || t == "timestamptz":
		return "timestamptz"
	case strings.Contains(t, "timestamp") || strings.Contains(t, "datetime") || t == "smalldatetime":
		return "timestamp"
	case t == "date" || t == "date32":
		return "date"
	case strings.HasPrefix(t, "time"):
		return "time"
	case t == "uuid" || t == "uniqueidentifier":
		return "uuid"
	case t == "json" || t == "jsonb":
		return "json"
	case strings.Contains(t, "blob") || strings.Contains(t, "binary") || t == "bytea" || t == "raw" || t == "image" || t == "varbinary":
		return "binary"
	case strings.HasPrefix(t, "varchar") || strings.HasPrefix(t, "character varying") || t == "nvarchar" || t == "varchar2" || t == "nvarchar2":
		return "varchar"
	case t == "char" || t == "character" || t == "nchar" || t == "bpchar":
		return "char"
	}

	return "text"
}

// isIntType matches integer types by their first word, so interval or point
// are not taken for one.
func isIntType(t string) bool {
	w, _, _ := strings.Cut(t, " ")
	w = strings.TrimPrefix(w, "u")

	switch w {
	case "int", "integer", "serial", "mediumint", "smallint", "tinyint", "hugeint", "pls_integer", "binary_integer":
		return true
	}

	digits, ok := strings.CutPrefix(w, "int")

	return ok && digits != "" && strings.Trim(digits, "0123456789") == ""
}

func targetType(db, family, args string) string {
	n := strings.TrimSpace(strings.Split(args, ",")[0])
	if n == "max" || n == "-1" {
		n = ""
	}

	withArgs := func(t string) string {
		if args == "" {
			return t
		}

		return t + "(" + args + ")"
	}

	switch db {
	case "postgres", "duckdb":
		switch family {
		case "bigint", "smallint", "real", "date", "uuid", "time":
			return family
		case "int":
			return "integer"
		case "decimal":
			return withArgs("numeric")
		case "double":
			return "double precision"
		case "bool":
			return "boolean"
		case "timestamptz":
			return "timestamptz"
		case "timestamp":
			return "timestamp"
		case "json":
			if db == "duckdb" {
				return "json"
			}

			return "jsonb"
		case "binary":
			if db == "duckdb" {
				return "blob"
			}

			return "bytea"
		case "varchar":
			if n != "" {
				return "varchar(" + n + ")"
			}

			return "text"
		case "char":
			if n != "" {
				return "char(" + n + ")"
			}

			return "text"
		}

		return "text"
	case "mysql", "mariadb":
		switch family {
		case "bigint", "smallint", "int", "date", "time", "double":
			return family
		case "decimal":
			if args == "" {
				return "decimal(38,10)"
			}

			return withArgs("decimal")
		case "real":
			return "float"
		case "bool":
			return "tinyint(1)"
		case "timestamptz", "timestamp":
			return "datetime(6)"
		case "uuid":
			return "char(36)"
		case "json":
			return "json"
		case "binary":
			return "longblob"
		case "varchar":
			if n != "" {
				return "varchar(" + n + ")"
			}

			return "longtext"
		case "char":
			if n != "" {
				return "char(" + n + ")"
			}

			return "longtext"
		}

		return "longtext"
	case "mssql":
		switch family {
		case "bigint", "smallint", "int", "date", "real":
			return family
		case "decimal":
			if args == "" {
				return "decimal(38,10)"
			}

			return withArgs("decimal")
		case "double":
			return "float"
		case "bool":
			return "bit"
		case "timestamptz":
			return "datetimeoffset"
		case "timestamp":
			return "datetime2"
		case "time":
			return "time"
		case "uuid":
			return "uniqueidentifier"
		case "binary":
			return "varbinary(max)"
		case "varchar":
			if n != "" {
				if v, _ := strconv.Atoi(n); v > 4000 {
					return "nvarchar(max)"
				}

				return "nvarchar(" + n + ")"
			}

			return "nvarchar(max)"
		case "char":
			if n != "" {
				return "nchar(" + n + ")"
			}

			return "nvarchar(max)"
		}

		return "nvarchar(max)"
	case "oracle":
		switch family {
		case "bigint":
			return "NUMBER(19)"
		case "int":
			return "NUMBER(10)"
		case "smallint":
			return "NUMBER(5)"
		case "decimal":
			return withArgs("NUMBER")
		case "real":
			return "BINARY_FLOAT"
		case "double":
			return "BINARY_DOUBLE"
		case "bool":
			return "NUMBER(1)"
		case "timestamptz":
			return "TIMESTAMP WITH TIME ZONE"
		case "timestamp":
			return "TIMESTAMP"
		case "date":
			return "DATE"
		case "time":
			return "VARCHAR2(20)"
		case "uuid":
			return "VARCHAR2(36)"
		case "binary":
			return "BLOB"
		case "varchar":
			if v, _ := strconv.Atoi(n); v > 0 && v <= 4000 {
				return "VARCHAR2(" + n + " CHAR)"
			}

			return "CLOB"
		case "char":
			if n != "" {
				return "CHAR(" + n + ")"
			}

			return "CLOB"
		}

		return "CLOB"
	case "sqlite":
		switch family {
		case "bigint", "smallint", "int", "bool":
			return "INTEGER"
		case "real", "double":
			return "REAL"
		case "decimal":
			return "NUMERIC"
		case "binary":
			return "BLOB"
		}

		return "TEXT"
	case "clickhouse":
		switch family {
		case "bigint":
			return "Int64"
		case "int":
			return "Int32"
		case "smallint":
			return "Int16"
		case "decimal":
			if args == "" {
				return "Decimal(38,10)"
			}

			return withArgs("Decimal")
		case "real":
			return "Float32"
		case "double":
			return "Float64"
		case "bool":
			return "Bool"
		case "timestamptz", "timestamp":
			return "DateTime64(6)"
		case "date":
			return "Date32"
		case "uuid":
			return "UUID"
		}

		return "String"
	case "trino":
		switch family {
		case "bigint", "smallint", "real", "date", "uuid", "json":
			return family
		case "int":
			return "integer"
		case "decimal":
			if args == "" {
				return "decimal(38,10)"
			}

			return withArgs("decimal")
		case "double":
			return "double"
		case "bool":
			return "boolean"
		case "timestamptz":
			return "timestamp(6) with time zone"
		case "timestamp":
			return "timestamp(6)"
		case "time":
			return "time(6)"
		case "binary":
			return "varbinary"
		}

		return "varchar"
	}

	// ODBC and others: plain standard names.
	switch family {
	case "bigint", "smallint", "date", "time", "timestamp":
		return strings.ToUpper(family)
	case "int":
		return "INTEGER"
	case "decimal":
		return withArgs("DECIMAL")
	case "real", "double":
		return "DOUBLE PRECISION"
	case "timestamptz":
		return "TIMESTAMP"
	case "varchar":
		if n != "" {
			return "VARCHAR(" + n + ")"
		}
	}

	return "VARCHAR(4000)"
}

// CreateTableSQL writes a CREATE TABLE of the target database for columns
// read from the source, with the primary key when it has one.
func CreateTableSQL(from string, to Driver, schema, table string, cols []ColumnInfo) string {
	var b strings.Builder

	b.WriteString("CREATE TABLE " + qualify(to, schema, table) + " (\n")

	var pk []string

	for i, c := range cols {
		if i > 0 {
			b.WriteString(",\n")
		}

		typ := MapType(from, to.Type(), c.Type)
		notNull := !c.Nullable || c.PrimaryKey

		// ClickHouse columns hold no NULL unless wrapped in Nullable.
		if to.Type() == "clickhouse" {
			if !notNull {
				typ = "Nullable(" + typ + ")"
			}

			notNull = false
		}

		b.WriteString("  " + to.QuoteIdent(c.Name) + " " + typ)

		if notNull {
			b.WriteString(" NOT NULL")
		}

		if c.PrimaryKey {
			pk = append(pk, to.QuoteIdent(c.Name))
		}
	}

	switch {
	case to.Type() == "clickhouse":
		b.WriteString("\n) ENGINE = MergeTree ORDER BY ")

		if len(pk) > 0 {
			b.WriteString("(" + strings.Join(pk, ", ") + ")")
		} else {
			b.WriteString("tuple()")
		}

		return b.String()
	case len(pk) > 0 && to.Type() != "trino":
		b.WriteString(",\n  PRIMARY KEY (" + strings.Join(pk, ", ") + ")")
	}

	b.WriteString("\n)")

	return b.String()
}
