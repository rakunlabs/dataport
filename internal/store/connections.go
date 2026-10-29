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
	Limits Limits `json:"limits"`
	// OwnerID is the user who created a personal connection; empty for the
	// connections an admin defines. Only the owner uses a personal one.
	OwnerID string `json:"owner_id"`
	// HideWorkspace, HideNotebooks and HideMCP keep the connection out of
	// the workspace lists, the notebook cells and MCP agents.
	HideWorkspace bool `json:"hide_workspace"`
	HideNotebooks bool `json:"hide_notebooks"`
	HideMCP       bool `json:"hide_mcp"`
	// NotebookOnly allows the connection only in notebook cells: queries,
	// the schema browser, tools and agents' direct calls are refused.
	NotebookOnly bool     `json:"notebook_only"`
	CreatedAt    int64    `json:"created_at"`
	UpdatedAt    int64    `json:"updated_at"`
	Access       []Access `json:"access"`

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
	// CanWrite is set when the user's grants allow writes, so the user may
	// turn their own read-only choice off; SelfReadOnly is that choice.
	CanWrite     bool `json:"can_write"`
	SelfReadOnly bool `json:"self_read_only"`
	Production   bool `json:"production"`
	MaxRows      int  `json:"max_rows"`
	TimeoutMS    int  `json:"timeout_ms"`
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
	// Explain lists what the execution plan dialog offers.
	Explain any `json:"explain,omitempty"`
	// MaxRowsLimit is the most rows a user may ask one statement for; zero
	// means no limit.
	MaxRowsLimit int `json:"max_rows_limit"`
	// MaxTimeoutMS is the longest time limit a user may choose; zero means
	// runs may have none.
	MaxTimeoutMS int `json:"max_timeout_ms"`

	// Personal is set for a connection the user created for themselves.
	Personal      bool `json:"personal"`
	HideWorkspace bool `json:"hide_workspace"`
	HideNotebooks bool `json:"hide_notebooks"`
	HideMCP       bool `json:"hide_mcp"`
	NotebookOnly  bool `json:"notebook_only"`
}

const connectionColumns = "id, name, description, db_type, host, port, database_name, username, password_enc, options, color, read_only, production, user_credentials, max_rows, timeout_ms, limits, owner_id, hide_workspace, hide_notebooks, hide_mcp, notebook_only, created_at, updated_at"

func scanConnection(row interface{ Scan(...any) error }) (*Connection, error) {
	var (
		c          Connection
		options    string
		readOnly   int
		production int
		userCreds  int
		limits     string
		hide       [4]int
	)

	if err := row.Scan(&c.ID, &c.Name, &c.Description, &c.DBType, &c.Host, &c.Port, &c.Database, &c.Username,
		&c.PasswordEnc, &options, &c.Color, &readOnly, &production, &userCreds, &c.MaxRows, &c.TimeoutMS, &limits,
		&c.OwnerID, &hide[0], &hide[1], &hide[2], &hide[3], &c.CreatedAt, &c.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}

		return nil, err
	}

	c.ReadOnly = readOnly != 0
	c.Production = production != 0
	c.UserCredentials = userCreds != 0
	c.HideWorkspace, c.HideNotebooks, c.HideMCP, c.NotebookOnly = hide[0] != 0, hide[1] != 0, hide[2] != 0, hide[3] != 0
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
			") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"),
			c.ID, c.Name, c.Description, c.DBType, c.Host, c.Port, c.Database, c.Username, c.PasswordEnc,
			string(options), c.Color, b2i(c.ReadOnly), b2i(c.Production), b2i(c.UserCredentials), c.MaxRows, c.TimeoutMS,
			limitsJSON(c.Limits), c.OwnerID, b2i(c.HideWorkspace), b2i(c.HideNotebooks), b2i(c.HideMCP), b2i(c.NotebookOnly),
			ts, ts); err != nil {
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
			" hide_workspace = ?, hide_notebooks = ?, hide_mcp = ?, notebook_only = ?, updated_at = ? WHERE id = ?"),
			c.Name, c.Description, c.DBType, c.Host, c.Port, c.Database, c.Username, c.PasswordEnc,
			string(options), c.Color, b2i(c.ReadOnly), b2i(c.Production), b2i(c.UserCredentials), c.MaxRows, c.TimeoutMS,
			limitsJSON(c.Limits), b2i(c.HideWorkspace), b2i(c.HideNotebooks), b2i(c.HideMCP), b2i(c.NotebookOnly), c.UpdatedAt, c.ID)
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
	// user's grants (direct or through a group) allow writes, and the user
	// did not choose to use it read-only.
	ReadOnly bool
	// CanWrite is what the grants allow before the user's own choice, so
	// the user can turn writes back on.
	CanWrite bool
	// SelfReadOnly is set when the user chose read-only.
	SelfReadOnly bool
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
		CanWrite:    g.CanWrite,
		// The user's own read-only choice.
		SelfReadOnly: g.SelfReadOnly,
		Production:   c.Production,
		MaxRows:      c.MaxRows,
		TimeoutMS:    c.TimeoutMS,

		UserCredentials: c.UserCredentials,
		Personal:        c.OwnerID != "",
		HideWorkspace:   c.HideWorkspace,
		HideNotebooks:   c.HideNotebooks,
		HideMCP:         c.HideMCP,
		NotebookOnly:    c.NotebookOnly,
	}
}

