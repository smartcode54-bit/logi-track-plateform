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
// listener. Widening this list needs an ADR (main spec §16, PUBLIC_ROUTE_GROUPS):
// /media is the owner-approved widening of 2026-10-10 for the local storage
// backend (signed object URLs, T11; main spec §2.6, ADR 0029 notes).
var PublicPrefixes = []string{"/v1/mobile", "/v1/auth", "/public/v1", "/evidence", "/healthz", "/media"}

// IsPublicPrefix reports whether p is one of PublicPrefixes.
func IsPublicPrefix(p string) bool { return slices.Contains(PublicPrefixes, p) }

// Group is a set of routes under one path prefix. Mount registers routes on a
// router already scoped to Prefix (use "" for the prefix itself).
type Group struct {
	Prefix string
	Public bool
	Mount  func(r fiber.Router)
	// UploadPaths are absolute path prefixes below Prefix (ending in "/") whose PUT
	// requests may carry a body up to the API's upload limit (UPLOAD_MAX_BYTES)
	// instead of the 4 MiB default: the local storage backend's upload routes (T11).
	UploadPaths []string
}

// UploadPaths returns the upload path prefixes of the groups that listener mounts.
func UploadPaths(listener string, groups []Group, allow []string) []string {
	var out []string
	for _, g := range groups {
		if listener == Public && (!g.Public || !slices.Contains(allow, g.Prefix)) {
			continue
		}
		out = append(out, g.UploadPaths...)
	}
	return out
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
		for _, u := range g.UploadPaths {
			if !strings.HasPrefix(u, g.Prefix+"/") || !strings.HasSuffix(u, "/") {
				return fmt.Errorf("ingress: upload path %q of group %q must be below the prefix and end in /", u, g.Prefix)
			}
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

// Route is one registered route of a listener.
type Route struct {
	Method string
	Path   string
}

// MethodUse is the Method of a Routes entry for a middleware or a mounted sub-app registered
// with Use: it runs for every method and every path at or below Path.
const MethodUse = "USE"

// Routes lists the routes registered on app, sorted by path and method: one entry per method
// and path of a handler, and one MethodUse entry per path where a middleware or a sub-app was
// registered with Use (a sub-app's own routes join only at startup; its mount point stands for
// them). It leaves out the HEAD routes Fiber derives from GET at startup and the middleware
// registered with Use at "/", the listener-wide chain of the api's builder (see RootMiddleware);
// a sub-app mounted at "/" is listed.
func Routes(app *fiber.App) []Route {
	// GetRoutes(true) is GetRoutes(false) without the Use registrations, which Fiber copies onto
	// every method; the entries beyond it, per method and path, are those registrations.
	handlers := map[Route]int{}
	for _, r := range app.GetRoutes(true) {
		handlers[Route{r.Method, r.Path}]++
	}
	var out []Route
	for _, r := range app.GetRoutes(false) {
		k := Route{r.Method, r.Path}
		switch {
		case handlers[k] > 0:
			handlers[k]--
			out = append(out, k)
		case r.Path != "/" || len(r.Handlers) == 0: // a Use without handlers is a mounted sub-app
			out = append(out, Route{MethodUse, r.Path})
		}
	}
	slices.SortFunc(out, func(a, b Route) int {
		if c := strings.Compare(a.Path, b.Path); c != 0 {
			return c
		}
		return strings.Compare(a.Method, b.Method)
	})
	return slices.Compact(out)
}

// RootMiddleware returns the number of handlers registered with Use at "/", the middleware that
// runs for every request of the listener. A route check cannot see what such a handler serves
// (a path-dispatching middleware such as pprof or expvar answers paths no route lists), so the
// public listener's count is pinned by a test of its builder.
func RootMiddleware(app *fiber.App) int {
	n := 0
	for _, r := range app.GetRoutes(false) {
		if r.Method == fiber.MethodGet && r.Path == "/" {
			n += len(r.Handlers)
		}
	}
	for _, r := range app.GetRoutes(true) {
		if r.Method == fiber.MethodGet && r.Path == "/" {
			n -= len(r.Handlers)
		}
	}
	return n
}

// PublicPathAllowed reports whether the public listener may serve path (main spec §2.6):
// /healthz itself, or the group root or any path below /v1/mobile, /v1/auth, /public/v1,
// /evidence and /media. A sibling such as /v1/mobilex or a child such as /healthz/x is refused.
func PublicPathAllowed(path string) bool {
	for _, p := range PublicPrefixes {
		if path == p || (p != "/healthz" && strings.HasPrefix(path, p+"/")) {
			return true
		}
	}
	return false
}

// PublicUseAllowed reports whether the public listener may hold a Use registration (middleware
// or mounted sub-app) at path. Use matches every path at or below its own, so it is allowed
// only where all of them are: at or below /v1/mobile, /v1/auth, /public/v1, /evidence and /media. At
// /healthz it would answer /healthz/anything, so it is refused there.
func PublicUseAllowed(path string) bool {
	return path != "/healthz" && PublicPathAllowed(path)
}

// CheckPublicRoutes fails for every route of the public listener outside the allow-list. It
// backs Validate at route level: a group check alone cannot see a child path of /healthz, a
// Use registration (middleware or sub-app) at or below /healthz, or a route registered on the
// public app outside a group (go-ci gen-check, main spec §17.2). The Use chain at "/" is not a
// route; RootMiddleware covers it.
func CheckPublicRoutes(routes []Route) error {
	var bad []string
	for _, r := range routes {
		ok := PublicPathAllowed(r.Path)
		if r.Method == MethodUse {
			ok = PublicUseAllowed(r.Path)
		}
		if !ok {
			bad = append(bad, r.Method+" "+r.Path)
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("ingress: the public listener serves %d route(s) outside %v: %s",
			len(bad), PublicPrefixes, strings.Join(bad, ", "))
	}
	return nil
}
