-- 0019_scope_vehicle_locations.sql: Appendix C §C.3.7 (issue T53). Never edit this file once it is merged; follow-ups
-- take the next number (R31).
-- The live-map projection for a dispatcher (GET /v1/vehicle-locations, fleet:view_live_map | dispatch:view_operations).
-- Scope principals are served exclusively from scope_* views (R85; internal/scope, sqlc vet rule scope-views-only), and
-- vehicle_locations had none: its p_dispatch_read policy (0004) admits the trucks of a dispatcher's recent in-scope work
-- across tenants, so the rows are right, but the base table also carries the carrier's own telemetry. The view adds
-- columns only, never rows:
--   scope_vehicle_locations  vehicle_locations without gps_vehicle_id (the Cartrack unit, which scope_trucks hides too),
--                            engine_on, position_description, driver_name_raw (the raw Cartrack driver name), odometer_km,
--                            fuel_level, fuel_percentage and tenant_source.
-- security_invoker, like the views of 0003, 0004 and 0017, so the caller's RLS still applies through the view.
-- Grants: SELECT to logitrack_app (the API reads it in db.WithPrincipal) and logitrack_readonly (reporting, as 0009
-- granted every relation of public); nothing else (the catalog test allows SELECT only on a scope_* view).
-- Depends on: 0004 (vehicle_locations), 0009 (roles). It changes no table and depends on nothing in 0010-0018.

-- +goose Up
CREATE VIEW scope_vehicle_locations WITH (security_invoker = true) AS
  SELECT truck_id, tenant_id, license_plate, lat, lng, speed_kmh, heading, last_gps_at, updated_at
    FROM vehicle_locations;           -- hidden: gps_vehicle_id, engine_on, position_description, driver_name_raw, odometer_km, fuel_*

GRANT SELECT ON scope_vehicle_locations TO logitrack_app, logitrack_readonly;

-- +goose Down
DROP VIEW scope_vehicle_locations;
