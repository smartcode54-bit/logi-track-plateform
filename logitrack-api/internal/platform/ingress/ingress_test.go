package ingress

import (
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestPublicPathAllowed(t *testing.T) {
	for path, want := range map[string]bool{
		"/healthz":               true,
		"/healthz/":              false,
		"/healthz/debug":         false,
		"/healthzx":              false,
		"/v1/mobile":             true,
		"/v1/mobile/tasks/:id":   true,
		"/v1/mobilex":            false,
		"/v1/auth/login":         true,
		"/public/v1/line/notify": true,
		"/evidence":              true,
		"/evidence/:token":       true,
		"/v1/me":                 false,
		"/v1/admin/queues":       false,
		"/readyz":                false,
		"/":                      false,
		"*":                      false,
		"":                       false,
	} {
		if got := PublicPathAllowed(path); got != want {
			t.Errorf("PublicPathAllowed(%q) = %t, want %t", path, got, want)
		}
	}
}

func TestCheckPublicRoutesNamesEveryOffender(t *testing.T) {
	if err := CheckPublicRoutes([]Route{{"GET", "/healthz"}, {"POST", "/v1/mobile/tasks"}}); err != nil {
		t.Fatal(err)
	}
	err := CheckPublicRoutes([]Route{{"GET", "/healthz"}, {"GET", "/v1/me"}, {"DELETE", "/healthz/cache"}})
	if err == nil || !strings.Contains(err.Error(), "2 route(s)") ||
		!strings.Contains(err.Error(), "GET /v1/me") || !strings.Contains(err.Error(), "DELETE /healthz/cache") {
		t.Fatalf("err = %v", err)
	}
}

// A route registered on the public app below /healthz passes the group check (the prefix is
// allow-listed) but not the route check.
func TestRouteCheckCatchesWhatTheGroupCheckCannot(t *testing.T) {
	groups := []Group{{Prefix: "/healthz", Public: true, Mount: func(r fiber.Router) {
		r.Get("", func(c fiber.Ctx) error { return nil })
		r.Get("/debug", func(c fiber.Ctx) error { return nil })
	}}}
	if err := Validate(groups); err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error { return c.Next() })
	Mount(app, Public, groups, PublicPrefixes)
	routes := Routes(app)
	if len(routes) != 2 || routes[0] != (Route{"GET", "/healthz"}) || routes[1] != (Route{"GET", "/healthz/debug"}) {
		t.Fatalf("routes = %v (middleware and automatic HEAD routes must not be listed)", routes)
	}
	if err := CheckPublicRoutes(routes); err == nil || !strings.Contains(err.Error(), "GET /healthz/debug") {
		t.Fatalf("err = %v", err)
	}
}

