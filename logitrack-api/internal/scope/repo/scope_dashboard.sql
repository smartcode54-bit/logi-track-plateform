-- Dispatcher and customer-scope reads of the dashboard summary (T53, Appendix B §B.2.19): the operations blocks of
-- GET /v1/dashboard/summary for a scope principal, aggregated over the scope_trips and scope_incidents projections only
-- (Appendix C §C.3.7). The rows are the p_scope_read rows of the caller (db.WithPrincipal); the staff path runs the
-- same aggregates over the base tables in internal/dashboard.

-- name: ScopeDashboardTripTotals :one
SELECT count(*)::bigint AS delivered,
       coalesce(sum(t.parcel_count), 0)::bigint AS parcels,
       (count(*) FILTER (WHERE EXISTS (SELECT 1 FROM scope_incidents i WHERE i.trip_id = t.id)))::bigint AS with_incident
  FROM scope_trips t
 WHERE t.status = 'delivered' AND t.delivered_at >= sqlc.arg(from_at) AND t.delivered_at < sqlc.arg(to_at);

-- name: ScopeDashboardDeliveredByDay :many
SELECT bkk_date(t.delivered_at) AS day, count(*)::bigint AS delivered
  FROM scope_trips t
 WHERE t.status = 'delivered' AND t.delivered_at >= sqlc.arg(from_at) AND t.delivered_at < sqlc.arg(to_at)
 GROUP BY 1
 ORDER BY 1;

-- name: ScopeDashboardDeliveredCount :one
SELECT count(*)::bigint
  FROM scope_trips t
 WHERE t.status = 'delivered' AND t.delivered_at >= sqlc.arg(from_at) AND t.delivered_at < sqlc.arg(to_at);

-- name: ScopeDashboardIncidentCount :one
SELECT count(*)::bigint FROM scope_incidents WHERE created_at >= sqlc.arg(from_at) AND created_at < sqlc.arg(to_at);

-- name: ScopeDashboardRecentDelivered :many
SELECT id, delivered_at, truck_license_plate_snapshot, destination_raw, origin_raw
  FROM scope_trips
 WHERE status = 'delivered' AND delivered_at IS NOT NULL
 ORDER BY delivered_at DESC, id DESC
 LIMIT sqlc.arg(row_limit);

-- name: ScopeDashboardRecentIncidents :many
SELECT id, created_at, delay_cause, description
  FROM scope_incidents
 ORDER BY created_at DESC, id DESC
 LIMIT sqlc.arg(row_limit);
