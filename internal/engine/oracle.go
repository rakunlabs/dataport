package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	goora "github.com/sijms/go-ora/v2"
	"github.com/sijms/go-ora/v2/network"

	"github.com/rakunlabs/dataport/internal/store"
)

func init() {
	register(oracleDriver{})
}

// oracleDriver uses go-ora, a pure Go client: no Instant Client is needed.
// The connection's database field is the service name; the "sid" option
// connects by SID instead.
//
// Oracle has no read-only transaction the driver can start, so a read-only
// run is SET TRANSACTION READ ONLY, which Oracle enforces, and is always
// rolled back. DDL commits implicitly in Oracle, so a run that mixes DDL
// with other changes cannot be rolled back as a whole.
type oracleDriver struct{}

func (oracleDriver) Type() string     { return "oracle" }
func (oracleDriver) Dialect() Dialect { return DialectOracle }
func (oracleDriver) DefaultPort() int { return 1521 }

func (oracleDriver) Open(c *store.Connection, password string) (*sql.DB, error) {
	port := c.Port
	if port == 0 {
		port = 1521
	}

	opts := map[string]string{"CONNECTION TIMEOUT": "10", "PROGRAM": "dataport"}

	if sid := strings.TrimSpace(c.Options["sid"]); sid != "" {
		opts["SID"] = sid
	}

	switch optionOr(c.Options, "ssl", "false") {
	case "true":
		opts["SSL"] = "true"
	case "skip-verify":
		opts["SSL"] = "true"
		opts["SSL VERIFY"] = "false"
	}

	service := c.Database
	if opts["SID"] != "" {
		service = ""
	}

	return sql.OpenDB(goora.NewConnector(goora.BuildUrl(c.Host, port, service, c.Username, password, opts))), nil
}

func (oracleDriver) BackendID(context.Context, *sql.Conn) (string, error) { return "", nil }

// Kill has nothing to do: go-ora sends a break when the context ends.
func (oracleDriver) Kill(context.Context, *sql.DB, string) error { return nil }

func (oracleDriver) BeginTx(ctx context.Context, conn *sql.Conn, readOnly bool) (*sql.Tx, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}

	if readOnly {
		if _, err := tx.ExecContext(ctx, "SET TRANSACTION READ ONLY"); err != nil {
			_ = tx.Rollback()

			return nil, err
		}
	}

	return tx, nil
}

func (oracleDriver) Prepare(context.Context, Runner, time.Duration) error { return nil }

func (oracleDriver) Query(ctx context.Context, tx Runner, stmt string, maxRows int) (*ResultSet, error) {
	rows, err := tx.QueryContext(ctx, stmt)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanRows(rows, maxRows)
}

// CheckReadOnly rejects database links, which reach another server outside
// this transaction. Oracle itself refuses writes and FOR UPDATE in a
// read-only transaction.
func (oracleDriver) CheckReadOnly(stmt string) error {
	if strings.Contains(codeOnly(stmt, DialectOracle), "@") {
		return fmt.Errorf("%w: database links are not allowed", ErrReadOnly)
	}

	return nil
}

// IsBlock reports a PL/SQL block: in Oracle BEGIN never starts a
// transaction.
func (oracleDriver) IsBlock(stmt string) bool {
	kw := FirstKeyword(stmt, DialectOracle)

	return kw == "BEGIN" || kw == "DECLARE"
}

// Split keeps PL/SQL blocks and stored program definitions whole: they end
// with a line holding only "/", as in SQL*Plus, or at the end of the
// script. Other statements end at a semicolon, which Oracle does not accept
// in the statement text.
func (d oracleDriver) Split(script string) []string {
	var out []string

	for _, chunk := range splitSlash(script) {
		if oracleBlock(chunk) {
			out = append(out, strings.TrimSpace(chunk))

			continue
		}

		out = append(out, Split(chunk, DialectOracle)...)
	}

	return out
}

// splitSlash cuts a script at lines that hold only "/".
func splitSlash(script string) []string {
	var (
		out []string
		cur strings.Builder
	)

	for line := range strings.Lines(script) {
		if strings.TrimSpace(line) == "/" {
			if strings.TrimSpace(cur.String()) != "" {
				out = append(out, cur.String())
			}

			cur.Reset()

			continue
		}

		cur.WriteString(line)
	}

	if strings.TrimSpace(cur.String()) != "" {
		out = append(out, cur.String())
	}

	return out
}

