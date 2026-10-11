-- The staff write routes of the operations domain (T31, issue #51; Appendix B §B.2.11-§B.2.13): the master rows a
-- write snapshots or links (a truck's plate and vehicle class, a driver, a billing party), the parties of a task or a
-- trip (the dispatch:{partyId}:* topics of their events), the delivery side effect on the driver, and the stop
-- replacement of a task update. Every statement runs in the caller's request transaction (db.WithPrincipal), so RLS
-- decides: a master row the caller cannot read is not found.

-- --- master rows a write reads ------------------------------------------------------------------------------------

-- name: WriteTruck :one
-- The truck of a task or a trip edit: its plate and vehicle class are snapshotted on the row (the class from the
-- truck master's mapper, unknown = NULL and the raw type kept, R15); legacy_doc_id is the Firestore id the write-back
-- names (truckId). Primary-key lookup.
SELECT t.id, t.tenant_id, t.license_plate, t.vehicle_class, t.type_raw, t.legacy_doc_id
FROM trucks t
WHERE t.id = sqlc.arg(id);

-- name: WriteDriver :one
-- The driver or helper of a task, a standby record or an incident: its tenant (the stamping reads it again through
-- tenancy.TenantOf), its status, and the Firestore references the write-back names: legacy_doc_id (tasks.driverId,
-- main spec §13.8) and the Firebase uid of its login (trip, standby and incident driverId, the helper array, Q1).
SELECT d.id, d.tenant_id, d.status, d.legacy_doc_id, d.legacy_auth_uid AS driver_auth_uid,
       u.legacy_auth_uid AS user_auth_uid, d.full_name_th, d.first_name, d.last_name, d.mobile, d.active_task_id
FROM drivers d
LEFT JOIN users u ON u.id = d.user_id
WHERE d.id = sqlc.arg(id);

-- name: WriteParty :one
-- A billing party a task names (billingPartyId, the linked source and destination, a stop's linked destination): the
-- customer or carrier it bills, with the Firestore id of that customers or subcontractors document (the write-back
-- names billingCustomerId / *LinkedCustomerId by it) and its code and name (the denormalised *Code / *Name fields).
SELECT bp.id, bp.kind, bp.customer_id, bp.tenant_id, c.legacy_doc_id AS customer_doc_id,
       tn.legacy_doc_id AS tenant_doc_id, c.code AS customer_code, tn.code AS tenant_code,
       c.name AS customer_name, tn.name_th AS tenant_name
FROM billing_parties bp
LEFT JOIN customers c ON c.id = bp.customer_id
LEFT JOIN tenants tn ON tn.id = bp.tenant_id
WHERE bp.id = sqlc.arg(id);

-- --- realtime topics -------------------------------------------------------------------------------------------

-- name: TaskParties :many
-- The billing parties of a task (its billing party, its linked source and destination, its stops' linked
-- destinations): the dispatch:{partyId}:tasks topics of its events (Appendix B §B.4.2), the set app_task_in_scope reads.
SELECT DISTINCT p::uuid AS party_id FROM (
  SELECT unnest(ARRAY[t.billing_party_id, t.source_linked_party_id, t.destination_linked_party_id]) AS p
  FROM tasks t WHERE t.id = sqlc.arg(id)
  UNION ALL
  SELECT s.destination_linked_party_id FROM task_delivery_stops s WHERE s.task_id = sqlc.arg(id)
) x
WHERE p IS NOT NULL
ORDER BY 1;

-- name: TripParties :many
-- The billing parties of a trip: its billed party and the parties of its task (dispatch:{partyId}:trips topics).
SELECT DISTINCT p::uuid AS party_id FROM (
  SELECT r.billing_party_id AS p FROM trip_records r WHERE r.id = sqlc.arg(id)
  UNION ALL
  SELECT unnest(ARRAY[t.billing_party_id, t.source_linked_party_id, t.destination_linked_party_id])
  FROM trip_records r JOIN tasks t ON t.id = r.task_id WHERE r.id = sqlc.arg(id)
  UNION ALL
  SELECT s.destination_linked_party_id
  FROM trip_records r JOIN task_delivery_stops s ON s.task_id = r.task_id WHERE r.id = sqlc.arg(id)
) x
WHERE p IS NOT NULL
ORDER BY 1;

-- --- writes ------------------------------------------------------------------------------------------------------

-- name: DeleteTaskStops :execrows
-- A task update that replaces the planned stops of a multi-drop task (still pending or assigned: no trip delivers
-- them yet); the new plan is inserted as stop 1..n in the same transaction.
DELETE FROM task_delivery_stops WHERE task_id = sqlc.arg(task_id);

-- name: ClearDriverActiveTask :execrows
-- The delivery of a trip (POST /v1/trips/{id}/deliver, admin_web) ends the job the driver was running: the driver's
-- active truck is cleared when it still names this task (a driver who has since checked in to another job keeps it).
UPDATE drivers
SET active_truck_id = NULL, active_truck_plate = NULL, active_task_id = NULL, active_started_at = NULL
WHERE id = sqlc.arg(driver_id) AND active_task_id = sqlc.arg(task_id);

-- name: SetIncidentPhotos :one
-- The photos of an admin-reported incident (POST /v1/incidents): committed after the insert with the report as their
-- owner, then linked here in the same transaction (a NULL keeps the column).
UPDATE incident_reports
SET map_photo_file_id = coalesce(sqlc.narg(map_photo_file_id)::uuid, map_photo_file_id),
    situation1_photo_file_id = coalesce(sqlc.narg(situation1_photo_file_id)::uuid, situation1_photo_file_id),
    situation2_photo_file_id = coalesce(sqlc.narg(situation2_photo_file_id)::uuid, situation2_photo_file_id)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: TrucksByPlate :many
-- The fleet trucks of a plate as the task import matches it (import-dialog.tsx normalizePlate: upper case without
-- spaces and dashes), at most two (two trucks of one plate make the cell ambiguous).
SELECT t.id, t.tenant_id, t.license_plate, t.vehicle_class, t.type_raw, t.legacy_doc_id
FROM trucks t
WHERE upper(regexp_replace(t.license_plate, '[[:space:]-]', '', 'g')) = sqlc.arg(plate_key)::text
ORDER BY t.id
LIMIT 2;
