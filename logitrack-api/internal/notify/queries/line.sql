-- notify.line (main spec §7.6, Appendix B §B.5.3, T49): the values of the LINE cards, read in the consumer system
-- transaction (the port of fn:lineNotify.ts, which read Firestore with the Admin SDK). The national ID card and the
-- licence of a driver are never selected: the cards go to a customer LINE group.

-- name: LineTaskIDByLegacy :one
SELECT id FROM tasks WHERE legacy_doc_id = @ref::text;

-- name: LineTripIDByRef :one
-- A trip the caller names by its Firestore doc id or its number (trip_no): the doc id first.
SELECT id FROM trip_records WHERE legacy_doc_id = @ref::text OR trip_no = @ref::text
ORDER BY coalesce(legacy_doc_id = @ref::text, false) DESC LIMIT 1;

-- name: LineStandbyIDByLegacy :one
SELECT id FROM standby_records WHERE legacy_doc_id = @ref::text;

-- name: LineLock :exec
-- Serialises the senders of one card of one record (an API event, a shim request and a staff force may arrive
-- together) for the consumer transaction, without locking the row other writers need.
SELECT pg_advisory_xact_lock(hashtextextended('notify.line:' || @card::text || ':' || @record_id::text, 0));

-- name: LineTask :one
SELECT t.id, t.tenant_id, coalesce(t.legacy_doc_id, t.id::text)::text AS ref, coalesce(t.task_no, '')::text AS task_no,
       t.plan_at, t.check_in_at, t.source_hub_raw, t.destination_raw, t.source_linked_party_id,
       t.destination_linked_party_id, t.truck_id, coalesce(t.legacy_truck_type, t.truck_type, '')::text AS truck_type,
       coalesce(t.license_plate_snapshot, '')::text AS license_plate, t.driver_id, t.helper_driver_id,
       t.line_checkin_notified_at
FROM tasks t
WHERE t.id = @id;

-- name: LineTrip :one
-- The trip with what the delivered card reads of its task. ocrData.tripId counts only as a string (str(), legacy).
SELECT tr.id, tr.tenant_id, tr.trip_no, tr.status, tr.driver_id, coalesce(tr.legacy_driver_ref, '')::text AS legacy_driver_ref,
       tr.origin_raw, tr.destination_raw, coalesce(tr.partner_code, '')::text AS partner_code,
       coalesce(CASE WHEN jsonb_typeof(tr.ocr_data -> 'tripId') = 'string' THEN tr.ocr_data ->> 'tripId' END, '')::text AS ocr_trip_id,
       tr.truck_id, coalesce(tr.truck_type_raw, '')::text AS truck_type_raw,
       coalesce(tr.truck_license_plate_snapshot, '')::text AS truck_plate, tr.parcel_count, tr.std, tr.ata, tr.delivered_at,
       coalesce(tr.delivered_via, '')::text AS delivered_via, tr.created_at, tr.billing_party_id,
       tr.line_delivered_notified_at, tr.evidence_token, tr.evidence_token_revoked_at,
       t.id AS task_id, t.check_in_at AS task_check_in_at, t.source_hub_raw AS task_source_hub_raw,
       t.destination_raw AS task_destination_raw, t.source_linked_party_id AS task_source_party_id,
       t.destination_linked_party_id AS task_destination_party_id, t.driver_id AS task_driver_id,
       t.helper_driver_id AS task_helper_driver_id
FROM trip_records tr
LEFT JOIN tasks t ON t.id = tr.task_id
WHERE tr.id = @id;

-- name: LineStandby :one
SELECT s.id, s.tenant_id, s.driver_id, coalesce(s.legacy_driver_ref, '')::text AS legacy_driver_ref, s.customer_party_id,
       s.start_location_raw, s.end_location_raw, s.started_at, s.ended_at, s.created_at, s.duration_minutes,
       coalesce(s.note, '')::text AS note, coalesce(s.truck_license_plate_snapshot, '')::text AS truck_plate,
       s.line_notified_at, s.evidence_token, s.evidence_token_revoked_at,
       t.driver_id AS task_driver_id, t.helper_driver_id AS task_helper_driver_id
