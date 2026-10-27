package engine

import (
	"archive/zip"
	"bufio"
	"cmp"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

// ExportRequest asks for tables of one schema in a file format.
type ExportRequest struct {
	Schema string
	Tables []string
	// Format is "csv", "json" or "sql".
	Format string
	// Structure adds CREATE TABLE statements to SQL exports; Data the rows.
	Structure bool
	Data      bool
	Timeout   time.Duration
}

// ExportStats tells the log what an export wrote.
type ExportStats struct {
	Tables int
	Rows   int64
}

var ErrBadExport = errors.New("invalid export")

const maxExportTables = 1000

// ExportFileName is the name of the file an export writes.
func ExportFileName(req ExportRequest) string {
	base := req.Schema
	if len(req.Tables) == 1 {
		base = req.Tables[0]
	}

	base = safeFileName(base)
	if base == "" {
		base = "export"
	}

	switch {
	case req.Format == "sql":
		return base + ".sql"
	case len(req.Tables) > 1:
		return base + "-" + req.Format + ".zip"
	}

	return base + "." + req.Format
}

// safeFileName replaces path separators, quotes and control characters, so
// a table name can neither leave the download folder nor the zip.
func safeFileName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == '"' || r < ' ' {
			return '_'
		}

		return r
	}, name)

	return strings.TrimLeft(name, ".")
}

// Export writes the tables to w. Several tables in CSV or JSON become a zip
// with one file per table; SQL is one script.
func (m *Manager) Export(ctx context.Context, c *store.Connection, password string, req ExportRequest, w io.Writer) (*ExportStats, error) {
	switch {
	case len(req.Tables) == 0:
		return nil, fmt.Errorf("%w: no table chosen", ErrBadExport)
	case len(req.Tables) > maxExportTables:
		return nil, fmt.Errorf("%w: at most %d tables at once", ErrBadExport, maxExportTables)
	case req.Format != "csv" && req.Format != "json" && req.Format != "sql":
		return nil, fmt.Errorf("%w: unknown format %q", ErrBadExport, req.Format)
	case req.Format == "sql" && !req.Structure && !req.Data:
		return nil, fmt.Errorf("%w: choose the structure, the data or both", ErrBadExport)
	}

	stats := &ExportStats{}

	err := m.Session(ctx, c, password, req.Timeout, func(ctx context.Context, d Driver, conn *sql.Conn) error {
		bw := bufio.NewWriterSize(w, 64<<10)
		defer func() { _ = bw.Flush() }()

		if req.Format == "sql" {
			return exportSQL(ctx, d, conn, req, bw, stats)
		}

		if len(req.Tables) == 1 {
			return exportTable(ctx, d, conn, req.Schema, req.Tables[0], req.Format, bw, stats)
		}

		zw := zip.NewWriter(bw)

		for _, table := range req.Tables {
			f, err := zw.Create(cmp.Or(safeFileName(table), "table") + "." + req.Format)
			if err != nil {
				return err
			}

			if err := exportTable(ctx, d, conn, req.Schema, table, req.Format, f, stats); err != nil {
				return fmt.Errorf("%s: %w", table, err)
			}
		}

		return zw.Close()
	})

	return stats, err
}

func exportTable(ctx context.Context, d Driver, conn *sql.Conn, schema, table, format string, w io.Writer, stats *ExportStats) error {
	stats.Tables++

	var (
		cols    []Column
		binary  []bool
		convert = converterFor(d)
		cw      *csv.Writer
		record  []string
		first   = true
		keys    [][]byte
	)

	err := StreamTable(ctx, d, conn, schema, table, func(c []Column) error {
		cols = c
		binary = make([]bool, len(c))

		for i, col := range c {
			binary[i] = binaryTypes[col.Type]
		}

		switch format {
		case "csv":
			cw = csv.NewWriter(w)
			record = make([]string, len(c))

			for i, col := range c {
				record[i] = col.Name
			}

			return cw.Write(record)
		default:
			keys = make([][]byte, len(c))

			for i, col := range c {
				keys[i], _ = json.Marshal(col.Name)
			}

			_, err := io.WriteString(w, "[")

			return err
		}
	}, func(row []any) error {
		stats.Rows++

		if format == "csv" {
			for i, v := range row {
				record[i] = csvText(cellValue(cols[i].Type, binary[i], convert, v))
			}

			return cw.Write(record)
		}

		var b strings.Builder

		if first {
			b.WriteString("\n  {")
		} else {
			b.WriteString(",\n  {")
		}

		first = false

		for i, v := range row {
			if i > 0 {
				b.WriteString(", ")
			}

			b.Write(keys[i])
			b.WriteString(": ")

			out, err := json.Marshal(cellValue(cols[i].Type, binary[i], convert, v))
			if err != nil {
				out = []byte("null")
			}

			b.Write(out)
		}

		b.WriteString("}")

		_, err := io.WriteString(w, b.String())

		return err
	})
	if err != nil {
		return err
	}

	if format == "csv" {
		cw.Flush()

		return cw.Error()
	}

	_, err = io.WriteString(w, "\n]\n")

	return err
}

