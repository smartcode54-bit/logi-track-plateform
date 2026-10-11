-- The driver monitor of a dispatcher or a customer-scope principal (GET /v1/trips/monitor, T30, W8): the page and the
-- filter options of the staff monitor (internal/operations/queries/monitor.sql, whose header defines the plate, class
-- and place keys) over the projections only, so the page carries no price, party, review or evidence column. A name
-- comes from scope_drivers (visible through recent in-scope work), so an old trip may show its driver id only.

-- name: ScopeMonitorTrips :many
-- One keyset page (created_at, id), newest first; from / to bound created_at, [from, to).
SELECT sqlc.embed(t),
       k.task_no AS task_no, k.status AS task_status, k.check_in_at AS task_check_in_at,
       k.run_order AS task_run_order, k.plan_at AS task_plan_at, k.truck_id AS task_truck_id,
       k.license_plate_snapshot AS task_plate, k.truck_type AS task_vehicle_class,
       dr.full_name_th AS driver_full_name_th, dr.first_name AS driver_first_name, dr.last_name AS driver_last_name,
       (SELECT count(*) FROM scope_incidents i WHERE i.trip_id = t.id)::int AS incident_count,
       (SELECT count(*) FROM scope_trip_photos ph WHERE ph.trip_id = t.id)::int AS photo_count
FROM scope_trips t
LEFT JOIN scope_tasks k ON k.id = t.task_id
LEFT JOIN scope_drivers dr ON dr.id = t.driver_id
WHERE t.created_at >= sqlc.arg(from_at)::timestamptz AND t.created_at < sqlc.arg(to_at)::timestamptz
  AND (sqlc.narg(statuses)::text[] IS NULL OR t.status = ANY (sqlc.narg(statuses)::text[])
       OR (sqlc.arg(incident_status)::boolean AND EXISTS (SELECT 1 FROM scope_incidents i WHERE i.trip_id = t.id)))
  AND (sqlc.narg(job_type)::text IS NULL OR t.job_type = sqlc.narg(job_type)::text)
  AND (sqlc.narg(driver_id)::uuid IS NULL OR t.driver_id = sqlc.narg(driver_id)::uuid)
  AND (sqlc.narg(partner)::text IS NULL OR btrim(t.partner_code) = sqlc.narg(partner)::text)
  AND (NOT sqlc.arg(partner_none)::boolean OR coalesce(btrim(t.partner_code), '') = '')
  AND (sqlc.narg(plate_truck)::uuid IS NULL OR coalesce(t.truck_id, k.truck_id) = sqlc.narg(plate_truck)::uuid)
  AND (sqlc.narg(plate_raw)::text IS NULL
       OR (coalesce(t.truck_id, k.truck_id) IS NULL
           AND coalesce(nullif(btrim(t.truck_license_plate_snapshot), ''), nullif(btrim(k.license_plate_snapshot), ''))
               = sqlc.narg(plate_raw)::text))
  AND (NOT sqlc.arg(plate_none)::boolean
       OR (coalesce(t.truck_id, k.truck_id) IS NULL
           AND coalesce(nullif(btrim(t.truck_license_plate_snapshot), ''), nullif(btrim(k.license_plate_snapshot), '')) IS NULL))
  AND (sqlc.narg(vehicle_class)::text IS NULL OR coalesce(t.vehicle_class, k.truck_type) = sqlc.narg(vehicle_class)::text)
  AND (NOT sqlc.arg(vehicle_class_none)::boolean OR coalesce(t.vehicle_class, k.truck_type) IS NULL)
  AND (NOT sqlc.arg(origin_on)::boolean
       OR t.origin_hub_id = ANY (sqlc.arg(origin_hubs)::uuid[])
       OR (t.origin_hub_id IS NULL
           AND CASE WHEN upper(btrim(t.origin_raw)) ~ '^(SOCE|SOCN|SOCW)($|[ (])' THEN left(upper(btrim(t.origin_raw)), 4) END
               = sqlc.narg(origin_soc)::text)
       OR (t.origin_hub_id IS NULL AND upper(btrim(t.origin_raw)) !~ '^(SOCE|SOCN|SOCW)($|[ (])'
           AND btrim(t.origin_raw) = sqlc.narg(origin_raw)::text)
       OR (sqlc.arg(origin_none)::boolean AND t.origin_hub_id IS NULL AND coalesce(btrim(t.origin_raw), '') = ''))
  AND (NOT sqlc.arg(destination_on)::boolean
       OR EXISTS (SELECT 1 FROM scope_trip_stops st WHERE st.trip_id = t.id
                  AND (st.destination_hub_id = ANY (sqlc.arg(destination_hubs)::uuid[])
                       OR (st.destination_hub_id IS NULL
                           AND CASE WHEN upper(btrim(st.destination_raw)) ~ '^(SOCE|SOCN|SOCW)($|[ (])'
                                    THEN left(upper(btrim(st.destination_raw)), 4) END = sqlc.narg(destination_soc)::text)
                       OR (st.destination_hub_id IS NULL AND upper(btrim(st.destination_raw)) !~ '^(SOCE|SOCN|SOCW)($|[ (])'
                           AND btrim(st.destination_raw) = sqlc.narg(destination_raw)::text)
                       OR (sqlc.arg(destination_none)::boolean AND st.destination_hub_id IS NULL
                           AND btrim(st.destination_raw) = '')))
       OR (NOT EXISTS (SELECT 1 FROM scope_trip_stops st WHERE st.trip_id = t.id)
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
       OR strpos(lower(concat_ws(' ', t.trip_no, k.task_no, dr.full_name_th, dr.first_name, dr.last_name)),
                 lower(sqlc.narg(q)::text)) > 0)
  AND (sqlc.narg(after_created)::timestamptz IS NULL
       OR (t.created_at, t.id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY t.created_at DESC, t.id DESC
LIMIT sqlc.arg(row_limit);

-- name: ScopeMonitorFacets :many
-- The filter options over the date range: one row per (facet, key) with the number of trips it matches (the shape of
-- the staff MonitorFacets).
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
         coalesce(t.truck_id, k.truck_id) AS truck_id,
         coalesce(nullif(btrim(t.truck_license_plate_snapshot), ''), nullif(btrim(k.license_plate_snapshot), '')) AS plate,
         coalesce(btrim(t.partner_code), '') AS partner,
         coalesce(t.vehicle_class, k.truck_type, '') AS vehicle_class
  FROM scope_trips t
  LEFT JOIN scope_tasks k ON k.id = t.task_id
  WHERE t.created_at >= sqlc.arg(from_at)::timestamptz AND t.created_at < sqlc.arg(to_at)::timestamptz
),
places AS (
  SELECT b.id, st.destination_hub_id AS hub_id,
         CASE WHEN st.destination_hub_id IS NULL AND upper(btrim(st.destination_raw)) ~ '^(SOCE|SOCN|SOCW)($|[ (])'
              THEN left(upper(btrim(st.destination_raw)), 4)
              WHEN st.destination_hub_id IS NULL THEN btrim(st.destination_raw) END AS value
  FROM base b
  JOIN scope_trip_stops st ON st.trip_id = b.id
  UNION
  SELECT b.id, b.destination_hub_id, b.destination_value
  FROM base b
  WHERE NOT EXISTS (SELECT 1 FROM scope_trip_stops st WHERE st.trip_id = b.id)
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
WHERE b.status = 'incident' OR EXISTS (SELECT 1 FROM scope_incidents i WHERE i.trip_id = b.id)
UNION ALL
SELECT 'total', NULL::uuid, '', '', count(*)::int FROM base b;
