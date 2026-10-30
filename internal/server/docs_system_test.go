package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/dataport/internal/auth"
	"github.com/rakunlabs/dataport/internal/engine"
	"github.com/rakunlabs/dataport/internal/store"
)

func TestDocSchemaIncluded(t *testing.T) {
	dc := &store.DocConnection{Exclude: []string{"tmp_*", "Audit"}}
	for schema, want := range map[string]bool{"public": true, "tmp_1": false, "audit": false, "AUDIT": false, "sales": true} {
		if got := docSchemaIncluded(dc, schema); got != want {
			t.Errorf("exclude %s: %v", schema, got)
		}
	}
	dc = &store.DocConnection{Include: []string{"sales*"}, Exclude: []string{"sales_old"}}
	for schema, want := range map[string]bool{"sales": true, "sales_eu": true, "sales_old": false, "public": false} {
		if got := docSchemaIncluded(dc, schema); got != want {
			t.Errorf("include %s: %v", schema, got)
		}
	}
}

// An admin documents a connection for everyone: every schema it chooses is
// built at once by the server, and users read it without building it and
// without a database account of their own.
func TestConnectionDocs(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")
	s := env.srv
	k := docKeyFor(t, env)

	c, _ := env.st.GetConnection(ctx, k.ConnectionID)
	c.Access = append(c.Access, store.Access{SubjectType: store.SubjectUser, SubjectID: env.bob.ID})
	if err := env.st.UpdateConnection(ctx, c); err != nil {
		t.Fatal(err)
	}

	// Only admins build; bob only reads.
	if err := s.saveFeatureAccess(ctx, docAccessSetting, FeatureAccess{Restricted: true}); err != nil {
		t.Fatal(err)
	}
	bobKey := store.DocKey{ConnectionID: c.ID, UserID: env.bob.ID, Schema: "main"}
	if _, err := s.documentation(ctx, bobKey, false); !errors.Is(err, errDocsReadOnly) {
		t.Fatalf("before: %v", err)
	}

	dc := &store.DocConnection{ConnectionID: c.ID, Enabled: true, Exclude: []string{"temp*"}}
	if err := env.st.SaveDocConnection(ctx, dc); err != nil {
		t.Fatal(err)
	}
	// Not built yet: a reader still cannot start it.
	if _, err := s.documentation(ctx, bobKey, false); !errors.Is(err, errDocsReadOnly) {
		t.Fatalf("unbuilt: %v", err)
	}

	s.buildConnectionDocs(ctx, dc, nil)

	got, err := s.documentation(ctx, bobKey, false)
	if err != nil || !got.Status.ReadOnly || len(got.Document.Tables) != 2 {
		t.Fatalf("shared read: %v %+v", err, got)
	}
	if _, err := s.documentation(ctx, bobKey, true); !errors.Is(err, errDocsBuildOff) {
		t.Fatalf("reader refresh: %v", err)
	}
	if _, err := env.st.GetDoc(ctx, bobKey); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("a reader got a snapshot of their own")
	}

	// It is listed for bob, who never opened it.
	if d := listedDocs(t, s, env.bob); len(d) != 1 || !d[0].Ready || !d[0].System {
		t.Fatalf("listed: %+v", d)
	}

	// An own-account connection is read by the documentation account; users
	// need no account of their own for it.
	c.UserCredentials = true
	if err := env.st.UpdateConnection(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err := s.documentation(ctx, bobKey, false); !errors.Is(err, errDocsReadOnly) {
		t.Fatalf("account needed: %v", err)
	}
	enc, _ := s.cipher.Encrypt("doc-pw")
	dc.Username, dc.PasswordEnc = "doc_reader", enc
	if err := env.st.SaveDocConnection(ctx, dc); err != nil {
		t.Fatal(err)
	}
	// The account changed, so the old document no longer counts.
	if _, err := s.documentation(ctx, bobKey, false); !errors.Is(err, errDocsReadOnly) {
		t.Fatalf("stale document served: %v", err)
	}
	s.buildConnectionDocs(ctx, dc, nil)
	if _, err := env.st.GetCredential(ctx, c.ID, env.bob.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("bob has an account")
	}
	if got, err := s.documentation(ctx, bobKey, false); err != nil || len(got.Document.Tables) != 2 {
		t.Fatalf("own-account read: %v %+v", err, got)
	}

	// Access still decides who reads it.
	c.Access = c.Access[:1]
	if err := env.st.UpdateConnection(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err := s.documentation(ctx, bobKey, false); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoked: %v", err)
	}

	// Excluded schemas fall back to users' own documentation.
	dc.Exclude = []string{"main"}
	if err := env.st.SaveDocConnection(ctx, dc); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.systemDocumentation(ctx, env.alice, k, false, true); ok {
		t.Fatal("excluded schema served from the connection's documentation")
	}
	if _, err := s.discoverConnectionDocs(ctx, dc); err != nil {
		t.Fatal(err)
	}
	if list, _ := env.st.ConnectionDocs(ctx, c.ID, store.DocSystemUser); len(list) != 0 {
		t.Fatalf("excluded snapshot kept: %+v", list)
	}

	// Turned off, its snapshots go.
	dc.Exclude, dc.Enabled = nil, false
	if err := env.st.SaveDocConnection(ctx, dc); err != nil {
		t.Fatal(err)
	}
	if _, err := env.st.GetDoc(ctx, store.DocKey{ConnectionID: c.ID, UserID: store.DocSystemUser, Schema: "main"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("snapshot of a connection turned off kept")
	}
}

// The schedule lists the schemas of a documented connection and builds those
// nobody opened; users' own copies of them are left alone, and the run still
// ends.
func TestConnectionDocsSchedule(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")
	s := env.srv
	k := docKeyFor(t, env)

	// alice's own snapshot exists before the admin turns documentation on.
	if _, err := s.documentation(ctx, k, false); err != nil {
		t.Fatal(err)
	}
	dc := &store.DocConnection{ConnectionID: k.ConnectionID, Enabled: true}
	if err := env.st.SaveDocConnection(ctx, dc); err != nil {
		t.Fatal(err)
	}
	if err := env.st.SetSetting(ctx, docScheduleSetting, `{"enabled":true,"cron_spec":"@every 1h"}`); err != nil {
		t.Fatal(err)
	}
	if err := s.dispatchDocsAt(ctx, time.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	sys, err := env.st.GetDoc(ctx, store.DocKey{ConnectionID: k.ConnectionID, UserID: store.DocSystemUser, Schema: "main"})
	if err != nil || sys.Payload == "" {
		t.Fatalf("schedule did not build it: %v %+v", err, sys)
	}
	run := s.docRunStatus(ctx)
	if run.Running || run.Done != run.Total || run.Total != 1 {
		t.Fatalf("run: %+v", run)
	}
	// alice now reads the connection's documentation.
	got, err := s.documentation(ctx, k, false)
	if err != nil || !got.Status.System || got.Document.GeneratedAt != sys.GeneratedAt {
		t.Fatalf("alice: %v %+v", err, got.Status)
	}
}

// Schemas an admin closes to documentation are built and read by nobody,
// also while the connection's documentation for everyone is off, and their
// saved snapshots go.
func TestBlockedDocs(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")
	s := env.srv
	k := docKeyFor(t, env)

	if _, err := s.documentation(ctx, k, false); err != nil {
		t.Fatal(err)
	}

	dc := &store.DocConnection{ConnectionID: k.ConnectionID, Exclude: []string{"MAIN"}, BlockExcluded: true}
	if err := env.st.SaveDocConnection(ctx, dc); err != nil {
		t.Fatal(err)
	}
	if err := s.dropBlockedDocs(ctx, dc); err != nil {
		t.Fatal(err)
	}
	if _, err := env.st.GetDoc(ctx, k); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("snapshot of a closed schema kept")
	}
	for _, force := range []bool{false, true} {
		if _, err := s.documentation(ctx, k, force); !errors.Is(err, errDocsBlocked) {
			t.Fatalf("force %v: %v", force, err)
		}
	}
	if _, err := env.st.GetDoc(ctx, k); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("a closed schema was built")
	}
	if d := listedDocs(t, s, env.alice); len(d) != 0 {
		t.Fatalf("listed: %+v", d)
	}

	// Without blocking, an excluded schema is the user's to document.
	dc.BlockExcluded = false
	if err := env.st.SaveDocConnection(ctx, dc); err != nil {
		t.Fatal(err)
	}
	if _, err := s.documentation(ctx, k, false); err != nil {
		t.Fatal(err)
	}

	// Closing it again removes it on the next scheduled refresh too, and the
	// run counts it as skipped, not failed.
	dc.BlockExcluded = true
	if err := env.st.SaveDocConnection(ctx, dc); err != nil {
		t.Fatal(err)
	}
	if err := env.st.SetSetting(ctx, docScheduleSetting, `{"enabled":true,"cron_spec":"@every 1h"}`); err != nil {
		t.Fatal(err)
	}
	if err := s.dispatchDocsAt(ctx, time.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := env.st.GetDoc(ctx, k); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("schedule kept a closed schema")
	}
	if run := s.docRunStatus(ctx); run.Failed != 0 || run.Status == store.DocRunFailed || run.Done != run.Total {
		t.Fatalf("run: %+v", run)
	}
}

