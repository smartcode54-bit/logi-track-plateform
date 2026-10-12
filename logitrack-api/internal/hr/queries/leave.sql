-- Leave requests (T45, Appendix B §B.2.17; Appendix A §A.2.7). leave_requests is a tenant table (G-tenant, Appendix C
-- §C.3.0) with the driver's own-row policies; leave_request_attachments follows its request. Staff read and decide the
-- requests of their tenant reach (own tenant and the carriers working for it); request statements run in
-- db.WithPrincipal and also name the reach (main spec §4.7, Appendix C §C.3.8; authz.Principal.TenantReach). Rows in the
-- quarantine tenant stay hidden by the restrictive p_not_quarantine policy.

-- name: ListLeaveRequests :many
-- GET /v1/leave-requests: keyset (created_at, id), newest first (the legacy listener ordered by createdAt desc,
-- leave-requests/page.tsx:63-66); driver_name follows lib/driverName.ts.
SELECT sqlc.embed(l),
       coalesce(nullif(btrim(d.full_name_th), ''), nullif(btrim(d.first_name || ' ' || d.last_name), ''), '')::text
         AS driver_name
FROM leave_requests l
LEFT JOIN drivers d ON d.id = l.driver_id
WHERE (sqlc.arg(all_tenants)::boolean OR l.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND (cardinality(sqlc.arg(statuses)::text[]) = 0 OR l.status = ANY (sqlc.arg(statuses)::text[]))
  AND (sqlc.narg(driver_id)::uuid IS NULL OR l.driver_id = sqlc.narg(driver_id)::uuid)
  AND (sqlc.narg(after_at)::timestamptz IS NULL
       OR (l.created_at, l.id) < (sqlc.narg(after_at)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY l.created_at DESC, l.id DESC
LIMIT sqlc.arg(row_limit);

-- name: LeaveStatusCounts :many
-- The first page's meta.statusCounts: every status of the reach with the other filters applied (the legacy page counted
-- the whole collection in the browser).
SELECT l.status, count(*)::int AS n
FROM leave_requests l
WHERE (sqlc.arg(all_tenants)::boolean OR l.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND (sqlc.narg(driver_id)::uuid IS NULL OR l.driver_id = sqlc.narg(driver_id)::uuid)
GROUP BY l.status;

-- name: GetLeaveRequest :one
SELECT sqlc.embed(l),
       coalesce(nullif(btrim(d.full_name_th), ''), nullif(btrim(d.first_name || ' ' || d.last_name), ''), '')::text
         AS driver_name
FROM leave_requests l
LEFT JOIN drivers d ON d.id = l.driver_id
WHERE l.id = sqlc.arg(id) AND (sqlc.arg(all_tenants)::boolean OR l.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]));

-- name: LockLeaveRequest :one
-- A decision locks its request first: two decisions of one request serialise, and the second sees the first's status.
SELECT * FROM leave_requests
WHERE id = sqlc.arg(id) AND (sqlc.arg(all_tenants)::boolean OR tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
FOR UPDATE;

-- name: DecideLeaveRequest :one
-- approve | reject (from pending only): the decision, its maker and time (legacy approverId / approvedAt, set on a
-- rejection too) and the rejection's reason.
UPDATE leave_requests
SET status = sqlc.arg(status), approver_user_id = sqlc.narg(approver_user_id), decided_at = sqlc.arg(decided_at),
    rejection_reason = sqlc.narg(rejection_reason)
WHERE id = sqlc.arg(id) AND status = 'pending'
RETURNING *;

-- name: CancelLeaveRequest :one
-- cancel (an approved request): the status only, as the legacy page wrote (leave-requests/page.tsx:117-129); the
-- decision's maker and time stay.
UPDATE leave_requests SET status = 'cancelled' WHERE id = sqlc.arg(id) AND status = 'approved'
RETURNING *;

-- name: LeaveAttachments :many
-- The evidence of a page of requests, in their order (leave_evidence files, signed after the request transaction).
SELECT leave_request_id, file_id, position FROM leave_request_attachments
WHERE leave_request_id = ANY (sqlc.arg(ids)::uuid[])
ORDER BY leave_request_id, position, file_id;

-- name: SelfLegacyUID :one
-- The caller's Firebase uid (users.legacy_auth_uid, p_self_read): the approverId the legacy web wrote.
SELECT legacy_auth_uid FROM users WHERE id = sqlc.arg(id);
