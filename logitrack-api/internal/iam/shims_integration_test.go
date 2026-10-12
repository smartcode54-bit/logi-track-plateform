//go:build integration

package iam_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebase/firebasetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

// The callable shim targets of T32 (issue #52; Appendix B §B.2.21, §B.2.22, Appendix C §C.6.5; R45) as cmd/api mounts
// them (app.MobileGroups) behind the real auth.RequireShim: a cf_shim API key together with the caller's Firebase ID
// token, minted here with the test keys of firebasetest and verified against the securetoken issuer of the test project
// in every bridge mode (the world runs with the bridge off). GET /v1/mobile/me answers setDriverClaims with the driver's
// Firestore doc id; POST /v1/mobile/tasks/{id}/stops lets a driver add stops to its own tasks only (operations
// PostgreSQL-owned here; internal/operations covers the write-back). shim_requests_total{callable} counts every
// authenticated call per callable from 0; the targets add well under 300 ms (p95, in-process).
func TestShimTargets(t *testing.T) {
	fb := firebasetest.New(t, "logitrack-shim-test")
	reg := prometheus.NewRegistry()
	w := newWorldOpts(t, worldOpts{firebase: auth.Firebase{Verifier: fb.Verifier(nil)},
		groups: func(w *world, svc *auth.Service) []ingress.Group {
			if err := svc.Register(reg); err != nil {
				t.Fatal(err)
			}
			owners, err := iam.NewTaskOwners(w.pool)
			if err != nil {
				t.Fatal(err)
			}
			ops, err := operations.New(operations.Deps{Pool: w.pool, Log: zerolog.Nop(), PGOwned: true, Owners: owners})
			if err != nil {
				t.Fatal(err)
			}
			return app.MobileGroups(app.APIDeps{Auth: svc, Operations: ops})
		}})
	// Every series exists from the start, so a shim nobody calls reports 0 (R26 retirement evidence).
	for _, callable := range []string{app.ShimSetDriverClaims, app.ShimAddDeliveryStop, app.ShimComputeTripBilling} {
		if n, ok := shimCount(t, reg, callable); !ok || n != 0 {
			t.Fatalf("shim_requests_total{callable=%q} = %v (present %v), want 0", callable, n, ok)
		}
	}

	pa := w.login("pa@example.test")
	keyID, key := w.mustKey(pa, map[string]any{"name": "callable shims", "scope": "cf_shim",
		"capabilities": []string{"mobile:view_tasks", "mobile:submit_trip"}})

	// A driver imported from Firestore (its drivers doc id), a driver created in Go (no doc id: its uuid is the doc id
	// of the write-back, R25), a staff user, each with its Firebase uid.
	drvUser := w.user("shim.driver1@example.test")
	w.member(drvUser, w.A, "driver")
	drv := w.id(`INSERT INTO drivers (tenant_id, tenant_source, user_id, first_name, last_name, mobile, legacy_doc_id)
		VALUES ($1, 'self', $2, 'ส', 'ข', '0811111111', 'drvDoc0001') RETURNING id::text`, w.A, drvUser)
	w.exec(`UPDATE users SET legacy_auth_uid = 'fbShimDriver000000000000001' WHERE id = $1`, drvUser)
	drv2User := w.user("shim.driver2@example.test")
	w.member(drv2User, w.A, "driver")
	drv2 := w.id(`INSERT INTO drivers (tenant_id, tenant_source, user_id, first_name, last_name, mobile)
		VALUES ($1, 'self', $2, 'ค', 'ง', '0822222222') RETURNING id::text`, w.A, drv2User)
	w.exec(`UPDATE users SET legacy_auth_uid = 'fbShimDriver000000000000002' WHERE id = $1`, drv2User)
	staff := w.user("shim.staff@example.test")
	w.member(staff, w.O, "operator")
	w.exec(`UPDATE users SET legacy_auth_uid = 'fbShimStaff0000000000000001' WHERE id = $1`, staff)
	now := time.Now()
	token := func(uid string) string { return fb.SignIDToken(fb.IDTokenClaims(uid, now.Add(-5*time.Minute), now)) }
	drvTok, drv2Tok, staffTok := token("fbShimDriver000000000000001"), token("fbShimDriver000000000000002"), token("fbShimStaff0000000000000001")
	shim := func(base, method, path, bearer string, body any, headers ...string) resp {
		t.Helper()
		return shimRequest(t, base+path, method, bearer, body, headers...)
	}
	me := func(bearer string, headers ...string) resp {
		t.Helper()
		return shim(w.public, http.MethodGet, "/v1/mobile/me", bearer, nil, headers...)
	}

	// --- setDriverClaims -> GET /v1/mobile/me ---------------------------------------------------------------------
	calls := map[string]float64{}
	r := me(drvTok, auth.HeaderAPIKey, key)
	calls[app.ShimSetDriverClaims]++
	expect(t, r, http.StatusOK, "")
	d := r.data().(map[string]any)
	dr, _ := d["driver"].(map[string]any)
	if d["id"] != drvUser || dr["id"] != drv || dr["legacyDocId"] != "drvDoc0001" || dr["tenantId"] != w.A ||
		!slices.Contains(capsOf(d), string(authz.MobileSubmitTrip)) {
		t.Fatalf("driver me: %s", r.raw)
	}
	if bytes.Count(r.raw, []byte(`"driver":`)) != 1 {
		t.Fatalf("the driver key appears more than once: %s", r.raw)
	}
	r = me(drv2Tok, auth.HeaderAPIKey, key)
	calls[app.ShimSetDriverClaims]++
	if dr, _ := r.data().(map[string]any)["driver"].(map[string]any); r.status != http.StatusOK || dr["legacyDocId"] != drv2 {
		t.Fatalf("Go-created driver me: %d %s", r.status, r.raw)
	}
	r = me(staffTok, auth.HeaderAPIKey, key)
	calls[app.ShimSetDriverClaims]++
	if r.status != http.StatusOK || r.data().(map[string]any)["driver"] != nil {
		t.Fatalf("staff me: %d %s", r.status, r.raw)
	}
	// No LogiTrack account for the Firebase uid: 403 no_account (the shim answers "User is not a driver").
	expect(t, me(token("fbNobody00000000000000000001"), auth.HeaderAPIKey, key), http.StatusForbidden, auth.CodeNoAccount)
	// Missing either credential is 401; the internal listener refuses a cf_shim key; a Go access token is no
	// Firebase token.
	expect(t, me(drvTok), http.StatusUnauthorized, "unauthenticated")
	expect(t, me("", auth.HeaderAPIKey, key), http.StatusUnauthorized, "unauthenticated")
	r = shim(w.internal, http.MethodGet, "/v1/mobile/me", drvTok, nil, auth.HeaderAPIKey, key)
	expect(t, r, http.StatusForbidden, authz.CodePermissionDenied)
	if reason(r) != auth.ReasonKeyScope {
		t.Fatalf("internal listener: %s", r.raw)
	}
	expect(t, me(w.login("shim.staff@example.test"), auth.HeaderAPIKey, key), http.StatusUnauthorized, "invalid_token")

	// --- addDeliveryStop -> POST /v1/mobile/tasks/{id}/stops -----------------------------------------------------
	task := w.id(`INSERT INTO tasks (tenant_id, tenant_source, task_type, status, plan_at, source_hub_raw, destination_raw,
		driver_id, legacy_doc_id) VALUES ($1, 'driver', 'first_mile', 'assigned', now(), 'SRC', 'DST', $2, 'fsShimTask1')
		RETURNING id::text`, w.A, drv)
	stops := func(ref, bearer string, body any) resp {
		t.Helper()
		return shim(w.public, http.MethodPost, "/v1/mobile/tasks/"+ref+"/stops", bearer, body, auth.HeaderAPIKey, key)
	}
	r = stops("fsShimTask1", drvTok, map[string]any{"destination": "bkk-1", "isCustom": true})
	calls[app.ShimAddDeliveryStop]++
	expect(t, r, http.StatusOK, "")
	if d := r.data().(map[string]any); d["taskId"] != task || d["upgradedFromSingle"] != true || len(d["stops"].([]any)) != 2 {
		t.Fatalf("driver stop: %s", r.raw)
	}
	// A valid key with another driver's token cannot act on the task (403), nor can a staff user (no mobile:submit_trip).
	r = stops(task, drv2Tok, map[string]any{"destination": "bkk-2"})
	calls[app.ShimAddDeliveryStop]++
	expect(t, r, http.StatusForbidden, operations.CodeNotTaskOwner)
	r = stops(task, staffTok, map[string]any{"destination": "bkk-2"})
	calls[app.ShimAddDeliveryStop]++
	expect(t, r, http.StatusForbidden, authz.CodePermissionDenied)
	expect(t, shim(w.public, http.MethodPost, "/v1/mobile/tasks/"+task+"/stops", drvTok, map[string]any{"destination": "x"}),
		http.StatusUnauthorized, "unauthenticated")

	// Latency added by the targets (AC: < 300 ms p95), measured end to end over loopback.
	var meTimes, stopTimes []time.Duration
	for i := range 40 {
		start := time.Now()
		expect(t, me(drvTok, auth.HeaderAPIKey, key), http.StatusOK, "")
		meTimes = append(meTimes, time.Since(start))
		calls[app.ShimSetDriverClaims]++
		start = time.Now()
		expect(t, stops(task, drvTok, map[string]any{"destination": fmt.Sprintf("lat-%02d", i)}), http.StatusOK, "")
		stopTimes = append(stopTimes, time.Since(start))
		calls[app.ShimAddDeliveryStop]++
	}
	for name, ds := range map[string][]time.Duration{"GET /v1/mobile/me": meTimes, "POST /v1/mobile/tasks/{id}/stops": stopTimes} {
		p95 := percentile(ds, 95)
		t.Logf("%s p95 %s over %d calls", name, p95, len(ds))
		if p95 >= 300*time.Millisecond {
			t.Fatalf("%s p95 %s, want < 300ms", name, p95)
		}
	}

	// shim_requests_total counts the authenticated calls per callable (a refused credential is not a shim call).
	for _, callable := range []string{app.ShimSetDriverClaims, app.ShimAddDeliveryStop} {
		if n, _ := shimCount(t, reg, callable); n != calls[callable] {
			t.Fatalf("shim_requests_total{callable=%q} = %v, want %v", callable, n, calls[callable])
		}
	}

	// A revoked key is 401 invalid_token (revoked) on every shim target from the next request.
	expect(t, w.send(http.MethodDelete, "/v1/api-keys/"+keyID, pa, nil), http.StatusNoContent, "")
	for _, r := range []resp{me(drvTok, auth.HeaderAPIKey, key), stops(task, drvTok, map[string]any{"destination": "bkk-9"})} {
		expect(t, r, http.StatusUnauthorized, "invalid_token")
		if reason(r) != auth.ReasonKeyRevoked {
			t.Fatalf("revoked key: %s", r.raw)
		}
	}
}

