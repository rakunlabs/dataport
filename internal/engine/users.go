package engine

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/rakunlabs/dataport/internal/store"
)

// Users and their privileges, read from the catalog, as DBX's user admin
// shows them. Changes are SQL the user reviews and runs like any other.

type DBUser struct {
	Name string `json:"name"`
	// Host is the MySQL account host, empty elsewhere.
	Host string `json:"host,omitempty"`
	// Attributes summarise the account, such as "SUPERUSER, LOGIN".
	Attributes string `json:"attributes"`
	// Login is false for roles that cannot sign in.
	Login bool `json:"login"`
	// Locked is set for disabled or locked accounts.
	Locked bool `json:"locked"`
}

// UserLister is implemented by drivers that list accounts and roles.
type UserLister interface {
	Users(ctx context.Context, q Querier) ([]DBUser, error)
	// Grants describes what an account may do, one line per grant.
	Grants(ctx context.Context, q Querier, name, host string) ([]string, error)
}

var ErrNoUsers = errors.New("users and privileges are not available for this database")

func (m *Manager) Users(ctx context.Context, c *store.Connection, password string) ([]DBUser, error) {
	list := []DBUser{}

	err := m.meta(ctx, c, password, func(ctx context.Context, d Driver, conn *sql.Conn) (err error) {
		u, ok := d.(UserLister)
		if !ok {
			return ErrNoUsers
		}

		list, err = u.Users(ctx, conn)

		return err
	})

	return list, err
}

func (m *Manager) Grants(ctx context.Context, c *store.Connection, password, name, host string) ([]string, error) {
	list := []string{}

	err := m.meta(ctx, c, password, func(ctx context.Context, d Driver, conn *sql.Conn) (err error) {
		u, ok := d.(UserLister)
		if !ok {
			return ErrNoUsers
		}

		list, err = u.Grants(ctx, conn, name, host)

		return err
	})

	return list, err
}

func scanUsers(rows *sql.Rows, err error) ([]DBUser, error) {
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	list := []DBUser{}

	for rows.Next() {
		var (
			u     DBUser
			host  sql.NullString
			attrs sql.NullString
		)

		if err := rows.Scan(&u.Name, &host, &attrs, &u.Login, &u.Locked); err != nil {
			return nil, err
		}

		u.Host, u.Attributes = host.String, attrs.String
		list = append(list, u)
	}

	return list, rows.Err()
}

func linesOf(rows *sql.Rows, err error) ([]string, error) {
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	list := []string{}

	for rows.Next() {
		var s sql.NullString
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}

		if s.String != "" {
			list = append(list, s.String)
		}
	}

	return list, rows.Err()
}

func (postgresDriver) Users(ctx context.Context, q Querier) ([]DBUser, error) {
	return scanUsers(q.QueryContext(ctx, `SELECT rolname, '',
  concat_ws(', ',
    CASE WHEN rolsuper THEN 'SUPERUSER' END,
    CASE WHEN rolcreatedb THEN 'CREATEDB' END,
    CASE WHEN rolcreaterole THEN 'CREATEROLE' END,
    CASE WHEN rolreplication THEN 'REPLICATION' END,
    CASE WHEN rolbypassrls THEN 'BYPASSRLS' END,
    CASE WHEN rolconnlimit >= 0 THEN 'CONNECTION LIMIT ' || rolconnlimit END,
    CASE WHEN rolvaliduntil IS NOT NULL THEN 'VALID UNTIL ' || rolvaliduntil::date END),
  rolcanlogin,
  rolvaliduntil IS NOT NULL AND rolvaliduntil < now()
FROM pg_catalog.pg_roles
WHERE rolname NOT LIKE 'pg\_%'
ORDER BY rolcanlogin DESC, rolname`))
}

