package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

const (
	SubjectUser  = "user"
	SubjectGroup = "group"
)

// Connection is a target database defined by an admin. Users never see the
// host, credentials or options; they only get a ConnectionView.
type Connection struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	DBType      string            `json:"db_type"`
	Host        string            `json:"host"`
	Port        int               `json:"port"`
	Database    string            `json:"database"`
	Username    string            `json:"username"`
	PasswordEnc string            `json:"-"`
	HasPassword bool              `json:"has_password"`
	Options     map[string]string `json:"options"`
	Color       string            `json:"color"`
	ReadOnly    bool              `json:"read_only"`
	// Production asks users to confirm every write before it runs.
	Production bool `json:"production"`
	// UserCredentials makes every user sign in to the database with their
	// own account. Username and password of the connection are not used.
	UserCredentials bool `json:"user_credentials"`
	MaxRows         int  `json:"max_rows"`
	TimeoutMS       int  `json:"timeout_ms"`
	// Limits are the pool settings; users signing in with their own
	// account may override them for their own pool.
	Limits    Limits   `json:"limits"`
	CreatedAt int64    `json:"created_at"`
	UpdatedAt int64    `json:"updated_at"`
	Access    []Access `json:"access"`

	// Owner is set at runtime when Username and the password are a user's
	// own account, so the engine keeps a separate pool for that user.
	Owner string `json:"-"`
}

type Access struct {
	SubjectType string `json:"subject_type"`
	SubjectID   string `json:"subject_id"`
	ReadOnly    bool   `json:"read_only"`
}

// Limits configure the connection pool to a target database. Zero values
// fall back to the server defaults; durations are in seconds.
type Limits struct {
	MaxOpen        int `json:"max_open,omitempty"`
	MaxIdle        int `json:"max_idle,omitempty"`
	MaxLifetimeSec int `json:"max_lifetime_sec,omitempty"`
	MaxIdleTimeSec int `json:"max_idle_time_sec,omitempty"`
}

// Merge returns l with the fields set in o replacing its own.
func (l Limits) Merge(o Limits) Limits {
	if o.MaxOpen > 0 {
		l.MaxOpen = o.MaxOpen
	}

	if o.MaxIdle > 0 {
		l.MaxIdle = o.MaxIdle
	}

	if o.MaxLifetimeSec > 0 {
		l.MaxLifetimeSec = o.MaxLifetimeSec
	}

	if o.MaxIdleTimeSec > 0 {
		l.MaxIdleTimeSec = o.MaxIdleTimeSec
	}

	return l
}

func parseLimits(s string) Limits {
	var l Limits
	if s != "" {
		_ = json.Unmarshal([]byte(s), &l)
	}

	return l
}

func limitsJSON(l Limits) string {
	data, _ := json.Marshal(l)

	return string(data)
}

// ConnectionView is what a regular user sees about a connection.
type ConnectionView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	DBType      string `json:"db_type"`
	Database    string `json:"database"`
	Color       string `json:"color"`
	ReadOnly    bool   `json:"read_only"`
	Production  bool   `json:"production"`
	MaxRows     int    `json:"max_rows"`
	TimeoutMS   int    `json:"timeout_ms"`
	// UserCredentials is set when the user signs in with their own database
	// account; CredentialUser is the stored username, empty until saved.
	UserCredentials bool   `json:"user_credentials"`
	CredentialUser  string `json:"credential_user,omitempty"`
	// Limits are the connection's pool settings; for own-account
	// connections, MyLimits are the user's overrides.
	Limits   *Limits `json:"limits,omitempty"`
	MyLimits *Limits `json:"my_limits,omitempty"`
	// Editable is set when rows can be edited in the grid, which needs
	// transactions.
	Editable bool `json:"editable"`
	// ManualTx is set when a query tab may keep a transaction open over
	// several runs.
	ManualTx bool `json:"manual_tx"`
	// MaxRowsLimit is the most rows a user may ask one statement for; zero
	// means no limit.
	MaxRowsLimit int `json:"max_rows_limit"`
	// MaxTimeoutMS is the longest time limit a user may choose; zero means
	// runs may have none.
	MaxTimeoutMS int `json:"max_timeout_ms"`
}

const connectionColumns = "id, name, description, db_type, host, port, database_name, username, password_enc, options, color, read_only, production, user_credentials, max_rows, timeout_ms, limits, created_at, updated_at"

