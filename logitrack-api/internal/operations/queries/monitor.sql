-- The driver monitor (GET /v1/trips/monitor, T30, W8; main spec §5.4) and the read joins of the T30 lists: one paged
-- server join of trip_records with its task (check-in, ADR 0001; the assigned round; the task's truck), the driver's
-- Thai name, the incident and photo counts and, for accounting:view_income, the stored price preview
-- (trip_billing_snapshots, never computed on read), replacing useDriverMonitor.ts:443-840 (an unbounded trips listener, three capped side
-- listeners, a rate-card fan-out and per-row pricing calls). Staff only: a dispatcher or a customer-scope principal
-- reads the same page from the projections (internal/scope/repo/scope_monitor.sql), with the same keys.
--
-- Filter keys (ADR 0005, ADR 0006; internal/operations/monitor.go maps the API values to these arguments):
--   plate         the truck is coalesce(trip.truck_id, task.truck_id) (never drivers.active_truck_*, ADR 0005 §6); a row
--                 without one is matched by its plate, coalesce(trip snapshot, task snapshot), trimmed, never normalised;
--                 a row with neither is the none bucket;
--   vehicle class coalesce(trip.vehicle_class, task.truck_type): the mapped class (unknown = NULL, R15), NULL = none;
--   origin        the hub stored at write (origin_hub_id: nameToCode + code pass-through, main spec §13.4), else the
--                 SOC key of the raw text (SOCE/SOCN/SOCW and their spellings, normalizeSocIdToKey; trip_records keeps
--                 no origin SOC column), else the trimmed raw text as its own bucket (never merged), else none;
--   destination   every stop of a multi-drop trip (trip_delivery_stops) or, without stops, the trip's destination
--                 (destination_hub_id, else destination_soc_key or the SOC key of the raw text, else the raw text,
--                 else none); a trip matches when any of its stops does (ADR 0006 §5).
-- Indexes: trip_records_tenant_created (trip_records_created under X-Act-On-Tenant: *) for the page and the options,
-- tasks_pkey, drivers_pkey, trip_billing_snapshots_pkey, incident_reports_trip, trip_photos (trip_id, photo_type),
-- trip_delivery_stops (trip_id, stop_index).

-- name: MonitorTrips :many
-- One keyset page (created_at, id), newest first; from / to bound created_at, [from, to). statuses with
-- incident_status: the legacy "incident" status filter also matches a trip that has an incident report.
SELECT sqlc.embed(t),
       tk.task_no AS task_no, tk.status AS task_status, tk.check_in_at AS task_check_in_at,
       tk.run_order AS task_run_order, tk.plan_at AS task_plan_at, tk.truck_id AS task_truck_id,
       tk.license_plate_snapshot AS task_plate, tk.truck_type AS task_vehicle_class,
       d.full_name_th AS driver_full_name_th, d.first_name AS driver_first_name, d.last_name AS driver_last_name,
       (SELECT count(*) FROM incident_reports i WHERE i.trip_id = t.id)::int AS incident_count,
       (SELECT count(*) FROM trip_photos ph WHERE ph.trip_id = t.id)::int AS photo_count,
       s.estimate_thb AS price_estimate_thb, s.unpriced_reason AS price_unpriced_reason,
       s.manual_override AS price_manual_override, s.job_category_at_pricing AS price_job_category,
       s.is_multi_delivery AS price_is_multi_delivery, s.computed_at AS price_computed_at
FROM trip_records t
LEFT JOIN tasks tk ON tk.id = t.task_id
LEFT JOIN drivers d ON d.id = t.driver_id
LEFT JOIN trip_billing_snapshots s ON sqlc.arg(with_price)::boolean AND s.trip_id = t.id
WHERE (sqlc.arg(all_tenants)::boolean OR t.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND t.created_at >= sqlc.arg(from_at)::timestamptz AND t.created_at < sqlc.arg(to_at)::timestamptz
  AND (sqlc.narg(statuses)::text[] IS NULL OR t.status = ANY (sqlc.narg(statuses)::text[])
       OR (sqlc.arg(incident_status)::boolean AND EXISTS (SELECT 1 FROM incident_reports i WHERE i.trip_id = t.id)))
  AND (sqlc.narg(job_type)::text IS NULL OR t.job_type = sqlc.narg(job_type)::text)
  AND (sqlc.narg(driver_id)::uuid IS NULL OR t.driver_id = sqlc.narg(driver_id)::uuid)
  AND (sqlc.narg(billing_party_id)::uuid IS NULL OR t.billing_party_id = sqlc.narg(billing_party_id)::uuid)
  AND (sqlc.narg(partner)::text IS NULL OR btrim(t.partner_code) = sqlc.narg(partner)::text)
  AND (NOT sqlc.arg(partner_none)::boolean OR coalesce(btrim(t.partner_code), '') = '')
  AND (sqlc.narg(plate_truck)::uuid IS NULL OR coalesce(t.truck_id, tk.truck_id) = sqlc.narg(plate_truck)::uuid)
  AND (sqlc.narg(plate_raw)::text IS NULL
       OR (coalesce(t.truck_id, tk.truck_id) IS NULL
           AND coalesce(nullif(btrim(t.truck_license_plate_snapshot), ''), nullif(btrim(tk.license_plate_snapshot), ''))
               = sqlc.narg(plate_raw)::text))
  AND (NOT sqlc.arg(plate_none)::boolean
       OR (coalesce(t.truck_id, tk.truck_id) IS NULL
           AND coalesce(nullif(btrim(t.truck_license_plate_snapshot), ''), nullif(btrim(tk.license_plate_snapshot), '')) IS NULL))
  AND (sqlc.narg(vehicle_class)::text IS NULL OR coalesce(t.vehicle_class, tk.truck_type) = sqlc.narg(vehicle_class)::text)
  AND (NOT sqlc.arg(vehicle_class_none)::boolean OR coalesce(t.vehicle_class, tk.truck_type) IS NULL)
  AND (NOT sqlc.arg(origin_on)::boolean
       OR t.origin_hub_id = ANY (sqlc.arg(origin_hubs)::uuid[])
       OR (t.origin_hub_id IS NULL
           AND CASE WHEN upper(btrim(t.origin_raw)) ~ '^(SOCE|SOCN|SOCW)($|[ (])' THEN left(upper(btrim(t.origin_raw)), 4) END
               = sqlc.narg(origin_soc)::text)
       OR (t.origin_hub_id IS NULL AND upper(btrim(t.origin_raw)) !~ '^(SOCE|SOCN|SOCW)($|[ (])'
           AND btrim(t.origin_raw) = sqlc.narg(origin_raw)::text)
       OR (sqlc.arg(origin_none)::boolean AND t.origin_hub_id IS NULL AND coalesce(btrim(t.origin_raw), '') = ''))
  AND (NOT sqlc.arg(destination_on)::boolean
       OR EXISTS (SELECT 1 FROM trip_delivery_stops st WHERE st.trip_id = t.id
                  AND (st.destination_hub_id = ANY (sqlc.arg(destination_hubs)::uuid[])
                       OR (st.destination_hub_id IS NULL
                           AND CASE WHEN upper(btrim(st.destination_raw)) ~ '^(SOCE|SOCN|SOCW)($|[ (])'
                                    THEN left(upper(btrim(st.destination_raw)), 4) END = sqlc.narg(destination_soc)::text)
                       OR (st.destination_hub_id IS NULL AND upper(btrim(st.destination_raw)) !~ '^(SOCE|SOCN|SOCW)($|[ (])'
                           AND btrim(st.destination_raw) = sqlc.narg(destination_raw)::text)
                       OR (sqlc.arg(destination_none)::boolean AND st.destination_hub_id IS NULL
                           AND btrim(st.destination_raw) = '')))
       OR (NOT EXISTS (SELECT 1 FROM trip_delivery_stops st WHERE st.trip_id = t.id)
           AND (t.destination_hub_id = ANY (sqlc.arg(destination_hubs)::uuid[])
                OR (t.destination_hub_id IS NULL
                    AND coalesce(t.destination_soc_key,
                                 CASE WHEN upper(btrim(t.destination_raw)) ~ '^(SOCE|SOCN|SOCW)($|[ (])'
                                      THEN left(upper(btrim(t.destination_raw)), 4) END) = sqlc.narg(destination_soc)::text)
                OR (t.destination_hub_id IS NULL AND t.destination_soc_key IS NULL
                    AND upper(btrim(t.destination_raw)) !~ '^(SOCE|SOCN|SOCW)($|[ (])'
                    AND btrim(t.destination_raw) = sqlc.narg(destination_raw)::text)
                OR (sqlc.arg(destination_none)::boolean AND t.destination_hub_id IS NULL AND t.destination_soc_key IS NULL
                    AND coalesce(btrim(t.destination_raw), '') = ''))))
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(concat_ws(' ', t.trip_no, tk.task_no, d.full_name_th, d.first_name, d.last_name)),
                 lower(sqlc.narg(q)::text)) > 0)
  AND (sqlc.narg(after_created)::timestamptz IS NULL
       OR (t.created_at, t.id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY t.created_at DESC, t.id DESC
LIMIT sqlc.arg(row_limit);

-- name: MonitorFacets :many
-- The filter options of the monitor over the date range (and the customer, when given): one row per (facet, key) with
-- the number of trips it matches, so every option matches a trip and no trip is unreachable by every option
-- (ADR 0005 §4, ADR 0006 §2): origin, destination (a trip counted once per place it served), plate (ref_id the truck,
-- else value the raw plate; label a plate of the truck's rows), partner, vehicle_class, status, plus 'incident' (trips
-- with the incident status or an incident report) and 'total'. An empty value is the none bucket; ref_id is a hub (places) or
-- a truck (plates).
WITH base AS (
  SELECT t.id, t.status, t.origin_hub_id,
         CASE WHEN t.origin_hub_id IS NULL AND upper(btrim(t.origin_raw)) ~ '^(SOCE|SOCN|SOCW)($|[ (])'
              THEN left(upper(btrim(t.origin_raw)), 4)
              WHEN t.origin_hub_id IS NULL THEN coalesce(btrim(t.origin_raw), '') END AS origin_value,
         t.destination_hub_id,
         CASE WHEN t.destination_hub_id IS NULL AND t.destination_soc_key IS NOT NULL THEN t.destination_soc_key
              WHEN t.destination_hub_id IS NULL AND upper(btrim(t.destination_raw)) ~ '^(SOCE|SOCN|SOCW)($|[ (])'
              THEN left(upper(btrim(t.destination_raw)), 4)
              WHEN t.destination_hub_id IS NULL THEN coalesce(btrim(t.destination_raw), '') END AS destination_value,
         coalesce(t.truck_id, tk.truck_id) AS truck_id,
         coalesce(nullif(btrim(t.truck_license_plate_snapshot), ''), nullif(btrim(tk.license_plate_snapshot), '')) AS plate,
         coalesce(btrim(t.partner_code), '') AS partner,
         coalesce(t.vehicle_class, tk.truck_type, '') AS vehicle_class
  FROM trip_records t
  LEFT JOIN tasks tk ON tk.id = t.task_id
  WHERE (sqlc.arg(all_tenants)::boolean OR t.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
    AND t.created_at >= sqlc.arg(from_at)::timestamptz AND t.created_at < sqlc.arg(to_at)::timestamptz
    AND (sqlc.narg(billing_party_id)::uuid IS NULL OR t.billing_party_id = sqlc.narg(billing_party_id)::uuid)
),
places AS (
  SELECT b.id, st.destination_hub_id AS hub_id,
         CASE WHEN st.destination_hub_id IS NULL AND upper(btrim(st.destination_raw)) ~ '^(SOCE|SOCN|SOCW)($|[ (])'
              THEN left(upper(btrim(st.destination_raw)), 4)
              WHEN st.destination_hub_id IS NULL THEN btrim(st.destination_raw) END AS value
  FROM base b
  JOIN trip_delivery_stops st ON st.trip_id = b.id
  UNION
  SELECT b.id, b.destination_hub_id, b.destination_value
  FROM base b
  WHERE NOT EXISTS (SELECT 1 FROM trip_delivery_stops st WHERE st.trip_id = b.id)
)
SELECT 'origin'::text AS facet, b.origin_hub_id AS ref_id, coalesce(b.origin_value, '')::text AS value,
       ''::text AS label, count(*)::int AS n
FROM base b
GROUP BY b.origin_hub_id, b.origin_value
UNION ALL
SELECT 'destination', p.hub_id, coalesce(p.value, ''), '', count(*)::int
FROM places p
GROUP BY p.hub_id, p.value
UNION ALL
SELECT 'plate', b.truck_id, CASE WHEN b.truck_id IS NULL THEN coalesce(b.plate, '') ELSE '' END,
       coalesce(max(b.plate), ''), count(*)::int
FROM base b
GROUP BY b.truck_id, CASE WHEN b.truck_id IS NULL THEN coalesce(b.plate, '') ELSE '' END
UNION ALL
SELECT 'partner', NULL::uuid, b.partner, '', count(*)::int FROM base b GROUP BY b.partner
UNION ALL
SELECT 'vehicle_class', NULL::uuid, b.vehicle_class, '', count(*)::int FROM base b GROUP BY b.vehicle_class
UNION ALL
SELECT 'status', NULL::uuid, b.status, '', count(*)::int FROM base b GROUP BY b.status
UNION ALL
SELECT 'incident', NULL::uuid, '', '', count(*)::int FROM base b
WHERE b.status = 'incident' OR EXISTS (SELECT 1 FROM incident_reports i WHERE i.trip_id = b.id)
UNION ALL
SELECT 'total', NULL::uuid, '', '', count(*)::int FROM base b;

-- name: TripStopsOf :many
-- The stop progress of a page of trips (the monitor's stops column; one round trip).
SELECT * FROM trip_delivery_stops WHERE trip_id = ANY (sqlc.arg(trip_ids)::uuid[]) ORDER BY trip_id, stop_index;

-- name: DriverNamesOf :many
-- The names of the drivers and helpers of a page (the boards, standby, incidents: the legacy pages listened to 300
-- drivers for this, incident-reports/page.tsx:157-169). The caller's RLS decides which rows it reads; an unreadable
-- driver keeps its id only.
SELECT d.id, d.full_name_th, d.first_name, d.last_name FROM drivers d WHERE d.id = ANY (sqlc.arg(ids)::uuid[]) ORDER BY d.id;

-- name: TripNumbersOf :many
-- The trip numbers of a page of incidents (the caller's reach).
SELECT t.id, t.trip_no FROM trip_records t
WHERE t.id = ANY (sqlc.arg(ids)::uuid[])
  AND (sqlc.arg(all_tenants)::boolean OR t.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
ORDER BY t.id;

-- name: BillingPreviewsOf :many
-- The stored price of a page of trips (accounting:view_income only; trip_billing_snapshots, written by the billing
-- service, never computed on read).
SELECT s.trip_id, s.estimate_thb, s.unpriced_reason, s.manual_override, s.job_category_at_pricing, s.is_multi_delivery,
       s.computed_at
FROM trip_billing_snapshots s
WHERE s.trip_id = ANY (sqlc.arg(trip_ids)::uuid[])
ORDER BY s.trip_id;
