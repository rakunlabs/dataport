package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// DocConnection is how a connection's documentation is built for every user
// of it: which schemas, and the database account that reads their catalog.
// Its snapshots are kept under DocSystemUser.
type DocConnection struct {
	ConnectionID string   `json:"connection_id"`
	Enabled      bool     `json:"enabled"`
	Include      []string `json:"include"`
	Exclude      []string `json:"exclude"`
	// BlockExcluded closes the schemas not chosen to documentation: nobody
	// builds or reads them. Off, users may still document them themselves.
	// It applies whether or not Enabled is set.
	BlockExcluded bool `json:"block_excluded"`
	// LookupTables are the tables whose rows the documentation shows, as
	// "schema.table" patterns ("*" matches any run of characters). The
	// documentation reads no other table data. They apply whether or not
	// Enabled is set.
	LookupTables []string `json:"lookup_tables"`
	// Username and PasswordEnc are the documentation account. Empty, the
	// connection's own account is used; own-account connections need one.
	Username     string `json:"username"`
	PasswordEnc  string `json:"-"`
	DiscoveredAt int64  `json:"discovered_at"`
	LastError    string `json:"last_error"`
	UpdatedAt    int64  `json:"updated_at"`
}

// DocSystemUser is the user id of the snapshots built for every user of a
// connection. It is no user's id.
const DocSystemUser = "system"

// DocSharedUser is the user id of the snapshots of a connection whose users
// all sign in with its one database account: they see the same catalog, so
// they share one snapshot per schema, whoever builds it. It is no user's id.
const DocSharedUser = "shared"

// docOwnersSQL lists the snapshot owners that are no user.
const docOwnersSQL = "('" + DocSystemUser + "', '" + DocSharedUser + "')"

// AdoptSharedDocs turns users' snapshots of a connection into its shared
// ones: the newest of every schema becomes the shared snapshot, unless
// there is one, and the users' copies go.
func (s *Store) AdoptSharedDocs(ctx context.Context, connectionID string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		docs := s.t("schema_docs")

		if _, err := tx.ExecContext(ctx, s.q("INSERT INTO "+docs+
			" (connection_id, user_id, schema_name, source_version, payload, generated_at, attempted_at, last_error, lease_token, lease_until)"+
			" SELECT d.connection_id, ?, d.schema_name, d.source_version, d.payload, d.generated_at, d.attempted_at, d.last_error, '', 0"+
			" FROM "+docs+" d WHERE d.connection_id = ? AND d.user_id NOT IN "+docOwnersSQL+
			" AND NOT EXISTS (SELECT 1 FROM "+docs+" o WHERE o.connection_id = d.connection_id AND o.schema_name = d.schema_name"+
			" AND o.user_id NOT IN "+docOwnersSQL+" AND (o.generated_at > d.generated_at OR (o.generated_at = d.generated_at AND o.user_id < d.user_id)))"+
			" ON CONFLICT (connection_id, user_id, schema_name) DO NOTHING"), DocSharedUser, connectionID); err != nil {
			return err
		}

		_, err := tx.ExecContext(ctx, s.q("DELETE FROM "+docs+" WHERE connection_id = ? AND user_id NOT IN "+docOwnersSQL), connectionID)

		return err
	})
}

const docConnColumns = "connection_id, enabled, include_schemas, exclude_schemas, username, password_enc, discovered_at, last_error, updated_at, block_excluded, lookup_tables"

func scanDocConnection(row interface{ Scan(...any) error }) (*DocConnection, error) {
	var (
		d                DocConnection
		enabled, block   int
		include, exclude string
		lookups          string
	)

	if err := row.Scan(&d.ConnectionID, &enabled, &include, &exclude, &d.Username, &d.PasswordEnc, &d.DiscoveredAt, &d.LastError, &d.UpdatedAt, &block, &lookups); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}

		return nil, err
	}

	d.Enabled, d.BlockExcluded = enabled != 0, block != 0
	d.Include, d.Exclude = decodePatterns(include), decodePatterns(exclude)
	d.LookupTables = decodePatterns(lookups)

	return &d, nil
}

func decodePatterns(raw string) []string {
	list := []string{}
	_ = json.Unmarshal([]byte(raw), &list)

	if list == nil {
		list = []string{}
	}

	return list
}

func encodePatterns(list []string) string {
	if list == nil {
		list = []string{}
	}

	b, _ := json.Marshal(list)

	return string(b)
}