// oracleBlock reports text that is PL/SQL: an anonymous block or a stored
// program, whose semicolons belong to the body.
func oracleBlock(s string) bool {
	words := strings.Fields(strings.ToUpper(stripLeading(s, DialectOracle)))
	if len(words) == 0 {
		return false
	}

	switch words[0] {
	case "BEGIN", "DECLARE":
		return true
	case "CREATE":
		for _, w := range words[1:min(len(words), 6)] {
			switch w {
			case "PROCEDURE", "FUNCTION", "PACKAGE", "TRIGGER", "TYPE":
				return true
			}
		}
	}

	return false
}

// Reset rolls back anything left open and resets package state.
func (oracleDriver) Reset(ctx context.Context, conn *sql.Conn) error {
	_, err := conn.ExecContext(ctx, "BEGIN DBMS_SESSION.RESET_PACKAGE; END;")

	return err
}

func (oracleDriver) Placeholder(n int) string {
	return ":" + strconv.Itoa(n)
}

func (oracleDriver) QuoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

var oracleSystemSchemas = []string{
	"SYS", "SYSTEM", "XDB", "OUTLN", "DBSNMP", "APPQOSSYS", "CTXSYS", "MDSYS", "ORDSYS", "WMSYS", "LBACSYS",
	"OLAPSYS", "AUDSYS", "GSMADMIN_INTERNAL", "DVSYS", "ORDDATA", "OJVMSYS", "DVF", "REMOTE_SCHEDULER_AGENT",
	"GSMCATUSER", "GSMUSER", "SYSBACKUP", "SYSDG", "SYSKM", "SYSRAC", "SYS$UMF", "DBSFWUSER", "GGSYS",
	"ANONYMOUS", "XS$NULL", "DIP", "ORACLE_OCM", "MDDATA", "GSMROOTUSER", "PDBADMIN", "ORDPLUGINS", "SI_INFORMTN_SCHEMA",
}

// Schemas lists owners of tables or views the account can see, its own
// schema first.
func (oracleDriver) Schemas(ctx context.Context, db Querier, _ *store.Connection) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT owner FROM (
  SELECT DISTINCT owner FROM all_tables UNION SELECT DISTINCT owner FROM all_views
) WHERE owner NOT IN (`+strings.ToUpper(quotedList(oracleSystemSchemas))+`) AND owner NOT LIKE 'APEX\_%' ESCAPE '\'
ORDER BY CASE WHEN owner = SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA') THEN 0 ELSE 1 END, owner`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanStrings(rows)
}

func (oracleDriver) Objects(ctx context.Context, db Querier, schema string) ([]Object, error) {
	rows, err := db.QueryContext(ctx, `SELECT name, kind FROM (
  SELECT table_name AS name, 'table' AS kind FROM all_tables WHERE owner = :1 AND nested = 'NO' AND secondary = 'N'
  AND table_name NOT IN (SELECT mview_name FROM all_mviews WHERE owner = :2)
  UNION ALL SELECT view_name, 'view' FROM all_views WHERE owner = :3
  UNION ALL SELECT mview_name, 'materialized_view' FROM all_mviews WHERE owner = :4
) ORDER BY name`, schema, schema, schema, schema)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanObjects(rows)
}

