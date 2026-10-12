-- The LINE request routes (T49, Appendix B §B.2.11-§B.2.13, §B.2.21): every query runs in the caller's request
-- transaction (db.WithPrincipal), so RLS decides which record a staff member or a driver may name. A staff request
-- locks the record (FOR UPDATE: PostgreSQL then applies the UPDATE policies too, so the record must be in the caller's
-- write reach, as POST /v1/trips/{id}/deliver); a record the caller cannot edit is 404. A driver's request reads; one
-- on a record it cannot read is handed to notify.line, which checks the owner itself.

-- name: LockTaskForLine :one
SELECT id, tenant_id, line_checkin_notified_at AS notified_at FROM tasks WHERE id = @id FOR UPDATE;

-- name: LockTripForLine :one
SELECT id, tenant_id, line_delivered_notified_at AS notified_at FROM trip_records WHERE id = @id FOR UPDATE;

-- name: LockStandbyForLine :one
SELECT id, tenant_id, line_notified_at AS notified_at FROM standby_records WHERE id = @id FOR UPDATE;

-- name: TaskForLine :one
SELECT id, tenant_id, line_checkin_notified_at AS notified_at FROM tasks WHERE id = @id;

-- name: TaskForLineByLegacy :one
SELECT id, tenant_id, line_checkin_notified_at AS notified_at FROM tasks WHERE legacy_doc_id = @ref::text;

-- name: TripForLine :one
SELECT id, tenant_id, line_delivered_notified_at AS notified_at FROM trip_records WHERE id = @id;

-- name: TripForLineByRef :one
-- The Firestore doc id first, then the trip number.
SELECT id, tenant_id, line_delivered_notified_at AS notified_at FROM trip_records
WHERE legacy_doc_id = @ref::text OR trip_no = @ref::text
ORDER BY coalesce(legacy_doc_id = @ref::text, false) DESC LIMIT 1;

-- name: StandbyForLine :one
SELECT id, tenant_id, line_notified_at AS notified_at FROM standby_records WHERE id = @id;

-- name: StandbyForLineByLegacy :one
SELECT id, tenant_id, line_notified_at AS notified_at FROM standby_records WHERE legacy_doc_id = @ref::text;

-- name: LockTripEvidence :one
-- A revocation (POST /v1/trips/{id}/evidence/revoke): the record in the caller's write reach, locked, with its token.
SELECT id, evidence_token FROM trip_records WHERE id = @id FOR UPDATE;

-- name: LockStandbyEvidence :one
SELECT id, evidence_token FROM standby_records WHERE id = @id FOR UPDATE;