func scanConnection(row interface{ Scan(...any) error }) (*Connection, error) {
	var (
		c          Connection
		options    string
		readOnly   int
		production int
		userCreds  int
		limits     string
	)

	if err := row.Scan(&c.ID, &c.Name, &c.Description, &c.DBType, &c.Host, &c.Port, &c.Database, &c.Username,
		&c.PasswordEnc, &options, &c.Color, &readOnly, &production, &userCreds, &c.MaxRows, &c.TimeoutMS, &limits, &c.CreatedAt, &c.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}

		return nil, err
	}

	c.ReadOnly = readOnly != 0
	c.Production = production != 0
	c.UserCredentials = userCreds != 0
	c.Limits = parseLimits(limits)
	c.HasPassword = c.PasswordEnc != ""
	c.Options = map[string]string{}

	if options != "" {
		_ = json.Unmarshal([]byte(options), &c.Options)
	}

	return &c, nil
}

func (s *Store) ListConnections(ctx context.Context) ([]*Connection, error) {
	rows, err := s.query(ctx, "SELECT "+connectionColumns+" FROM "+s.t("connections")+" ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	list := []*Connection{}
	byID := map[string]*Connection{}

	for rows.Next() {
		c, err := scanConnection(rows)
		if err != nil {
			return nil, err
		}

		c.Access = []Access{}
		list = append(list, c)
		byID[c.ID] = c
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	access, err := s.query(ctx, "SELECT connection_id, subject_type, subject_id, read_only FROM "+s.t("connection_access"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = access.Close() }()

	for access.Next() {
		var (
			id       string
			a        Access
			readOnly int
		)

		if err := access.Scan(&id, &a.SubjectType, &a.SubjectID, &readOnly); err != nil {
			return nil, err
		}

		a.ReadOnly = readOnly != 0

		if c := byID[id]; c != nil {
			c.Access = append(c.Access, a)
		}
	}

	return list, access.Err()
}

func (s *Store) GetConnection(ctx context.Context, id string) (*Connection, error) {
	c, err := scanConnection(s.queryRow(ctx, "SELECT "+connectionColumns+" FROM "+s.t("connections")+" WHERE id = ?", id))
	if err != nil {
		return nil, err
	}

	c.Access, err = s.connectionAccess(ctx, id)

	return c, err
}

func (s *Store) CreateConnection(ctx context.Context, c *Connection) error {
	ts := now()
	c.ID = NewID()
	c.CreatedAt, c.UpdatedAt = ts, ts

	options, err := json.Marshal(nonNil(c.Options))
	if err != nil {
		return err
	}

	return wrapWriteErr(s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, s.q("INSERT INTO "+s.t("connections")+" ("+connectionColumns+
			") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"),
			c.ID, c.Name, c.Description, c.DBType, c.Host, c.Port, c.Database, c.Username, c.PasswordEnc,
			string(options), c.Color, b2i(c.ReadOnly), b2i(c.Production), b2i(c.UserCredentials), c.MaxRows, c.TimeoutMS,
			limitsJSON(c.Limits), ts, ts); err != nil {
			return err
		}

		return s.setConnectionAccess(ctx, tx, c.ID, c.Access)
	}))
}

// UpdateConnection replaces every field. PasswordEnc is kept as is, so the
// caller decides whether the password changes. Access is replaced only when
// it is not nil.
func (s *Store) UpdateConnection(ctx context.Context, c *Connection) error {
	c.UpdatedAt = now()

	options, err := json.Marshal(nonNil(c.Options))
	if err != nil {
		return err
	}

	return wrapWriteErr(s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, s.q("UPDATE "+s.t("connections")+
			" SET name = ?, description = ?, db_type = ?, host = ?, port = ?, database_name = ?, username = ?, password_enc = ?,"+
			" options = ?, color = ?, read_only = ?, production = ?, user_credentials = ?, max_rows = ?, timeout_ms = ?, limits = ?,"+
			" updated_at = ? WHERE id = ?"),
			c.Name, c.Description, c.DBType, c.Host, c.Port, c.Database, c.Username, c.PasswordEnc,
			string(options), c.Color, b2i(c.ReadOnly), b2i(c.Production), b2i(c.UserCredentials), c.MaxRows, c.TimeoutMS,
			limitsJSON(c.Limits), c.UpdatedAt, c.ID)
		if err != nil {
			return err
		}

		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}

		if c.Access != nil {
			return s.setConnectionAccess(ctx, tx, c.ID, c.Access)
		}

		return nil
	}))
}

func (s *Store) DeleteConnection(ctx context.Context, id string) error {
	res, err := s.exec(ctx, "DELETE FROM "+s.t("connections")+" WHERE id = ?", id)
	if err != nil {
		return err
	}

	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}

	return nil
}

