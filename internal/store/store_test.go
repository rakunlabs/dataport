package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/dataport/internal/config"
)

// stores returns the stores to test against: SQLite always, and PostgreSQL
// when DATAPORT_TEST_POSTGRES_DSN is set (make env provides one at
// postgres://postgres:pw@127.0.0.1:55432/postgres?sslmode=disable).
func stores(t *testing.T) map[string]*Store {
	t.Helper()

	ctx := context.Background()
	out := map[string]*Store{}

	lite, err := New(ctx, config.Store{Type: "sqlite", TablePrefix: "dp_", DSN: "file:" + filepath.Join(t.TempDir(), "t.db")}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = lite.Close() })
	out["sqlite"] = lite

	if dsn := os.Getenv("DATAPORT_TEST_POSTGRES_DSN"); dsn != "" {
		// A fresh prefix per test keeps runs apart in one database.
		prefix := "dpt" + strings.ToLower(NewID()[20:]) + "_"

		pg, err := New(ctx, config.Store{Type: "postgres", TablePrefix: prefix, DSN: dsn}, "")
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() {
			dropPrefixed(t, pg.db, prefix)
			_ = pg.Close()
		})

		out["postgres"] = pg
	}

	return out
}

func dropPrefixed(t *testing.T, db *sql.DB, prefix string) {
	t.Helper()

	rows, err := db.Query("SELECT tablename FROM pg_tables WHERE schemaname = current_schema() AND starts_with(tablename, $1)", prefix)
	if err != nil {
		t.Log(err)

		return
	}

	var names []string

	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		names = append(names, `"`+n+`"`)
	}

	_ = rows.Close()

	if len(names) > 0 {
		if _, err := db.Exec("DROP TABLE " + strings.Join(names, ", ") + " CASCADE"); err != nil {
			t.Log(err)
		}
	}
}

func TestLimitsMerge(t *testing.T) {
	base := Limits{MaxOpen: 5, MaxIdle: 5, MaxLifetimeSec: 3600, MaxIdleTimeSec: 300}

	if got := base.Merge(Limits{MaxOpen: 2, MaxIdleTimeSec: 60}); got != (Limits{MaxOpen: 2, MaxIdle: 5, MaxLifetimeSec: 3600, MaxIdleTimeSec: 60}) {
		t.Fatalf("merge: %+v", got)
	}

	if got := base.Merge(Limits{}); got != base {
		t.Fatalf("empty merge must keep the base: %+v", got)
	}
}

func TestStore(t *testing.T) {
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) { exercise(t, s) })
	}
}