func (postgresDriver) Grants(ctx context.Context, q Querier, name, _ string) ([]string, error) {
	return linesOf(q.QueryContext(ctx, `WITH target AS (SELECT oid, rolname FROM pg_catalog.pg_roles WHERE rolname = $1)
SELECT line FROM (
  SELECT 10 AS sort, 'Member of ' || quote_ident(p.rolname) || CASE WHEN m.admin_option THEN ' WITH ADMIN OPTION' ELSE '' END AS line
  FROM pg_catalog.pg_auth_members m JOIN target t ON t.oid = m.member JOIN pg_catalog.pg_roles p ON p.oid = m.roleid
  UNION ALL
  SELECT 20, 'Has member ' || quote_ident(r.rolname)
  FROM pg_catalog.pg_auth_members m JOIN target t ON t.oid = m.roleid JOIN pg_catalog.pg_roles r ON r.oid = m.member
  UNION ALL
  SELECT 30, 'DATABASE ' || quote_ident(d.datname) || ': ' || concat_ws(', ',
      CASE WHEN has_database_privilege(t.oid, d.oid, 'CONNECT') THEN 'CONNECT' END,
      CASE WHEN has_database_privilege(t.oid, d.oid, 'CREATE') THEN 'CREATE' END,
      CASE WHEN has_database_privilege(t.oid, d.oid, 'TEMPORARY') THEN 'TEMPORARY' END)
  FROM target t CROSS JOIN pg_catalog.pg_database d
  WHERE NOT d.datistemplate AND (has_database_privilege(t.oid, d.oid, 'CONNECT') OR has_database_privilege(t.oid, d.oid, 'CREATE'))
  UNION ALL
  SELECT 40, 'SCHEMA ' || quote_ident(n.nspname) || ': ' || concat_ws(', ',
      CASE WHEN has_schema_privilege(t.oid, n.oid, 'USAGE') THEN 'USAGE' END,
      CASE WHEN has_schema_privilege(t.oid, n.oid, 'CREATE') THEN 'CREATE' END)
  FROM target t CROSS JOIN pg_catalog.pg_namespace n
  WHERE n.nspname NOT LIKE 'pg\_%' AND n.nspname <> 'information_schema'
    AND (has_schema_privilege(t.oid, n.oid, 'USAGE') OR has_schema_privilege(t.oid, n.oid, 'CREATE'))
  UNION ALL
  SELECT 50, 'TABLE ' || quote_ident(table_schema) || '.' || quote_ident(table_name) || ': ' ||
    string_agg(privilege_type || CASE WHEN is_grantable = 'YES' THEN ' (grantable)' ELSE '' END, ', ' ORDER BY privilege_type)
  FROM information_schema.role_table_grants
  WHERE grantee = $1 AND table_schema NOT IN ('pg_catalog', 'information_schema')
  GROUP BY table_schema, table_name
) g ORDER BY sort, line`, name))
}

func (mysqlDriver) Users(ctx context.Context, q Querier) ([]DBUser, error) {
	list, err := scanUsers(q.QueryContext(ctx, "SELECT User, Host, plugin, TRUE, account_locked = 'Y' FROM mysql.user ORDER BY User, Host"))
	if err == nil {
		return list, nil
	}

	// Without access to mysql.user, the grantees the account can see.
	return scanUsers(q.QueryContext(ctx, `SELECT DISTINCT
  TRIM(BOTH '''' FROM SUBSTRING_INDEX(GRANTEE, '@', 1)),
  TRIM(BOTH '''' FROM SUBSTRING_INDEX(GRANTEE, '@', -1)), '', TRUE, FALSE
FROM information_schema.USER_PRIVILEGES ORDER BY 1, 2`))
}

func (d mysqlDriver) Grants(ctx context.Context, q Querier, name, host string) ([]string, error) {
	if host == "" {
		host = "%"
	}

	return linesOf(q.QueryContext(ctx, "SHOW GRANTS FOR "+stringLiteral(d, name)+"@"+stringLiteral(d, host)))
}

func (mssqlDriver) Users(ctx context.Context, q Querier) ([]DBUser, error) {
	list, err := scanUsers(q.QueryContext(ctx, `SELECT name, '', type_desc, CAST(CASE WHEN type IN ('S', 'U', 'E') THEN 1 ELSE 0 END AS bit), is_disabled
FROM sys.server_principals
WHERE type IN ('S', 'U', 'G', 'E', 'X') AND name NOT LIKE '##%' AND name NOT LIKE 'NT %'
ORDER BY name`))
	if err == nil && len(list) > 0 {
		return list, nil
	}

	return scanUsers(q.QueryContext(ctx, `SELECT name, '', type_desc, CAST(CASE WHEN type = 'R' THEN 0 ELSE 1 END AS bit), CAST(0 AS bit)
FROM sys.database_principals
WHERE type IN ('S', 'U', 'G', 'E', 'X', 'R') AND name NOT IN ('guest', 'INFORMATION_SCHEMA', 'sys', 'public') AND is_fixed_role = 0
ORDER BY name`))
}

func (mssqlDriver) Grants(ctx context.Context, q Querier, name, _ string) ([]string, error) {
	return linesOf(q.QueryContext(ctx, `SELECT 'Server role ' + r.name
FROM sys.server_role_members m
JOIN sys.server_principals r ON r.principal_id = m.role_principal_id
JOIN sys.server_principals p ON p.principal_id = m.member_principal_id
WHERE p.name = @p1
UNION ALL
SELECT 'Database role ' + r.name
FROM sys.database_role_members m
JOIN sys.database_principals r ON r.principal_id = m.role_principal_id
JOIN sys.database_principals u ON u.principal_id = m.member_principal_id
LEFT JOIN sys.server_principals sp ON sp.sid = u.sid
WHERE u.name = @p1 OR sp.name = @p1
UNION ALL
SELECT perm.state_desc + ' ' + perm.permission_name
  + ISNULL(' ON ' + QUOTENAME(OBJECT_SCHEMA_NAME(perm.major_id)) + '.' + QUOTENAME(OBJECT_NAME(perm.major_id)), '')
FROM sys.database_permissions perm
JOIN sys.database_principals u ON u.principal_id = perm.grantee_principal_id
LEFT JOIN sys.server_principals sp ON sp.sid = u.sid
WHERE u.name = @p1 OR sp.name = @p1`, name))
}