// SetPersonalConnections turns personal connections on or off. While off
// they are kept but neither listed nor usable, also by their owners.
func (s *Store) SetPersonalConnections(on bool) {
	s.personal.Store(on)
}

// personalGrant is the grant of a personal connection: only its owner has
// one, while personal connections are on.
func (s *Store) personalGrant(user *User, c *Connection) (*Grant, bool) {
	if !s.personal.Load() || c.OwnerID != user.ID {
		return nil, false
	}

	return &Grant{Connection: c, ReadOnly: c.ReadOnly}, true
}

// PersonalConnections returns the connections the user created.
func (s *Store) PersonalConnections(ctx context.Context, userID string) ([]*Connection, error) {
	rows, err := s.query(ctx, "SELECT "+connectionColumns+" FROM "+s.t("connections")+" WHERE owner_id = ? ORDER BY name", userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	list := []*Connection{}

	for rows.Next() {
		c, err := scanConnection(rows)
		if err != nil {
			return nil, err
		}

		c.Access = []Access{}
		list = append(list, c)
	}

	return list, rows.Err()
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
// connection with the connection's own read-only setting. Connections the
// user chose to use read-only come back read-only.
func (s *Store) ListGrants(ctx context.Context, user *User) ([]*Grant, error) {
	grants, err := s.listGrants(ctx, user)
	if err != nil {
		return nil, err
	}

	self, err := s.SelfReadOnly(ctx, user.ID)
	if err != nil {
		return nil, err
	}

	for _, g := range grants {
		g.applySelf(self[g.Connection.ID])
	}

	return grants, nil
}

// applySelf records what the grant allows and narrows it to the user's
// own read-only choice.
func (g *Grant) applySelf(self bool) {
	g.CanWrite = !g.ReadOnly
	g.SelfReadOnly = self && g.CanWrite
	g.ReadOnly = g.ReadOnly || g.SelfReadOnly
}

func (s *Store) listGrants(ctx context.Context, user *User) ([]*Grant, error) {
	conns, err := s.ListConnections(ctx)
	if err != nil {
		return nil, err
	}

	if user.IsAdmin() {
		grants := make([]*Grant, 0, len(conns))
		for _, c := range conns {
			if c.OwnerID != "" {
				if g, ok := s.personalGrant(user, c); ok {
					grants = append(grants, g)
				}

				continue
			}

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
		if c.OwnerID != "" {
			if g, ok := s.personalGrant(user, c); ok {
				grants = append(grants, g)
			}

			continue
		}

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
// same to the caller on purpose. The user's own read-only choice applies.
func (s *Store) GetGrant(ctx context.Context, user *User, connectionID string) (*Grant, error) {
	g, err := s.getGrant(ctx, user, connectionID)
	if err != nil {
		return nil, err
	}

	self, err := s.isSelfReadOnly(ctx, user.ID, connectionID)
	if err != nil {
		return nil, err
	}

	g.applySelf(self)

	return g, nil
}

func (s *Store) getGrant(ctx context.Context, user *User, connectionID string) (*Grant, error) {
	c, err := s.GetConnection(ctx, connectionID)
	if err != nil {
		return nil, err
	}

	// Nobody else reaches a personal connection, admins neither: it holds
	// its owner's own account.
	if c.OwnerID != "" {
		if g, ok := s.personalGrant(user, c); ok {
			return g, nil
		}

		return nil, ErrNotFound
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

// SelfReadOnly returns the connections the user chose to use read-only.
func (s *Store) SelfReadOnly(ctx context.Context, userID string) (map[string]bool, error) {
	rows, err := s.query(ctx, "SELECT connection_id FROM "+s.t("user_read_only")+" WHERE user_id = ?", userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := map[string]bool{}

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}

		out[id] = true
	}

	return out, rows.Err()
}

func (s *Store) isSelfReadOnly(ctx context.Context, userID, connectionID string) (bool, error) {
	var n int

	err := s.queryRow(ctx, "SELECT COUNT(*) FROM "+s.t("user_read_only")+" WHERE user_id = ? AND connection_id = ?", userID, connectionID).Scan(&n)

	return n > 0, err
}

// SetSelfReadOnly turns the user's own read-only choice on a connection on
// or off.
func (s *Store) SetSelfReadOnly(ctx context.Context, userID, connectionID string, on bool) error {
	if !on {
		_, err := s.exec(ctx, "DELETE FROM "+s.t("user_read_only")+" WHERE user_id = ? AND connection_id = ?", userID, connectionID)

		return err
	}

	_, err := s.exec(ctx, "INSERT INTO "+s.t("user_read_only")+" (user_id, connection_id, created_at) VALUES (?, ?, ?) ON CONFLICT (user_id, connection_id) DO NOTHING",
		userID, connectionID, now())

	return err
}

func nonNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}

	return m
}
