package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rakunlabs/dataport/internal/auth"
	"github.com/rakunlabs/dataport/internal/notebook"
	"github.com/rakunlabs/dataport/internal/store"
)

func TestNotebookAccessByGroup(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")
	st, srv := env.st, env.srv

	grants, _ := st.ListGrants(ctx, env.alice)

	var shop string

	for _, g := range grants {
		if g.Connection.Name == "shop" {
			shop = g.Connection.ID
		}
	}

	n := &store.Notebook{OwnerID: env.alice.ID, Name: "Read", Path: "read", Document: notebook.Document{Cells: []notebook.Cell{
		{ID: "a", Path: "a", ConnectionID: shop, SQL: "SELECT 1", Enabled: true, Output: true},
	}}}
	if err := st.SaveNotebook(ctx, n); err != nil {
		t.Fatal(err)
	}

	if err := st.PublishNotebook(ctx, n.ID, n.Version, true); err != nil {
		t.Fatal(err)
	}

	session, err := srv.auth.CreateSession(ctx, env.alice.ID, "", "", false)
	if err != nil {
		t.Fatal(err)
	}

	raw, hash, _ := auth.NewAccessToken()
	if err := st.CreateAccessToken(ctx, &store.AccessToken{UserID: env.alice.ID, Name: "t", TokenHash: hash}); err != nil {
		t.Fatal(err)
	}

	list := func() int {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/notebooks", nil)
		r.AddCookie(&http.Cookie{Name: cookieName, Value: session})
		w := httptest.NewRecorder()
		srv.mux.ServeHTTP(w, r)

		return w.Code
	}

	published := func() int {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/run/read/a", nil)
		r.Header.Set("Authorization", "Bearer "+raw)
		w := httptest.NewRecorder()
		srv.mux.ServeHTTP(w, r)

		return w.Code
	}

	tools := func() []string {
		client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)

		s, err := client.Connect(ctx, &mcp.StreamableClientTransport{
			Endpoint:   env.httpSrv.URL + "/mcp",
			HTTPClient: &http.Client{Transport: bearer{token: raw, next: http.DefaultTransport}},
			MaxRetries: -1,
		}, nil)
		if err != nil {
			t.Fatal(err)
		}

		defer func() { _ = s.Close() }()

		res, err := s.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}

		var names []string

		for _, tool := range res.Tools {
			if strings.Contains(tool.Name, "notebook") {
				names = append(names, tool.Name)
			}
		}

		return names
	}

	setAccess := func(a string) {
		r := httptest.NewRequest(http.MethodPut, "/admin/notebook-access", strings.NewReader(a))
		w := httptest.NewRecorder()
		srv.adminSetNotebookAccess(w, r)

		if w.Code != http.StatusOK {
			t.Fatal(w.Code, w.Body.String())
		}
	}

	// Open to everyone by default.
	if c := list(); c != http.StatusOK {
		t.Fatalf("list: %d", c)
	}

	if c := published(); c != http.StatusOK {
		t.Fatalf("published: %d", c)
	}

	if len(tools()) == 0 {
		t.Fatal("no notebook tools")
	}

	team := &store.Group{Name: "analysts"}
	if err := st.CreateGroup(ctx, team); err != nil {
		t.Fatal(err)
	}

	// Restricted to a group alice is not in.
	setAccess(`{"restricted": true, "group_ids": ["` + team.ID + `"]}`)

	if c := list(); c != http.StatusForbidden {
		t.Fatalf("list while restricted: %d", c)
	}

	if c := published(); c != http.StatusForbidden {
		t.Fatalf("published while restricted: %d", c)
	}

	if names := tools(); len(names) != 0 {
		t.Fatalf("notebook tools while restricted: %v", names)
	}

	alice, _ := st.GetUser(ctx, env.alice.ID)
	if srv.toMe(ctx, alice).CanUseNotebooks {
		t.Fatal("me reports notebooks open")
	}

	// In the group, alice may use them again.
	team.MemberIDs = []string{env.alice.ID}
	if err := st.UpdateGroup(ctx, team); err != nil {
		t.Fatal(err)
	}

	if c := list(); c != http.StatusOK {
		t.Fatalf("list as a member: %d", c)
	}

	if c := published(); c != http.StatusOK {
		t.Fatalf("published as a member: %d", c)
	}

	// Unknown groups are refused.
	r := httptest.NewRequest(http.MethodPut, "/admin/notebook-access", strings.NewReader(`{"restricted": true, "group_ids": ["nope"]}`))
	w := httptest.NewRecorder()
	srv.adminSetNotebookAccess(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown group: %d", w.Code)
	}
}
