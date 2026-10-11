-- Dashboard summary and badges (T53, W8 aggregates, Appendix B §B.2.19, Appendix E §E.2.1). Every query runs under
-- db.WithPrincipal: RLS keeps each count to the caller's reach (own tenant plus the carriers working for it, R60), so
-- the counts are the rows the caller's pages list. They replace the dashboard's ~10.7k document reads per visit
-- (DashboardStats.tsx:111-149,175-178, ActivityChart.tsx:45-50, RecentUpdates.tsx:51-77, ComplianceSummary.tsx:32)
-- and the badge listeners (app-sidebar.tsx:63-74, ExpenseAuditWidget.tsx:17-21, ChatStatusWidget.tsx:19-23).

-- name: TripTotals :one
-- Delivered trips of the period, their parcels, and how many of them have an incident report (the legacy on-time rule:
-- a delivered trip with an incident is delayed, DashboardStats.tsx:164-189). trip_records_delivered serves the range.
SELECT count(*)::bigint AS delivered,
       coalesce(sum(t.parcel_count), 0)::bigint AS parcels,
       (count(*) FILTER (WHERE EXISTS (SELECT 1 FROM incident_reports i WHERE i.trip_id = t.id)))::bigint AS with_incident
  FROM trip_records t
 WHERE t.status = 'delivered' AND t.delivered_at >= sqlc.arg(from_at) AND t.delivered_at < sqlc.arg(to_at);

-- name: DeliveredByDay :many
-- The activity histogram: delivered trips per Bangkok calendar day (ActivityChart.tsx:55-81 bucketed by the browser's
-- day; the server uses bkk_date, the platform's Bangkok calendar).
SELECT bkk_date(t.delivered_at) AS day, count(*)::bigint AS delivered
  FROM trip_records t
 WHERE t.status = 'delivered' AND t.delivered_at >= sqlc.arg(from_at) AND t.delivered_at < sqlc.arg(to_at)
 GROUP BY 1
 ORDER BY 1;

-- name: DeliveredCount :one
-- Delivered trips of the window before the period (the activity trend).
SELECT count(*)::bigint
  FROM trip_records t
 WHERE t.status = 'delivered' AND t.delivered_at >= sqlc.arg(from_at) AND t.delivered_at < sqlc.arg(to_at);

-- name: IncidentCount :one
SELECT count(*)::bigint FROM incident_reports WHERE created_at >= sqlc.arg(from_at) AND created_at < sqlc.arg(to_at);

-- name: DriverStatusCounts :one
-- The drivers card (DashboardStats.tsx:120-131: Active + On-Duty, all as the fallback).
SELECT (count(*) FILTER (WHERE status = 'active'))::bigint AS active,
       (count(*) FILTER (WHERE status = 'on_duty'))::bigint AS on_duty,
       (count(*) FILTER (WHERE status = 'inactive'))::bigint AS inactive,
       count(*)::bigint AS total
  FROM drivers;

-- name: PendingExpenses :one
-- Expenses awaiting the audit (status pending), by type (ExpenseAuditWidget.tsx:17-21; vehicle_expenses_pending).
SELECT (count(*) FILTER (WHERE expense_type = 'fuel'))::bigint AS fuel,
       (count(*) FILTER (WHERE expense_type = 'other'))::bigint AS other,
       count(*)::bigint AS total
  FROM vehicle_expenses
 WHERE status = 'pending';

-- name: ChatQueueCounts :one
-- Open chats nobody is assigned to, and the urgent ones among them (ChatStatusWidget.tsx:19-36).
SELECT count(*)::bigint AS unassigned,
       (count(*) FILTER (WHERE priority = 'urgent'))::bigint AS urgent
  FROM chats
 WHERE status <> 'closed' AND assigned_admin_user_id IS NULL;

-- name: ComplianceTrucks :many
-- Active trucks of the caller's tenant (the legacy "own" trucks, ComplianceSummary.tsx:32-34; every tenant in reach
-- under X-Act-On-Tenant: *) whose tax, insurance or next service date falls before the horizon, or whose next service
-- is at most pm_km away (PM_ALERT_THRESHOLD_KM). The service computes the items.
SELECT t.id, t.license_plate, t.tax_expiry_date, t.insurance_expiry_date, t.next_service_date, t.next_service_mileage,
       t.current_mileage
  FROM trucks t
 WHERE t.status = 'active'
   AND (sqlc.narg(tenant_id)::uuid IS NULL OR t.tenant_id = sqlc.narg(tenant_id)::uuid)
   AND (t.tax_expiry_date <= sqlc.arg(horizon)::date OR t.insurance_expiry_date <= sqlc.arg(horizon)::date
        OR t.next_service_date <= sqlc.arg(horizon)::date
        OR (t.next_service_mileage IS NOT NULL AND t.current_mileage IS NOT NULL
            AND t.next_service_mileage - t.current_mileage <= sqlc.arg(pm_km)::int))
 ORDER BY t.license_plate, t.id;

-- name: RecentDelivered :many
-- The recent-updates feed (RecentUpdates.tsx:51-77): the latest delivered trips.
SELECT id, delivered_at, truck_license_plate_snapshot, destination_raw, origin_raw
  FROM trip_records
 WHERE status = 'delivered' AND delivered_at IS NOT NULL
 ORDER BY delivered_at DESC, id DESC
 LIMIT sqlc.arg(row_limit);

-- name: RecentDrivers :many
SELECT id, first_name, last_name, full_name_th, updated_at
  FROM drivers
 ORDER BY updated_at DESC, id DESC
 LIMIT sqlc.arg(row_limit);

-- name: RecentIncidents :many
SELECT id, created_at, delay_cause, description
  FROM incident_reports
 ORDER BY created_at DESC, id DESC
 LIMIT sqlc.arg(row_limit);

-- name: RecentMaintenance :many
-- Completed maintenance (the legacy feed kept status completed only).
SELECT id, updated_at, service_type
  FROM maintenance_records
 WHERE status = 'completed'
 ORDER BY updated_at DESC, id DESC
 LIMIT sqlc.arg(row_limit);

-- name: WaitlistCount :one
-- The waitlist badge (waitlist is RLS-exempt; the route checked waitlist:view, a steward key).
SELECT count(*)::bigint FROM waitlist;
