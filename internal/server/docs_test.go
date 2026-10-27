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

	"github.com/rakunlabs/dataport/internal/auth"
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
	// The schedule refreshes snapshots not attempted since its last run,
	// once, and clears errors.
	if err := env.st.SetSetting(ctx, docScheduleSetting, `{"enabled":true,"cron_spec":"@every 1h"}`); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Hour)
	run("CREATE TABLE scheduled (id INTEGER PRIMARY KEY)")
	if err := other.dispatchDocsAt(ctx, later); err != nil {
		t.Fatal(err)
	}
	d, _ := env.st.GetDoc(ctx, k)
	if d.LastError != "" {
		t.Fatalf("scheduled refresh failed: %+v", d)
	}
	// Attempted after the last occurrence, so another dispatcher skips it.
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
	if err := s.dispatchDocsAt(ctx, later.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	after, _ = env.st.GetDoc(ctx, k)
	if after.GeneratedAt != d.GeneratedAt || after.LastError == "" {
		t.Fatal("revoked user's job ran with an old grant")
	}
}

func TestDocScheduleHTTP(t *testing.T) {
	env := newMCPEnv(t, "/dp")
	hash, _ := auth.HashPassword("x-password")
	if err := env.st.CreateUser(context.Background(), &store.User{Username: "admin", PasswordHash: hash, Role: store.RoleAdmin}); err != nil {
		t.Fatal(err)
	}
	login := func(user string) []*http.Cookie {
		resp, err := http.Post(env.httpSrv.URL+"/dp/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"`+user+`","password":"x-password"}`))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.Cookies()
	}
	request := func(cookies []*http.Cookie, method, url, body string, want int) []byte {
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
	url := env.httpSrv.URL + "/dp/api/v1/admin/docs/schedule"
	alice := func() []*http.Cookie { return login("alice") }
	request(alice(), "GET", url, "", http.StatusForbidden)
	admin := login("admin")
	var v struct {
		Enabled  bool   `json:"enabled"`
		CronSpec string `json:"cron_spec"`
		NextRun  int64  `json:"next_run"`
		Run      docRun `json:"run"`
	}
	_ = json.Unmarshal(request(admin, "GET", url, "", http.StatusOK), &v)
	if !v.Enabled || v.CronSpec != docScheduleDefault || v.NextRun <= time.Now().UnixMilli() {
		t.Fatalf("default schedule: %+v", v)
	}
	_ = json.Unmarshal(request(admin, "PUT", url, `{"enabled":true,"cron_spec":"CRON_TZ=Europe/Istanbul 0 3 * * *"}`, http.StatusOK), &v)
	if !v.Enabled || v.CronSpec != "CRON_TZ=Europe/Istanbul 0 3 * * *" || v.NextRun <= time.Now().UnixMilli() {
		t.Fatalf("saved schedule: %+v", v)
	}
	for _, spec := range []string{"not cron", "@every 1s", "CRON_TZ=bad-zone 0 3 * * *", "0 0 31 2 *", ""} {
		request(admin, "PUT", url, `{"enabled":true,"cron_spec":"`+spec+`"}`, http.StatusBadRequest)
	}
	// Turning it off keeps the expression for later.
	_ = json.Unmarshal(request(admin, "PUT", url, `{"enabled":false,"cron_spec":"@hourly"}`, http.StatusOK), &v)
	if v.Enabled || v.CronSpec != "@hourly" || v.NextRun != 0 {
		t.Fatalf("not disabled: %+v", v)
	}

	// Run now refreshes every saved snapshot in the background, even while
	// the schedule is off, and reports its progress.
	k := docKeyFor(t, env)
	if _, err := env.srv.documentation(context.Background(), k, false); err != nil {
		t.Fatal(err)
	}
	before, _ := env.st.GetDoc(context.Background(), k)
	runURL := env.httpSrv.URL + "/dp/api/v1/admin/docs/run"
	request(alice(), "POST", runURL, "", http.StatusForbidden)
	request(admin, "POST", runURL, "", http.StatusOK)
	deadline := time.Now().Add(10 * time.Second)
	for {
		_ = json.Unmarshal(request(admin, "GET", url, "", http.StatusOK), &v)
		if !v.Run.Running && v.Run.FinishedAt > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not finish: %+v", v.Run)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !v.Run.Manual || v.Run.Total != 1 || v.Run.Done != 1 || v.Run.Failed != 0 || v.Run.Status != store.DocRunDone || v.Run.Username != "admin" {
		t.Fatalf("run status: %+v", v.Run)
	}
	after, _ := env.st.GetDoc(context.Background(), k)
	if after.AttemptedAt <= before.AttemptedAt || after.Payload == "" {
		t.Fatalf("snapshot not refreshed: %+v", after)
	}

	// The run is recorded, so the page lists it, also after a restart, with
	// every saved snapshot.
	var page struct {
		History   []store.DocRun  `json:"history"`
		Snapshots []store.DocInfo `json:"snapshots"`
	}
	_ = json.Unmarshal(request(admin, "GET", url, "", http.StatusOK), &page)
	if len(page.History) != 1 || page.History[0].Status != store.DocRunDone || page.History[0].Username != "admin" ||
		len(page.Snapshots) != 1 || !page.Snapshots[0].Ready || page.Snapshots[0].Username != "alice" {
		t.Fatalf("history: %+v snapshots: %+v", page.History, page.Snapshots)
	}
	env.srv.docRunMu.Lock()
	env.srv.docRun = docRun{}
	env.srv.docRunMu.Unlock()
	_ = json.Unmarshal(request(admin, "GET", url, "", http.StatusOK), &v)
	if v.Run.Status != store.DocRunDone || v.Run.Done != 1 {
		t.Fatalf("status after restart: %+v", v.Run)
	}

	// Users list their own documentation.
	var mine struct {
		Docs []store.DocInfo `json:"docs"`
	}
	_ = json.Unmarshal(request(alice(), "GET", env.httpSrv.URL+"/dp/api/v1/docs", "", http.StatusOK), &mine)
	if len(mine.Docs) != 1 || mine.Docs[0].Schema != k.Schema || mine.Docs[0].Username != "" {
		t.Fatalf("own docs: %+v", mine.Docs)
	}
	_ = json.Unmarshal(request(admin, "GET", env.httpSrv.URL+"/dp/api/v1/docs", "", http.StatusOK), &mine)
	if len(mine.Docs) != 0 {
		t.Fatalf("admin sees alice's docs: %+v", mine.Docs)
	}
}

func TestDocRunKeepsOldDocument(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")
	k := docKeyFor(t, env)
	s := env.srv
	first, err := s.documentation(ctx, k, false)
	if err != nil {
		t.Fatal(err)
	}
	// While a refresh holds the lease, readers keep getting the old document.
	if ok, err := env.st.ClaimDoc(ctx, k, "running", time.Now().Add(time.Minute).UnixMilli(), 0); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	during, err := s.documentation(ctx, k, false)
	if err != nil || during.Document.GeneratedAt != first.Document.GeneratedAt || !during.Status.Refreshing {
		t.Fatalf("old document not served during refresh: %v %+v", err, during)
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
