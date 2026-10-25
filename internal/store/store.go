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
		dsn := cfg.DSN
		if dsn == "" {
			if err := os.MkdirAll(dataDir, 0o700); err != nil {
				return nil, fmt.Errorf("create data dir %s; %w", dataDir, err)
			}

			dsn = "file:" + filepath.Join(dataDir, "dataport.db")
		}

		dsn = sqliteDSN(dsn)

		s.db, err = sql.Open("sqlite", dsn)
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

	if err := s.migrate(ctx); err != nil {
		_ = s.db.Close()

		return nil, fmt.Errorf("migrate store; %w", err)
	}

	return s, nil
}

func sqliteDSN(dsn string) string {
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}

	return dsn + sep + "_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
}

func (s *Store) migrate(ctx context.Context) error {
	m := muz.Migrate{
		Path:      "migrations",
		FS:        migrationsFS,
		Extension: ".sql",
		Values:    map[string]string{"TABLE_PREFIX": s.prefix},
	}

	var driver *muz.SQLDriver
	if s.postgres {
		driver = muz.NewPostgresDriver(s.db, s.prefix+"migrations", slog.Default())
		driver.LockKey = "dataport:" + s.prefix + "migrations"
	} else {
		driver = muz.NewSQLiteDriver(s.db, s.prefix+"migrations", slog.Default())
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