// Use matches every path at or below its own, so a middleware or a sub-app registered with Use
// at or below /healthz answers /healthz/anything on the public listener. Routes lists each such
// registration once, as USE, and the route check refuses it; below the other public prefixes,
// where every path is allowed, it passes.
func TestRouteCheckSeesUseRegistrations(t *testing.T) {
	h := func(c fiber.Ctx) error { return c.SendString("served") }
	sub := func() *fiber.App {
		s := fiber.New()
		s.Get("/y", h)
		return s
	}
	for name, tc := range map[string]struct {
		groups []Group
		app    func(app *fiber.App)
		want   []Route
		bad    string // "" when the check passes
	}{
		"use with a path below /healthz": {
			groups: []Group{{Prefix: "/healthz", Public: true, Mount: func(r fiber.Router) {
				r.Get("", h)
				r.Use("/debug", h)
			}}},
			want: []Route{{"GET", "/healthz"}, {MethodUse, "/healthz/debug"}},
			bad:  "USE /healthz/debug",
		},
		"use at /healthz": {
			groups: []Group{{Prefix: "/healthz", Public: true, Mount: func(r fiber.Router) {
				r.Use(h)
				r.Get("", h)
			}}},
			want: []Route{{"GET", "/healthz"}, {MethodUse, "/healthz"}},
			bad:  "USE /healthz",
		},
		"sub-app below /healthz": {
			groups: []Group{{Prefix: "/healthz", Public: true, Mount: func(r fiber.Router) {
				r.Get("", h)
				r.Use("/x", sub())
			}}},
			want: []Route{{"GET", "/healthz"}, {MethodUse, "/healthz/x"}},
			bad:  "USE /healthz/x",
		},
		"use with a path on the app": {
			app:  func(app *fiber.App) { app.Use("/debug", h) },
			want: []Route{{MethodUse, "/debug"}},
			bad:  "USE /debug",
		},
		"sub-app at the root of the app": {
			app:  func(app *fiber.App) { app.Use(sub()) },
			want: []Route{{MethodUse, "/"}},
			bad:  "USE /",
		},
		"middleware and a sub-app below /v1/mobile": {
			groups: []Group{{Prefix: "/v1/mobile", Public: true, Mount: func(r fiber.Router) {
				r.Use(h)
				r.Get("/tasks", h)
				r.Use("/files", sub())
			}}},
			want: []Route{{MethodUse, "/v1/mobile"}, {MethodUse, "/v1/mobile/files"}, {"GET", "/v1/mobile/tasks"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := Validate(tc.groups); err != nil {
				t.Fatal(err)
			}
			app := fiber.New()
			app.Use(func(c fiber.Ctx) error { return c.Next() }) // the listener-wide chain: not a route
			Mount(app, Public, tc.groups, PublicPrefixes)
			if tc.app != nil {
				tc.app(app)
			}
			routes := Routes(app)
			if !slices.Equal(routes, tc.want) {
				t.Fatalf("routes = %v, want %v", routes, tc.want)
			}
			err := CheckPublicRoutes(routes)
			if tc.bad == "" && err != nil {
				t.Fatal(err)
			}
			if tc.bad != "" && (err == nil || !strings.Contains(err.Error(), tc.bad)) {
				t.Fatalf("err = %v, want it to name %s", err, tc.bad)
			}
		})
	}
}

func TestPublicUseAllowed(t *testing.T) {
	for path, want := range map[string]bool{
		"/v1/mobile":       true,
		"/v1/mobile/files": true,
		"/evidence":        true,
		"/healthz":         false,
		"/healthz/debug":   false,
		"/v1":              false,
		"/":                false,
	} {
		if got := PublicUseAllowed(path); got != want {
			t.Errorf("PublicUseAllowed(%q) = %t, want %t", path, got, want)
		}
	}
}

// RootMiddleware counts the handlers registered with Use at "/", merged or not, and no route.
func TestRootMiddlewareCountsTheListenerWideChain(t *testing.T) {
	h := func(c fiber.Ctx) error { return c.Next() }
	app := fiber.New()
	app.Use(h, h)
	app.Use(h)
	app.Get("/", h)
	app.Group("/v1/mobile").Use(h)
	if n := RootMiddleware(app); n != 3 {
		t.Fatalf("RootMiddleware = %d, want 3", n)
	}
	if routes := Routes(app); !slices.Equal(routes, []Route{{"GET", "/"}, {MethodUse, "/v1/mobile"}}) {
		t.Fatalf("routes = %v", routes)
	}
}

// /media is the owner-approved widening of 2026-10-10 (local storage backend, T11).
func TestMediaIsPublicAndUploadPathsStayBelowTheirGroup(t *testing.T) {
	if !IsPublicPrefix("/media") || !PublicPathAllowed("/media/*") || !PublicUseAllowed("/media") || PublicPathAllowed("/mediax") {
		t.Fatal("/media must be a public group")
	}
	mount := func(fiber.Router) {}
	for _, bad := range [][]string{{"/v1/files/"}, {"/media"}, {"/media/x"}, {"/mediax/"}} {
		if err := Validate([]Group{{Prefix: "/media", Public: true, Mount: mount, UploadPaths: bad}}); err == nil {
			t.Errorf("upload paths %v accepted", bad)
		}
	}
	groups := []Group{
		{Prefix: "/v1/uploads", Mount: mount, UploadPaths: []string{"/v1/uploads/local/"}},
		{Prefix: "/media", Public: true, Mount: mount, UploadPaths: []string{"/media/"}},
	}
	if err := Validate(groups); err != nil {
		t.Fatal(err)
	}
	if got := UploadPaths(Internal, groups, nil); !slices.Equal(got, []string{"/v1/uploads/local/", "/media/"}) {
		t.Fatalf("internal upload paths %v", got)
	}
	if got := UploadPaths(Public, groups, []string{"/media"}); !slices.Equal(got, []string{"/media/"}) {
		t.Fatalf("public upload paths %v", got)
	}
	if got := UploadPaths(Public, groups, []string{"/healthz"}); len(got) != 0 {
		t.Fatalf("a group outside PUBLIC_ROUTE_GROUPS lends its upload paths: %v", got)
	}
}
