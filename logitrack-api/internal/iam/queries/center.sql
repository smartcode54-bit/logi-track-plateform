-- Security Center read model (T51, Appendix B §B.2.5 /v1/stats/users-by-role, §B.2.6 /v1/security/overview). The
-- identity tables are platform-only under RLS, so these counts run in db.WithSystem and name the caller's reach
-- explicitly, with the predicate of GET /v1/users (all_users, tenant_ids, scope_only: the members of the tenant
-- reach and, for a steward, the accounts that belong to no tenant and hold no platform role).

-- name: OverviewUsersByStatus :many
SELECT u.status, count(*)::int AS users
FROM users u
WHERE (sqlc.arg(all_users)::boolean
    OR EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = u.id AND m.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
    OR (sqlc.arg(scope_only)::boolean AND NOT EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = u.id)
        AND NOT EXISTS (SELECT 1 FROM user_platform_roles r WHERE r.user_id = u.id)))
GROUP BY u.status;

-- name: OverviewSessions :many
-- Live sessions (as GET /v1/users/{id}/sessions counts them) and the sessions revoked since `since`, by reason;
-- reason '' is the live row.
SELECT coalesce(s.revoked_reason, '')::text AS reason, count(*)::int AS sessions
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE ((s.revoked_at IS NULL AND s.absolute_expires_at > sqlc.arg(now)::timestamptz)
       OR s.revoked_at >= sqlc.arg(since)::timestamptz)
  AND (sqlc.arg(all_users)::boolean
    OR EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = u.id AND m.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
    OR (sqlc.arg(scope_only)::boolean AND NOT EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = u.id)
        AND NOT EXISTS (SELECT 1 FROM user_platform_roles r WHERE r.user_id = u.id)))
GROUP BY 1;

-- name: OverviewFailedLogins :one
-- Failed sign-ins and lockouts since `since` (consumer-written, tenant_id NULL, Appendix C §C.4.13): every row for
-- all_users, else the rows whose target is a user in reach (a failure on an unknown email has no target).
SELECT count(*) FILTER (WHERE e.event_type = 'login_failed')::int AS failed,
       count(*) FILTER (WHERE e.event_type = 'login_lockout')::int AS lockouts
FROM security_events e
WHERE e.event_type IN ('login_failed', 'login_lockout') AND e.created_at >= sqlc.arg(since)::timestamptz
  AND (sqlc.arg(all_users)::boolean OR EXISTS (
        SELECT 1 FROM users u
        WHERE u.id = e.target_user_id
          AND (EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = u.id AND m.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
            OR (sqlc.arg(scope_only)::boolean AND NOT EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = u.id)
                AND NOT EXISTS (SELECT 1 FROM user_platform_roles r WHERE r.user_id = u.id)))));

-- name: UsersByRole :many
-- GET /v1/stats/users-by-role: users that are not deleted, in reach, counted once per role they hold on each axis
-- (tenant roles only in reach tenants), plus 'total' and 'none' (no membership, scope or platform role).
WITH reach AS (
  SELECT u.id FROM users u
  WHERE u.status <> 'deleted'
    AND (sqlc.arg(all_users)::boolean
      OR EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = u.id AND m.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
      OR (sqlc.arg(scope_only)::boolean AND NOT EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = u.id)
          AND NOT EXISTS (SELECT 1 FROM user_platform_roles r WHERE r.user_id = u.id)))
)
SELECT 'tenant'::text AS axis, m.role::text AS role, count(DISTINCT m.user_id)::int AS users
FROM memberships m JOIN reach ON reach.id = m.user_id
WHERE sqlc.arg(all_users)::boolean OR m.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[])
GROUP BY m.role
UNION ALL
SELECT 'scope'::text, s.kind::text, count(DISTINCT s.user_id)::int
FROM user_scopes s JOIN reach ON reach.id = s.user_id
GROUP BY s.kind
UNION ALL
SELECT 'platform'::text, r.role::text, count(*)::int
FROM user_platform_roles r JOIN reach ON reach.id = r.user_id
GROUP BY r.role
UNION ALL
SELECT 'total'::text, ''::text, count(*)::int FROM reach
UNION ALL
SELECT 'none'::text, ''::text, count(*)::int FROM reach
WHERE NOT EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = reach.id)
  AND NOT EXISTS (SELECT 1 FROM user_scopes s WHERE s.user_id = reach.id)
  AND NOT EXISTS (SELECT 1 FROM user_platform_roles r WHERE r.user_id = reach.id);

