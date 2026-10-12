//go:build integration

package iam_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync/atomic"
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
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx/ratelimit"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/writeback"
)

// The callable shim targets of T32 (issue #52; Appendix B §B.2.21, §B.2.22, Appendix C §C.6.5; R45) as cmd/api mounts
// them (app.MobileGroups) behind the real auth.RequireShim: a cf_shim API key together with the caller's Firebase ID
// token, minted here with the test keys of firebasetest and verified against the securetoken issuer of the test project
// in every bridge mode (the world runs with the bridge off). GET /v1/mobile/me answers setDriverClaims with the driver's
// Firestore doc id; POST /v1/mobile/tasks/{id}/stops lets a driver add stops to its own tasks only (operations
// PostgreSQL-owned here; internal/operations covers the write-back). shim_requests_total{callable} counts every
// authenticated call per callable from 0; the targets add well under 300 ms (p95, in-process).
func TestShimTargets(t *testing.T) {
	fb, reg, w := newShimWorld(t, shimWorldOpts{})
	// Every series exists from the start, so a shim nobody calls reports 0 (R26 retirement evidence).
	for _, callable := range []string{app.ShimSetDriverClaims, app.ShimAddDeliveryStop, app.ShimComputeTripBilling,
		app.ShimMarkBroadcastRead} {
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

// shimWorldOpts vary the world of the shim tests: the request rate limits, and operations Firestore-owned with its
// write-back (nil: PostgreSQL-owned).
type shimWorldOpts struct {
	rateLimit bool
	writeBack *operations.WriteBack
}

// newShimWorld is the world of the shim tests: the /v1/mobile group as cmd/api mounts it (app.MobileGroups) behind the
// real auth.RequireShim, a Firebase ID-token verifier of the test project (firebasetest's local keys) and the shim
// counter on reg.
func newShimWorld(t *testing.T, o shimWorldOpts) (*firebasetest.Backend, *prometheus.Registry, *world) {
	t.Helper()
	fb := firebasetest.New(t, "logitrack-shim-test")
	reg := prometheus.NewRegistry()
	w := newWorldOpts(t, worldOpts{firebase: auth.Firebase{Verifier: fb.Verifier(nil)}, rateLimit: o.rateLimit,
		groups: func(w *world, svc *auth.Service) []ingress.Group {
			if err := svc.Register(reg); err != nil {
				t.Fatal(err)
			}
			owners, err := iam.NewTaskOwners(w.pool)
			if err != nil {
				t.Fatal(err)
			}
			ops, err := operations.New(operations.Deps{Pool: w.pool, Log: zerolog.Nop(), PGOwned: o.writeBack == nil,
				WriteBack: o.writeBack, Owners: owners})
			if err != nil {
				t.Fatal(err)
			}
			return app.MobileGroups(app.APIDeps{Auth: svc, Operations: ops})
		}})
	return fb, reg, w
}

// shimDriver creates a driver of tenant A signed in to Firebase as uid (users.legacy_auth_uid) and returns its drivers
// id.
func (w *world) shimDriver(email, uid, mobile string) string {
	w.t.Helper()
	u := w.user(email)
	w.member(u, w.A, "driver")
	d := w.id(`INSERT INTO drivers (tenant_id, tenant_source, user_id, first_name, last_name, mobile)
		VALUES ($1, 'self', $2, 'ด', 'ร', $3) RETURNING id::text`, w.A, u, mobile)
	w.exec(`UPDATE users SET legacy_auth_uid = $2 WHERE id = $1`, u, uid)
	return d
}

// The one cf_shim key is shared by every driver (a platform key; its rl:apikey:{id} bucket is the overall ceiling).
// A caller Go refuses never spends that budget, and a user calling in a loop spends only its own rl:shim_user bucket
// (T32R review): before, any Firebase sign-in could empty the key's bucket and fail every driver's shim calls with 429.
func TestShimRateLimits(t *testing.T) {
	fb, _, w := newShimWorld(t, shimWorldOpts{rateLimit: true})
	pa := w.login("pa@example.test")
	keyID, key := w.mustKey(pa, map[string]any{"name": "callable shims", "scope": "cf_shim",
		"capabilities": []string{"mobile:view_tasks", "mobile:submit_trip"}})
	w.shimDriver("rl.driver1@example.test", "fbRateDriver0000000000000001", "0831111111")
	drv2 := w.shimDriver("rl.driver2@example.test", "fbRateDriver0000000000000002", "0832222222")
	disabled := w.user("rl.disabled@example.test")
	w.exec(`UPDATE users SET legacy_auth_uid = 'fbRateDisabled00000000000001', status = 'disabled', disabled_at = now() WHERE id = $1`, disabled)
	now := time.Now()
	token := func(uid string) string { return fb.SignIDToken(fb.IDTokenClaims(uid, now.Add(-5*time.Minute), now)) }
	drv1Tok, drv2Tok := token("fbRateDriver0000000000000001"), token("fbRateDriver0000000000000002")
	me := func(bearer string) resp {
		t.Helper()
		return shimRequest(t, w.public+"/v1/mobile/me", http.MethodGet, bearer, nil, auth.HeaderAPIKey, key)
	}
	stops := func(ref, bearer string) resp {
		t.Helper()
		return shimRequest(t, w.public+"/v1/mobile/tasks/"+ref+"/stops", http.MethodPost, bearer,
			map[string]any{"destination": "bkk-1"}, auth.HeaderAPIKey, key)
	}

	// The key's ceiling at 3 a minute: twenty refused callers (no account, a token Go cannot verify, a disabled
	// account) spend nothing of it, so both drivers still get through, and the fourth call is over the ceiling.
	w.exec(`UPDATE api_keys SET rate_per_min = 3 WHERE id = $1`, keyID)
	nobody, disabledTok := token("fbNobody00000000000000000002"), token("fbRateDisabled00000000000001")
	for range 7 {
		expect(t, me(nobody), http.StatusForbidden, auth.CodeNoAccount)
		expect(t, me("not-a-firebase-token"), http.StatusUnauthorized, auth.CodeInvalidToken)
		expect(t, me(disabledTok), http.StatusForbidden, auth.CodeAccountDisabled)
	}
	expect(t, me(drv1Tok), http.StatusOK, "")
	expect(t, me(drv2Tok), http.StatusOK, "")
	expect(t, me(drv2Tok), http.StatusOK, "")
	r := me(drv1Tok)
	expect(t, r, http.StatusTooManyRequests, httpx.CodeResourceExhaust)
	if bucket(r) != ratelimit.APIKey.Name {
		t.Fatalf("over the key's ceiling: %s", r.raw)
	}

	// The ceiling raised (hitl: the production key's rate_per_min): driver 1 calling in a loop on driver 2's task (403
	// not_task_owner) runs out of its own bucket, and driver 2 is not affected.
	w.exec(`UPDATE api_keys SET rate_per_min = 100000 WHERE id = $1`, keyID)
	task := w.id(`INSERT INTO tasks (tenant_id, tenant_source, task_type, status, plan_at, source_hub_raw, destination_raw,
		driver_id, legacy_doc_id) VALUES ($1, 'driver', 'first_mile', 'assigned', now(), 'SRC', 'DST', $2, 'fsRateTask1')
		RETURNING id::text`, w.A, drv2)
	limited := false
	for i := 0; i < 2*ratelimit.ShimUser.Default.Count && !limited; i++ {
		r := stops(task, drv1Tok)
		switch {
		case r.status == http.StatusTooManyRequests && bucket(r) == ratelimit.ShimUser.Name:
			limited = true
		case r.status != http.StatusForbidden || r.code() != operations.CodeNotTaskOwner:
			t.Fatalf("driver 1 on driver 2's task, call %d: %d %s", i, r.status, r.raw)
		}
	}
	if !limited {
		t.Fatalf("driver 1 was never limited by %s", ratelimit.ShimUser.Name)
	}
	expect(t, me(drv1Tok), http.StatusTooManyRequests, httpx.CodeResourceExhaust)
	expect(t, me(drv2Tok), http.StatusOK, "")
	if r := stops("fsRateTask1", drv2Tok); r.status != http.StatusOK {
		t.Fatalf("driver 2's own task while driver 1 is limited: %d %s", r.status, r.raw)
	}
}

// While operations is Firestore-owned (P2-P7b, when the addDeliveryStop shim is on) the target answers right after
// the Firestore commit, through the real auth.RequireShim: the mirror's acknowledgement, which takes seconds, is never
// awaited for a cf_shim call (T32R review), so the added latency stays under 300 ms p95.
func TestShimStopWriteBack(t *testing.T) {
	mem := writeback.NewMemory()
	acks := &neverAcks{}
	fb, _, w := newShimWorld(t, shimWorldOpts{writeBack: &operations.WriteBack{Writer: mem, Acks: acks, Wait: operations.DefaultMirrorWait}})
	pa := w.login("pa@example.test")
	_, key := w.mustKey(pa, map[string]any{"name": "callable shims", "scope": "cf_shim",
		"capabilities": []string{"mobile:view_tasks", "mobile:submit_trip"}})
	drv := w.shimDriver("wb.driver@example.test", "fbWriteBackDriver00000000001", "0841111111")
	task := w.id(`INSERT INTO tasks (tenant_id, tenant_source, task_type, status, plan_at, source_hub_raw, destination_raw,
		driver_id, legacy_doc_id) VALUES ($1, 'driver', 'first_mile', 'assigned', now(), 'SRC', 'SOCE', $2, 'fsWbTask1')
		RETURNING id::text`, w.A, drv)
	mem.Put("tasks", "fsWbTask1", map[string]any{"destination": "SOCE", "isMultiDelivery": false}, time.Now())
	now := time.Now()
	tok := fb.SignIDToken(fb.IDTokenClaims("fbWriteBackDriver00000000001", now.Add(-5*time.Minute), now))

	var took []time.Duration
	for i := range 20 {
		start := time.Now()
		r := shimRequest(t, w.public+"/v1/mobile/tasks/fsWbTask1/stops", http.MethodPost, tok,
			map[string]any{"destination": fmt.Sprintf("wb-%02d", i)}, auth.HeaderAPIKey, key)
		took = append(took, time.Since(start))
		expect(t, r, http.StatusAccepted, "")
		if d := r.data().(map[string]any); d["taskId"] != task || d["pending"] != true || d["upgradedFromSingle"] != (i == 0) {
			t.Fatalf("stop %d: %s", i, r.raw)
		}
	}
	if acks.calls.Load() != 0 {
		t.Fatalf("a shim call waited for the mirror (%d)", acks.calls.Load())
	}
	if doc, _ := mem.Doc("tasks", "fsWbTask1"); len(doc["deliveryStops"].([]any)) != 21 || doc["isMultiDelivery"] != true {
		t.Fatalf("document %v", doc)
	}
	p95 := percentile(took, 95)
	t.Logf("POST /v1/mobile/tasks/{id}/stops (write-back, mirror never acknowledging) p95 %s over %d calls", p95, len(took))
	if p95 >= 300*time.Millisecond {
		t.Fatalf("p95 %s, want < 300ms", p95)
	}
}

// neverAcks is a mirror that never acknowledges: WaitMirrorAck blocks for the whole timeout, as cache.WaitMirrorAck
// does while the mirror is behind.
type neverAcks struct{ calls atomic.Int32 }

func (a *neverAcks) WaitMirrorAck(ctx context.Context, _, _ string, _ time.Time, timeout time.Duration) (bool, error) {
	a.calls.Add(1)
	select {
	case <-time.After(timeout):
		return false, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// bucket is details.bucket of a 429.
func bucket(r resp) string {
	e, _ := r.body["error"].(map[string]any)
	d, _ := e["details"].(map[string]any)
	s, _ := d["bucket"].(string)
	return s
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
