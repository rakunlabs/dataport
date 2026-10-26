package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/store"
)

func docKeyFor(t *testing.T, env *mcpEnv) store.DocKey {
	t.Helper()
	grants, err := env.st.ListGrants(context.Background(), env.alice)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range grants {
		if g.Connection.Name == "shop" {
			return store.DocKey{ConnectionID: g.Connection.ID, UserID: env.alice.ID, Schema: "main"}
		}
	}
	t.Fatal("missing shop")
	return store.DocKey{}
}

func TestSavedDocumentation(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")
	k := docKeyFor(t, env)
	s := env.srv
	first, err := s.documentation(ctx, k, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Document.Tables) != 2 {
		t.Fatalf("tables: %+v", first.Document.Tables)
	}
	g, pw, err := s.resolveGrant(ctx, env.alice, k.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	run := func(sql string) {
		t.Helper()
		result, err := s.engine.Execute(ctx, g.Connection, pw, engine.ExecRequest{SQL: sql, MaxRows: 1})
		if err != nil || result.FirstError() != "" {
			t.Fatalf("seed %s: %v %+v", sql, err, result)
		}
	}
	run("CREATE TABLE added (id INTEGER PRIMARY KEY)")
	// A separate server object (another replica) reads the same persisted
	// document, not the changed live catalog.
	other := &Server{store: env.st, engine: s.engine, cipher: s.cipher}
	cached, err := other.documentation(ctx, k, false)
	if err != nil || cached.Document.GeneratedAt != first.Document.GeneratedAt || len(cached.Document.Tables) != 2 {
		t.Fatalf("not cached: %v %+v", err, cached)
	}
	fresh, err := other.documentation(ctx, k, true)
	if err != nil || len(fresh.Document.Tables) != 3 {
		t.Fatalf("manual refresh: %v %+v", err, fresh)
	}
	// Invalid DB objects fail a catalog read. The old payload survives.
	run("CREATE VIEW broken AS SELECT * FROM missing_table")
	failed, err := s.documentation(ctx, k, true)
	if err != nil || len(failed.Document.Tables) != 3 || failed.Status.LastError != "refresh_failed" || failed.Document.GeneratedAt != fresh.Document.GeneratedAt {
		t.Fatalf("lost old payload on failure: %v %+v", err, failed)
	}
	run("DROP VIEW broken")
	// A due schedule is dispatched once, advances next_run and clears errors.
	if err := env.st.SetDocSchedule(ctx, k, "@hourly", time.Now().Add(-time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	run("CREATE TABLE scheduled (id INTEGER PRIMARY KEY)")
	if err := other.dispatchDocs(ctx); err != nil {
		t.Fatal(err)
	}
	d, _ := env.st.GetDoc(ctx, k)
	if d.NextRun <= time.Now().UnixMilli() || d.LastError != "" {
		t.Fatalf("schedule not advanced: %+v", d)
	}
	if err := s.dispatchDocs(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := env.st.GetDoc(ctx, k)
	if d.GeneratedAt != after.GeneratedAt {
		t.Fatal("second dispatcher regenerated the same occurrence")
	}
	var doc engine.SchemaDoc
	if err := json.Unmarshal([]byte(d.Payload), &doc); err != nil || len(doc.Tables) != 4 {
		t.Fatalf("scheduled output: %v %+v", err, doc)
	}
	// Another user must not read even an already generated snapshot.
	bobKey := k
	bobKey.UserID = env.bob.ID
	if _, err := s.documentation(ctx, bobKey, false); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("bob read alice's documentation: %v", err)
	}
	// Revocation is effective immediately, including cached reads and jobs.
	c, _ := env.st.GetConnection(ctx, k.ConnectionID)
	c.Access = []store.Access{}
	if err := env.st.UpdateConnection(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err := s.documentation(ctx, k, false); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoked user read cache: %v", err)
	}
	if err := env.st.SetDocSchedule(ctx, k, "@hourly", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.dispatchDocs(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ = env.st.GetDoc(ctx, k)
	if after.GeneratedAt != d.GeneratedAt || after.LastError == "" {
		t.Fatal("revoked user's job ran with an old grant")
	}
}

func TestDocScheduleHTTP(t *testing.T) {
	env := newMCPEnv(t, "/dp")
	k := docKeyFor(t, env)
	base := env.httpSrv.URL + "/dp/api/v1/connections/" + k.ConnectionID + "/docs"
	resp, err := http.Post(env.httpSrv.URL+"/dp/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"alice","password":"x-password"}`))
	if err != nil {
		t.Fatal(err)
	}
	cookies := resp.Cookies()
	_ = resp.Body.Close()
	request := func(method, url, body string, want int) []byte {
		t.Helper()
		req, _ := http.NewRequest(method, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for _, c := range cookies {
			req.AddCookie(c)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		var raw json.RawMessage
		_ = json.NewDecoder(res.Body).Decode(&raw)
		if res.StatusCode != want {
			t.Fatalf("%s %s: %d %s", method, url, res.StatusCode, raw)
		}
		return raw
	}
	request("GET", base+"?schema=main", "", http.StatusOK)
	raw := request("PUT", base+"/schedule?schema=main", `{"cron_spec":"CRON_TZ=Europe/Istanbul 0 3 * * *"}`, http.StatusOK)
	var status docStatus
	_ = json.Unmarshal(raw, &status)
	if status.NextRun <= time.Now().UnixMilli() || status.CronSpec == "" {
		t.Fatalf("schedule: %s", raw)
	}
	request("PUT", base+"/schedule?schema=main", `{"cron_spec":"not cron"}`, http.StatusBadRequest)
	request("PUT", base+"/schedule?schema=main", `{"cron_spec":"@every 1s"}`, http.StatusBadRequest)
	request("POST", base+"/refresh?schema=main", `{}`, http.StatusOK)
	request("PUT", base+"/schedule?schema=main", `{"cron_spec":""}`, http.StatusOK)
	d, _ := env.st.GetDoc(context.Background(), k)
	if d.NextRun != 0 || d.CronSpec != "" {
		t.Fatalf("not disabled: %+v", d)
	}
}

func TestDocNextRun(t *testing.T) {
	at := time.Date(2026, 10, 26, 0, 30, 0, 0, time.UTC)
	next, err := docNextRun("CRON_TZ=Europe/Istanbul 0 3 * * *", at)
	want := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC).UnixMilli()
	if err != nil || next != want {
		t.Fatalf("next = %d, %v; want %d", next, err, want)
	}
	for _, spec := range []string{"@every 1s", "CRON_TZ=bad-zone 0 3 * * *", "never", "0 0 31 2 *"} {
		if _, err := docNextRun(spec, at); err == nil {
			t.Errorf("accepted %s", spec)
		}
	}
}

func TestDocIdentityIsolation(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")
	k := docKeyFor(t, env)
	c, _ := env.st.GetConnection(ctx, k.ConnectionID)
	c.UserCredentials = true
	c.Access = append(c.Access, store.Access{SubjectType: store.SubjectUser, SubjectID: env.bob.ID})
	if err := env.st.UpdateConnection(ctx, c); err != nil {
		t.Fatal(err)
	}
	for _, u := range []*store.User{env.alice, env.bob} {
		enc, err := env.srv.cipher.Encrypt("db-password")
		if err != nil {
			t.Fatal(err)
		}
		if err := env.st.SetCredential(ctx, &store.Credential{ConnectionID: c.ID, UserID: u.ID, Username: u.Username, PasswordEnc: enc}); err != nil {
			t.Fatal(err)
		}
	}
	// Concurrent first readers (including different server objects) join
	// one persisted generation rather than overwriting one another.
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			s := &Server{store: env.st, engine: env.srv.engine, cipher: env.srv.cipher}
			if _, err := s.documentation(ctx, k, false); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	alice, _ := env.st.GetDoc(ctx, k)
	bobKey := k
	bobKey.UserID = env.bob.ID
	if _, err := env.st.GetDoc(ctx, bobKey); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("alice's first read created a shared-user snapshot")
	}
	if _, err := env.srv.documentation(ctx, bobKey, false); err != nil {
		t.Fatal(err)
	}
	bob, _ := env.st.GetDoc(ctx, bobKey)
	if bob.SourceVersion == alice.SourceVersion {
		t.Fatal("different DB identities use the same source fingerprint")
	}
	// Changing the stored identity invalidates even a successful snapshot.
	enc, _ := env.srv.cipher.Encrypt("other-db-password")
	if err := env.st.SetCredential(ctx, &store.Credential{ConnectionID: c.ID, UserID: env.alice.ID, Username: "new-account", PasswordEnc: enc}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.srv.documentation(ctx, k, false); err != nil {
		t.Fatal(err)
	}
	changed, _ := env.st.GetDoc(ctx, k)
	if changed.SourceVersion == alice.SourceVersion {
		t.Fatal("credential change served old snapshot")
	}
	if err := env.st.DeleteCredential(ctx, c.ID, env.alice.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.srv.documentation(ctx, k, false); err == nil {
		t.Fatal("deleted credentials can still read cached metadata")
	}
	env.bob.Disabled = true
	if err := env.st.UpdateUser(ctx, env.bob); err != nil {
		t.Fatal(err)
	}
	if _, err := env.srv.documentation(ctx, bobKey, false); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("disabled user's cache is accessible: %v", err)
	}
}
