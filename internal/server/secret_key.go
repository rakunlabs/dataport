package server

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/rakunlabs/dataport/internal/crypto"
	"github.com/rakunlabs/dataport/internal/store"
)

// Rotating the secret key seals every stored secret again with a new key,
// in one transaction. The running server reads both keys from then on;
// the configuration has to be changed to the new key before the next
// restart, or the secrets cannot be read.

var rotateMu sync.Mutex

type keyStatus struct {
	// Rotated is set once this instance runs with a key given in the UI,
	// which the configuration does not hold yet.
	Rotated bool `json:"rotated"`
	// Empty is set while the key in use is empty.
	Empty bool `json:"empty"`
	// Shared is set when other replicas may share the store: they keep the
	// key they started with.
	Shared bool `json:"shared"`
	// Secrets counts the stored secrets, and those the current key cannot
	// read.
	Secrets    int `json:"secrets"`
	Unreadable int `json:"unreadable"`
}

func (s *Server) keyStatus(ctx context.Context) (keyStatus, error) {
	v := keyStatus{
		Rotated: !s.cipher.Is(s.base.Auth.SecretKey),
		Empty:   s.cipher.Is(""),
		Shared:  s.router != nil || s.base.Store.Type == "postgres",
	}

	// A dry run counts what the current key reads.
	count, err := s.store.CheckSecrets(ctx, func(enc string) (string, error) {
		if !s.cipher.Current(enc) {
			return "", errSecret
		}

		return "", store.ErrKeepSecret
	})

	v.Secrets, v.Unreadable = count.Rewritten+count.Unreadable, count.Unreadable

	return v, err
}

func (s *Server) adminGetSecretKey(w http.ResponseWriter, r *http.Request) {
	v, err := s.keyStatus(r.Context())
	if err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, v)
}

func (s *Server) adminRotateSecretKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Current string `json:"current"`
		Key     string `json:"key"`
	}

	if !readJSON(w, r, &in) {
		return
	}

	switch {
	case !s.cipher.Is(in.Current):
		writeError(w, http.StatusBadRequest, "the current secret key is wrong")

		return
	case strings.TrimSpace(in.Key) == "" || len(in.Key) < 16:
		writeError(w, http.StatusBadRequest, "the new secret key needs at least 16 characters")

		return
	case in.Key == in.Current:
		writeError(w, http.StatusBadRequest, "the new secret key is the current one")

		return
	}

	rotateMu.Lock()
	defer rotateMu.Unlock()

	ctx := r.Context()

	// Snapshot versions hash the sealed passwords; remember them, to move
	// the snapshots over once the passwords are sealed again.
	before := s.docVersions(ctx)

	sealer, err := crypto.New(in.Key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())

		return
	}

	// Nothing new is sealed with the old key from here until the new one is
	// in use; saves wait for the rotation.
	done := sync.OnceFunc(s.cipher.Rotating())
	defer done()

	// Read with every key the server knows, seal with the new one only.
	count, err := s.store.RewriteSecrets(ctx, func(enc string) (string, error) {
		plain, err := s.cipher.Decrypt(enc)
		if err != nil {
			return "", err
		}

		return sealer.Encrypt(plain)
	})
	if err != nil {
		done()
		slog.Error("rotate secret key", "error", err)
		writeStoreError(w, err)

		return
	}

	// The key before stays readable, for values sealed with it elsewhere.
	err = s.cipher.Rotate(in.Key)

	done()

	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())

		return
	}

	// A save that encrypted just before the rotation and stored after it
	// left a value sealed with the old key; seal those again.
	if again, err := s.store.RewriteSecrets(ctx, func(enc string) (string, error) {
		if s.cipher.Current(enc) {
			return "", store.ErrKeepSecret
		}

		plain, err := s.cipher.Decrypt(enc)
		if err != nil {
			return "", err
		}

		return s.cipher.Encrypt(plain)
	}); err != nil {
		slog.Warn("seal secrets saved during the rotation", "error", err)
	} else {
		count.Unreadable = again.Unreadable
	}

	s.moveDocVersions(ctx, before)

	if s.sso != nil {
		if err := s.sso.Load(ctx); err != nil {
			slog.Warn("reload sso providers after key rotation", "error", err)
		}
	}

	slog.Warn("the secret key was rotated in the UI; set DATAPORT_AUTH_SECRET_KEY to the new key before the next restart",
		"rewritten", count.Rewritten, "unreadable", count.Unreadable, "by", currentUser(r).Username)

	v, err := s.keyStatus(ctx)
	if err != nil {
		writeStoreError(w, err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": v, "rewritten": count.Rewritten, "unreadable": count.Unreadable})
}

// docVersions maps the source version of every built snapshot to the key
// that computes it again.
func (s *Server) docVersions(ctx context.Context) map[store.DocSource]store.DocKey {
	out := map[store.DocSource]store.DocKey{}

	list, err := s.store.DocSources(ctx)
	if err != nil {
		slog.Warn("list documentation sources", "error", err)

		return out
	}

	for _, d := range list {
		k := store.DocKey{ConnectionID: d.ConnectionID, UserID: d.UserID}
		if _, _, version, err := s.docAccess(ctx, k); err == nil && version == d.Version {
			out[d] = k
		}
	}

	return out
}

// moveDocVersions gives the snapshots that were current before a rotation
// the version they have now, so they are not built again.
func (s *Server) moveDocVersions(ctx context.Context, before map[store.DocSource]store.DocKey) {
	for d, k := range before {
		_, _, version, err := s.docAccess(ctx, k)
		if err != nil || version == d.Version {
			continue
		}

		if err := s.store.RenameDocSource(ctx, d.ConnectionID, d.Version, version); err != nil {
			slog.Warn("move documentation version", "connection", d.ConnectionID, "error", err)
		}
	}
}
