-- notify.fcm (main spec §7.6, Appendix B §B.5.3): recipients are read server-side from the entity the
-- event names, devices from device_tokens (R4); every push is logged in notification_deliveries.

-- name: InboxClaimed :one
-- A message whose deliveries were already recorded (its consumer_inbox row committed) is acked at once.
SELECT EXISTS (SELECT 1 FROM consumer_inbox WHERE consumer = @consumer AND message_id = @message_id)::boolean AS claimed;

-- name: GetTaskForPush :one
-- task_ref is the id the app knows: the Firestore document id while APK 3.x is installed (legacy_doc_id,
-- else the uuid write-back mints as the document id, R25).
SELECT id, coalesce(legacy_doc_id, id::text)::text AS task_ref, task_type, status, driver_id, helper_driver_id,
       source_hub_raw, destination_raw, to_char(plan_date, 'YYYY-MM-DD')::text AS plan_date,
       coalesce(plan_time, '')::text AS plan_time
FROM tasks
WHERE id = @id;

-- name: DriverDevices :many
-- The devices of drivers: the device_tokens rows of each driver's linked user (drivers.user_id), so a
-- driver re-linked to another user never reaches the old user's phone; at most @per_driver of them per
-- driver, the most recently seen (registration keeps as many per user, auth.MaxDevicesPerUser; the cap
-- also bounds ETL rows). driver_ref is the id the app knows (legacy_doc_id, else the uuid).
SELECT driver_id, driver_ref, user_id, install_id, token
FROM (SELECT d.id AS driver_id, coalesce(d.legacy_doc_id, d.id::text)::text AS driver_ref,
             dt.user_id, dt.install_id, dt.token,
             row_number() OVER (PARTITION BY d.id ORDER BY dt.last_seen_at DESC, dt.install_id) AS rank
      FROM drivers d
      JOIN device_tokens dt ON dt.user_id = d.user_id
      WHERE d.id = ANY (@driver_ids::uuid[])) devices
WHERE rank <= @per_driver::bigint
ORDER BY driver_id, install_id;

-- name: RevokedSessionDevices :many
-- R50, R83, R84: the device of each revoked session is the device_tokens row of the same user and
-- install. An install signed in again since (it holds a live session, at most one per install) gets
-- nothing: the push would end the new session.
SELECT DISTINCT ON (dt.install_id) s.id AS session_id, dt.install_id, dt.token
FROM sessions s
JOIN device_tokens dt ON dt.user_id = s.user_id AND dt.install_id = s.install_id
WHERE s.user_id = @user_id
  AND s.id = ANY (@session_ids::uuid[])
  AND s.revoked_at IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM sessions live
                  WHERE live.user_id = s.user_id AND live.install_id = s.install_id
                    AND live.revoked_at IS NULL AND live.absolute_expires_at > now())
ORDER BY dt.install_id, s.revoked_at DESC, s.id;

-- name: InsertDelivery :exec
INSERT INTO notification_deliveries (outbox_event_id, user_id, install_id, kind, payload, status, error, created_at, sent_at)
VALUES (sqlc.narg(outbox_event_id), @user_id, @install_id, @kind, @payload, @status, sqlc.narg(error), @created_at,
        sqlc.narg(sent_at));

-- name: DeleteInvalidToken :execrows
-- FCM said UNREGISTERED (or INVALID_ARGUMENT naming the token): the row goes, unless the device has
-- registered another token since.
DELETE FROM device_tokens WHERE user_id = @user_id AND install_id = @install_id AND token = @token;

-- name: GetMaintenanceForPush :one
-- maintenance.created / maintenance.reminder_requested (T40): maintenance_ref is the id the app knows (legacy_doc_id,
-- else the uuid the write-back mints as the document id, R25); the plate, day, time and place build the legacy body.
SELECT m.id, coalesce(m.legacy_doc_id, m.id::text)::text AS maintenance_ref, m.status, m.truck_id,
       coalesce(t.license_plate, m.truck_license_plate_snapshot, '')::text AS plate,
       to_char(m.start_date, 'YYYY-MM-DD')::text AS start_date,
       coalesce(m.appointment_time, '')::text AS appointment_time, coalesce(m.provider, '')::text AS provider
FROM maintenance_records m
LEFT JOIN trucks t ON t.id = m.truck_id
WHERE m.id = @id;

-- name: TruckDrivers :many
-- The drivers of a truck's maintenance (the maintenance gate of app_driver_truck_ids()): its active assignments and
-- the driver who holds it as the active truck (the legacy page pushed to currentAssignments[0], maintenance.ts:36-44).
SELECT DISTINCT driver_id::uuid AS driver_id
FROM (SELECT a.driver_id FROM truck_assignments a WHERE a.truck_id = @truck_id AND a.status = 'active'
      UNION ALL
      SELECT d.id FROM drivers d WHERE d.active_truck_id = @truck_id) reach
ORDER BY driver_id;

-- name: GetLeaveForPush :one
-- leave.decided (T45): leave_ref is the id the app knows (legacy_doc_id, else the uuid, R25); the type and days build
-- the body.
SELECT id, coalesce(legacy_doc_id, id::text)::text AS leave_ref, driver_id, leave_type,
       to_char(start_date, 'YYYY-MM-DD')::text AS start_date, to_char(end_date, 'YYYY-MM-DD')::text AS end_date
FROM leave_requests
WHERE id = @id;
