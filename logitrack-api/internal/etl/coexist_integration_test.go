//go:build integration

// Review-panel regressions of T24S (#44) on postgres:18-alpine and the in-process Firestore stand-in: the minting
// UPDATE's updated_at reaches the document, a login link moves the projected authId, denormalised fields follow the
// row they copy, a distance pair follows its hub's code, customerDriverIds keeps its stored spelling, a mirrored
// maintenance field re-projects its truck, the mint commits before Firestore sees a document, and one document never
// stops a sweep (an unknown literal keeps PostgreSQL's value, an unloadable billable document is held). Also the
// users field mirror (lastLogin*, fcmTokens).
package etl

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp"
)

// project runs the projections one firestore.shadow event names and adds up their counts.
func (h *syncHarness) project(proj *Projector, key, body string) ProjectStats {
	h.t.Helper()
	targets, err := ShadowTargets(key, []byte(body))
	if err != nil {
		h.t.Fatalf("%s: %v", key, err)
	}
	var st ProjectStats
	for _, tg := range targets {
		s, err := proj.Project(h.ctx, tg)
		if err != nil {
			h.t.Fatalf("project %s %+v: %v", key, tg, err)
		}
		st.add(s)
	}
	return st
}

func (h *syncHarness) reconcile(p MirrorPlan, proj *Projector) *Reconciliation {
	h.t.Helper()
	rec, err := h.e.ReconcileSync(h.ctx, h.client, SyncReconcileOptions{Mirror: p, Projection: proj.Collections})
	if err != nil {
		h.t.Fatal(err)
	}
	return rec
}

func mismatches(rec *Reconciliation) string {
	var out []string
	for _, c := range rec.Mismatches() {
		out = append(out, c.Name+" "+c.Key+": "+c.Note)
	}
	if len(out) == 0 && !rec.Match {
		b, _ := json.Marshal(rec)
		out = append(out, string(b))
	}
	slices.Sort(out)
	return strings.Join(out, "\n")
}

// copyRow inserts a copy of the row of table whose legacy_doc_id is legacy, with set applied (a new id and no
// legacy_doc_id unless set says otherwise): a row born in PostgreSQL as the Go services write it.
func (h *syncHarness) copyRow(table, legacy string, set map[string]any) uuid.UUID {
	h.t.Helper()
	patch := map[string]any{"legacy_doc_id": nil}
	for k, v := range set {
		patch[k] = v
	}
	b, err := json.Marshal(patch)
	if err != nil {
		h.t.Fatal(err)
	}
	t := pgx.Identifier{table}.Sanitize()
	return h.id(`INSERT INTO `+t+` SELECT (jsonb_populate_record(NULL::`+t+`, to_jsonb(r) || jsonb_build_object('id', uuidv7()) || $2::jsonb)).*
		FROM `+t+` r WHERE r.legacy_doc_id = $1 RETURNING id`, legacy, string(b))
}

// outboxSince lists the outbox rows after id as (routing key, payload).
func (h *syncHarness) outboxSince(after int64) ([][2]string, int64) {
	h.t.Helper()
	rows, err := h.pool.Query(h.ctx, `SELECT id, routing_key, payload::text FROM outbox_events WHERE id > $1 ORDER BY id`, after)
	if err != nil {
		h.t.Fatal(err)
	}
	defer rows.Close()
	var out [][2]string
	last := after
	for rows.Next() {
		var id int64
		var key, payload string
		if err := rows.Scan(&id, &key, &payload); err != nil {
			h.t.Fatal(err)
		}
		out = append(out, [2]string{key, payload})
		last = id
	}
	return out, last
}

func (h *syncHarness) lastOutbox() int64 {
	h.t.Helper()
	var id int64
	if err := h.pool.QueryRow(h.ctx, `SELECT coalesce(max(id), 0) FROM outbox_events`).Scan(&id); err != nil {
		h.t.Fatal(err)
	}
	return id
}

