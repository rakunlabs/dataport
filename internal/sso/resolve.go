package sso

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"

	"github.com/rakunlabs/ada/middleware/auth/identity"

	"github.com/rakunlabs/dataport/internal/auth"
	"github.com/rakunlabs/dataport/internal/store"
)

// ErrDenied is returned when an identity may not sign in. Its message is
// shown to the user, so it never names other accounts.
type ErrDenied struct {
	Reason string
}

func (e *ErrDenied) Error() string { return e.Reason }

func denied(reason string) error {
	return &ErrDenied{Reason: reason}
}

// Resolve maps an identity from the provider to a dataport user:
//
//  1. an identity already linked to a user signs in as that user;
//  2. with LinkByEmail, a verified email that matches exactly one user links
//     to that user;
//  3. with AutoCreate, a new user is created.
//
// Anything else is refused, so by default only identities an admin linked
// or users an admin created (with LinkByEmail) can sign in. The admin role
// and mapped groups are synced afterwards when a mapping is configured.
func (m *Manager) Resolve(ctx context.Context, p Provider, id *identity.Identity) (*store.User, error) {
	if id.Subject == "" {
		return nil, denied("the provider did not return a user id")
	}

	email := strings.ToLower(strings.TrimSpace(id.Email))

	slog.Debug("sso identity", "provider", p.Name, "subject", id.Subject, "email", email, "email_verified", id.EmailVerified)

	if len(p.AllowedDomains) > 0 {
		_, domain, _ := strings.Cut(email, "@")
		if email == "" || !id.EmailVerified || !slices.Contains(p.AllowedDomains, domain) {
			return nil, denied("your account's email domain is not allowed")
		}
	}

	user, err := m.find(ctx, p, id, email)
	if err != nil {
		return nil, err
	}

	if user.Disabled {
		return nil, denied("your account is disabled")
	}

	if len(p.AdminRoles) > 0 || len(p.GroupMapping) > 0 {
		if user, err = m.sync(ctx, p, user, Roles(id, p.RolesClaim)); err != nil {
			return nil, err
		}
	}

	return user, nil
}

func (m *Manager) find(ctx context.Context, p Provider, id *identity.Identity, email string) (*store.User, error) {
	link, err := m.store.GetIdentity(ctx, p.Name, id.Subject)

	switch {
	case err == nil:
		_ = m.store.TouchIdentity(ctx, p.Name, id.Subject, email)

		return m.store.GetUser(ctx, link.UserID)
	case !errors.Is(err, store.ErrNotFound):
		return nil, err
	}

	if p.LinkByEmail && email != "" && id.EmailVerified {
		user, err := m.store.GetUserByEmail(ctx, email)

		switch {
		case err == nil:
			if err := m.link(ctx, p, id, email, user.ID); err != nil {
				return nil, err
			}

			slog.Info("sso identity linked by email", "provider", p.Name, "user", user.Username)

			return m.store.GetUser(ctx, user.ID)
		case errors.Is(err, store.ErrConflict):
			return nil, denied("more than one account uses your email; ask an administrator to link your account")
		case !errors.Is(err, store.ErrNotFound):
			return nil, err
		}
	}

	if !p.AutoCreate {
		return nil, denied("no dataport account is linked to this sign-in; ask an administrator for access")
	}

	return m.create(ctx, p, id, email)
}

func (m *Manager) link(ctx context.Context, p Provider, id *identity.Identity, email, userID string) error {
	err := m.store.LinkIdentity(ctx, &store.Identity{Provider: p.Name, Subject: id.Subject, UserID: userID, Email: email})
	if errors.Is(err, store.ErrConflict) {
		// Another request linked it in the meantime.
		return nil
	}

	return err
}

var usernameCleaner = regexp.MustCompile(`[^a-zA-Z0-9._@-]+`)

func (m *Manager) create(ctx context.Context, p Provider, id *identity.Identity, email string) (*store.User, error) {
	base := ""
	if email != "" && id.EmailVerified {
		base = email
	} else if v, ok := id.Claims["preferred_username"].(string); ok {
		base = v
	}

	base = usernameCleaner.ReplaceAllString(base, "")
	if len(base) < 2 {
		base = p.Name + "-" + id.Subject
		base = usernameCleaner.ReplaceAllString(base, "")
	}

	if len(base) > 56 {
		base = base[:56]
	}

	// The password is random and never shown: the user signs in with SSO.
	hash, err := auth.HashPassword(auth.RandomPassword() + auth.RandomPassword())
	if err != nil {
		return nil, err
	}

	verifiedEmail := ""
	if id.EmailVerified {
		verifiedEmail = email
	}

	for i := 0; i < 20; i++ {
		username := base
		if i > 0 {
			username = fmt.Sprintf("%s-%d", base, i+1)
		}

		user := &store.User{
			Username:     username,
			DisplayName:  id.Name,
			Email:        verifiedEmail,
			PasswordHash: hash,
			Role:         store.RoleUser,
		}

		err := m.store.CreateUser(ctx, user)
		if errors.Is(err, store.ErrConflict) {
			continue
		}

		if err != nil {
			return nil, err
		}

		if err := m.link(ctx, p, id, email, user.ID); err != nil {
			return nil, err
		}

		slog.Info("sso user created", "provider", p.Name, "user", username)

		return m.store.GetUser(ctx, user.ID)
	}

	return nil, errors.New("could not pick a free username")
}

// sync applies the admin role and the mapped groups from the claim values.
// Only groups named in GroupMapping are added or removed; groups an admin
// assigned by hand stay as they are.
func (m *Manager) sync(ctx context.Context, p Provider, user *store.User, roles []string) (*store.User, error) {
	changed := false

	wantAdmin := slices.ContainsFunc(roles, func(r string) bool { return slices.Contains(p.AdminRoles, r) })
	if len(p.AdminRoles) > 0 && wantAdmin != user.IsAdmin() {
		if !wantAdmin {
			n, err := m.store.CountActiveAdmins(ctx)
			if err != nil {
				return nil, err
			}

			if n <= 1 {
				slog.Warn("sso would demote the last admin; keeping the role", "user", user.Username)

				wantAdmin = true
			}
		}

		if wantAdmin != user.IsAdmin() {
			user.Role = store.RoleUser
			if wantAdmin {
				user.Role = store.RoleAdmin
			}

			changed = true
		}
	}

	if len(p.GroupMapping) > 0 {
		managed := map[string]bool{}
		wanted := map[string]bool{}

		for claim, group := range p.GroupMapping {
			managed[group] = true
			if slices.Contains(roles, claim) {
				wanted[group] = true
			}
		}

		groups := slices.DeleteFunc(slices.Clone(user.GroupIDs), func(g string) bool { return managed[g] && !wanted[g] })
		for g := range wanted {
			if !slices.Contains(groups, g) {
				groups = append(groups, g)
			}
		}

		slices.Sort(groups)

		current := slices.Clone(user.GroupIDs)
		slices.Sort(current)

		if !slices.Equal(groups, current) {
			user.GroupIDs = groups
			changed = true
		}
	}

	if !changed {
		return user, nil
	}

	if user.GroupIDs == nil {
		user.GroupIDs = []string{}
	}

	user.PasswordHash = ""
	if err := m.store.UpdateUser(ctx, user); err != nil {
		return nil, fmt.Errorf("sync roles; %w", err)
	}

	return m.store.GetUser(ctx, user.ID)
}