func csvText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return floatLiteral(x)
	}

	return fmt.Sprint(v)
}

// sqlRowsPerInsert keeps INSERT statements of an SQL export readable and
// within the statement size of every database.
const sqlRowsPerInsert = 100

func exportSQL(ctx context.Context, d Driver, conn *sql.Conn, req ExportRequest, w io.Writer, stats *ExportStats) error {
	if _, err := fmt.Fprintf(w, "-- dataport export of %s, %s\n", orDefault(req.Schema, d.Type()), time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}

	convert := converterFor(d)

	for _, table := range req.Tables {
		stats.Tables++

		if _, err := fmt.Fprintf(w, "\n-- %s\n", qualify(d, req.Schema, table)); err != nil {
			return err
		}

		if req.Structure {
			ddl, err := tableDDL(ctx, d, conn, req.Schema, table)
			if err != nil {
				return fmt.Errorf("%s: %w", table, err)
			}

			if _, err := io.WriteString(w, strings.TrimRight(ddl, ";\n")+";\n"); err != nil {
				return err
			}
		}

		if !req.Data {
			continue
		}

		var (
			cols  []Column
			head  string
			batch []string
			per   = sqlRowsPerInsert
		)

		if d.Type() == "oracle" {
			per = 1
		}

		flush := func() error {
			if len(batch) == 0 {
				return nil
			}

			_, err := io.WriteString(w, head+strings.Join(batch, ",\n  ")+";\n")
			batch = batch[:0]

			return err
		}

		err := StreamTable(ctx, d, conn, req.Schema, table, func(c []Column) error {
			cols = c
			names := make([]string, len(c))

			for i, col := range c {
				names[i] = d.QuoteIdent(col.Name)
			}

			head = "INSERT INTO " + qualify(d, req.Schema, table) + " (" + strings.Join(names, ", ") + ") VALUES\n  "

			return nil
		}, func(row []any) error {
			stats.Rows++

			vals := make([]string, len(row))
			for i, v := range row {
				if convert != nil {
					if out, ok := convert(cols[i].Type, v); ok {
						v = out
					}
				}

				vals[i] = Literal(d, cols[i].Type, v)
			}

			batch = append(batch, "("+strings.Join(vals, ", ")+")")
			if len(batch) >= per {
				return flush()
			}

			return nil
		})
		if err != nil {
			return fmt.Errorf("%s: %w", table, err)
		}

		if err := flush(); err != nil {
			return err
		}
	}

	return nil
}

// tableDDL returns the CREATE statement of a table, from the driver when it
// can write one, or built from the columns.
func tableDDL(ctx context.Context, d Driver, conn *sql.Conn, schema, table string) (string, error) {
	cols, err := d.Columns(ctx, conn, schema, table)
	if err != nil {
		return "", err
	}

	if s, ok := d.(Structurer); ok {
		t := &TableStructure{Columns: cols}

		if t.Indexes, err = s.Indexes(ctx, conn, schema, table); err != nil {
			return "", err
		}

		if t.ForeignKeys, err = s.ForeignKeys(ctx, conn, schema, table); err != nil {
			return "", err
		}

		ddl, err := s.DDL(ctx, conn, schema, table, t)

		return withoutOwner(ddl), err
	}

	return CreateTableSQL(d.Type(), d, schema, table, cols), nil
}

// withoutOwner drops the OWNER TO statements of a DDL, as pg_dump
// --no-owner does: the role may not exist where the file is loaded.
func withoutOwner(ddl string) string {
	parts := strings.Split(ddl, "\n\n")
	kept := parts[:0]

	for _, p := range parts {
		line := strings.TrimSpace(p)
		if strings.HasPrefix(line, "ALTER ") && !strings.Contains(line, "\n") && strings.Contains(line, " OWNER TO ") {
			continue
		}

		kept = append(kept, p)
	}

	return strings.TrimRight(strings.Join(kept, "\n\n"), "\n") + "\n"
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}

	return s
}
