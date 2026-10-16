package httpx

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog"
)

type repositoryLabel struct {
	Name string `json:"name"`
}

var repositoryLabelSchema = NewSchema[repositoryLabel]("test.repository_label", map[string]Field{"name": {Kind: Safe}})

func TestRepositoryRedactionsFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name, value, path string
		kind              FieldKind
		status            int
	}{
		{"masked", "****", "/data/name", Person, 200},
		{"unmasked", "Raw Person", "/data/name", Person, 503},
		{"missing", "****", "/data/absent", Person, 503},
		{"outside data", "****", "/meta/name", Person, 503},
		{"invalid classification", "****", "/data/name", Safe, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := fiber.New(fiber.Config{ErrorHandler: ErrorHandler(zerolog.Nop())})
			app.Use(AdmissionGuard(nil), func(c fiber.Ctx) error {
				SetProjectionAudience(c, ProjectionAudience{Support: true, Actor: "own", Tenant: "tenant", ConsentVersion: "0", Finalize: func(_ context.Context, _ []string, fn func(map[string]Consent) error) error {
					return fn(map[string]Consent{"tenant": {Version: "0"}})
				}})
				return c.Next()
			})
			app.Get("/label", func(c fiber.Ctx) error {
				if err := EndpointGate(c, EndpointPolicy{ID: "GET /label", Method: "GET", Route: "/label", Class: PolicyOwnAccount, Phase: 1, Plane: "self", Target: "authenticated_caller", PredicateID: "own", SchemaID: "test.repository_label"}); err != nil {
					return err
				}
				return JSONWithRedactions(c, 200, repositoryLabel{Name: tc.value}, []RedactedField{{Path: tc.path, Kind: tc.kind}})
			})
			res, err := app.Test(httptest.NewRequest("GET", "/label", nil))
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(res.Body)
			_ = res.Body.Close()
			if res.StatusCode != tc.status {
				t.Fatalf("status %d: %s", res.StatusCode, body)
			}
			if tc.status == 503 && strings.Contains(string(body), "Raw Person") {
				t.Fatal("refusal leaked raw label")
			}
		})
	}
}
