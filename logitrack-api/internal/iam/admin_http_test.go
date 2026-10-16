package iam

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
)

func TestAdminHTTPCallKeepsVerifiedContextVersion(t *testing.T) {
	for _, version := range []int64{0, 1, 2} {
		t.Run(strconv.FormatInt(version, 10), func(t *testing.T) {
			app := fiber.New()
			app.Post("/call", func(c fiber.Ctx) error {
				authz.SetPrincipal(c, &authz.Principal{UserID: uuid.New(), SessionID: uuid.New(), ContextVersion: version})
				cl, err := (adminHTTP{}).call(c)
				if err != nil {
					return err
				}
				if cl.contextVersion != strconv.FormatInt(version, 10) {
					t.Errorf("verified context version %d became %q", version, cl.contextVersion)
				}
				return c.SendStatus(http.StatusNoContent)
			})
			res, err := app.Test(httptest.NewRequest(http.MethodPost, "/call", nil))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = res.Body.Close() }()
			if res.StatusCode != http.StatusNoContent {
				t.Fatalf("call: %d", res.StatusCode)
			}
		})
	}
}
