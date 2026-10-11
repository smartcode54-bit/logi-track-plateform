-- Dispatcher and customer-scope reads of trip records: the scope_trips projection only (Appendix C §C.3.7).

-- name: ListScopeTrips :many
SELECT * FROM scope_trips
 WHERE (sqlc.narg(before_created_at)::timestamptz IS NULL
        OR (created_at, id) < (sqlc.narg(before_created_at)::timestamptz, sqlc.narg(before_id)::uuid))
 ORDER BY created_at DESC, id DESC
 LIMIT sqlc.arg(row_limit);

-- name: GetScopeTrip :one
SELECT * FROM scope_trips WHERE id = sqlc.arg(id);

-- name: ListScopeTripsByCreated :many
-- GET /v1/trips?axis=created for a scope principal (T30): newest first, keyset (created_at, id); from / to bound
-- created_at, [from, to).
SELECT * FROM scope_trips
 WHERE (sqlc.narg(from_at)::timestamptz IS NULL OR created_at >= sqlc.narg(from_at)::timestamptz)
   AND (sqlc.narg(to_at)::timestamptz IS NULL OR created_at < sqlc.narg(to_at)::timestamptz)
   AND (sqlc.narg(statuses)::text[] IS NULL OR status = ANY (sqlc.narg(statuses)::text[]))
   AND (sqlc.narg(driver_id)::uuid IS NULL OR driver_id = sqlc.narg(driver_id)::uuid)
   AND (sqlc.narg(truck_id)::uuid IS NULL OR truck_id = sqlc.narg(truck_id)::uuid)
   AND (sqlc.narg(after_created)::timestamptz IS NULL
        OR (created_at, id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
 ORDER BY created_at DESC, id DESC
 LIMIT sqlc.arg(row_limit);

-- name: ListScopeTripsByDelivered :many
-- GET /v1/trips?axis=delivered for a scope principal (T30): delivered trips, latest first, keyset (delivered_at, id).
SELECT * FROM scope_trips
 WHERE status = 'delivered' AND delivered_at IS NOT NULL
   AND (sqlc.narg(from_at)::timestamptz IS NULL OR delivered_at >= sqlc.narg(from_at)::timestamptz)
   AND (sqlc.narg(to_at)::timestamptz IS NULL OR delivered_at < sqlc.narg(to_at)::timestamptz)
   AND (sqlc.narg(driver_id)::uuid IS NULL OR driver_id = sqlc.narg(driver_id)::uuid)
   AND (sqlc.narg(truck_id)::uuid IS NULL OR truck_id = sqlc.narg(truck_id)::uuid)
   AND (sqlc.narg(after_delivered)::timestamptz IS NULL
        OR (delivered_at, id) < (sqlc.narg(after_delivered)::timestamptz, sqlc.arg(after_id)::uuid))
 ORDER BY delivered_at DESC, id DESC
 LIMIT sqlc.arg(row_limit);

-- name: ScopeTripByNo :one
-- GET /v1/trips/by-no/{tripNo} for a scope principal (T30). The rename record is staff only, so a number from before a
-- rename does not resolve here.
SELECT * FROM scope_trips WHERE trip_no = sqlc.arg(trip_no)::text;

-- name: ScopeTripsByID :many
-- The trip numbers of a page of incidents (T30).
SELECT * FROM scope_trips WHERE id = ANY (sqlc.arg(ids)::uuid[]) ORDER BY id;

-- name: ScopeTripsOfTasks :many
-- The newest trip of a task (the boards' trip column) for a scope principal (T30): newest first per task.
SELECT * FROM scope_trips
 WHERE task_id = ANY (sqlc.arg(task_ids)::uuid[])
 ORDER BY task_id, created_at DESC, id DESC;

-- name: ScopeTripStopsOf :many
-- The stop progress of a page of the projection (scope_trip_stops, 0017).
SELECT * FROM scope_trip_stops WHERE trip_id = ANY (sqlc.arg(trip_ids)::uuid[]) ORDER BY trip_id, stop_index;

-- name: ScopeTripPhotos :many
-- The evidence photos of one trip (scope_trip_photos, 0017); the API sorts them by the ADR 0018 rank and signs the
-- files.
SELECT * FROM scope_trip_photos WHERE trip_id = sqlc.arg(trip_id) ORDER BY position, id;
