-- Vehicle locations (T53, Appendix A §A.2.3, Appendix B §B.2.8): the latest Cartrack position per truck. The reads run
-- under db.WithPrincipal (RLS: G-tenant for staff, p_dispatch_read for dispatchers); the cartrack.sync consumer writes
-- in its system transaction.

-- name: ListVehicleLocations :many
-- GET /v1/vehicle-locations: every position the caller's RLS admits, or only those of the given trucks (vehicle_locations
-- holds one row per truck with a GPS id, so the list is bounded by the fleet; no page is needed).
SELECT truck_id, tenant_id, gps_vehicle_id, license_plate, lat, lng, speed_kmh, heading, engine_on, position_description,
       driver_name_raw, last_gps_at, odometer_km, fuel_level, fuel_percentage, updated_at
  FROM vehicle_locations
 WHERE cardinality(sqlc.arg(truck_ids)::uuid[]) = 0 OR truck_id = ANY (sqlc.arg(truck_ids)::uuid[])
 ORDER BY license_plate NULLS LAST, truck_id;

-- name: GPSTrucks :many
-- The trucks the sync maps Cartrack vehicles to (trucks_gps: one truck per GPS id across tenants), outside the
-- quarantine tenant (the ETL rejects a position of a quarantined truck the same way, §A.3.4).
SELECT t.id, t.tenant_id, t.gps_vehicle_id::text AS gps_vehicle_id, t.license_plate
  FROM trucks t
  JOIN tenants te ON te.id = t.tenant_id
 WHERE t.gps_vehicle_id IS NOT NULL AND btrim(t.gps_vehicle_id) <> '' AND te.kind <> 'quarantine';

-- name: UpsertVehicleLocation :one
-- Latest only: one row per truck, replaced by a fix at least as new as the stored one (a fix without a GPS time is
-- stamped with the sync time by the consumer, so it always wins over an older one). The row follows its truck's
-- tenant (tenant_source 'truck', tenancy.Resolve chain viaTruck). updated is false when an older fix was ignored.
INSERT INTO vehicle_locations AS v (truck_id, tenant_id, tenant_source, gps_vehicle_id, license_plate, lat, lng, speed_kmh,
       heading, engine_on, position_description, driver_name_raw, last_gps_at, odometer_km, fuel_level, fuel_percentage)
VALUES (sqlc.arg(truck_id), sqlc.arg(tenant_id), 'truck', sqlc.arg(gps_vehicle_id), sqlc.narg(license_plate),
        sqlc.arg(lat), sqlc.arg(lng), sqlc.narg(speed_kmh), sqlc.narg(heading), sqlc.narg(engine_on),
        sqlc.narg(position_description), sqlc.narg(driver_name_raw), sqlc.arg(last_gps_at), sqlc.narg(odometer_km),
        sqlc.narg(fuel_level), sqlc.narg(fuel_percentage))
ON CONFLICT (truck_id) DO UPDATE SET
       tenant_id = EXCLUDED.tenant_id, tenant_source = EXCLUDED.tenant_source, gps_vehicle_id = EXCLUDED.gps_vehicle_id,
       license_plate = EXCLUDED.license_plate, lat = EXCLUDED.lat, lng = EXCLUDED.lng, speed_kmh = EXCLUDED.speed_kmh,
       heading = EXCLUDED.heading, engine_on = EXCLUDED.engine_on, position_description = EXCLUDED.position_description,
       driver_name_raw = EXCLUDED.driver_name_raw, last_gps_at = EXCLUDED.last_gps_at, odometer_km = EXCLUDED.odometer_km,
       fuel_level = EXCLUDED.fuel_level, fuel_percentage = EXCLUDED.fuel_percentage
 WHERE v.last_gps_at IS NULL OR EXCLUDED.last_gps_at >= v.last_gps_at
RETURNING truck_id, tenant_id, updated_at;
