-- The live map of a dispatcher (T53, GET /v1/vehicle-locations, Appendix B §B.2.8): the scope_vehicle_locations
-- projection (0019) only, so no Cartrack unit id, raw driver name, odometer, fuel or engine reading of another carrier
-- reaches a scope principal (Appendix C §C.3.7). The rows are the caller's p_dispatch_read rows (db.WithPrincipal).

-- name: ListScopeVehicleLocations :many
SELECT * FROM scope_vehicle_locations
 WHERE cardinality(sqlc.arg(truck_ids)::uuid[]) = 0 OR truck_id = ANY (sqlc.arg(truck_ids)::uuid[])
 ORDER BY license_plate NULLS LAST, truck_id;