-- name: QuarantineTables :many
-- The tenant-stamped tables the api login may read (tenant_source marks them, R11): their rows in the quarantine
-- tenant are the open quarantine rows of the overview (platform principals under X-Act-On-Tenant: *).
SELECT c.relname::text AS table_name
FROM pg_catalog.pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
JOIN pg_catalog.pg_attribute a ON a.attrelid = c.oid AND a.attname = 'tenant_source' AND NOT a.attisdropped
JOIN pg_catalog.pg_attribute t ON t.attrelid = c.oid AND t.attname = 'tenant_id' AND NOT t.attisdropped
WHERE n.nspname = 'public' AND c.relkind = 'r' AND has_table_privilege(c.oid, 'SELECT')
ORDER BY c.relname;

-- name: OutboxBacklog :one
-- System status: the outbox rows the relay has not published yet and the age of the oldest (relay lag).
SELECT count(*)::int AS pending,
       coalesce(extract(epoch FROM (sqlc.arg(now)::timestamptz - min(created_at))), 0)::int AS oldest_seconds
FROM outbox_events
WHERE published_at IS NULL;

-- name: JobCounts :one
-- System status: jobs waiting or running now, and the jobs that failed or succeeded since `since`.
SELECT count(*) FILTER (WHERE status = 'queued')::int AS queued,
       count(*) FILTER (WHERE status = 'running')::int AS running,
       count(*) FILTER (WHERE status = 'failed' AND finished_at >= sqlc.arg(since)::timestamptz)::int AS failed,
       count(*) FILTER (WHERE status = 'succeeded' AND finished_at >= sqlc.arg(since)::timestamptz)::int AS succeeded
FROM jobs
WHERE status IN ('queued', 'running') OR finished_at >= sqlc.arg(since)::timestamptz;

-- name: SchedulerLeaderPresent :one
-- System status: some session of this database holds the scheduler's advisory lock
-- pg_try_advisory_lock(hashtext('lt-scheduler')) (internal/scheduler LockName): a bigint key appears in pg_locks as
-- classid = high 32 bits, objid = low 32 bits, objsubid = 1.
SELECT EXISTS (
  SELECT 1 FROM pg_catalog.pg_locks l
  WHERE l.locktype = 'advisory' AND l.granted AND l.objsubid = 1
    AND l.database = (SELECT oid FROM pg_catalog.pg_database WHERE datname = current_database())
    AND l.classid::bigint = ((hashtext(sqlc.arg(lock_name)::text)::bigint >> 32) & 4294967295)
    AND l.objid::bigint = (hashtext(sqlc.arg(lock_name)::text)::bigint & 4294967295)
)::boolean AS present;

-- name: MobileAppSetting :one
-- settings('mobile_app'): the floor half (minAllowedVersion) and the release half (latestVersion), ADR 0007.
SELECT coalesce(value->>'minAllowedVersion', '')::text AS min_allowed_version,
       coalesce(value->>'latestVersion', '')::text AS latest_version
FROM settings WHERE key = 'mobile_app';

-- name: InstallationVersions :many
-- Run in db.WithPrincipal: RLS gives tenant staff the installations of their tenant reach, a platform principal
-- under X-Act-On-Tenant: * every row. Per app version: installations, and those seen since `since`.
SELECT coalesce(app_version, '')::text AS app_version, count(*)::int AS installs,
       count(*) FILTER (WHERE last_seen_at >= sqlc.arg(since)::timestamptz)::int AS seen
FROM mobile_installations
GROUP BY 1;
