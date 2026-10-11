//go:build integration

package iam_test

import (
	"fmt"
	"net/http"
	"testing"
)

// The quarantine review of P1 (T24, Appendix B §B.2.4, Appendix C §C.3.10): platform principals list the rows of the
// quarantine tenant with their resolver trace; a platform admin re-homes one with its children and files and writes
// tenant_rehomed; anyone else gets 403.
func TestQuarantineReview(t *testing.T) {
	w := newWorld(t)
	// A driverless legacy task (D6) with a trip and a photo, and a trip that follows a quarantined task, in Q.
	task := w.id(`INSERT INTO tasks (tenant_id, tenant_source, task_type, status, plan_at, source_hub_raw, destination_raw, task_no,
		legacy_doc_id, legacy_driver_ref, driver_ref_match) VALUES ($1, 'quarantine', 'first_mile', 'pending', now(), 'S', 'D',
		'FM-01092026-009', 'task_q', 'uid_gone', 'none') RETURNING id::text`, w.Q)
	trip := w.id(`INSERT INTO trip_records (tenant_id, tenant_source, trip_no, status, job_type, task_id, legacy_task_ref, task_ref_match,
		legacy_doc_id, created_at) VALUES ($1, 'quarantine', 'TRIP-Q', 'in_transit', 'first_mile', $2, 'task_q', 'doc_id', 'TRIP-Q', now())
		RETURNING id::text`, w.Q, task)
	w.exec(`INSERT INTO file_objects (bucket, object_key, tenant_id, purpose, owner_kind, owner_id, status, storage_backend)
		VALUES ('logitrack', 'trip_records/TRIP-Q/seal.jpg', $1, 'trip_photo', 'trip', $2, 'missing_at_source', 's3')`, w.Q, trip)
	// A chat without driver cannot leave the quarantine tenant (its CHECK needs one).
	chat := w.id(`INSERT INTO chats (tenant_id, tenant_source, legacy_driver_ref, driver_ref_match, legacy_doc_id)
		VALUES ($1, 'quarantine', 'uid_gone', 'none', 'chat_q') RETURNING id::text`, w.Q)
	// A renewal entry of an unknown truck: the immutable ledger keeps it in Q, listed for the review only.
	ledger := w.id(`INSERT INTO transactions (tenant_id, tenant_source, tx_type, sub_type, amount_thb, tx_date, legacy_doc_id)
		VALUES ($1, 'quarantine', 'insurance', 'annual', 9000, '2026-08-20', 'tx_q') RETURNING id::text`, w.Q)

	pa, su, ta := w.login("pa@example.test"), w.login("su@example.test"), w.login("ta.o@example.test")

	// Only platform principals read the review.
	expect(t, w.get("/v1/tenants/quarantine/rows", ta), http.StatusForbidden, "permission_denied")
	r := w.get("/v1/tenants/quarantine/rows?table=trip_records", su)
	expect(t, r, http.StatusOK, "")
	rows := r.data().([]any)
	if len(rows) != 1 {
		t.Fatalf("quarantined trips: %s", r.raw)
	}
	row := rows[0].(map[string]any)
	tr := row["trace"].(map[string]any)
	steps := tr["steps"].([]any)
	first := steps[0].(map[string]any)
	if row["id"] != trip || row["label"] != "TRIP-Q" || tr["outcome"] != "follows_parent" || first["link"] != "task" ||
		first["ref"] != "task_q" || first["match"] != "doc_id" || first["rowId"] != task || first["quarantined"] != true {
		t.Errorf("trip trace: %s", r.raw)
	}
	r = w.get("/v1/tenants/quarantine/rows?limit=2", pa)
	expect(t, r, http.StatusOK, "")
	byTable := r.body["meta"].(map[string]any)["byTable"].(map[string]any)
	if byTable["tasks"] != float64(3) || byTable["trip_records"] != float64(1) || byTable["chats"] != float64(1) ||
		byTable["transactions"] != float64(1) {
		t.Errorf("meta.byTable: %s", r.raw)
	}
	seen, cur := 0, r.body["nextCursor"]
	seen += len(r.data().([]any))
	for cur != nil && cur != "" {
		r = w.get(fmt.Sprintf("/v1/tenants/quarantine/rows?limit=2&cursor=%s", cur), pa)
		expect(t, r, http.StatusOK, "")
		seen += len(r.data().([]any))
		cur = r.body["nextCursor"]
	}
	if seen != 6 {
		t.Errorf("paged rows: %d, want the 6 rows of the quarantine tenant", seen)
	}
	// The quarantined ledger entry is listed with its truck link (which ran out) ...
	r = w.get("/v1/tenants/quarantine/rows?table=transactions", su)
	expect(t, r, http.StatusOK, "")
	if rows := r.data().([]any); len(rows) != 1 {
		t.Errorf("quarantined transactions: %s", r.raw)
	} else {
		row := rows[0].(map[string]any)
		tr := row["trace"].(map[string]any)
		step := tr["steps"].([]any)[0].(map[string]any)
		if row["id"] != ledger || row["legacyDocId"] != "tx_q" || row["label"] != "insurance annual" || tr["outcome"] != "chain_exhausted" ||
			step["link"] != "truck" || step["rowId"] != nil || step["quarantined"] != false {
			t.Errorf("transaction row: %s", r.raw)
		}
	}
	// ... and never re-homed: the ledger is immutable (409 immutable_ledger, nothing written); an id outside Q is 404.
	w.noEvent(func() resp {
		r := w.send(http.MethodPost, "/v1/tenants/quarantine/rows/transactions/"+ledger+"/rehome", pa, map[string]any{"tenantId": w.O})
		expect(t, r, http.StatusConflict, "failed_precondition")
		if reason(r) != "immutable_ledger" {
			t.Errorf("ledger re-home: %s", r.raw)
		}
		return r
	})
	if v := w.id(`SELECT tenant_id::text FROM transactions WHERE id = $1`, ledger); v != w.Q {
		t.Errorf("ledger entry moved: %s", v)
	}
	expect(t, w.send(http.MethodPost, "/v1/tenants/quarantine/rows/transactions/"+task+"/rehome", pa, map[string]any{"tenantId": w.O}),
		http.StatusNotFound, "not_found")
	expect(t, w.get("/v1/tenants/quarantine/rows?table=users", pa), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.get("/v1/tenants/quarantine/rows?nope=1", pa), http.StatusBadRequest, "bad_request")

	path := "/v1/tenants/quarantine/rows/tasks/" + task + "/rehome"
	// Support reads but never writes; tenant staff neither.
	w.noEvent(func() resp { return w.send(http.MethodPost, path, su, map[string]any{"tenantId": w.O}) })
	expect(t, w.send(http.MethodPost, path, su, map[string]any{"tenantId": w.O}), http.StatusForbidden, "permission_denied")
	expect(t, w.send(http.MethodPost, path, ta, map[string]any{"tenantId": w.O}), http.StatusForbidden, "permission_denied")
	// Bad targets write nothing.
	w.noEvent(func() resp {
		r := w.send(http.MethodPost, path, pa, map[string]any{"tenantId": w.Q})
		expect(t, r, http.StatusUnprocessableEntity, "invalid_argument")
		return r
	})
	expect(t, w.send(http.MethodPost, path, pa, map[string]any{}), http.StatusUnprocessableEntity, "invalid_argument")
	expect(t, w.send(http.MethodPost, "/v1/tenants/quarantine/rows/users/"+task+"/rehome", pa, map[string]any{"tenantId": w.O}),
		http.StatusNotFound, "not_found")
	// The trip follows its task: re-homing it alone is refused while the task stays quarantined.
	w.noEvent(func() resp {
		r := w.send(http.MethodPost, "/v1/tenants/quarantine/rows/trip_records/"+trip+"/rehome", pa, map[string]any{"tenantId": w.O})
		expect(t, r, http.StatusConflict, "failed_precondition")
		if reason(r) != "linked_row_quarantined" {
			t.Errorf("trip alone: %s", r.raw)
		}
		return r
	})
	w.noEvent(func() resp {
		r := w.send(http.MethodPost, "/v1/tenants/quarantine/rows/chats/"+chat+"/rehome", pa, map[string]any{"tenantId": w.O})
		expect(t, r, http.StatusConflict, "failed_precondition")
		if reason(r) != "needs_reference" {
			t.Errorf("driverless chat: %s", r.raw)
		}
		return r
	})

	// The platform admin re-homes the task: the trip and the photo follow, one tenant_rehomed with the admin as actor.
	w.oneEvent("tenant_rehomed", func() resp { return w.send(http.MethodPost, path, pa, map[string]any{"tenantId": w.O}) }, http.StatusNoContent)
	if v := w.id(`SELECT tenant_id::text || ' ' || tenant_source FROM tasks WHERE id = $1`, task); v != w.O+" form" {
		t.Errorf("task: %s", v)
	}
	if v := w.id(`SELECT tenant_id::text || ' ' || tenant_source FROM trip_records WHERE id = $1`, trip); v != w.O+" task" {
		t.Errorf("trip: %s", v)
	}
	if v := w.id(`SELECT tenant_id::text FROM file_objects WHERE object_key = 'trip_records/TRIP-Q/seal.jpg'`); v != w.O {
		t.Errorf("photo: %s", v)
	}
	if n := w.count(`SELECT count(*) FROM security_events WHERE event_type = 'tenant_rehomed' AND actor_user_id = $1::uuid AND tenant_id = $2::uuid
		AND details->>'table' = 'tasks' AND details->>'id' = $3::text AND details->>'fromTenantId' = $4::text AND details->>'toTenantId' = $2::text
		AND jsonb_array_length(details->'moved') = 3`, w.userID("pa@example.test"), w.O, task, w.Q); n != 1 {
		t.Errorf("tenant_rehomed details: %d", n)
	}
	// A row that left the quarantine tenant is no longer under review: a second re-home is 404.
	expect(t, w.send(http.MethodPost, path, pa, map[string]any{"tenantId": w.A}), http.StatusNotFound, "not_found")
	if n := len(w.get("/v1/tenants/quarantine/rows?table=trip_records", pa).data().([]any)); n != 0 {
		t.Errorf("re-homed trip still listed: %d", n)
	}
}