// GetDocConnection returns a connection's documentation settings, or
// ErrNotFound when none were saved.
func (s *Store) GetDocConnection(ctx context.Context, connectionID string) (*DocConnection, error) {
	return scanDocConnection(s.queryRow(ctx, "SELECT "+docConnColumns+" FROM "+s.t("doc_connections")+" WHERE connection_id = ?", connectionID))
}

func (s *Store) ListDocConnections(ctx context.Context) ([]*DocConnection, error) {
	rows, err := s.query(ctx, "SELECT "+docConnColumns+" FROM "+s.t("doc_connections"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	list := []*DocConnection{}

	for rows.Next() {
		d, err := scanDocConnection(rows)
		if err != nil {
			return nil, err
		}

		list = append(list, d)
	}

	return list, rows.Err()
}

// SaveDocConnection stores the settings. Turned off, the connection's
// shared snapshots are removed with them.
func (s *Store) SaveDocConnection(ctx context.Context, d *DocConnection) error {
	d.UpdatedAt = now()

	return s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, s.q("UPDATE "+s.t("doc_connections")+
			" SET enabled = ?, include_schemas = ?, exclude_schemas = ?, username = ?, password_enc = ?, updated_at = ?, block_excluded = ?, lookup_tables = ? WHERE connection_id = ?"),
			b2i(d.Enabled), encodePatterns(d.Include), encodePatterns(d.Exclude), d.Username, d.PasswordEnc, d.UpdatedAt, b2i(d.BlockExcluded), encodePatterns(d.LookupTables), d.ConnectionID)
		if err != nil {
			return err
		}

		if n, _ := res.RowsAffected(); n == 0 {
			if _, err := tx.ExecContext(ctx, s.q("INSERT INTO "+s.t("doc_connections")+" ("+docConnColumns+") VALUES (?, ?, ?, ?, ?, ?, 0, '', ?, ?, ?)"),
				d.ConnectionID, b2i(d.Enabled), encodePatterns(d.Include), encodePatterns(d.Exclude), d.Username, d.PasswordEnc, d.UpdatedAt, b2i(d.BlockExcluded), encodePatterns(d.LookupTables)); err != nil {
				return err
			}
		}

		if !d.Enabled {
			_, err = tx.ExecContext(ctx, s.q("DELETE FROM "+s.t("schema_docs")+" WHERE connection_id = ? AND user_id = ?"), d.ConnectionID, DocSystemUser)
		}

		return err
	})
}

// SetDocDiscovery records when the schemas of a connection were last listed
// for its documentation, and a safe error code when that failed.
func (s *Store) SetDocDiscovery(ctx context.Context, connectionID string, at int64, lastError string) error {
	_, err := s.exec(ctx, "UPDATE "+s.t("doc_connections")+" SET discovered_at = ?, last_error = ? WHERE connection_id = ?", at, lastError, connectionID)

	return err
}

// KeepDocs removes a user's snapshots of a connection whose schema is not
// in keep.
func (s *Store) KeepDocs(ctx context.Context, connectionID, userID string, keep []string) error {
	query := "DELETE FROM " + s.t("schema_docs") + " WHERE connection_id = ? AND user_id = ?"
	args := []any{connectionID, userID}

	if len(keep) > 0 {
		query += " AND schema_name NOT IN (?" + strings.Repeat(", ?", len(keep)-1) + ")"
		for _, k := range keep {
			args = append(args, k)
		}
	}

	_, err := s.exec(ctx, query, args...)

	return err
}

// ConnectionDocs lists a user's snapshots of one connection.
func (s *Store) ConnectionDocs(ctx context.Context, connectionID, userID string) ([]DocInfo, error) {
	return s.scanDocInfos(ctx, docInfoSelect+docInfoFrom(s)+" WHERE d.connection_id = ? AND d.user_id = ? ORDER BY d.schema_name", connectionID, userID)
}

// ConnectionDocKeys lists every snapshot of a connection, of every user.
func (s *Store) ConnectionDocKeys(ctx context.Context, connectionID string) ([]DocKey, error) {
	rows, err := s.query(ctx, "SELECT connection_id, user_id, schema_name FROM "+s.t("schema_docs")+" WHERE connection_id = ?", connectionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	list := []DocKey{}

	for rows.Next() {
		var k DocKey
		if err := rows.Scan(&k.ConnectionID, &k.UserID, &k.Schema); err != nil {
			return nil, err
		}

		list = append(list, k)
	}

	return list, rows.Err()
}

// DeleteDoc removes one snapshot.
func (s *Store) DeleteDoc(ctx context.Context, k DocKey) error {
	_, err := s.exec(ctx, "DELETE FROM "+s.t("schema_docs")+" WHERE "+docWhere, k.ConnectionID, k.UserID, k.Schema)

	return err
}
