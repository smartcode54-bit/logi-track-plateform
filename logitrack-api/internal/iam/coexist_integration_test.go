//go:build integration

package iam_test

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebase/firebasetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/coexist"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp/gcptest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
)

// shadowRelay hands the outbox rows firestore.shadow binds to its consumer, as the relay and the broker do.
type shadowRelay struct {
	w      *world
	shadow *coexist.Shadow
	keys   []string
	last   int64
}

func newShadowRelay(w *world, shadow *coexist.Shadow) *shadowRelay {
	r := &shadowRelay{w: w, shadow: shadow}
	for _, q := range mq.Default.Queues {
		if q.Name == coexist.QueueShadow {
			for _, b := range q.Bindings {
				r.keys = append(r.keys, b.Key)
			}
		}
	}
	if err := w.pool.QueryRow(context.Background(), `SELECT coalesce(max(id), 0) FROM outbox_events`).Scan(&r.last); err != nil {
		w.t.Fatal(err)
	}
	return r
}

// drain delivers the new rows and returns their routing keys.
func (r *shadowRelay) drain() []string {
	r.w.t.Helper()
	ctx := context.Background()
	rows, err := r.w.pool.Query(ctx, `SELECT id, routing_key, payload::text FROM outbox_events WHERE id > $1 ORDER BY id`, r.last)
	if err != nil {
		r.w.t.Fatal(err)
	}
	type event struct{ key, body string }
	var events []event
	for rows.Next() {
		var e event
		if err := rows.Scan(&r.last, &e.key, &e.body); err != nil {
			r.w.t.Fatal(err)
		}
		events = append(events, e)
	}
	rows.Close()
	var keys []string
	for _, e := range events {
		keys = append(keys, e.key)
		if !slices.Contains(r.keys, e.key) {
			continue
		}
		if err := r.shadow.Handle(ctx, &mq.Delivery{RoutingKey: e.key, Body: []byte(e.body)}); err != nil {
			r.w.t.Fatalf("firestore.shadow %s %s: %v", e.key, e.body, err)
		}
	}
	return keys
}

// Review panel (T24S): every write that moves a driver's login queues driver.updated, so firestore.shadow projects the
// driver again and its Firestore authId (by which the installed 3.x app finds its driver, and the Firestore rules let
// a driver read its own data) follows the login PostgreSQL links: a login created for a driver projected before
// (POST /v1/users with driverId, the Firebase uid given by the bridge in the same transaction), an unlink, a link of
// another login. A re-home out of the quarantine tenant projects the driver it moves.
func TestDriverLoginChangesReachTheProjectedDriver(t *testing.T) {
	b := firebasetest.New(t, "logitrack-iam-test")
	w := newWorldWith(t, auth.Firebase{Mode: auth.BridgeWeb, Verifier: b.Verifier(time.Now), Signer: b.ServiceAccount(),
		Accounts: b.Accounts()})
	ta, pa := w.login("ta.o@example.test"), w.login("pa@example.test")
	plan, errs := coexist.ParsePlan("auth,masterdata", "", "")
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	fs := gcptest.New(t, "logitrack-test")
	shadow, err := coexist.NewShadow(coexist.ShadowConfig{Enabled: true, Pool: w.pool, FS: fs.Firestore(), Plan: plan, Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	relay := newShadowRelay(w, shadow)
	authID := func(path string) any {
		t.Helper()
		d, ok := fs.Doc(path)
		if !ok {
			t.Fatalf("no document %s", path)
		}
		return d.Fields["authId"]
	}

	drv := w.id(`INSERT INTO drivers (tenant_id, tenant_source, first_name, last_name, mobile, status)
		VALUES ($1, 'self', 'Somchai', 'Jaidee', '0811111111', 'active') RETURNING id::text`, w.O)
	if err := shadow.Handle(context.Background(), &mq.Delivery{RoutingKey: "driver.created",
		Body: []byte(`{"entity":"driver","id":"` + drv + `"}`)}); err != nil {
		t.Fatal(err)
	}
	path := "drivers/" + drv
	if v := authID(path); v != nil {
		t.Fatalf("a driver without a login has authId %v", v)
	}
	r := w.send(http.MethodPost, "/v1/users", ta, map[string]any{"email": "login.drv@example.test", "displayName": "Somchai",
		"role": "driver", "tenantId": w.O, "driverId": drv})
	expect(t, r, http.StatusCreated, "")
	uid := w.id(`SELECT legacy_auth_uid FROM users WHERE email = 'login.drv@example.test'`)
	if keys := relay.drain(); !slices.Contains(keys, "driver.updated") {
		t.Fatalf("POST /v1/users with a driver queued %v", keys)
	}
	if v := authID(path); v != uid {
		t.Errorf("authId after the login was created: %v, want %s", v, uid)
	}
	login := w.userID("login.drv@example.test")
	expect(t, w.send(http.MethodDelete, "/v1/users/"+login+"/driver-link", ta, nil), http.StatusNoContent, "")
	relay.drain()
	if v := authID(path); v != nil {
		t.Errorf("authId after the unlink: %v", v)
	}
	other := w.user("other.drv@example.test")
	w.exec(`UPDATE users SET legacy_auth_uid = 'fbOther0000000000000000001' WHERE id = $1`, other)
	expect(t, w.send(http.MethodPut, "/v1/users/"+other+"/driver-link", ta, map[string]any{"driverId": drv}), http.StatusNoContent, "")
	relay.drain()
	if v := authID(path); v != "fbOther0000000000000000001" {
		t.Errorf("authId after another login was linked: %v", v)
	}

	// A driver re-homed out of the quarantine tenant is projected (a quarantined row never is).
	q := w.id(`INSERT INTO drivers (tenant_id, tenant_source, first_name, last_name, mobile, status)
		VALUES ($1, 'quarantine', 'Ghost', 'Driver', '0822222222', 'active') RETURNING id::text`, w.Q)
	expect(t, w.send(http.MethodPost, "/v1/tenants/quarantine/rows/drivers/"+q+"/rehome", pa, map[string]any{"tenantId": w.A}),
		http.StatusNoContent, "")
	if keys := relay.drain(); !slices.Contains(keys, "driver.updated") {
		t.Fatalf("the re-home queued %v", keys)
	}
	d, ok := fs.Doc("drivers/" + q)
	if !ok {
		t.Fatal("the re-homed driver was not projected")
	}
	if sub, want := d.Fields["subcontractorId"], w.id(`SELECT coalesce(legacy_doc_id, id::text) FROM tenants WHERE id = $1`, w.A); sub != want {
		t.Errorf("the re-homed driver's subcontractorId %v, want %s", sub, want)
	}
}
