-- name: SupportFileReferenced :one
-- Exact source ownership, never uploader/contractor/platform reach. The committed row
-- must still be linked by the admitted domain, not merely claim that owner kind.
SELECT EXISTS (
 SELECT 1 FROM drivers d WHERE sqlc.arg(owner_kind)::text = 'driver' AND d.id = sqlc.arg(owner_id)::uuid
  AND d.tenant_id = sqlc.arg(tenant_id)::uuid AND
  ((sqlc.arg(purpose)::text = 'driver_profile' AND d.profile_file_id = sqlc.arg(file_id)::uuid)
   OR (sqlc.arg(purpose)::text = 'driver_id_card' AND d.id_card_file_id = sqlc.arg(file_id)::uuid)
   OR (sqlc.arg(purpose)::text = 'driver_license' AND d.license_file_id = sqlc.arg(file_id)::uuid))
 UNION ALL
 SELECT 1 FROM truck_files tf JOIN trucks t ON t.id = tf.truck_id
 WHERE sqlc.arg(owner_kind)::text = 'truck' AND t.id = sqlc.arg(owner_id)::uuid AND t.tenant_id = sqlc.arg(tenant_id)::uuid
  AND tf.file_id = sqlc.arg(file_id)::uuid AND sqlc.arg(purpose)::text IN ('truck_photo','truck_document','truck_receipt','insurance_document')
 UNION ALL
 SELECT 1 FROM maintenance_files mf JOIN maintenance_records m ON m.id = mf.maintenance_id
 WHERE sqlc.arg(owner_kind)::text = 'maintenance' AND m.id = sqlc.arg(owner_id)::uuid AND m.tenant_id = sqlc.arg(tenant_id)::uuid
  AND mf.file_id = sqlc.arg(file_id)::uuid AND sqlc.arg(purpose)::text = 'maintenance_file'
 UNION ALL
 SELECT 1 FROM tenant_files tf JOIN tenants t ON t.id = tf.tenant_id
 WHERE sqlc.arg(owner_kind)::text = 'tenant' AND t.id = sqlc.arg(owner_id)::uuid AND t.id = sqlc.arg(tenant_id)::uuid
  AND tf.file_id = sqlc.arg(file_id)::uuid AND sqlc.arg(purpose)::text = 'tenant_document'
) AS allowed;

-- name: PutUploadGrant :one
INSERT INTO storage_upload_grants(file_id,user_id,session_id,tenant_id,auth_version,context_version,expires_at)
VALUES (sqlc.arg(file_id),sqlc.arg(user_id),sqlc.arg(session_id),sqlc.narg(tenant_id)::uuid,sqlc.arg(auth_version),sqlc.arg(context_version),sqlc.arg(expires_at))
ON CONFLICT(file_id) DO UPDATE SET expires_at=EXCLUDED.expires_at
WHERE storage_upload_grants.user_id=EXCLUDED.user_id AND storage_upload_grants.session_id=EXCLUDED.session_id
 AND storage_upload_grants.tenant_id IS NOT DISTINCT FROM EXCLUDED.tenant_id
 AND storage_upload_grants.auth_version=EXCLUDED.auth_version AND storage_upload_grants.context_version=EXCLUDED.context_version
RETURNING file_id;

-- name: HasUploadGrant :one
SELECT EXISTS(SELECT 1 FROM storage_upload_grants WHERE file_id=sqlc.arg(file_id)) AS present;

-- name: UploadGrantLive :one
SELECT EXISTS (
 SELECT 1 FROM storage_upload_grants g JOIN sessions s ON s.id=g.session_id JOIN users u ON u.id=g.user_id
 WHERE g.file_id=sqlc.arg(file_id) AND g.user_id=sqlc.arg(user_id) AND g.session_id=sqlc.arg(session_id)
 AND g.tenant_id IS NOT DISTINCT FROM sqlc.narg(tenant_id)::uuid
 AND g.auth_version=sqlc.arg(auth_version) AND g.context_version=sqlc.arg(context_version)
 AND g.expires_at>sqlc.arg(now)::timestamptz AND s.user_id=g.user_id AND s.access_mode='member'
 AND s.context_version=g.context_version AND s.active_tenant_id IS NOT DISTINCT FROM g.tenant_id
 AND s.revoked_at IS NULL AND s.absolute_expires_at>sqlc.arg(now)::timestamptz
 AND EXISTS (SELECT 1 FROM refresh_tokens rt WHERE rt.session_id=s.id AND rt.revoked_at IS NULL AND rt.expires_at>sqlc.arg(now)::timestamptz)
 AND u.status='active' AND u.auth_version=g.auth_version AND s.platform='web'
) AS allowed;

-- name: CommittableUploadGrant :one
-- A transitioned grant stays invalid after leaving support/returning to the same tenant.
SELECT (NOT EXISTS(SELECT 1 FROM storage_upload_grants WHERE file_id=sqlc.arg(file_id)::uuid)
 OR EXISTS (
 SELECT 1 FROM storage_upload_grants g JOIN sessions s ON s.id=g.session_id JOIN users u ON u.id=g.user_id
 WHERE g.file_id=sqlc.arg(file_id) AND s.user_id=g.user_id AND s.access_mode='member'
 AND s.context_version=g.context_version AND s.active_tenant_id IS NOT DISTINCT FROM g.tenant_id
 AND s.revoked_at IS NULL AND s.absolute_expires_at>sqlc.arg(now)::timestamptz
 AND EXISTS (SELECT 1 FROM refresh_tokens rt WHERE rt.session_id=s.id AND rt.revoked_at IS NULL AND rt.expires_at>sqlc.arg(now)::timestamptz)
 AND g.expires_at>sqlc.arg(now)::timestamptz AND u.status='active' AND u.auth_version=g.auth_version
 ))::boolean AS allowed;

-- name: UploadSessionPlatform :one
SELECT platform FROM sessions WHERE id=sqlc.arg(session_id)::uuid AND user_id=sqlc.arg(user_id)::uuid;

-- name: ContextUploadGrantsReady :one
-- Ordinary storage remains usable at the held P0 migration stop. Support entry
-- is refused until this additive transport migration has reached the database.
SELECT (to_regclass('public.storage_upload_grants') IS NOT NULL)::boolean AS ready;
