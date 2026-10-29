package server

import (
	"context"
	"errors"
	"testing"

	"github.com/rakunlabs/dataport/internal/store"
)

// Users outside the groups that may build documentation read what was
// built with the same database account, and cannot build or refresh it.
func TestDocBuildAccess(t *testing.T) {
	ctx := context.Background()
	env := newMCPEnv(t, "")
	s := env.srv
	k := docKeyFor(t, env)

	// bob shares alice's connection and its shared database account.
	c, _ := env.st.GetConnection(ctx, k.ConnectionID)
	c.Access = append(c.Access, store.Access{SubjectType: store.SubjectUser, SubjectID: env.bob.ID})
	if err := env.st.UpdateConnection(ctx, c); err != nil {
		t.Fatal(err)
	}
	bobKey := k
	bobKey.UserID = env.bob.ID

	builders := &store.Group{Name: "builders", MemberIDs: []string{env.alice.ID}}
	if err := env.st.CreateGroup(ctx, builders); err != nil {
		t.Fatal(err)
	}
	if err := s.saveFeatureAccess(ctx, docAccessSetting, FeatureAccess{Restricted: true, GroupIDs: []string{builders.ID}}); err != nil {
		t.Fatal(err)
	}
	bob, _ := env.st.GetUser(ctx, env.bob.ID)
	alice, _ := env.st.GetUser(ctx, env.alice.ID)
	if s.docsBuildAllowed(ctx, bob) || !s.docsBuildAllowed(ctx, alice) {
		t.Fatal("build access by group")
	}

	// Nothing built yet: bob cannot start a scan.
	if _, err := s.documentation(ctx, bobKey, false); !errors.Is(err, errDocsReadOnly) {
		t.Fatalf("unbuilt: %v", err)
	}
	if _, err := env.st.GetDoc(ctx, bobKey); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("a read-only user created a snapshot")
	}

	// Once alice built it, bob reads her snapshot read-only.
	built, err := s.documentation(ctx, k, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.documentation(ctx, bobKey, false)
	if err != nil || !got.Status.ReadOnly || got.Document.GeneratedAt != built.Document.GeneratedAt {
		t.Fatalf("shared read: %v %+v", err, got)
	}
	if _, err := s.documentation(ctx, bobKey, true); !errors.Is(err, errDocsBuildOff) {
		t.Fatalf("refresh: %v", err)
	}

	// An own-account connection has a database identity per user, so
	// alice's snapshot is not bob's.
	c.UserCredentials = true
	if err := env.st.UpdateConnection(ctx, c); err != nil {
		t.Fatal(err)
	}
	for _, u := range []*store.User{env.alice, env.bob} {
		enc, _ := s.cipher.Encrypt("pw")
		if err := env.st.SetCredential(ctx, &store.Credential{ConnectionID: c.ID, UserID: u.ID, Username: u.Username, PasswordEnc: enc}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.documentation(ctx, k, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.documentation(ctx, bobKey, false); !errors.Is(err, errDocsReadOnly) {
		t.Fatalf("own account leaked another user's snapshot: %v", err)
	}

	// Group features follow the group's settings and its deletion.
	if err := s.setFeatureGroup(ctx, notebookAccessSetting, builders.ID, true); err != nil {
		t.Fatal(err)
	}
	if a := s.notebookAccess(ctx); len(a.GroupIDs) != 1 {
		t.Fatalf("notebook groups: %+v", a)
	}
	if err := s.setFeatureGroup(ctx, docAccessSetting, builders.ID, false); err != nil {
		t.Fatal(err)
	}
	if s.docsBuildAllowed(ctx, alice) {
		t.Fatal("removed group still builds")
	}
}
