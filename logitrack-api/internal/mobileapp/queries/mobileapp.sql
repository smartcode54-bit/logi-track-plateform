-- The driver app's version gate, releases and installations (T52, ADR 0007, Appendix B §B.2.18, §B.2.21).
-- settings and mobile_app_releases are exempt-service-layer tables (no RLS): this package is their only access path
-- and runs them under db.WithSystem after Go authorised the caller. mobile_installations is a tenant table: the
-- driver's heartbeat and the staff reads run under db.WithPrincipal, so RLS decides the rows.

-- name: GetMobileAppSettings :one
SELECT value, updated_at FROM settings WHERE key = 'mobile_app';

-- name: LockMobileAppSettings :one
-- The floor and the release writers serialise on the row: each reads the other's half under this lock.
SELECT value FROM settings WHERE key = 'mobile_app' FOR UPDATE;

-- name: MergeMobileAppSettings :exec
-- One writer's half of settings('mobile_app') merged over the document (ADR 0007 two-writer rule: the floor writer
-- passes only minAllowedVersion*, the release writer only the release fields; neither removes the other's keys).
INSERT INTO settings (key, value, updated_by) VALUES ('mobile_app', @half::jsonb, sqlc.narg(updated_by)::uuid)
ON CONFLICT (key) DO UPDATE SET value = settings.value || EXCLUDED.value, updated_by = EXCLUDED.updated_by;

-- name: UserEmail :one
SELECT coalesce(email::text, '')::text AS email FROM users WHERE id = @id::uuid;

-- name: APIKeyName :one
SELECT name FROM api_keys WHERE id = @id::uuid;

-- name: GetRelease :one
SELECT r.id, r.flavor, r.version, r.build_number, r.apk_file_id, r.apk_size_bytes, r.apk_sha256, r.release_notes,
       r.released_at, r.released_by, r.created_at, f.object_key, f.bucket, f.storage_backend
FROM mobile_app_releases r JOIN file_objects f ON f.id = r.apk_file_id
WHERE r.flavor = @flavor AND r.version = @version;

-- name: InsertRelease :exec
INSERT INTO mobile_app_releases (id, flavor, version, build_number, apk_file_id, apk_size_bytes, apk_sha256,
                                 release_notes, released_at, released_by)
VALUES (@id, @flavor, @version, @build_number, @apk_file_id, @apk_size_bytes, @apk_sha256, sqlc.narg(release_notes)::text,
        @released_at, @released_by);

-- name: ListReleases :many
-- GET /v1/app-releases: newest first, keyset (released_at, id).
SELECT r.id, r.flavor, r.version, r.build_number, r.apk_file_id, r.apk_size_bytes, r.apk_sha256, r.release_notes,
       r.released_at, r.released_by, r.created_at, f.object_key, f.bucket, f.storage_backend
FROM mobile_app_releases r JOIN file_objects f ON f.id = r.apk_file_id
WHERE (sqlc.narg(flavor)::text IS NULL OR r.flavor = sqlc.narg(flavor)::text)
  AND (sqlc.narg(after_at)::timestamptz IS NULL
       OR (r.released_at, r.id) < (sqlc.narg(after_at)::timestamptz, sqlc.narg(after_id)::uuid))
ORDER BY r.released_at DESC, r.id DESC
LIMIT @row_limit::int;

-- name: UpsertInstallation :exec
-- POST /v1/mobile/heartbeat under the driver's principal: RLS p_driver_insert / p_driver_update allow only the
-- driver's own (driver_id, install_id) rows in its tenant, so a guessed install id of another driver is a new row of
-- the caller, never a write to the other driver's (R33). tenant_id is frozen at the first heartbeat.
INSERT INTO mobile_installations (driver_id, install_id, tenant_id, tenant_source, platform, app_version, build_number,
                                  flavor, first_seen_at, last_seen_at)
