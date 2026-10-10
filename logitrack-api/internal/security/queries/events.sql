-- security_events (Appendix A 0008_platform, Appendix C §C.4.13, R85): append-only; this package is its
-- only writer. Rows are appended in the caller's WithSystem transaction, with the change they describe.

-- name: InsertSecurityEvent :exec
INSERT INTO security_events (event_type, severity, summary, details, actor_user_id, actor_email, target_user_id,
                             tenant_id, request_id, created_at)
VALUES (sqlc.arg(event_type), sqlc.arg(severity), sqlc.arg(summary), sqlc.arg(details), sqlc.narg(actor_user_id),
        sqlc.narg(actor_email), sqlc.narg(target_user_id), sqlc.narg(tenant_id), sqlc.narg(request_id),
        sqlc.arg(created_at));
