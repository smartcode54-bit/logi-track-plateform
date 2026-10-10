-- Each query names a base table or writes; sqlc vet must report every one of them (make gen-check counts).

-- name: JoinsABaseTable :many
SELECT s.id, t.billing_party_id FROM scope_tasks s JOIN tasks t ON t.id = s.id;

-- name: CommaJoin :many
SELECT s.id FROM scope_trips s, trip_billing_snapshots b WHERE b.trip_id = s.id;

-- name: SchemaQualifiedAndQuoted :many
SELECT id FROM "public"."payroll_runs";

-- name: Subquery :many
SELECT id FROM scope_drivers WHERE id IN (SELECT driver_id FROM driver_penalties);

-- name: CommonTableExpression :many
WITH r AS (SELECT id FROM customer_rate_entries) SELECT id FROM r;

-- name: WritesThroughTheView :exec
UPDATE scope_tasks SET status = 'cancelled' WHERE id = sqlc.arg(id);