// shimRequest sends one request with an optional JSON body and decodes the envelope.
func shimRequest(t *testing.T, url, method, bearer string, body any, headers ...string) resp {
	t.Helper()
	var rd io.Reader = bytes.NewReader(nil)
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	out := resp{status: res.StatusCode, raw: raw}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out.body); err != nil {
			t.Fatalf("%s %s: not JSON: %s", method, url, raw)
		}
	}
	if res.StatusCode == http.StatusOK && method == http.MethodGet && res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("%s %s: Cache-Control %q, want no-store", method, url, res.Header.Get("Cache-Control"))
	}
	return out
}

// shimCount reads shim_requests_total{callable} from reg.
func shimCount(t *testing.T, reg *prometheus.Registry, callable string) (float64, bool) {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "shim_requests_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "callable" && l.GetValue() == callable {
					return m.GetCounter().GetValue(), true
				}
			}
		}
	}
	return 0, false
}

func capsOf(d map[string]any) []string {
	var out []string
	for _, v := range d["capabilities"].([]any) {
		out = append(out, v.(string))
	}
	return out
}

// percentile is the nearest-rank p-th percentile of ds.
func percentile(ds []time.Duration, p int) time.Duration {
	s := slices.Clone(ds)
	slices.Sort(s)
	i := (len(s)*p + 99) / 100
	return s[max(i-1, 0)]
}