func (oracleDriver) Users(ctx context.Context, q Querier) ([]DBUser, error) {
	list, err := scanUsers(q.QueryContext(ctx, `SELECT username, '', account_status || NVL2(default_tablespace, ', ' || default_tablespace, ''), 1,
  CASE WHEN account_status LIKE '%LOCKED%' OR account_status LIKE '%EXPIRED%' THEN 1 ELSE 0 END
FROM dba_users WHERE oracle_maintained = 'N' ORDER BY username`))
	if err == nil {
		return list, nil
	}

	return scanUsers(q.QueryContext(ctx, `SELECT username, '', '', 1, 0 FROM all_users WHERE oracle_maintained = 'N' ORDER BY username`))
}

func (oracleDriver) Grants(ctx context.Context, q Querier, name, _ string) ([]string, error) {
	list, err := linesOf(q.QueryContext(ctx, `SELECT 'Role ' || granted_role || CASE WHEN admin_option = 'YES' THEN ' WITH ADMIN OPTION' END FROM dba_role_privs WHERE grantee = :1
UNION ALL
SELECT 'System ' || privilege || CASE WHEN admin_option = 'YES' THEN ' WITH ADMIN OPTION' END FROM dba_sys_privs WHERE grantee = :1
UNION ALL
SELECT privilege || ' ON ' || owner || '.' || table_name || CASE WHEN grantable = 'YES' THEN ' (grantable)' END FROM dba_tab_privs WHERE grantee = :1`, name))
	if err == nil {
		return list, nil
	}

	// Without DBA views, what is visible to the account itself.
	return linesOf(q.QueryContext(ctx, `SELECT 'Role ' || granted_role FROM user_role_privs WHERE username = :1
UNION ALL
SELECT privilege || ' ON ' || owner || '.' || table_name FROM all_tab_privs WHERE grantee = :1`, name))
}

func (clickhouseDriver) Users(ctx context.Context, q Querier) ([]DBUser, error) {
	users, err := scanUsers(q.QueryContext(ctx, `SELECT name, '', toString(auth_type), true, false FROM system.users ORDER BY name`))
	if err != nil {
		return nil, err
	}

	roles, err := scanUsers(q.QueryContext(ctx, `SELECT name, '', 'ROLE', false, false FROM system.roles ORDER BY name`))
	if err != nil {
		return users, nil //nolint:nilerr // Roles need extra access; users alone still help.
	}

	return append(users, roles...), nil
}

func (d clickhouseDriver) Grants(ctx context.Context, q Querier, name, _ string) ([]string, error) {
	return linesOf(q.QueryContext(ctx, "SHOW GRANTS FOR "+d.QuoteIdent(name)))
}

// redactions hide passwords in statements before they reach the query log.
var redactions = []struct{ prefix, quote string }{
	{"IDENTIFIED BY ", "'"},
	{"IDENTIFIED BY ", `"`},
	{"PASSWORD ", "'"},
	{"PASSWORD = ", "'"},
	{"PASSWORD=", "'"},
	{"BY PASSWORD ", "'"},
	{"IDENTIFIED WITH SHA256_PASSWORD BY ", "'"},
	{"IDENTIFIED WITH PLAINTEXT_PASSWORD BY ", "'"},
}

// Redact replaces the passwords in CREATE USER, ALTER USER and similar
// statements with ***, so the query log never holds them.
func Redact(sql string) string {
	upper := strings.ToUpper(sql)
	if !strings.Contains(upper, "PASSWORD") && !strings.Contains(upper, "IDENTIFIED") {
		return sql
	}

	out := sql

	for _, r := range redactions {
		for from := 0; ; {
			u := strings.ToUpper(out)

			i := strings.Index(u[from:], r.prefix+r.quote)
			if i < 0 {
				break
			}

			start := from + i + len(r.prefix) + 1
			end := start

			for end < len(out) {
				if out[end] == r.quote[0] {
					if end+1 < len(out) && out[end+1] == r.quote[0] {
						end += 2

						continue
					}

					break
				}

				end++
			}

			out = out[:start] + "***" + out[min(end, len(out)):]
			from = start + 3
		}
	}

	return out
}
