//go:build integration

package mobileapp_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
)

func setSupportConsent(t *testing.T, w *world, tenant string, enabled bool) {
	t.Helper()
	err := db.WithSystem(context.Background(), w.pool, nil, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `UPDATE tenants SET support_access_enabled=$2,support_access_version=support_access_version+1,support_access_changed_at=clock_timestamp(),support_access_changed_by=(SELECT id FROM users WHERE email='pa@example.test') WHERE id=$1 AND support_access_enabled<>$2`, tenant, enabled)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestPlatformInstallationOFFCursorNeverContainsInstallID(t *testing.T) {
	w := newWorld(t)
	w.exec(`INSERT INTO user_platform_roles(user_id,role) VALUES($1,'support')`, w.user("su@example.test"))
	const first = "SENTINEL-device-install-first"
	const second = "SENTINEL-device-install-second"
	w.installation(w.D1, w.O, first, "3.5.0", "prod", w.now)
	w.installation(w.D1, w.O, second, "3.5.0", "prod", w.now.Add(-time.Second))
	for _, email := range []string{"pa@example.test", "su@example.test"} {
		tok := w.login(email)
		r := w.get("/v1/platform/app-installations?limit=1", tok)
		expect(t, r, http.StatusOK, "")
		if strings.Contains(string(r.raw), first) || strings.Contains(string(r.raw), second) {
			t.Fatalf("OFF response leaked: %s", r.raw)
		}
		cur, _ := r.body["nextCursor"].(string)
		if cur == "" {
			t.Fatal("missing cursor")
		}
		raw, err := base64.RawURLEncoding.DecodeString(cur)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), first) || strings.Contains(string(raw), second) || strings.Contains(string(raw), `"i"`) {
			t.Fatalf("decoded cursor leaked install ID: %s", raw)
		}
		page := w.get("/v1/platform/app-installations?limit=1&cursor="+cur, tok)
		expect(t, page, 200, "")
		if len(page.list()) != 1 || page.body["nextCursor"] != nil {
			t.Fatalf("continuation: %s", page.raw)
		}
	}
}
