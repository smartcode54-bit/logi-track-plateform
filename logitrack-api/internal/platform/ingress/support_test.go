package ingress

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

func TestDuplicatePoliciesAreAnErrorAndDenyAtRuntime(t *testing.T) {
	server := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
	server.Use(httpx.AdmissionGuard(nil), func(c fiber.Ctx) error {
		httpx.SetProjectionAudience(c, httpx.ProjectionAudience{Support: true})
		return c.Next()
	})
	calls := 0
	registry := Mount(server, Internal, []Group{{Prefix: "/v1", Mount: func(r fiber.Router) {
		for _, schema := range []string{"first", "conflicting"} {
			Bind(r, "/data", httpx.EndpointPolicy{ID: "GET /v1/data", Method: "GET", Route: "/v1/data", Class: httpx.PolicyOwnAccount, Phase: 1, Plane: "self", Target: "authenticated_caller", PredicateID: "own", SchemaID: schema}, func(c fiber.Ctx) error { calls++; return c.SendString("SENTINEL") })
		}
	}}}, nil)
	if registry.Check() == nil {
		t.Fatal("duplicate/conflicting registration admitted")
	}
	res, err := server.Test(httptest.NewRequest("GET", "/v1/data", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if calls != 0 || res.StatusCode != 403 || !strings.Contains(string(body), "support_view_unavailable") {
		t.Fatal(calls, res.StatusCode, string(body))
	}
}
