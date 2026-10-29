package engine

import (
	"context"
	"database/sql"
	"errors"
)

// The schema a table named without one is read from, per database: the
// first schema of the search path that has it, the current database or the
// default schema.

type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func resolveOne(ctx context.Context, q Querier, query string, args ...any) (string, error) {
	rq, ok := q.(rowQuerier)
	if !ok {
		return "", errors.New("resolve: no row query")
	}

	var s sql.NullString

	err := rq.QueryRowContext(ctx, query, args...).Scan(&s)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}

	return s.String, err
}

func (postgresDriver) ResolveTable(ctx context.Context, q Querier, table string) (string, error) {
	return resolveOne(ctx, q, `SELECT n.nspname FROM pg_catalog.pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE c.oid = pg_catalog.to_regclass(pg_catalog.quote_ident($1))`, table)
}

func (mysqlDriver) ResolveTable(ctx context.Context, q Querier, table string) (string, error) {
	return resolveOne(ctx, q, `SELECT TABLE_SCHEMA FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?`, table)
}

func (mssqlDriver) ResolveTable(ctx context.Context, q Querier, table string) (string, error) {
	return resolveOne(ctx, q, `SELECT OBJECT_SCHEMA_NAME(OBJECT_ID(QUOTENAME(@p1)))`, table)
}

func (oracleDriver) ResolveTable(ctx context.Context, q Querier, table string) (string, error) {
	return resolveOne(ctx, q, `SELECT owner FROM all_tables WHERE owner = SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA') AND table_name = :1`, table)
}

func (sqliteDriver) ResolveTable(ctx context.Context, q Querier, table string) (string, error) {
	return resolveOne(ctx, q, `SELECT 'main' FROM pragma_table_info(?) LIMIT 1`, table)
}
