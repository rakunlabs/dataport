package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/rakunlabs/dataport/internal/store"
)

// A connection that signs every user in with its one database account
// shows them all the same catalog, so its documentation is kept once per
// schema under store.DocSharedUser: whoever builds or refreshes it updates
// the copy everyone reads. Reading still needs access to the connection.
// Connections where users sign in with their own accounts keep a snapshot
// per user, as accounts may see different tables.

// sharedDocs tells whether the users of a connection share its snapshots.
func sharedDocs(c *store.Connection) bool {
	return !c.UserCredentials && c.OwnerID == "" && !c.NotebookOnly
}

// sharedDocAccess is docAccess for the shared snapshots of a connection.
// Its version is the one docAccess gives every user of the connection.
func (s *Server) sharedDocAccess(ctx context.Context, connectionID string) (*store.Grant, string, string, error) {
	c, err := s.store.GetConnection(ctx, connectionID)
	if err != nil {
		return nil, "", "", err
	}

	if !sharedDocs(c) {
		return nil, "", "", store.ErrNotFound
	}

	password, err := s.cipher.Decrypt(c.PasswordEnc)
	if err != nil {
		slog.Error("decrypt connection password", "connection", c.ID, "error", err)

		return nil, "", "", errSecret
	}

	connection := *c
	connection.Access = nil

	b, err := json.Marshal(struct {
		Connection *store.Connection
		Secret     string
		Credential *store.Credential
	}{&connection, c.PasswordEnc, nil})
	if err != nil {
		return nil, "", "", err
	}

	h := sha256.Sum256(b)

	return &store.Grant{Connection: &connection, ReadOnly: true}, password, hex.EncodeToString(h[:]), nil
}

// sharedAccountDocumentation serves a schema of a connection whose users
// share its snapshots. ok is false for any other connection, and the
// caller falls back to the user's own snapshots.
func (s *Server) sharedAccountDocumentation(ctx context.Context, u *store.User, k store.DocKey, force, canBuild bool) (*docResponse, bool, error) {
	g, err := s.store.GetGrant(ctx, u, k.ConnectionID)
	if err != nil {
		return nil, true, err
	}

	if g.Connection.NotebookOnly {
		return nil, true, errNotebookOnly
	}

	if !sharedDocs(g.Connection) {
		return nil, false, nil
	}

	if force && !canBuild {
		return nil, true, errDocsBuildOff
	}

	sk := store.DocKey{ConnectionID: k.ConnectionID, UserID: store.DocSharedUser, Schema: k.Schema}

	// Copies users built before the connection shared its snapshots, or
	// while it signed them in with their own accounts, become the shared
	// one instead of a new catalog scan.
	if d, err := s.store.GetDoc(ctx, sk); errors.Is(err, store.ErrNotFound) || (err == nil && d.Payload == "") {
		if err := s.store.AdoptSharedDocs(ctx, k.ConnectionID); err != nil {
			return nil, true, err
		}
	}

	if !canBuild {
		_, _, version, err := s.sharedDocAccess(ctx, k.ConnectionID)
		if err != nil {
			return nil, true, err
		}

		d, err := s.store.GetDoc(ctx, sk)
		if errors.Is(err, store.ErrNotFound) || (err == nil && (d.Payload == "" || d.SourceVersion != version)) {
			return nil, true, errDocsReadOnly
		}

		if err != nil {
			return nil, true, err
		}

		out, err := s.docOf(ctx, d)
		if err != nil {
			return nil, true, err
		}

		out.Status.ReadOnly = true

		return out, true, nil
	}

	out, err := s.ownDocumentation(ctx, sk, force)
	if err != nil {
		return nil, true, err
	}

	// The scan may take long; access revoked meanwhile is not served.
	if _, err := s.store.GetGrant(ctx, u, k.ConnectionID); err != nil {
		return nil, true, err
	}

	return out, true, nil
}
