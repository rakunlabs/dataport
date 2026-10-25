package sso

import (
	"strings"

	"github.com/rakunlabs/ada/middleware/auth/identity"
)

// Roles returns the identity's roles: the flat "roles" claim, which ada
// reads into Identity.Roles, together with the values found at the extra
// claim paths. This follows pika's effectiveRoles.
func Roles(id *identity.Identity, paths string) []string {
	seen := map[string]bool{}
	out := []string{}

	for _, src := range [][]string{id.Roles, HarvestRoles(id.Claims, paths)} {
		for _, r := range src {
			if r != "" && !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}

	return out
}

// HarvestRoles collects strings from the claims at each comma-separated
// dotted path, such as "groups" or "realm_access.roles". A "*" segment walks
// every value of a map, for "resource_access.*.roles". Leaves may be arrays
// or whitespace-separated strings. Adapted from pika's oidc_roles.go.
func HarvestRoles(claims map[string]any, paths string) []string {
	seen := map[string]bool{}
	out := []string{}

	for _, path := range strings.Split(paths, ",") {
		var segs []string

		for _, s := range strings.Split(path, ".") {
			if s = strings.TrimSpace(s); s != "" {
				segs = append(segs, s)
			}
		}

		if len(segs) == 0 {
			continue
		}

		walk(claims, segs, func(roles []string) {
			for _, r := range roles {
				if r != "" && !seen[r] {
					seen[r] = true
					out = append(out, r)
				}
			}
		})
	}

	return out
}

func walk(node any, segs []string, emit func([]string)) {
	if len(segs) == 0 {
		emit(leaf(node))

		return
	}

	m, ok := node.(map[string]any)
	if !ok {
		return
	}

	if segs[0] == "*" {
		for _, v := range m {
			walk(v, segs[1:], emit)
		}

		return
	}

	if next, ok := m[segs[0]]; ok {
		walk(next, segs[1:], emit)
	}
}

func leaf(v any) []string {
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}

		return out
	case []string:
		return t
	case string:
		return strings.Fields(t)
	}

	return nil
}
