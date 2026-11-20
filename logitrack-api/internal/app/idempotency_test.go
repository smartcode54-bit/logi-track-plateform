package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/idempotency"
)

// The Idempotency-Key scope of the replayable driver writes (T55, §B.1.5) is the caller's user id: a key is never
// shared across users, and a request without a principal gets no scope (the middleware answers 401).
func TestIdempotencyScope(t *testing.T) {
	user := uuid.Must(uuid.NewV7())
	for _, tc := range []struct {
		name string
		p    *authz.Principal
		want string
	}{
		{"no principal", nil, ""},
		{"nil user", &authz.Principal{}, ""},
		{"driver", &authz.Principal{UserID: user}, user.String()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := fiber.New()
			app.Get("/", func(c fiber.Ctx) error {
				if tc.p != nil {
					authz.SetPrincipal(c, tc.p)
				}
				return c.SendString(IdempotencyScope(c))
			})
			res, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(res.Body)
			if string(body) != tc.want {
				t.Errorf("scope %q, want %q", body, tc.want)
			}
		})
	}
}

// IDEMPOTENCY_TTL is validated with the api config (1h..720h), so a bad value fails the start instead of the first
// replay.
func TestIdempotencyConfigValidated(t *testing.T) {
	for _, tc := range []struct {
		ttl string
		ok  bool
	}{{"168h", true}, {"30m", false}, {"800h", false}} {
		d, err := time.ParseDuration(tc.ttl)
		if err != nil {
			t.Fatal(err)
		}
		err = idempotency.Config{TTL: d}.Validate()
		if (err == nil) != tc.ok {
			t.Errorf("IDEMPOTENCY_TTL=%s: err %v, want ok=%v", tc.ttl, err, tc.ok)
		}
	}
}
