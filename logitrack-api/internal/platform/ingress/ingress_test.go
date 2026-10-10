package ingress

import (
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
