package mobileapp

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/rs/zerolog"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/supportprojection"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

func TestReviewProjectedInstallationsKeepTenantRequired(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: httpx.ErrorHandler(zerolog.Nop())})
	app.Use(httpx.AdmissionGuard(nil))
	app.Get("/installations", func(c fiber.Ctx) error {
		httpx.SetProjectionAudience(c, httpx.ProjectionAudience{Platform: true})
		httpx.RequireResponseProjection(c)
		if err := httpx.EndpointGate(c, supportprojection.Policy("GET", InstallationsPrefix, "mobileapp.installation")); err != nil {
			return err
		}
		return authz.ErrTenantRequired()
	})
	res, err := app.Test(httptest.NewRequest("GET", "/installations", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != 403 || !strings.Contains(string(raw), `"code":"tenant_required"`) {
		t.Fatalf("tenant refusal: %d %s", res.StatusCode, raw)
	}
}