VALUES (@driver_id, @install_id, @tenant_id, 'driver', @platform, @app_version::text, sqlc.narg(build_number)::text, @flavor,
        @seen_at, @seen_at)
ON CONFLICT (driver_id, install_id) DO UPDATE
   SET platform = EXCLUDED.platform, app_version = EXCLUDED.app_version, build_number = EXCLUDED.build_number,
       flavor = EXCLUDED.flavor, last_seen_at = greatest(mobile_installations.last_seen_at, EXCLUDED.last_seen_at);

-- name: DriverInstallations :one
-- The driver's other installations and whether this one exists (the heartbeat caps new ids, so a token cannot fill
-- the table with made-up ones). Under the driver's principal RLS shows its own rows only.
SELECT count(*) FILTER (WHERE install_id <> @install_id::text)::int AS others,
       coalesce(bool_or(install_id = @install_id::text), false)::boolean AS present
FROM mobile_installations WHERE driver_id = @driver_id;

-- name: ListInstallations :many
-- GET /v1/app-installations under the staff principal: RLS gives the tenant reach (contractor reach included), a
-- platform principal under X-Act-On-Tenant: * every row. below_version filters app versions that parse
-- (mobileapp.ParseVersion: trimmed, "+build" cut, three runs of digits, each at most 2^53-1) and order below it; the
-- CASE keeps the cast away from rows that do not parse. Sort last_seen_at DESC, keyset (last_seen_at, driver_id,
-- install_id).
SELECT mi.driver_id, mi.install_id, mi.tenant_id, mi.platform, mi.app_version, mi.build_number, mi.flavor,
       mi.first_seen_at, mi.last_seen_at,
       d.first_name, d.last_name, d.full_name_th, t.name_th AS tenant_name_th, t.name_en AS tenant_name_en
FROM mobile_installations mi
LEFT JOIN drivers d ON d.id = mi.driver_id
LEFT JOIN tenants t ON t.id = mi.tenant_id
WHERE (sqlc.narg(tenant_id)::uuid IS NULL OR mi.tenant_id = sqlc.narg(tenant_id)::uuid)
  AND (sqlc.narg(since)::timestamptz IS NULL OR mi.last_seen_at >= sqlc.narg(since)::timestamptz)
  AND (sqlc.narg(flavor)::text IS NULL OR mi.flavor = sqlc.narg(flavor)::text)
  AND (sqlc.narg(below_version)::text[] IS NULL OR coalesce((
        SELECT v.parts < sqlc.narg(below_version)::text[]::numeric[] AND 9007199254740991 >= ALL (v.parts)
        FROM (SELECT CASE WHEN split_part(btrim(coalesce(mi.app_version, '')), '+', 1) ~ '^[0-9]+\.[0-9]+\.[0-9]+$'
                          THEN string_to_array(split_part(btrim(mi.app_version), '+', 1), '.')::numeric[] END AS parts) v
      ), false))
  AND (sqlc.narg(after_seen)::timestamptz IS NULL
       OR (mi.last_seen_at, mi.driver_id, mi.install_id)
          < (sqlc.narg(after_seen)::timestamptz, sqlc.narg(after_driver)::uuid, sqlc.narg(after_install)::text))
ORDER BY mi.last_seen_at DESC, mi.driver_id DESC, mi.install_id DESC
LIMIT @row_limit::int;

-- name: InstallationVersionCounts :many
-- GET /v1/app-installations/stats (under the principal) and the floor change's impact (system context): the
-- installations seen since `since`, per app version, flavor and platform. Go classifies the versions (StatusOf).
SELECT coalesce(app_version, '')::text AS app_version, flavor, platform, count(*)::int AS installs
FROM mobile_installations
WHERE last_seen_at >= @since::timestamptz
  AND (sqlc.narg(tenant_id)::uuid IS NULL OR tenant_id = sqlc.narg(tenant_id)::uuid)
GROUP BY 1, 2, 3;