FROM standby_records s
LEFT JOIN tasks t ON t.id = s.task_id
WHERE s.id = @id;

-- name: LineDriver :one
-- What the cards show of a driver: the name chain, the phone, and the plate fallback (activeTruck, else the truck of
-- the current assignment: plateFromDriver).
SELECT d.id, coalesce(d.full_name_th, '')::text AS full_name_th, d.first_name, d.last_name,
       coalesce(d.email::text, '')::text AS email, d.mobile, coalesce(d.active_truck_plate, '')::text AS active_truck_plate,
       coalesce((SELECT tk.license_plate FROM truck_assignments a JOIN trucks tk ON tk.id = a.truck_id
                 WHERE a.id = d.current_assignment_id), '')::text AS assignment_plate
FROM drivers d
WHERE d.id = @id;

-- name: LineParty :one
-- The LINE group of a billing party (its customer or carrier lineGroupId) and its code (the customerDriverIds key).
SELECT bp.customer_id, coalesce(c.line_group_id, tn.line_group_id, '')::text AS line_group_id,
       coalesce(c.code::text, tn.code::text, '')::text AS code
FROM billing_parties bp
LEFT JOIN customers c ON c.id = bp.customer_id
LEFT JOIN tenants tn ON tn.id = bp.tenant_id
WHERE bp.id = @id;

-- name: LineDriverCodes :many
-- The ids one customer keeps for a driver, in key order (the order Firestore returns the fields of a map).
SELECT id_type_key, value FROM driver_customer_codes
WHERE driver_id = @driver_id AND customer_id = @customer_id
ORDER BY id_type_key COLLATE "C";

-- name: LineHubNames :many
-- hubLabel: a hub code (exact, as the legacy Map) shows the Thai name of the hub, else its English name.
SELECT source_id::text AS code, name_th, coalesce(name_en, '')::text AS name_en
FROM hubs
WHERE source_id::text = ANY (@codes::text[]);

-- name: LineTruckType :one
SELECT type_raw FROM trucks WHERE id = @id;

-- name: LineTripPhotoCount :one
-- The evidence of a delivered trip as the gallery shows it: its photos and the map / situation photos of its incident
-- reports, committed files only.
SELECT ((SELECT count(*) FROM trip_photos p JOIN file_objects f ON f.id = p.file_id
         WHERE p.trip_id = @trip_id::uuid AND f.status = 'committed' AND f.deleted_at IS NULL)
      + (SELECT count(*) FROM incident_reports i
         JOIN file_objects f ON f.id IN (i.map_photo_file_id, i.situation1_photo_file_id, i.situation2_photo_file_id)
         WHERE i.trip_id = @trip_id::uuid AND f.status = 'committed' AND f.deleted_at IS NULL))::int AS photos;

-- name: LineTripDelayCauses :many
-- The delay causes of a trip incident reports in the order the legacy query returned them (document id).
SELECT coalesce(delay_cause, '')::text AS delay_cause FROM incident_reports
WHERE trip_id = @trip_id::uuid
ORDER BY coalesce(legacy_doc_id, id::text) COLLATE "C";

-- name: LineStandbyPhotoCount :one
SELECT count(*)::int AS photos FROM standby_photos p JOIN file_objects f ON f.id = p.file_id
WHERE p.standby_id = @standby_id AND f.status = 'committed' AND f.deleted_at IS NULL;

-- name: MarkTaskLineNotified :exec
UPDATE tasks SET line_checkin_notified_at = @at::timestamptz WHERE id = @id;

-- name: MarkTripLineNotified :exec
UPDATE trip_records SET line_delivered_notified_at = @at::timestamptz WHERE id = @id;

-- name: MarkStandbyLineNotified :exec
UPDATE standby_records SET line_notified_at = @at::timestamptz WHERE id = @id;