func exercise(t *testing.T, s *Store) {
	ctx := context.Background()

	must := func(err error) {
		t.Helper()

		if err != nil {
			t.Fatal(err)
		}
	}

	// users
	admin := &User{Username: "root", PasswordHash: "h", Role: RoleAdmin, Email: "Root@Example.com"}
	must(s.CreateUser(ctx, admin))

	ayse := &User{Username: "ayse", PasswordHash: "h", Role: RoleUser, Email: "ayse@example.com"}
	must(s.CreateUser(ctx, ayse))

	if err := s.CreateUser(ctx, &User{Username: "ayse", PasswordHash: "h", Role: RoleUser}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate username: %v", err)
	}

	if n, err := s.CountActiveAdmins(ctx); err != nil || n != 1 {
		t.Fatalf("admins %d %v", n, err)
	}

	if u, err := s.GetUserByEmail(ctx, "root@EXAMPLE.com"); err != nil || u.ID != admin.ID {
		t.Fatalf("email lookup ignores case: %v %v", u, err)
	}

	must(s.TouchUserLogin(ctx, ayse.ID))
	must(s.SetUserPassword(ctx, ayse.ID, "h2"))

	// groups
	g := &Group{Name: "analysts", MemberIDs: []string{ayse.ID}}
	must(s.CreateGroup(ctx, g))

	if list, err := s.ListGroups(ctx); err != nil || len(list) != 1 || !slices.Equal(list[0].MemberIDs, []string{ayse.ID}) {
		t.Fatalf("groups %+v %v", list, err)
	}

	// connections and grants
	c := &Connection{Name: "Shop", DBType: "postgres", Host: "h", Options: map[string]string{"sslmode": "disable"},
		ReadOnly: false, Access: []Access{{SubjectType: SubjectGroup, SubjectID: g.ID, ReadOnly: true}}}
	must(s.CreateConnection(ctx, c))

	c2 := &Connection{Name: "Own", DBType: "postgres", Host: "h", UserCredentials: true,
		Access: []Access{{SubjectType: SubjectUser, SubjectID: ayse.ID, ReadOnly: false}}}
	must(s.CreateConnection(ctx, c2))

	if err := s.CreateConnection(ctx, &Connection{Name: "Shop", DBType: "postgres", Host: "h"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate connection name: %v", err)
	}

	got, err := s.GetConnection(ctx, c.ID)
	if err != nil || got.Options["sslmode"] != "disable" || len(got.Access) != 1 {
		t.Fatalf("connection %+v %v", got, err)
	}

	grants, err := s.ListGrants(ctx, ayse)
	if err != nil || len(grants) != 2 {
		t.Fatalf("grants %v %v", grants, err)
	}

	for _, gr := range grants {
		if gr.Connection.ID == c.ID && !gr.ReadOnly {
			t.Fatal("group grant is read-only")
		}

		if gr.Connection.ID == c2.ID && (gr.ReadOnly || !gr.View().UserCredentials) {
			t.Fatalf("own account grant: %+v", gr.View())
		}
	}

	// own accounts
	must(s.SetCredential(ctx, &Credential{ConnectionID: c2.ID, UserID: ayse.ID, Username: "a", PasswordEnc: "x"}))
	must(s.SetCredential(ctx, &Credential{ConnectionID: c2.ID, UserID: ayse.ID, Username: "b", PasswordEnc: "y"}))

	if cred, err := s.GetCredential(ctx, c2.ID, ayse.ID); err != nil || cred.Username != "b" {
		t.Fatalf("credential upsert %+v %v", cred, err)
	}

	if m, err := s.UserCredentials(ctx, ayse.ID); err != nil || m[c2.ID].Username != "b" {
		t.Fatalf("user credentials %v %v", m, err)
	}

	// Pool limits round-trip on connections and on users' own accounts.
	got.Limits = Limits{MaxOpen: 8, MaxLifetimeSec: 600}
	must(s.UpdateConnection(ctx, got))

	if again, _ := s.GetConnection(ctx, c.ID); again.Limits != got.Limits {
		t.Fatalf("connection limits %+v", again.Limits)
	}

	must(s.SetCredential(ctx, &Credential{ConnectionID: c2.ID, UserID: ayse.ID, Username: "b", PasswordEnc: "y", Limits: Limits{MaxOpen: 2}}))

	if cred, _ := s.GetCredential(ctx, c2.ID, ayse.ID); cred.Limits.MaxOpen != 2 {
		t.Fatalf("credential limits %+v", cred.Limits)
	}

	// query log: search must take "?" and "%" as text, not placeholders.
	for i, q := range []string{"SELECT 1", "SELECT * FROM t WHERE a = '?'", "select 50% off"} {
		must(s.AddQueryLog(ctx, &QueryLog{UserID: ayse.ID, Username: "ayse", ConnectionID: c.ID, ConnectionName: "Shop",
			SQL: q, Status: "ok", CreatedAt: time.Now().UnixMilli() + int64(i)}))
	}

	if logs, err := s.ListQueryLog(ctx, QueryLogFilter{Search: "'?'"}); err != nil || len(logs) != 1 {
		t.Fatalf("search with ?: %d %v", len(logs), err)
	}

	if logs, err := s.ListQueryLog(ctx, QueryLogFilter{UserID: ayse.ID, Limit: 2}); err != nil || len(logs) != 2 || logs[0].SQL != "select 50% off" {
		t.Fatalf("log order and limit: %v %v", logs, err)
	}

	// saved queries, settings, identities, sessions
	sq := &SavedQuery{UserID: ayse.ID, Name: "q", SQL: "SELECT ?"}
	must(s.CreateSavedQuery(ctx, sq))

	if got, err := s.GetSavedQuery(ctx, ayse.ID, sq.ID); err != nil || got.SQL != "SELECT ?" {
		t.Fatalf("saved query keeps ?: %+v %v", got, err)
	}

	must(s.SetSetting(ctx, "sso", `{"a":1}`))
	must(s.SetSetting(ctx, "sso", `{"a":2}`))

	if v, err := s.GetSetting(ctx, "sso"); err != nil || v != `{"a":2}` {
		t.Fatalf("setting %q %v", v, err)
	}

	must(s.LinkIdentity(ctx, &Identity{Provider: "idp", Subject: "s", UserID: ayse.ID}))
	must(s.CreateSession(ctx, &Session{TokenHash: "t", UserID: ayse.ID, CreatedAt: 1, ExpiresAt: 1, LastSeenAt: 1}))

	if n, err := s.DeleteExpiredSessions(ctx); err != nil || n != 1 {
		t.Fatalf("expired sessions %d %v", n, err)
	}

	// Deleting the user cascades to grants, members, accounts and identities.
	must(s.DeleteUser(ctx, ayse.ID))

	if _, err := s.GetCredential(ctx, c2.ID, ayse.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("credential must go with the user: %v", err)
	}

	if _, err := s.GetIdentity(ctx, "idp", "s"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("identity must go with the user: %v", err)
	}

	if got, _ := s.GetConnection(ctx, c2.ID); len(got.Access) != 0 {
		t.Fatalf("direct grant must go with the user: %+v", got.Access)
	}

	must(s.DeleteConnection(ctx, c.ID))
	must(s.DeleteGroup(ctx, g.ID))
}
