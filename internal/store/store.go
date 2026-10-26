// Package store is the application database: users, groups, connections the
// admin defines, access grants, the query log and saved queries.
//
// The same SQL runs on SQLite and PostgreSQL. Queries are written with "?"
// placeholders and rebound for PostgreSQL.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/muz"
	_ "modernc.org/sqlite"

	"github.com/rakunlabs/dataport/internal/config"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

var ErrNotFound = errors.New("not found")

type Store struct {
	db       *sql.DB
	postgres bool
	prefix   string
}

func New(ctx context.Context, cfg config.Store, dataDir string) (*Store, error) {
	s := &Store{prefix: cfg.TablePrefix}

	var err error

	switch cfg.Type {
	case "postgres", "postgresql":
		if cfg.DSN == "" {
			return nil, errors.New("store dsn is required for postgres")
		}

		s.postgres = true

		s.db, err = sql.Open("pgx", cfg.DSN)
		if err != nil {
			return nil, fmt.Errorf("open postgres; %w", err)
		}
	case "sqlite", "":
		path, dsn, err := sqliteFile(cfg.DSN, dataDir)
		if err != nil {
			return nil, err
		}

		slog.Info("app database", "type", "sqlite", "file", path)

		s.db, err = sql.Open("sqlite", sqliteDSN(dsn))
		if err != nil {
			return nil, fmt.Errorf("open sqlite; %w", err)
		}

		// One writer at a time keeps SQLite free of "database is locked".
		s.db.SetMaxOpenConns(1)
	default:
		return nil, fmt.Errorf("unsupported store type %q", cfg.Type)
	}

	if err := s.db.PingContext(ctx); err != nil {
		_ = s.db.Close()

		return nil, fmt.Errorf("ping store; %w", err)
	}

	if err := s.migrateWith(ctx, cfg.MigrateDSN); err != nil {
		_ = s.db.Close()

		return nil, fmt.Errorf("migrate store; %w", err)
	}

	s.warnOldPrefix(ctx)

	return s, nil
}

// warnOldPrefix points out tables left by the old default prefix "dp_",
// which a store with the new default no longer reads.
func (s *Store) warnOldPrefix(ctx context.Context) {
	const old = "dp_"
	if s.prefix == old {
		return
	}

	query := "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?"
	if s.postgres {
		query = "SELECT COUNT(*) FROM pg_catalog.pg_tables WHERE schemaname = current_schema() AND tablename = ?"
	}

	var n int
	if err := s.queryRow(ctx, query, old+"users").Scan(&n); err != nil || n == 0 {
		return
	}

	slog.Warn("the store has tables with the old prefix dp_, which are not used; set DATAPORT_STORE_TABLE_PREFIX=dp_ to keep using them",
		"prefix", s.prefix)
}

// sqliteFile resolves the SQLite database file from the store dsn, which may
// be a plain path or a file: URI, and creates its directory.
func sqliteFile(dsn, dataDir string) (string, string, error) {
	if dsn == "" {
		dsn = filepath.Join(dataDir, "dataport.db")
	}

	path := strings.TrimPrefix(dsn, "file:")
	path, _, _ = strings.Cut(path, "?")

	if !strings.HasPrefix(dsn, "file:") {
		dsn = "file:" + dsn
	}

	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", "", fmt.Errorf("create directory of %s; %w", path, err)
	}

	return path, dsn, nil
}

// migrateWith runs the migrations, on a separate connection when migrateDSN
// is set (postgres only).
func (s *Store) migrateWith(ctx context.Context, migrateDSN string) error {
	if migrateDSN == "" {
		return s.migrate(ctx, s.db)
	}

	if !s.postgres {
		return errors.New("a separate migration connection is only supported for postgres")
	}

	db, err := sql.Open("pgx", migrateDSN)
	if err != nil {
		return fmt.Errorf("open migration connection; %w", err)
	}
	defer func() { _ = db.Close() }()

	db.SetMaxOpenConns(1)

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping migration connection; %w", err)
	}

	return s.migrate(ctx, db)
}

func sqliteDSN(dsn string) string {
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}

	return dsn + sep + "_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
}

func (s *Store) migrate(ctx context.Context, db *sql.DB) error {
	m := muz.Migrate{
		Path:      "migrations",
		FS:        migrationsFS,
		Extension: ".sql",
		Values:    map[string]string{"TABLE_PREFIX": s.prefix},
	}

	var driver *muz.SQLDriver
	if s.postgres {
		driver = muz.NewPostgresDriver(db, s.prefix+"migrations", slog.Default())
		driver.LockKey = "dataport:" + s.prefix + "migrations"
	} else {
		driver = muz.NewSQLiteDriver(db, s.prefix+"migrations", slog.Default())
	}

	return m.Migrate(ctx, driver)
}

func (s *Store) Close() error {
	return s.db.Close()
}

// t returns the prefixed table name.
func (s *Store) t(name string) string {
	return s.prefix + name
}

// q rebinds "?" placeholders to "$n" for PostgreSQL.
func (s *Store) q(query string) string {
	if !s.postgres {
		return query
	}

	var b strings.Builder

	b.Grow(len(query) + 16)

	n := 0

	for _, r := range query {
		if r == '?' {
			n++

			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))

			continue
		}

		b.WriteRune(r)
	}

	return b.String()
}

func (s *Store) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.db.ExecContext(ctx, s.q(query), args...)
}

func (s *Store) query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.db.QueryContext(ctx, s.q(query), args...)
}

func (s *Store) queryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return s.db.QueryRowContext(ctx, s.q(query), args...)
}

func (s *Store) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	if err := fn(tx); err != nil {
		_ = tx.Rollback()

		return err
	}

	return tx.Commit()
}

func NewID() string {
	return strings.ToLower(ulid.Make().String())
}

func now() int64 {
	return time.Now().UnixMilli()
}

func b2i(v bool) int {
	if v {
		return 1
	}

	return 0
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}

	msg := strings.ToLower(err.Error())

	return strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate key")
}

var ErrConflict = errors.New("already exists")

func wrapWriteErr(err error) error {
	if isUniqueViolation(err) {
		return ErrConflict
	}

	return err
}
