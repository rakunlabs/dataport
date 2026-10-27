package engine

import (
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

// scanRows reads at most maxRows rows and reports whether more were
// available.
func scanRows(rows *sql.Rows, maxRows int) (*ResultSet, error) {
	return scanRowsWith(rows, maxRows, nil)
}

// ValueConverter turns a driver-specific scanned value into a JSON value. It
// returns false to fall back to the generic conversion.
type ValueConverter func(dbType string, v any) (any, bool)

func scanRowsWith(rows *sql.Rows, maxRows int, convert ValueConverter) (*ResultSet, error) {
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}

	rs := &ResultSet{Kind: "rows", Columns: make([]Column, len(types)), Rows: [][]any{}}
	binary := make([]bool, len(types))

	for i, t := range types {
		name := columnTypeName(t)
		rs.Columns[i] = Column{Name: t.Name(), Type: name}
		binary[i] = binaryTypes[name]
	}

	values := make([]any, len(types))
	ptrs := make([]any, len(types))

	for i := range values {
		ptrs[i] = &values[i]
	}

	for rows.Next() {
		if len(rs.Rows) >= maxRows {
			rs.Truncated = true

			break
		}

		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}

		row := make([]any, len(values))
		for i, v := range values {
			row[i] = cellValue(rs.Columns[i].Type, binary[i], convert, v)
		}

		rs.Rows = append(rs.Rows, row)
	}

	if !rs.Truncated {
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	return rs, nil
}

// cellValue turns a scanned value into what the grid and the exports show.
func cellValue(typ string, binary bool, convert ValueConverter, v any) any {
	if convert != nil {
		if out, ok := convert(typ, v); ok {
			return out
		}
	}

	if b, ok := v.([]byte); ok && binary {
		return "\\x" + hex.EncodeToString(b)
	}

	return jsonValue(v)
}

// converterFor returns the value converter of a driver's results.
func converterFor(d Driver) ValueConverter {
	switch d.Type() {
	case "mssql":
		return mssqlValue
	case "odbc":
		return trimChar
	}

	return nil
}

// columnTypeName is the lower-case type of a result column. PostgreSQL
// reports arrays by their internal name, _text for text[]; they are written
// the way they are declared.
func columnTypeName(t *sql.ColumnType) string {
	name := strings.ToLower(t.DatabaseTypeName())
	if len(name) > 1 && name[0] == '_' {
		return name[1:] + "[]"
	}

	return name
}

var binaryTypes = map[string]bool{
	"bytea": true, "blob": true, "tinyblob": true, "mediumblob": true, "longblob": true,
	"binary": true, "varbinary": true, "geometry": true, "bit": true,
}

const maxSafeInt = 1<<53 - 1

// jsonValue converts a scanned value into something JSON and JavaScript can
// show without losing information.
func jsonValue(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case []byte:
		if utf8.Valid(x) {
			return string(x)
		}

		return "\\x" + hex.EncodeToString(x)
	case time.Time:
		return x.Format(time.RFC3339Nano)
	case int64:
		if x > maxSafeInt || x < -maxSafeInt {
			return fmt.Sprintf("%d", x)
		}

		return x
	case uint64:
		if x > maxSafeInt {
			return fmt.Sprintf("%d", x)
		}

		return x
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return fmt.Sprintf("%v", x)
		}

		return x
	case float32:
		f := float64(x)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return fmt.Sprintf("%v", x)
		}

		return f
	case string, bool, int, int8, int16, int32, uint, uint8, uint16, uint32:
		return x
	case fmt.Stringer:
		return x.String()
	default:
		if b, ok := v.([16]byte); ok {
			return base64.StdEncoding.EncodeToString(b[:])
		}

		// Arrays, maps and tuples (ClickHouse) are shown as JSON.
		if data, err := json.Marshal(x); err == nil {
			return string(data)
		}

		return fmt.Sprintf("%v", x)
	}
}
