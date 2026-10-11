-- 0017_scope_operation_children.sql: Appendix C §C.3.7 (issue T30). Never edit this file once it is merged; follow-ups
-- take the next number (R31).
-- The child rows of the operations projections, for the dispatcher and customer-scope reads of T30 (GET /v1/tasks with
-- the planned stops of a multi-drop task, GET /v1/trips/monitor with the stops a trip served, GET /v1/trips/{id}[/photos]
-- with the evidence photos, GET /v1/standby with its two photos). Scope principals are served exclusively from scope_*
-- views (R85; internal/scope, sqlc vet rule scope-views-only), and 0004 projected only the four parents. The children
-- already carry p_parent_read (a child row is visible when its parent is, under the caller's RLS: p_scope_read of the
-- parent for a scope principal), so these views add columns only, never rows:
--   scope_task_stops      task_delivery_stops without destination_linked_party_id (a billing party) and the estimated
--                         rate and drop fee (prices);
--   scope_trip_stops      trip_delivery_stops (no hidden column);
--   scope_trip_photos     trip_photos (no hidden column; the file id is signed by the API after the parent was read);
--   scope_standby_photos  standby_photos (no hidden column).
-- security_invoker, like the seven of 0003 and 0004, so the caller's RLS still applies through the view.
-- Grants: SELECT to logitrack_app (the API reads them in db.WithPrincipal) and logitrack_readonly (reporting, as 0009
-- granted every relation of public); nothing else (the catalog test allows SELECT only on a scope_* view).
-- Depends on: 0004 (the four child tables), 0009 (roles). Production applies it with `migrate up`; it depends on nothing
-- in 0010-0016.

-- +goose Up
CREATE VIEW scope_task_stops WITH (security_invoker = true) AS
  SELECT id, task_id, stop_index, destination_raw, destination_hub_id, status, delivered_at, delivered_lat, delivered_lng,
         sequence, added_at, source_id, is_custom
    FROM task_delivery_stops;         -- hidden: destination_linked_party_id, estimated_rate_thb, drop_fee_thb
CREATE VIEW scope_trip_stops WITH (security_invoker = true) AS
  SELECT id, trip_id, stop_index, destination_raw, destination_hub_id, source_id, status, delivered_at, delivered_lat,
         delivered_lng, completed_seq
    FROM trip_delivery_stops;
CREATE VIEW scope_trip_photos WITH (security_invoker = true) AS
  SELECT id, trip_id, stop_id, photo_type, photo_type_known, file_id, geocode_lat, geocode_lng, geocode_address,
         geocoded_at, position, created_at
    FROM trip_photos;
CREATE VIEW scope_standby_photos WITH (security_invoker = true) AS
  SELECT id, standby_id, photo_type, file_id, overlay_text, created_at
    FROM standby_photos;

GRANT SELECT ON scope_task_stops, scope_trip_stops, scope_trip_photos, scope_standby_photos TO logitrack_app, logitrack_readonly;

-- +goose Down
DROP VIEW scope_standby_photos;
DROP VIEW scope_trip_photos;
DROP VIEW scope_trip_stops;
DROP VIEW scope_task_stops;