func (oracleDriver) Columns(ctx context.Context, db Querier, schema, table string) ([]ColumnInfo, error) {
	rows, err := db.QueryContext(ctx, `SELECT c.column_name,
c.data_type || CASE
  WHEN c.data_type IN ('VARCHAR2', 'NVARCHAR2', 'CHAR', 'NCHAR', 'RAW') THEN '(' || c.char_length || ')'
  WHEN c.data_type = 'NUMBER' AND c.data_precision IS NOT NULL THEN '(' || c.data_precision || ',' || NVL(c.data_scale, 0) || ')'
  ELSE '' END,
CASE c.nullable WHEN 'Y' THEN 1 ELSE 0 END,
c.data_default,
CASE WHEN EXISTS (SELECT 1 FROM all_constraints k JOIN all_cons_columns kc ON kc.owner = k.owner AND kc.constraint_name = k.constraint_name
  WHERE k.constraint_type = 'P' AND k.owner = c.owner AND k.table_name = c.table_name AND kc.column_name = c.column_name) THEN 1 ELSE 0 END
FROM all_tab_columns c WHERE c.owner = :1 AND c.table_name = :2 ORDER BY c.column_id`, schema, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanIntColumns(rows)
}

// ////////////////////////////////////////////
// structure

func (oracleDriver) Indexes(ctx context.Context, db Querier, schema, table string) ([]IndexInfo, error) {
	rows, err := db.QueryContext(ctx, `SELECT i.index_name,
CASE i.uniqueness WHEN 'UNIQUE' THEN 1 ELSE 0 END,
CASE WHEN EXISTS (SELECT 1 FROM all_constraints k WHERE k.owner = i.table_owner AND k.index_name = i.index_name AND k.constraint_type = 'P') THEN 1 ELSE 0 END,
LOWER(i.index_type),
(SELECT LISTAGG(ic.column_name, CHR(31)) WITHIN GROUP (ORDER BY ic.column_position) FROM all_ind_columns ic
  WHERE ic.index_owner = i.owner AND ic.index_name = i.index_name)
FROM all_indexes i WHERE i.table_owner = :1 AND i.table_name = :2 AND i.index_type <> 'LOB'
ORDER BY 3 DESC, i.index_name`, schema, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	list := []IndexInfo{}

	for rows.Next() {
		var (
			ix              IndexInfo
			unique, primary int64
			cols            sql.NullString
		)

		if err := rows.Scan(&ix.Name, &unique, &primary, &ix.Method, &cols); err != nil {
			return nil, err
		}

		ix.Unique, ix.Primary = unique != 0, primary != 0
		ix.Columns = splitList(cols.String)
		list = append(list, ix)
	}

	return list, rows.Err()
}

func (oracleDriver) ForeignKeys(ctx context.Context, db Querier, schema, table string) ([]ForeignKey, error) {
	rows, err := db.QueryContext(ctx, `SELECT c.constraint_name, r.owner, r.table_name,
(SELECT LISTAGG(cc.column_name, CHR(31)) WITHIN GROUP (ORDER BY cc.position) FROM all_cons_columns cc
  WHERE cc.owner = c.owner AND cc.constraint_name = c.constraint_name),
(SELECT LISTAGG(rc.column_name, CHR(31)) WITHIN GROUP (ORDER BY rc.position) FROM all_cons_columns rc
  WHERE rc.owner = r.owner AND rc.constraint_name = r.constraint_name),
'NO ACTION', c.delete_rule
FROM all_constraints c JOIN all_constraints r ON r.owner = c.r_owner AND r.constraint_name = c.r_constraint_name
WHERE c.constraint_type = 'R' AND c.owner = :1 AND c.table_name = :2 ORDER BY c.constraint_name`, schema, table)
	if err != nil {
		return nil, err
	}

	return scanForeignKeys(rows)
}

// DDL asks DBMS_METADATA, which needs the account to see the object.
func (oracleDriver) DDL(ctx context.Context, db Querier, schema, table string, _ *TableStructure) (string, error) {
	kind, err := queryString(ctx, db, `SELECT object_type FROM all_objects
WHERE owner = :1 AND object_name = :2 AND object_type IN ('TABLE', 'VIEW', 'MATERIALIZED VIEW') ORDER BY object_type`, schema, table)
	if err != nil {
		return "", err
	}

	if kind == "" {
		kind = "TABLE"
	}

	kind = strings.ReplaceAll(kind, " ", "_")

	ddl, err := queryString(ctx, db, `SELECT DBMS_METADATA.GET_DDL(:1, :2, :3) FROM dual`, kind, table, schema)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(ddl) + "\n", nil
}

func oracleAuthError(err error) (bool, bool) {
	var oe *network.OracleError
	if !errors.As(err, &oe) {
		return false, false
	}

	// ORA-01017 invalid username/password, ORA-28000 account locked,
	// ORA-01005 null password.
	return oe.ErrCode == 1017 || oe.ErrCode == 28000 || oe.ErrCode == 1005, true
}
