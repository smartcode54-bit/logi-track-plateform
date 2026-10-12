-- The evidence gallery GET /evidence/{token} (T49, main spec §9.6; port of fn:tripEvidence.ts): the record a valid token
-- opened (ResolveEvidence) and its photos, read under db.WithSystem (the token is the access control; no principal).

-- name: GalleryTrip :one
-- The subtitle: origin and destination as recorded, and ocrData.tripId (any scalar) before the trip number.
SELECT id, origin_raw, destination_raw,
       coalesce(jsonb_typeof(ocr_data -> 'tripId') IN ('string', 'number', 'boolean'), false)::boolean AS has_ocr_trip_id,
       coalesce(ocr_data ->> 'tripId', '')::text AS ocr_trip_id,
       trip_no
FROM trip_records
WHERE id = @id;

-- name: GalleryTripPhotos :many
-- The trip photos, then those of its stops, in the order they were taken (position).
SELECT photo_type, file_id FROM trip_photos
WHERE trip_id = @trip_id
ORDER BY (stop_id IS NOT NULL), position, created_at, id;

-- name: GalleryTripIncidents :many
-- The map and situation photos of the trip incident reports, in document id order (the legacy query).
SELECT map_photo_file_id, situation1_photo_file_id, situation2_photo_file_id FROM incident_reports
WHERE trip_id = @trip_id::uuid
ORDER BY coalesce(legacy_doc_id, id::text) COLLATE "C";

-- name: GalleryStandby :one
SELECT id, start_location_raw, end_location_raw FROM standby_records WHERE id = @id;

-- name: GalleryStandbyPhotos :many
SELECT photo_type, file_id FROM standby_photos
WHERE standby_id = @standby_id
ORDER BY created_at, id;
