-- security_events (Appendix A 0008_platform, Appendix C §C.4.13, R85): append-only; this package is its
-- only writer. Rows are appended in the caller's WithSystem transaction, with the change they describe.

-- name: InsertSecurityEvent :exec
-- actor_email is the actor's address when the event is written, as the legacy feed recorded it: the caller's value
-- (the sign-in events pass the address that was typed), else the users row of actor_user_id (every caller runs in
-- WithSystem or as the ETL role, so RLS does not hide it). A later email change does not rewrite history.
INSERT INTO security_events (event_type, severity, summary, details, actor_user_id, actor_email, target_user_id,
                             tenant_id, request_id, created_at)
VALUES (sqlc.arg(event_type), sqlc.arg(severity), sqlc.arg(summary), sqlc.arg(details), sqlc.narg(actor_user_id)::uuid,
        coalesce(sqlc.narg(actor_email)::text,
                 (SELECT u.email::text FROM users u WHERE u.id = sqlc.narg(actor_user_id)::uuid)),
        sqlc.narg(target_user_id), sqlc.narg(tenant_id), sqlc.narg(request_id), sqlc.arg(created_at));

-- Reads (T51, GET /v1/security-events and the overview of GET /v1/security/overview) run in the caller's
-- db.WithPrincipal transaction, so RLS decides the rows (Appendix C §C.3.5 0008_platform): tenant staff read the
-- rows of their tenant (p_staff_read), a platform principal under X-Act-On-Tenant: * every row (p_bypass,
-- read-only), under X-Act-On-Tenant: <uuid> the rows of that tenant.

-- name: ListSecurityEvents :many
-- Keyset page, newest first (created_at DESC, id DESC: index security_events_feed, Appendix B §B.1.5).
SELECT id, created_at, event_type, severity, summary, details, actor_user_id, actor_email, actor_legacy_uid,
       target_user_id, tenant_id, request_id
FROM security_events
WHERE (cardinality(sqlc.arg(types)::text[]) = 0 OR event_type = ANY (sqlc.arg(types)::text[]))
  AND (sqlc.narg(severity)::text IS NULL OR severity = sqlc.narg(severity)::text)
  AND (sqlc.narg(from_at)::timestamptz IS NULL OR created_at >= sqlc.narg(from_at)::timestamptz)
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR created_at < sqlc.narg(to_at)::timestamptz)
  AND (sqlc.narg(after_created)::timestamptz IS NULL
    OR (created_at, id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(row_limit);

-- name: SecurityEventAges :many
-- The overview histogram: the events of the 7 days before now visible to the caller, by whole hours of age and
-- severity (at most 168 x 3 groups).
SELECT floor(extract(epoch FROM (sqlc.arg(now)::timestamptz - created_at)) / 3600)::int AS age_hours,
       severity, count(*)::int AS events
FROM security_events
WHERE created_at > sqlc.arg(now)::timestamptz - interval '7 days' AND created_at <= sqlc.arg(now)::timestamptz
GROUP BY 1, 2;