func (s *Store) connectionAccess(ctx context.Context, id string) ([]Access, error) {
	rows, err := s.query(ctx, "SELECT subject_type, subject_id, read_only FROM "+s.t("connection_access")+" WHERE connection_id = ?", id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	list := []Access{}

	for rows.Next() {
		var (
			a        Access
			readOnly int
		)

		if err := rows.Scan(&a.SubjectType, &a.SubjectID, &readOnly); err != nil {
			return nil, err
		}

		a.ReadOnly = readOnly != 0
		list = append(list, a)
	}

	return list, rows.Err()
}

func (s *Store) setConnectionAccess(ctx context.Context, tx *sql.Tx, id string, access []Access) error {
	if _, err := tx.ExecContext(ctx, s.q("DELETE FROM "+s.t("connection_access")+" WHERE connection_id = ?"), id); err != nil {
		return err
	}

	seen := map[string]struct{}{}
	ts := now()

	for _, a := range access {
		if a.SubjectType != SubjectUser && a.SubjectType != SubjectGroup {
			continue
		}

		key := a.SubjectType + ":" + a.SubjectID
		if _, ok := seen[key]; ok || a.SubjectID == "" {
			continue
		}

		seen[key] = struct{}{}

		if _, err := tx.ExecContext(ctx, s.q("INSERT INTO "+s.t("connection_access")+
			" (connection_id, subject_type, subject_id, read_only, created_at) VALUES (?, ?, ?, ?, ?)"),
			id, a.SubjectType, a.SubjectID, b2i(a.ReadOnly), ts); err != nil {
			return err
		}
	}

	return nil
}

// Grant is the effective permission of one user on one connection.
type Grant struct {
	Connection *Connection
	// ReadOnly is true unless the connection itself and at least one of the
	// user's grants (direct or through a group) allow writes.
	ReadOnly bool
}

func (g *Grant) View() ConnectionView {
	c := g.Connection

	return ConnectionView{
		ID:          c.ID,
		Name:        c.Name,
		Description: c.Description,
		DBType:      c.DBType,
		Database:    c.Database,
		Color:       c.Color,
		ReadOnly:    g.ReadOnly,
		Production:  c.Production,
		MaxRows:     c.MaxRows,
		TimeoutMS:   c.TimeoutMS,

		UserCredentials: c.UserCredentials,
	}
}

// grantQuery selects the access rows that apply to a user, directly or
// through any of the user's groups.
func (s *Store) grantQuery() string {
	return "SELECT a.connection_id, a.read_only FROM " + s.t("connection_access") + " a" +
		" WHERE (a.subject_type = 'user' AND a.subject_id = ?)" +
		" OR (a.subject_type = 'group' AND a.subject_id IN (SELECT group_id FROM " + s.t("group_members") + " WHERE user_id = ?))"
}

func (s *Store) userWritable(ctx context.Context, userID string) (map[string]bool, error) {
	rows, err := s.query(ctx, s.grantQuery(), userID, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	writable := map[string]bool{}

	for rows.Next() {
		var (
			id       string
			readOnly int
		)

		if err := rows.Scan(&id, &readOnly); err != nil {
			return nil, err
		}

		writable[id] = writable[id] || readOnly == 0
	}

	return writable, rows.Err()
}

// ListGrants returns every connection the user may use. Admins get every
// connection with the connection's own read-only setting.
func (s *Store) ListGrants(ctx context.Context, user *User) ([]*Grant, error) {
	conns, err := s.ListConnections(ctx)
	if err != nil {
		return nil, err
	}

	if user.IsAdmin() {
		grants := make([]*Grant, 0, len(conns))
		for _, c := range conns {
			grants = append(grants, &Grant{Connection: c, ReadOnly: c.ReadOnly})
		}

		return grants, nil
	}

	writable, err := s.userWritable(ctx, user.ID)
	if err != nil {
		return nil, err
	}

	grants := []*Grant{}

	for _, c := range conns {
		w, ok := writable[c.ID]
		if !ok {
			continue
		}

		grants = append(grants, &Grant{Connection: c, ReadOnly: c.ReadOnly || !w})
	}

	return grants, nil
}

// GetGrant returns the user's permission on one connection, or ErrNotFound
// when the user has none. A missing connection and a missing grant look the
// same to the caller on purpose.
func (s *Store) GetGrant(ctx context.Context, user *User, connectionID string) (*Grant, error) {
	c, err := s.GetConnection(ctx, connectionID)
	if err != nil {
		return nil, err
	}

	if user.IsAdmin() {
		return &Grant{Connection: c, ReadOnly: c.ReadOnly}, nil
	}

	writable, err := s.userWritable(ctx, user.ID)
	if err != nil {
		return nil, err
	}

	w, ok := writable[c.ID]
	if !ok {
		return nil, ErrNotFound
	}

	return &Grant{Connection: c, ReadOnly: c.ReadOnly || !w}, nil
}

func nonNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}

	return m
}