// The whole flow as an admin and a user see it: the admin picks the schemas
// of a connection and its documentation account, presses Refresh all, the
// documentation is built and pushed, and a user who may not build reads it
// without a database account of their own.
func TestDocsEndToEnd(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")
	hash, _ := auth.HashPassword("x-password")
	if err := env.st.CreateUser(ctx, &store.User{Username: "admin", PasswordHash: hash, Role: store.RoleAdmin}); err != nil {
		t.Fatal(err)
	}
	k := docKeyFor(t, env)

	// bob may open the connection, signs in with his own account there and
	// never saved one; building documentation is for admins only.
	c, _ := env.st.GetConnection(ctx, k.ConnectionID)
	c.Access = append(c.Access, store.Access{SubjectType: store.SubjectUser, SubjectID: env.bob.ID})
	c.UserCredentials = true
	if err := env.st.UpdateConnection(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := env.srv.saveFeatureAccess(ctx, docAccessSetting, FeatureAccess{Restricted: true}); err != nil {
		t.Fatal(err)
	}

	fake := &fakePushTarget{items: map[string]pushItem{}}
	ks := httptest.NewServer(fake)
	t.Cleanup(ks.Close)

	login := func(user string) []*http.Cookie {
		resp, err := http.Post(env.httpSrv.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"`+user+`","password":"x-password"}`))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.Cookies()
	}
	request := func(cookies []*http.Cookie, method, path, body string, want int) []byte {
		t.Helper()
		req, _ := http.NewRequest(method, env.httpSrv.URL+"/api/v1"+path, strings.NewReader(body))
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
			t.Fatalf("%s %s: %d %s", method, path, res.StatusCode, raw)
		}
		return raw
	}
	admin, bob := login("admin"), login("bob")

	// The push is set up with no schemas listed: it follows the choice.
	request(admin, "PUT", "/admin/docs/export", `{"enabled":true,"url":"`+ks.URL+`","collection":"docs","targets":[{"connection_id":"`+k.ConnectionID+`","user_id":"`+env.alice.ID+`","schemas":[]}]}`, http.StatusOK)

	// The admin turns documentation on with a documentation account; this
	// connection's only schema is chosen.
	request(admin, "PUT", "/admin/docs/connections/"+k.ConnectionID, `{"enabled":true,"exclude":["tmp_*"],"username":"doc_reader","password":"pw"}`, http.StatusOK)

	// Waits until the push target holds n items and nothing runs.
	settled := func(what string, done func() bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			fake.mu.Lock()
			ok := done()
			fake.mu.Unlock()
			env.srv.docExportMu.Lock()
			pushing := env.srv.docExportRun.Running
			env.srv.docExportMu.Unlock()
			if ok && !pushing && !env.srv.docRunStatus(ctx).Running {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: %d items, %+v", what, len(fake.items), env.srv.docExportStatusNow(ctx))
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	// Saving builds and pushes it already.
	settled("pushed after saving", func() bool { return len(fake.items) == 3 })

	// Refresh all builds it again and pushes; nothing changed, so the
	// target is not contacted.
	fake.mu.Lock()
	requests := fake.requests
	fake.mu.Unlock()
	before := env.srv.docExportStatusNow(ctx).StartedAt
	request(admin, "POST", "/admin/docs/run", "", http.StatusOK)
	settled("refresh all", func() bool { return env.srv.docExportStatusNow(ctx).StartedAt > before })
	if st := env.srv.docExportStatusNow(ctx); !st.Unchanged || fake.requests != requests || st.Tables != 3 {
		t.Fatalf("refresh all: %+v (%d requests)", st, fake.requests-requests)
	}

	// bob lists and reads it without building and without an account.
	var list struct {
		Docs       []store.DocInfo `json:"docs"`
		Documented []string        `json:"documented"`
	}
	_ = json.Unmarshal(request(bob, "GET", "/docs", "", http.StatusOK), &list)
	if len(list.Docs) != 1 || !list.Docs[0].Ready || len(list.Documented) != 1 {
		t.Fatalf("bob's list: %+v", list)
	}
	var doc struct {
		Document engine.SchemaDoc `json:"document"`
		Status   docStatus        `json:"status"`
	}
	_ = json.Unmarshal(request(bob, "GET", "/connections/"+k.ConnectionID+"/docs?schema=main", "", http.StatusOK), &doc)
	if len(doc.Document.Tables) != 2 || !doc.Status.ReadOnly {
		t.Fatalf("bob's read: %+v", doc.Status)
	}
	request(bob, "POST", "/connections/"+k.ConnectionID+"/docs/refresh?schema=main", "", http.StatusForbidden)
	if _, err := env.st.GetCredential(ctx, k.ConnectionID, env.bob.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("bob was asked for an account")
	}

	// Turned off, the push falls back to the push user's own access. alice
	// has no account on this connection, so the push fails and, as any
	// failed push, removes nothing; the settings show why.
	request(admin, "PUT", "/admin/docs/connections/"+k.ConnectionID, `{"enabled":false,"username":"doc_reader"}`, http.StatusOK)
	settled("off", func() bool { return len(env.srv.docExportStatusNow(ctx).Failed) == 1 })
	if len(fake.items) != 3 || !strings.Contains(env.srv.docExportStatusNow(ctx).Failed[0], "has not saved their database account") {
		t.Fatalf("off: %d %+v", len(fake.items), env.srv.docExportStatusNow(ctx))
	}

	// A save while a refresh holds the lock is built after it, not lost.
	fake.mu.Lock()
	fake.items = map[string]pushItem{}
	fake.mu.Unlock()
	env.srv.docDispatch.Lock()
	request(admin, "PUT", "/admin/docs/connections/"+k.ConnectionID, `{"enabled":true,"username":"doc_reader"}`, http.StatusOK)
	time.Sleep(100 * time.Millisecond)
	env.srv.docDispatch.Unlock()
	settled("built after a running refresh", func() bool { return len(fake.items) == 3 })
}

func TestDocSchemaLimitReported(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")
	k := docKeyFor(t, env)
	dc := &store.DocConnection{ConnectionID: k.ConnectionID, Enabled: true}
	if err := env.st.SaveDocConnection(ctx, dc); err != nil {
		t.Fatal(err)
	}
	if _, err := env.srv.discoverConnectionDocs(ctx, dc); err != nil {
		t.Fatal(err)
	}
	if got, _ := env.st.GetDocConnection(ctx, k.ConnectionID); got.LastError != "" {
		t.Fatalf("within the limit: %q", got.LastError)
	}
}

func listedDocs(t *testing.T, s *Server, u *store.User) []store.DocInfo {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, u))
	rec := httptest.NewRecorder()
	s.listDocs(rec, req)
	var out struct {
		Docs []store.DocInfo `json:"docs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	return out.Docs
}

// The admin endpoints: save, preview the schemas, and the build that saving
// starts. The password is never sent back.
func TestConnectionDocsHTTP(t *testing.T) {
	env := newMCPEnv(t, "")
	hash, _ := auth.HashPassword("x-password")
	if err := env.st.CreateUser(context.Background(), &store.User{Username: "admin", PasswordHash: hash, Role: store.RoleAdmin}); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(env.httpSrv.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"admin","password":"x-password"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	cookies := resp.Cookies()
	request := func(method, path, body string, want int) []byte {
		t.Helper()
		req, _ := http.NewRequest(method, env.httpSrv.URL+"/api/v1"+path, strings.NewReader(body))
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
			t.Fatalf("%s %s: %d %s", method, path, res.StatusCode, raw)
		}
		return raw
	}

	k := docKeyFor(t, env)
	var list []docConnectionView
	raw := request("GET", "/admin/docs/connections", "", http.StatusOK)
	if err := json.Unmarshal(raw, &list); err != nil || len(list) != 3 {
		t.Fatalf("list: %v %s", err, raw)
	}

	var preview struct {
		Schemas []struct {
			Name     string `json:"name"`
			Included bool   `json:"included"`
		} `json:"schemas"`
	}
	_ = json.Unmarshal(request("GET", "/admin/docs/connections/"+k.ConnectionID+"/schemas?exclude=main", "", http.StatusOK), &preview)
	if len(preview.Schemas) == 0 || preview.Schemas[0].Name != "main" || preview.Schemas[0].Included {
		t.Fatalf("preview: %+v", preview)
	}
	request("GET", "/admin/docs/connections/"+k.ConnectionID+"/schemas?exclude=[", "", http.StatusBadRequest)

	raw = request("PUT", "/admin/docs/connections/"+k.ConnectionID, `{"enabled":true,"exclude":["tmp_*"," "],"username":"reader","password":"secret"}`, http.StatusOK)
	if strings.Contains(string(raw), "secret") {
		t.Fatalf("password sent back: %s", raw)
	}
	var v docConnectionView
	_ = json.Unmarshal(raw, &v)
	if !v.Enabled || !v.HasPassword || v.Username != "reader" || len(v.Exclude) != 1 {
		t.Fatalf("saved: %+v", v)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		_ = json.Unmarshal(request("GET", "/admin/docs/connections", "", http.StatusOK), &list)
		var got docConnectionView
		for _, x := range list {
			if x.ConnectionID == k.ConnectionID {
				got = x
			}
		}
		if got.Ready == 1 && got.Schemas == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not built: %+v", got)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Without a username the connection's own account is used and the
	// saved password goes.
	_ = json.Unmarshal(request("PUT", "/admin/docs/connections/"+k.ConnectionID, `{"enabled":false}`, http.StatusOK), &v)
	if v.Enabled || v.HasPassword || v.Schemas != 0 {
		t.Fatalf("off: %+v", v)
	}
	request("POST", "/admin/docs/connections/"+k.ConnectionID+"/run", "", http.StatusBadRequest)
}