// Review panel (T24S): the minting UPDATE fires trg_set_updated_at; the document carries the updated_at it left, so
// rows born in PostgreSQL reconcile right after their first projection, and a second projection writes nothing.
func TestPostgreSQLBornRowsReconcileAfterTheirMint(t *testing.T) {
	h := newSyncHarness(t)
	o := owned("auth", "masterdata")
	p, proj := h.plan(o), h.projector(o)
	if _, err := proj.ProjectAll(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.sweep(p)
	if rec := h.reconcile(p, proj); !rec.Match {
		t.Fatalf("baseline:\n%s", mismatches(rec))
	}
	hub := h.id(`INSERT INTO hubs (source_id, name_th, network) VALUES ('HUBNEW', 'ฮับใหม่', 'SPX') RETURNING id`)
	h.exec(`INSERT INTO hub_soc_distances (hub_id, soc_key, direction, network, distance_m, distance_km, duration_s, duration_min,
		hub_lat, hub_lng, soc_lat, soc_lng) VALUES ($1, 'SOCE', 'hub_to_soc', 'SPX', 5000, 5, 630, 10.5, 13.7, 100.5, 13.8, 100.7)`, hub)
	customer := h.copyRow("customers", "cust_cjsf", map[string]any{"code": "NEWC", "logo_file_id": nil, "line_group_id": nil})
	h.exec(`INSERT INTO billing_parties (kind, customer_id) VALUES ('customer', $1)`, customer)
	company := h.copyRow("companies", "co_alpha", map[string]any{"logo_file_id": nil, "stamp_file_id": nil, "signature_file_id": nil})
	carrier := h.id(`INSERT INTO tenants (kind, name_th, legal_type, contractor_tenant_id) VALUES ('carrier', 'ขนส่งใหม่', 'company',
		'01900000-0000-7000-8000-000000000001') RETURNING id`)
	h.exec(`INSERT INTO billing_parties (kind, tenant_id) VALUES ('tenant', $1)`, carrier)
	truck := h.copyRow("trucks", "trk_alpha1", map[string]any{"license_plate": "NEW-0001", "gps_vehicle_id": nil, "vin": nil,
		"engine_number": nil, "active_maintenance_id": nil})
	asg := h.copyRow("truck_assignments", "ta_2", map[string]any{"truck_id": truck})
	driver := h.id(`SELECT driver_id FROM truck_assignments WHERE id = $1`, asg)
	events := [][2]string{
		{"hubs.changed", `{"ids":["` + hub.String() + `"]}`},
		{"customers.changed", `{"ids":["` + customer.String() + `"]}`},
		{"companies.changed", `{"ids":["` + company.String() + `"]}`},
		{"tenant.created", `{"tenantId":"` + carrier.String() + `"}`},
		{"truck.created", `{"entity":"truck","id":"` + truck.String() + `"}`},
		{"assignment.created", `{"entity":"truck_assignment","id":"` + asg.String() + `","truckId":"` + truck.String() + `","driverId":"` +
			driver.String() + `"}`},
	}
	for _, e := range events {
		if st := h.project(proj, e[0], e[1]); st.Written == 0 {
			t.Errorf("%s wrote nothing: %+v", e[0], st)
		}
	}
	for _, path := range []string{"hubs/" + hub.String(), "hub_soc_distances/HUBNEW_SOCE", "customers/" + customer.String(),
		"companies/" + company.String(), "subcontractors/" + carrier.String(), "trucks/" + truck.String(), "truckAssignment/" + asg.String()} {
		if _, ok := h.fs.Doc(path); !ok {
			t.Errorf("no document %s", path)
		}
	}
	// A truck born in Go starts in Firestore with its form's status and mileage (the maintenance flow keeps them from
	// then on), and the sweep's first sight of its document clears nothing in PostgreSQL.
	if f := h.doc("trucks/" + truck.String()); f["truckStatus"] != "Available" {
		t.Errorf("the new truck's status %v", f["truckStatus"])
	}
	h.sweep(p)
	if v := h.scalar(`SELECT n.current_mileage IS NOT DISTINCT FROM o.current_mileage AND n.status = o.status FROM trucks n, trucks o
		WHERE n.id = $1 AND o.legacy_doc_id = 'trk_alpha1'`, truck); v != true {
		t.Error("the first sweep of the new truck's document changed its maintenance columns")
	}
	if rec := h.reconcile(p, proj); !rec.Match {
		t.Fatalf("after the first projection of rows born in PostgreSQL:\n%s", mismatches(rec))
	}
	for _, e := range events {
		if st := h.project(proj, e[0], e[1]); st.Written != 0 {
			t.Errorf("%s projected again wrote %+v", e[0], st)
		}
	}
}

// Review panel (T24S): the projected authId follows the login PostgreSQL links. A re-link moves it to the new login's
// uid, an unlink removes it, a P1 uid no login holds stays, another driver's P1 uid stays (a duplicate the steward
// resolves), and the token a device writes on a document still naming the former login is not stored for the new one.
func TestProjectedAuthIdFollowsTheLink(t *testing.T) {
	h := newSyncHarness(t)
	o := owned("auth", "masterdata")
	p, proj := h.plan(o), h.projector(o)
	if _, err := proj.ProjectAll(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.sweep(p)
	link := func(email, uid, legacyDriver string) uuid.UUID {
		t.Helper()
		var u uuid.UUID
		if uid == "" {
			u = h.id(`INSERT INTO users (email) VALUES ($1) RETURNING id`, email)
		} else {
			u = h.id(`INSERT INTO users (legacy_auth_uid, email) VALUES ($1, $2) RETURNING id`, uid, email)
		}
		h.exec(`INSERT INTO memberships (user_id, tenant_id, role) SELECT $1, tenant_id, 'driver' FROM drivers WHERE legacy_doc_id = $2`, u, legacyDriver)
		h.exec(`UPDATE drivers SET user_id = NULL WHERE legacy_doc_id = $1`, legacyDriver)
		h.exec(`UPDATE drivers SET user_id = $1 WHERE legacy_doc_id = $2`, u, legacyDriver)
		return u
	}
	changed := func(legacyDriver string) {
		t.Helper()
		id := h.id(`SELECT id FROM drivers WHERE legacy_doc_id = $1`, legacyDriver)
		h.project(proj, "driver.updated", `{"entity":"driver","id":"`+id.String()+`","changed":["userId"]}`)
	}
	authID := func(path string) any { return h.doc(path)["authId"] }

	// P1: drv_own's uid_own login is linked; projecting changes nothing.
	link("a@example.test", "uid_own", "drv_own")
	changed("drv_own")
	if authID("drivers/drv_own") != "uid_own" {
		t.Fatalf("authId %v", authID("drivers/drv_own"))
	}
	// Re-linked to B in Go. Before the event is projected, A's device (which still finds the document) writes a token.
	b := link("b@example.test", "uid_b", "drv_own")
	h.fs.Merge("drivers/drv_own", map[string]any{"fcmToken": "tok-of-a-device"})
	h.sweep(p)
	if n := h.scalar(`SELECT count(*) FROM device_tokens WHERE token = 'tok-of-a-device'`); n != int64(0) {
		t.Errorf("a token written under the former login was stored for the new one (%v rows)", n)
	}
	if rec := h.reconcile(p, proj); rec.Match || !strings.Contains(mismatches(rec), "drivers/drv_own authId") {
		t.Errorf("the stale authId is not reported:\n%s", mismatches(rec))
	}
	changed("drv_own")
	if authID("drivers/drv_own") != "uid_b" {
		t.Fatalf("authId after the re-link %v", authID("drivers/drv_own"))
	}
	h.fs.Merge("drivers/drv_own", map[string]any{"fcmToken": "tok-of-b-device"})
	h.sweep(p)
	if v := h.scalar(`SELECT user_id = $1 FROM device_tokens WHERE token = 'tok-of-b-device'`, b); v != true {
		t.Errorf("the new login's token: %v", v)
	}
	// Unlinked: no account finds the driver.
	h.exec(`UPDATE drivers SET user_id = NULL WHERE legacy_doc_id = 'drv_own'`)
	changed("drv_own")
	if _, has := h.doc("drivers/drv_own")["authId"]; has {
		t.Errorf("authId after the unlink %v", authID("drivers/drv_own"))
	}
	// drv_alpha's P1 uid matched no login (user_unresolved): it stays until a login is linked, then follows it.
	changed("drv_alpha")
	if authID("drivers/drv_alpha") != "uid_alpha" {
		t.Errorf("an unresolved P1 uid: %v", authID("drivers/drv_alpha"))
	}
	link("c@example.test", "uid_c", "drv_alpha")
	changed("drv_alpha")
	if authID("drivers/drv_alpha") != "uid_c" {
		t.Errorf("a Go login on an unresolved-uid driver: %v", authID("drivers/drv_alpha"))
	}
	// A login without a Firebase uid yet (it gets one, and an event, from the bridge): no account finds the driver.
	link("nouid@example.test", "", "drv_alpha")
	changed("drv_alpha")
	if _, has := h.doc("drivers/drv_alpha")["authId"]; has {
		t.Errorf("a login without a uid: %v", authID("drivers/drv_alpha"))
	}
	// A driver born in Go, linked then unlinked; and a document holding another driver's P1 uid keeps it.
	d := h.id(`INSERT INTO drivers (tenant_id, tenant_source, first_name, last_name, mobile, status)
		VALUES ('01900000-0000-7000-8000-000000000001', 'self', 'New', 'Driver', '0800000001', 'active') RETURNING id`)
	h.project(proj, "driver.created", `{"entity":"driver","id":"`+d.String()+`"}`)
	path := "drivers/" + d.String()
	u := h.id(`INSERT INTO users (legacy_auth_uid, email) VALUES ('uid_d', 'd@example.test') RETURNING id`)
	h.exec(`INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, '01900000-0000-7000-8000-000000000001', 'driver')`, u)
	h.exec(`UPDATE drivers SET user_id = $1 WHERE id = $2`, u, d)
	h.project(proj, "driver.updated", `{"entity":"driver","id":"`+d.String()+`"}`)
	if authID(path) != "uid_d" {
		t.Errorf("Go-born driver linked: %v", authID(path))
	}
	h.exec(`UPDATE drivers SET user_id = NULL WHERE id = $1`, d)
	h.project(proj, "driver.updated", `{"entity":"driver","id":"`+d.String()+`"}`)
	if _, has := h.doc(path)["authId"]; has {
		t.Errorf("Go-born driver unlinked: %v", authID(path))
	}
	h.fs.Merge(path, map[string]any{"authId": "uid_broker"}) // drv_broker's P1 uid
	h.project(proj, "driver.updated", `{"entity":"driver","id":"`+d.String()+`"}`)
	if authID(path) != "uid_broker" {
		t.Errorf("another driver's P1 uid was removed: %v", authID(path))
	}
}

// Review panel (T24S): a distance pair follows its hub's code. A rename mints the pair again under the new natural id
// and empties the former document, a recompute keeps projecting there, a hub that takes the freed code gets its own
// pair, a hub whose loaded pairs spell its code in lower case gets new pairs in that spelling, and a code that is no
// valid document id leaves its pair unprojected without stopping the projection.
func TestDistancePairsFollowTheirHubCode(t *testing.T) {
	h := newSyncHarness(t)
	o := owned("auth", "masterdata")
	p, proj := h.plan(o), h.projector(o)
	if _, err := proj.ProjectAll(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.sweep(p)
	hubA := h.id(`SELECT id FROM hubs WHERE source_id = 'HUBA'`)
	km := h.doc("hub_soc_distances/HUBA_SOCE")["distanceKm"]
	h.exec(`UPDATE hubs SET source_id = 'HUBQ' WHERE id = $1`, hubA)
	h.project(proj, "hubs.changed", `{"ids":["`+hubA.String()+`"]}`)
	if f := h.doc("hub_soc_distances/HUBQ_SOCE"); f["distanceKm"] != km || f["hubId"] != "HUBQ" {
		t.Fatalf("the renamed hub's pair %v", f)
	}
	if f := h.doc("hub_soc_distances/HUBA_SOCE"); f["distanceKm"] != nil || f["durationMinutes"] != nil {
		t.Errorf("the former document still serves the pair: %v", f)
	}
	if _, ok := h.fs.Doc("soc_hub_distances/SOCE_HUBQ"); !ok {
		t.Error("the SOC->hub pair did not move")
	}
	if v := h.scalar(`SELECT string_agg(legacy_doc_id, ',' ORDER BY direction) FROM hub_soc_distances WHERE hub_id = $1`, hubA); v != "HUBQ_SOCE,SOCE_HUBQ" {
		t.Errorf("minted ids %v", v)
	}
	if f := h.doc("hubs/hub_a1"); f["source_id"] != "HUBQ" {
		t.Errorf("hub document %v", f["source_id"])
	}
	h.sweep(p)
	if rec := h.reconcile(p, proj); !rec.Match {
		t.Errorf("after the rename:\n%s", mismatches(rec))
	}
	// The distance job updates the pair in place: it is still projected under the new id.
	h.exec(`UPDATE hub_soc_distances SET distance_m = 7000, distance_km = 7 WHERE hub_id = $1 AND direction = 'hub_to_soc'`, hubA)
	h.project(proj, "settings.changed", `{"key":"distances_last_calculated"}`)
	if f := h.doc("hub_soc_distances/HUBQ_SOCE"); f["distanceKm"] != int64(7) {
		t.Errorf("after a recompute %v", f["distanceKm"])
	}
	// A new hub takes the freed code: its pair is minted and served under HUBA_SOCE.
	newA := h.id(`INSERT INTO hubs (source_id, name_th, network) VALUES ('HUBA', 'ฮับเอใหม่', 'SPX') RETURNING id`)
	h.exec(`INSERT INTO hub_soc_distances (hub_id, soc_key, direction, network, distance_m, distance_km, duration_s, duration_min,
		hub_lat, hub_lng, soc_lat, soc_lng) VALUES ($1, 'SOCE', 'hub_to_soc', 'SPX', 3000, 3, 300, 5, 13.7, 100.5, 13.8, 100.7)`, newA)
	h.project(proj, "hubs.changed", `{"ids":["`+newA.String()+`"]}`)
	if f := h.doc("hub_soc_distances/HUBA_SOCE"); f["distanceKm"] != int64(3) || f["hubId"] != "HUBA" {
		t.Errorf("the hub that took the code: %v", f)
	}
	// A loaded hub whose pairs spell its code in lower case: a pair computed in Go keeps that spelling.
	low := h.id(`INSERT INTO hubs (source_id, name_th, network) VALUES ('HUBL', 'ฮับแอล', 'SPX') RETURNING id`)
	h.exec(`INSERT INTO hub_soc_distances (hub_id, soc_key, direction, network, distance_m, distance_km, duration_s, duration_min,
		hub_lat, hub_lng, soc_lat, soc_lng, legacy_doc_id) VALUES ($1, 'SOCN', 'hub_to_soc', 'SPX', 1000, 1, 60, 1, 13.7, 100.5, 13.8, 100.7, 'hubl_SOCN')`, low)
	h.exec(`INSERT INTO hub_soc_distances (hub_id, soc_key, direction, network, distance_m, distance_km, duration_s, duration_min,
		hub_lat, hub_lng, soc_lat, soc_lng) VALUES ($1, 'SOCW', 'hub_to_soc', 'SPX', 2000, 2, 120, 2, 13.7, 100.5, 13.8, 100.7)`, low)
	h.project(proj, "hubs.changed", `{"ids":["`+low.String()+`"]}`)
	if _, ok := h.fs.Doc("hub_soc_distances/hubl_SOCW"); !ok {
		t.Error("a new pair of a lower-case hub is not minted in its spelling")
	}
	// A code that is no valid document id: its pair is skipped, the other pairs and the metadata are projected.
	bad := h.id(`INSERT INTO hubs (source_id, name_th, network) VALUES ('A/B', 'ทับ', 'SPX') RETURNING id`)
	good := h.id(`INSERT INTO hubs (source_id, name_th, network) VALUES ('ZGOOD', 'ดี', 'SPX') RETURNING id`)
	for _, hub := range []uuid.UUID{bad, good} {
		h.exec(`INSERT INTO hub_soc_distances (hub_id, soc_key, direction, network, distance_m, distance_km, duration_s, duration_min,
			hub_lat, hub_lng, soc_lat, soc_lng) VALUES ($1, 'SOCE', 'hub_to_soc', 'SPX', 1000, 1, 60, 1, 13.7, 100.5, 13.8, 100.7)`, hub)
	}
	h.exec(`INSERT INTO settings (key, value) VALUES ('distances_last_calculated', '{"at":"2026-10-05T02:00:00Z"}')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	st := h.project(proj, "settings.changed", `{"key":"distances_last_calculated"}`)
	if st.Skipped == 0 {
		t.Errorf("the pair of a code holding '/' was not skipped: %+v", st)
	}
	if _, ok := h.fs.Doc("hub_soc_distances/ZGOOD_SOCE"); !ok {
		t.Error("a valid pair of the same batch was not projected")
	}
	if v := h.scalar(`SELECT legacy_doc_id IS NULL FROM hub_soc_distances WHERE hub_id = $1`, bad); v != true {
		t.Errorf("an invalid id was minted: %v", v)
	}
	if f := h.doc("metadata/distances_last_calculated"); f["timestamp"] != (dump.Timestamp{Time: time.Date(2026, 10, 5, 2, 0, 0, 0, time.UTC)}) {
		t.Errorf("metadata %v", f)
	}
	if _, err := proj.ProjectAll(h.ctx); err != nil {
		t.Errorf("etl project stops on the invalid code: %v", err)
	}
}

// Review panel (T24S): the fields a document copies from another row follow it. A customer rename reaches the hubs
// linked to it (linkedCustomerName, "CODE — Name"), a customer code change moves the drivers' customerDriverIds entry,
// and a carrier rename reaches the hubs linked to the carrier; the reconciliation matches after each projection.
func TestDenormalisedFieldsFollowTheirSource(t *testing.T) {
	h := newSyncHarness(t)
	o := owned("auth", "masterdata")
	p, proj := h.plan(o), h.projector(o)
	if _, err := proj.ProjectAll(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.sweep(p)
	// The legacy form's label (the customers document's code spelling) is the same label.
	h.fs.Merge("hubs/hub_a1", map[string]any{"linkedCustomerName": "spx — Shopee Express"})
	hub := h.id(`SELECT id FROM hubs WHERE source_id = 'HUBA'`)
	if st := h.project(proj, "hubs.changed", `{"ids":["`+hub.String()+`"]}`); st.Written != 0 {
		t.Errorf("the legacy label was rewritten: %+v", st)
	}
	customer := h.id(`UPDATE customers SET name = 'Shopee Express Thailand' WHERE legacy_doc_id = 'cust_spx' RETURNING id`)
	h.project(proj, "customers.changed", `{"ids":["`+customer.String()+`"]}`)
	if v := h.doc("hubs/hub_a1")["linkedCustomerName"]; v != "SPX — Shopee Express Thailand" {
		t.Errorf("label after the rename %v", v)
	}
	h.sweep(p)
	if rec := h.reconcile(p, proj); !rec.Match {
		t.Errorf("after the rename:\n%s", mismatches(rec))
	}
	h.exec(`UPDATE customers SET code = 'SPXN' WHERE id = $1`, customer)
	h.project(proj, "customers.changed", `{"ids":["`+customer.String()+`"]}`)
	codes, _ := h.doc("drivers/drv_own")["customerDriverIds"].(map[string]any)
	if fmt.Sprint(codes) != fmt.Sprint(map[string]any{"SPXN": map[string]any{"appId": "A-001"}, "NOPE": map[string]any{"x": "1"}}) {
		t.Errorf("customerDriverIds after the code change %v", codes)
	}
	if v := h.doc("customers/cust_spx")["code"]; v != "SPXN" {
		t.Errorf("customer code %v", v)
	}
	if v := h.doc("hubs/hub_a1")["linkedCustomerName"]; v != "SPXN — Shopee Express Thailand" {
		t.Errorf("label after the code change %v", v)
	}
	h.sweep(p)
	if rec := h.reconcile(p, proj); !rec.Match {
		t.Errorf("after the code change:\n%s", mismatches(rec))
	}
	// A hub linked to a carrier follows the carrier's name.
	alpha := h.id(`SELECT id FROM tenants WHERE legacy_doc_id = 'sub_alpha'`)
	h.exec(`INSERT INTO billing_parties (kind, tenant_id) SELECT 'tenant', $1 WHERE NOT EXISTS (SELECT 1 FROM billing_parties WHERE tenant_id = $1)`, alpha)
	hubB := h.id(`UPDATE hubs SET linked_party_id = (SELECT id FROM billing_parties WHERE tenant_id = $1) WHERE source_id = 'HUBB' RETURNING id`, alpha)
	h.project(proj, "hubs.changed", `{"ids":["`+hubB.String()+`"]}`)
	h.exec(`UPDATE tenants SET name_th = 'อัลฟ่า ขนส่ง ใหม่' WHERE id = $1`, alpha)
	h.project(proj, "tenant.updated", `{"tenantId":"`+alpha.String()+`"}`)
	if v, _ := h.doc("hubs/hub_b")["linkedCustomerName"].(string); !strings.HasSuffix(v, "อัลฟ่า ขนส่ง ใหม่") {
		t.Errorf("label after the carrier rename %q", v)
	}
	h.sweep(p)
	if rec := h.reconcile(p, proj); !rec.Match {
		t.Errorf("after the carrier rename:\n%s", mismatches(rec))
	}
}

// Review panel (T24S): customerDriverIds is compared and written per entry, under the stored key whatever its case
// (Cloud Functions look it up by the customers document's code exactly, which keeps its legacy spelling), and an
// entry of a code no customer has stays.
func TestCustomerDriverIdsKeepTheirSpelling(t *testing.T) {
	h := newSyncHarness(t)
	o := owned("auth", "masterdata")
	proj := h.projector(o)
	if _, err := proj.ProjectAll(h.ctx); err != nil {
		t.Fatal(err)
	}
	driver := h.id(`SELECT id FROM drivers WHERE legacy_doc_id = 'drv_own'`)
	h.fs.Merge("drivers/drv_own", map[string]any{"customerDriverIds": map[string]any{"spx": map[string]any{"appId": "A-001"},
		"NOPE": map[string]any{"x": "1"}}})
	if st := h.project(proj, "driver.updated", `{"entity":"driver","id":"`+driver.String()+`"}`); st.Written != 0 {
		t.Errorf("an equivalent map under a lower-case key was rewritten: %+v", st)
	}
	h.exec(`UPDATE driver_customer_codes SET value = 'A-002' WHERE driver_id = $1`, driver)
	h.project(proj, "driver.updated", `{"entity":"driver","id":"`+driver.String()+`"}`)
	codes, _ := h.doc("drivers/drv_own")["customerDriverIds"].(map[string]any)
	if fmt.Sprint(codes) != fmt.Sprint(map[string]any{"spx": map[string]any{"appId": "A-002"}, "NOPE": map[string]any{"x": "1"}}) {
		t.Errorf("customerDriverIds after a Go edit %v", codes)
	}
	// resolveDriverCustomerCode (lineMessage.ts): the entry is found by the customers document's code.
	code, _ := h.doc("customers/cust_spx")["code"].(string)
	if entry, _ := codes[code].(map[string]any); entry["appId"] != "A-002" {
		t.Errorf("lookup by customers.code %q: %v", code, codes)
	}
}

// Review panel (T24S): a maintenance-flow write the field mirror applies moves trucks.updated_at; the sweep queues
// truck.updated, whose projection brings the document's updatedAt along, so the reconciliation matches and the next
// sweep applies nothing (no echo).
func TestMirroredMaintenanceFieldsReprojectTheTruck(t *testing.T) {
	h := newSyncHarness(t)
	o := owned("auth", "masterdata")
	p, proj := h.plan(o), h.projector(o)
	if _, err := proj.ProjectAll(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.sweep(p)
	mark := h.lastOutbox()
	h.fs.Merge("trucks/trk_own1", map[string]any{"truckStatus": "maintenance", "currentMileage": int64(130000),
		"updatedAt": dump.Timestamp{Time: time.Date(2026, 10, 3, 1, 0, 0, 0, time.UTC)}})
	h.sweep(p)
	events, last := h.outboxSince(mark)
	if len(events) != 1 || events[0][0] != "truck.updated" || !strings.Contains(events[0][1], "currentMileage") {
		t.Fatalf("events of the sweep %v", events)
	}
	if rec := h.reconcile(p, proj); rec.Match {
		t.Error("the moved updated_at is not reported before the projection")
	}
	h.project(proj, events[0][0], events[0][1])
	h.sweep(p)
	if rec := h.reconcile(p, proj); !rec.Match {
		t.Errorf("after the projection:\n%s", mismatches(rec))
	}
	if more, _ := h.outboxSince(last); len(more) != 0 {
		t.Errorf("the projected write came back as %v", more)
	}
	if f := h.doc("trucks/trk_own1"); f["truckStatus"] != "maintenance" || f["currentMileage"] != int64(130000) {
		t.Errorf("the maintenance flow's values were overwritten: %v", f)
	}
}

// committedMint checks at each write that the row of a projected document id is committed: a sweep that reads the
// document always finds its row.
type committedMint struct {
	FirestoreWriter
	h *syncHarness
}

func (c committedMint) Patch(ctx context.Context, path string, fields map[string]any, mask []string, pre gcp.Precondition) (dump.Doc, error) {
	table := map[string]string{"hubs": "hubs", "drivers": "drivers", "trucks": "trucks", "customers": "customers"}
	if coll, id, ok := strings.Cut(path, "/"); ok && table[coll] != "" {
		var n int
		if err := c.h.pool.QueryRow(ctx, `SELECT count(*) FROM `+table[coll]+` WHERE legacy_doc_id = $1`, id).Scan(&n); err != nil || n != 1 {
			c.h.t.Errorf("%s written before its row's id was committed (%d rows, %v)", path, n, err)
		}
	}
	return c.FirestoreWriter.Patch(ctx, path, fields, mask, pre)
}

// Review panel (T24S): the minting transaction commits before Firestore sees the document; and a rejection a sweep
// raised while it could not find the row (a document read before its row) is resolved once the row is found.
func TestMintCommitsBeforeTheWriteAndStaleRejectionsHeal(t *testing.T) {
	h := newSyncHarness(t)
	o := owned("auth", "masterdata")
	p, proj := h.plan(o), h.projector(o)
	proj.FS = committedMint{FirestoreWriter: h.client, h: h}
	hub := h.id(`INSERT INTO hubs (source_id, name_th, network) VALUES ('HUBM', 'มินต์', 'SPX') RETURNING id`)
	driver := h.id(`INSERT INTO drivers (tenant_id, tenant_source, first_name, last_name, mobile, status)
		VALUES ('01900000-0000-7000-8000-000000000001', 'self', 'Mint', 'Driver', '0800000002', 'active') RETURNING id`)
	// Documents a sweep reads before the rows' ids exist: rejected.
	h.fs.Set("hubs/"+hub.String(), map[string]any{"source_id": "HUBM", "source_name_th": "เก่า"})
	h.fs.Set("drivers/"+driver.String(), map[string]any{"firstName": "Mint", "lastName": "Driver", "mobile": "0800000002"})
	h.sweep(p)
	open := func(path string) any {
		return h.scalar(`SELECT count(*) FROM etl.quarantine WHERE doc_path = $1 AND resolved_at IS NULL`, path)
	}
	if open("hubs/"+hub.String()) != int64(1) || open("drivers/"+driver.String()) != int64(1) {
		t.Fatal("the documents of rows without ids were not rejected")
	}
	h.project(proj, "hubs.changed", `{"ids":["`+hub.String()+`"]}`)
	h.project(proj, "driver.created", `{"entity":"driver","id":"`+driver.String()+`"}`)
	h.sweep(p)
	for _, path := range []string{"hubs/" + hub.String(), "drivers/" + driver.String()} {
		if open(path) != int64(0) {
			t.Errorf("%s keeps its stale finding", path)
		}
		if v := h.scalar(`SELECT status FROM etl.source_docs WHERE doc_path = $1`, path); v != "loaded" {
			t.Errorf("%s: %v", path, v)
		}
	}
}

// Review panel (T24S): one document never stops a sweep. An unknown status on a priced trip keeps PostgreSQL's
// status (status_out_of_vocab on the field) and the rest of the document applies; a priced trip that still cannot be
// applied is held (pending, its finding open, retried by every sweep, reported by reconcile-sync) while the rest of
// the sweep commits; fixed at the source, it applies.
func TestOneDocumentNeverStopsASweep(t *testing.T) {
	h := newSyncHarness(t)
	p := h.plan(owned("auth"))
	h.sweep(p)
	h.fs.Merge("trip_records/TRIP001", map[string]any{"status": "loading", "partnerCode": "P-77"})
	h.fs.Merge("drivers/drv_own", map[string]any{"firstName": "สมชาย ใหม่"})
	rep := h.sweep(p)
	if v := h.scalar(`SELECT status || ' ' || partner_code FROM trip_records WHERE legacy_doc_id = 'TRIP001'`); v != "delivered P-77" {
		t.Errorf("TRIP001 %v", v)
	}
	if v := h.scalar(`SELECT first_name FROM drivers WHERE legacy_doc_id = 'drv_own'`); v != "สมชาย ใหม่" {
		t.Errorf("the driver edit of the same sweep %v", v)
	}
	if v := h.scalar(`SELECT count(*) FROM etl.quarantine WHERE doc_path = 'trip_records/TRIP001' AND field = 'status'
		AND reason_code = 'status_out_of_vocab' AND resolved_at IS NULL AND raw_value = '"loading"'`); v != int64(1) {
		t.Errorf("status_out_of_vocab findings %v (%+v)", v, rep.Collections)
	}
	if rep := h.sweep(p); applied(rep, "trip_records") != 0 {
		t.Errorf("a second sweep re-applied: %+v", rep.Collections)
	}
	// Another change of the same document while the literal stays: PostgreSQL's status is kept again.
	h.fs.Merge("trip_records/TRIP001", map[string]any{"partnerCode": "P-78"})
	h.sweep(p)
	if v := h.scalar(`SELECT status || ' ' || partner_code FROM trip_records WHERE legacy_doc_id = 'TRIP001'`); v != "delivered P-78" {
		t.Errorf("TRIP001 after another change %v", v)
	}

	// A priced trip that cannot be applied at all (no status) is held; the rest of the sweep commits.
	h.fs.Merge("trip_records/TRIP002", map[string]any{"status": "", "partnerCode": "P-99"})
	h.fs.Merge("drivers/drv_own", map[string]any{"firstName": "สมชาย 3"})
	rep = h.sweep(p)
	held := 0
	for _, c := range rep.Collections {
		if c.Name == "trip_records" {
			held = c.Held
		}
	}
	if held != 1 {
		t.Errorf("held %d: %+v", held, rep.Collections)
	}
	if v := h.scalar(`SELECT first_name FROM drivers WHERE legacy_doc_id = 'drv_own'`); v != "สมชาย 3" {
		t.Errorf("the sweep did not commit around the held document: %v", v)
	}
	if v := h.scalar(`SELECT coalesce(partner_code, '-') FROM trip_records WHERE legacy_doc_id = 'TRIP002'`); v == "P-99" {
		t.Error("the held document was applied")
	}
	if v := h.scalar(`SELECT sd.status || ' ' || q.reason_code FROM etl.source_docs sd JOIN etl.quarantine q ON q.doc_path = sd.doc_path
		AND q.resolved_at IS NULL WHERE sd.doc_path = 'trip_records/TRIP002'`); v != "pending missing_required" {
		t.Errorf("held document %v", v)
	}
	rec, err := h.e.ReconcileSync(h.ctx, h.client, SyncReconcileOptions{Mirror: p})
	if err != nil || rec.Match || !strings.Contains(mismatches(rec), "held") {
		t.Errorf("reconcile-sync does not report the held document: %v\n%s", err, mismatches(rec))
	}
	// Retried by the next sweep (still held), then applied once fixed at the source.
	if rep := h.sweep(p); rep.Collections[slices.IndexFunc(rep.Collections, func(c CollectionStats) bool { return c.Name == "trip_records" })].Held != 1 {
		t.Error("the held document was not retried")
	}
	h.fs.Merge("trip_records/TRIP002", map[string]any{"status": "delivered"})
	h.sweep(p)
	if v := h.scalar(`SELECT partner_code FROM trip_records WHERE legacy_doc_id = 'TRIP002'`); v != "P-99" {
		t.Errorf("after the fix at the source %v", v)
	}
	if v := h.scalar(`SELECT status FROM etl.source_docs WHERE doc_path = 'trip_records/TRIP002'`); v != "loaded" {
		t.Errorf("source doc %v", v)
	}
}

// The users fields the 3.x app keeps writing until P7b (class C): a sign-in reaches users.last_login_* when it is newer
// than PostgreSQL's, fcmTokens replaces the user's users.fcmTokens rows (a device's own row untouched), an unknown uid
// is rejected, and the reconciliation compares both.
func TestUsersFieldMirror(t *testing.T) {
	h := newSyncHarness(t)
	p := h.plan(owned("auth", "masterdata"))
	if p.Mode("users") != MirrorFields {
		t.Fatalf("users mode %q", p.Mode("users"))
	}
	user := h.id(`INSERT INTO users (legacy_auth_uid, email) VALUES ('uid_own', 'own@example.test') RETURNING id`)
	h.exec(`INSERT INTO device_tokens (user_id, install_id, token, platform) VALUES ($1, 'inst-4x', 'tok-4x', 'android')`, user)
	at := time.Date(2026, 10, 4, 1, 0, 0, 0, time.UTC)
	h.fs.Set("users/uid_own", map[string]any{"email": "own@example.test", "role": "driver", "lastLogin": dump.Timestamp{Time: at},
		"lastLoginLat": 13.7, "lastLoginLng": 100.5, "lastLoginGeoSource": "gps", "fcmTokens": map[string]any{"app": "tok-app-1"}})
	h.fs.Set("users/uid_nobody", map[string]any{"lastLogin": dump.Timestamp{Time: at}})
	h.sweep(p)
	if v := h.scalar(`SELECT last_login_at = $2 AND last_login_geo_source = 'gps' FROM users WHERE id = $1`, user, at); v != true {
		t.Errorf("last login %v", v)
	}
	if v := h.scalar(`SELECT string_agg(token || ':' || coalesce(legacy_source, '-'), ',' ORDER BY token) FROM device_tokens WHERE user_id = $1`,
		user); v != "tok-4x:-,tok-app-1:users.fcmTokens" {
		t.Errorf("tokens %v", v)
	}
	if v := h.scalar(`SELECT status FROM etl.source_docs WHERE doc_path = 'users/uid_nobody'`); v != "rejected" {
		t.Errorf("unknown uid %v", v)
	}
	if rec := h.reconcile(p, h.projector(owned("auth", "masterdata"))); strings.Contains(mismatches(rec), "users") {
		t.Errorf("reconcile:\n%s", mismatches(rec))
	}
	// A sign-in through Go is newer: an older 3.x login does not move it back. A token refresh replaces the row.
	later := at.Add(time.Hour)
	h.exec(`UPDATE users SET last_login_at = $2 WHERE id = $1`, user, later)
	h.fs.Merge("users/uid_own", map[string]any{"lastLogin": dump.Timestamp{Time: at.Add(time.Minute)}, "fcmTokens": map[string]any{"app": "tok-app-2"}})
	h.sweep(p)
	if v := h.scalar(`SELECT last_login_at = $2 FROM users WHERE id = $1`, user, later); v != true {
		t.Errorf("an older 3.x sign-in moved the last login back: %v", v)
	}
	if v := h.scalar(`SELECT string_agg(token, ',' ORDER BY token) FROM device_tokens WHERE user_id = $1`, user); v != "tok-4x,tok-app-2" {
		t.Errorf("tokens after the refresh %v", v)
	}
	// A projection-style rewrite of other fields applies nothing (only changed fields).
	h.exec(`UPDATE users SET last_login_at = NULL WHERE id = $1`, user)
	h.fs.Merge("users/uid_own", map[string]any{"role": "admin"})
	h.sweep(p)
	if v := h.scalar(`SELECT last_login_at IS NULL FROM users WHERE id = $1`, user); v != true {
		t.Errorf("an unchanged sign-in was applied again: %v", v)
	}
}
