//go:build integration

package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
)

type deviceRow struct {
	User, Install, Token, Platform string
	Driver, Flavor, Version        *string
	Legacy                         *string
}

func (h *harness) devices() []deviceRow {
	h.t.Helper()
	rs, err := h.etl.Query(context.Background(), `SELECT user_id::text, install_id, token, platform, driver_id::text, app_flavor, app_version, legacy_source
		FROM device_tokens ORDER BY user_id, install_id`)
	if err != nil {
		h.t.Fatal(err)
	}
	defer rs.Close()
	var out []deviceRow
	for rs.Next() {
		var r deviceRow
		if err := rs.Scan(&r.User, &r.Install, &r.Token, &r.Platform, &r.Driver, &r.Flavor, &r.Version, &r.Legacy); err != nil {
			h.t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// putDevice is PUT /v1/me/devices with the mobile X-App-Version header.
func (h *harness) putDevice(bearer, appVersion string, body any) resp {
	h.t.Helper()
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPut, h.internal+"/v1/me/devices", bytes.NewReader(b))
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	if appVersion != "" {
		req.Header.Set("X-App-Version", appVersion)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	out := resp{status: res.StatusCode, header: res.Header}
	_ = json.NewDecoder(res.Body).Decode(&out.body)
	out.raw, _ = json.Marshal(out.body)
	if out.status >= 400 {
		checkEnvelope(h.t, out)
	}
	return out
}

// PUT /v1/me/devices and DELETE /v1/me/devices/{installId} (Appendix B §B.2.3, Appendix C §C.8, R4):
// one row per (user, install), the token moves to whoever registers it last (shared phone, ETL legacy
// row of the same device), driver_id is the caller's linked driver, the routes are internal only.
func TestDeviceRegistration(t *testing.T) {
	h := newHarness(t)
	own := h.tenant("own_fleet", "Own")
	u := h.user("driver@logitrack.test")
	h.member(u, own, "driver")
	drv := h.driver(u, own, "0812345678")
	v := h.user("staff@logitrack.test")
	h.member(v, own, "operation_staff")
	mu := h.mustLogin("driver@logitrack.test", "android", "inst-u")
	mv := h.mustLogin("staff@logitrack.test", "android", "inst-v")

	if r := h.putDevice(mu.access, "3.2.0", map[string]any{"installId": "inst-u", "token": "tok-1", "platform": "android", "appFlavor": "prod"}); r.status != http.StatusNoContent {
		t.Fatalf("register: %d %s", r.status, r.raw)
	}
	rows := h.devices()
	if len(rows) != 1 || rows[0].User != u || rows[0].Install != "inst-u" || rows[0].Token != "tok-1" || rows[0].Driver == nil ||
		*rows[0].Driver != drv || *rows[0].Flavor != "prod" || *rows[0].Version != "3.2.0" || rows[0].Platform != "android" {
		t.Fatalf("rows %+v", rows)
	}
	// onTokenRefresh: the same install gets the new token; a request without the header keeps the version.
	if r := h.putDevice(mu.access, "", map[string]any{"installId": "inst-u", "token": "tok-2", "platform": "android"}); r.status != http.StatusNoContent {
		t.Fatalf("refresh token: %d %s", r.status, r.raw)
	}
	rows = h.devices()
	if len(rows) != 1 || rows[0].Token != "tok-2" || rows[0].Flavor != nil || *rows[0].Version != "3.2.0" {
		t.Fatalf("after token refresh %+v", rows)
	}
	// The ETL row of the same phone ('legacy:' + sha256 prefix) is replaced when the device re-registers.
	h.exec(`INSERT INTO device_tokens (user_id, install_id, token, driver_id, legacy_source) VALUES ($1, 'legacy:0123456789abcdef', 'tok-3', $2, 'drivers.fcmToken')`, u, drv)
	if r := h.putDevice(mu.access, "4.0.0", map[string]any{"installId": "inst-u2", "token": "tok-3", "platform": "ios"}); r.status != http.StatusNoContent {
		t.Fatalf("re-register legacy: %d %s", r.status, r.raw)
	}
	rows = h.devices()
	if len(rows) != 2 || rows[1].Install != "inst-u2" || rows[1].Token != "tok-3" || rows[1].Legacy != nil {
		t.Fatalf("legacy row not replaced: %+v", rows)
	}
	// Shared phone: another user registers the same token; it leaves the first user's row.
	if r := h.putDevice(mv.access, "", map[string]any{"installId": "inst-u", "token": "tok-2", "platform": "android"}); r.status != http.StatusNoContent {
		t.Fatalf("shared phone: %d %s", r.status, r.raw)
	}
	rows = h.devices()
	byUser := map[string][]string{}
	for _, r := range rows {
		byUser[r.User] = append(byUser[r.User], r.Install+"="+r.Token)
		if r.User == v && r.Driver != nil {
			t.Fatalf("staff row carries a driver: %+v", r)
		}
	}
	if fmt.Sprint(byUser[u]) != "[inst-u2=tok-3]" || fmt.Sprint(byUser[v]) != "[inst-u=tok-2]" {
		t.Fatalf("after the move %v", byUser)
	}

	// Validation: 422 invalid_argument with the offending fields.
	for _, tc := range []struct {
		body  map[string]any
		field string
	}{
		{map[string]any{"token": "t", "platform": "android"}, "installId"},
		{map[string]any{"installId": "legacy:0123", "token": "t", "platform": "android"}, "installId"},
		{map[string]any{"installId": "i", "token": "has space", "platform": "android"}, "token"},
		{map[string]any{"installId": "i", "platform": "android"}, "token"},
		{map[string]any{"installId": "i", "token": "t", "platform": "windows"}, "platform"},
		{map[string]any{"installId": "i", "token": "t", "platform": "ios", "appFlavor": "beta"}, "appFlavor"},
	} {
		r := h.putDevice(mu.access, "", tc.body)
		expectError(t, r, http.StatusUnprocessableEntity, "invalid_argument")
		fields, _ := r.details()["fields"].([]any)
		if len(fields) != 1 || fields[0].(map[string]any)["field"] != tc.field {
			t.Fatalf("%v: %s", tc.body, r.raw)
		}
	}

	// DELETE: own rows only, 204 whether or not the row exists.
	del := func(bearer, install string) resp {
		return h.call(h.internal, http.MethodDelete, "/v1/me/devices/"+install, bearer, nil)
	}
	if r := del(mu.access, "inst-u"); r.status != http.StatusNoContent { // v's row of the same install id stays
		t.Fatalf("delete: %d %s", r.status, r.raw)
	}
	if r := del(mu.access, "inst-u2"); r.status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.status, r.raw)
	}
	if r := del(mu.access, "inst-u2"); r.status != http.StatusNoContent {
		t.Fatalf("delete again: %d %s", r.status, r.raw)
	}
	if rows = h.devices(); len(rows) != 1 || rows[0].User != v {
		t.Fatalf("after deletes %+v", rows)
	}

	// Authenticated and internal only.
	expectError(t, h.call(h.internal, http.MethodPut, "/v1/me/devices", "", map[string]any{"installId": "i", "token": "t", "platform": "ios"}),
		http.StatusUnauthorized, "unauthenticated")
	expectError(t, h.call(h.public, http.MethodPut, "/v1/me/devices", mu.access, map[string]any{"installId": "i", "token": "t", "platform": "ios"}),
		http.StatusNotFound, "not_found")
	expectError(t, h.call(h.public, http.MethodDelete, "/v1/me/devices/inst-u", mu.access, nil), http.StatusNotFound, "not_found")

	// Racing registrations of one token from two users end with exactly one holder.
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			bearer, install := mu.access, "race-u"
			if i%2 == 1 {
				bearer, install = mv.access, "race-v"
			}
			b, _ := json.Marshal(map[string]any{"installId": install, "token": "tok-race", "platform": "android"})
			req, _ := http.NewRequest(http.MethodPut, h.internal+"/v1/me/devices", bytes.NewReader(b))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+bearer)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			_ = res.Body.Close()
			if res.StatusCode != http.StatusNoContent {
				t.Errorf("racing register: %d", res.StatusCode)
			}
		})
	}
	wg.Wait()
	if n := scalar[int64](h, `SELECT count(*) FROM device_tokens WHERE token = 'tok-race'`); n != 1 {
		t.Fatalf("%d rows hold the raced token", n)
	}
}
