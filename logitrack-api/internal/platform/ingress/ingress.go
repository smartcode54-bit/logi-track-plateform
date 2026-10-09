// Package ingress builds the two Fiber listeners of the api process from one
// route registry (main spec §2.6, Appendix B §B.1.2):
//
//   - internal (API_INTERNAL_ADDR) mounts every route group;
//   - public (API_PUBLIC_ADDR) mounts only groups marked public whose prefix is
//     in the PUBLIC_ROUTE_GROUPS allow-list. Every other path is 404 not_found,
//     never 401/403, so the internal surface is not disclosed.
package ingress

import (
	"fmt"
	"slices"
	"strings"

	"github.com/gofiber/fiber/v3"
)

// Listener names, also used as log, trace and metric labels.
const (
	Internal = "internal"
	Public   = "public"
)

// PublicPrefixes are the only groups that may ever be served on the public
// listener. Widening this list needs an ADR (main spec §16, PUBLIC_ROUTE_GROUPS).
var PublicPrefixes = []string{"/v1/mobile", "/v1/auth", "/public/v1", "/evidence", "/healthz"}

// IsPublicPrefix reports whether p is one of PublicPrefixes.
func IsPublicPrefix(p string) bool { return slices.Contains(PublicPrefixes, p) }

// Group is a set of routes under one path prefix. Mount registers routes on a
// router already scoped to Prefix (use "" for the prefix itself).
type Group struct {
	Prefix string
	Public bool
	Mount  func(r fiber.Router)
}

// Validate rejects groups that would break the ingress policy.
func Validate(groups []Group) error {
	seen := map[string]bool{}
	for _, g := range groups {
		if g.Prefix == "" || !strings.HasPrefix(g.Prefix, "/") || strings.HasSuffix(g.Prefix, "/") {
			return fmt.Errorf("ingress: invalid group prefix %q", g.Prefix)
		}
		if seen[g.Prefix] {
			return fmt.Errorf("ingress: duplicate group prefix %q", g.Prefix)
		}
		seen[g.Prefix] = true
		if g.Public && !IsPublicPrefix(g.Prefix) {
			return fmt.Errorf("ingress: group %q is marked public but is not one of %v", g.Prefix, PublicPrefixes)
		}
		if g.Mount == nil {
			return fmt.Errorf("ingress: group %q has no Mount function", g.Prefix)
		}
	}
	return nil
}

// Mount registers the groups that belong on the given listener.
func Mount(app *fiber.App, listener string, groups []Group, allow []string) {
	for _, g := range groups {
		if listener == Public && (!g.Public || !slices.Contains(allow, g.Prefix)) {
			continue
		}
		g.Mount(app.Group(g.Prefix))
	}
}
