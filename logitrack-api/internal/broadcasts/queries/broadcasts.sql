-- Broadcasts, their recipients and read receipts (T48, issue #68; Appendix A §A.2.6, Appendix B §B.2.16, §B.2.21).
-- Every query runs in the caller's request transaction (db.WithPrincipal), so RLS decides the rows (Appendix C §C.3.5,
-- 0007 and 0022): staff read the broadcasts of their tenant reach and the platform-wide ones (tenant_id NULL) and write
-- those of their reach, a steward also the platform-wide ones (R60); a driver reads the broadcasts of its tenant, the
-- platform-wide ones and the ones addressed to it, never a voided one; recipients and reads follow their broadcast, a
-- driver reads and writes only its own read receipt. The staff queries also name the reach
-- (authz.Principal.TenantReach, Appendix C §C.3.8): RLS is the safety net.

-- name: ListBroadcasts :many
-- GET /v1/broadcasts (BroadcastComposer.tsx:139-144, BroadcastHistoryModal.tsx:97-102): the history, newest first,
-- keyset (sent_at, id); voided broadcasts are left out (R14: the legacy pages deleted them). read_count is the number of
-- readers (broadcast_reads replaces readDriverAuthIds[] and readCount).
SELECT sqlc.embed(b),
       (SELECT count(*) FROM broadcast_reads r WHERE r.broadcast_id = b.id)::bigint AS read_count
FROM broadcasts b
WHERE b.voided_at IS NULL
  AND (b.tenant_id IS NULL OR sqlc.arg(all_tenants)::boolean OR b.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND (NOT sqlc.arg(has_cursor)::boolean
       OR b.sent_at < sqlc.arg(cursor_at)::timestamptz
       OR (b.sent_at = sqlc.arg(cursor_at)::timestamptz AND b.id < sqlc.arg(cursor_id)::uuid))
ORDER BY b.sent_at DESC, b.id DESC
LIMIT sqlc.arg(row_limit);

-- name: GetBroadcast :one
-- A broadcast the caller may read, with its read count (the answer of POST /v1/broadcasts, and the read before a void).
SELECT sqlc.embed(b),
       (SELECT count(*) FROM broadcast_reads r WHERE r.broadcast_id = b.id)::bigint AS read_count
FROM broadcasts b
WHERE b.id = sqlc.arg(id)::uuid;

-- name: LockBroadcast :one
-- A void locks its broadcast first, so two voids of one broadcast emit one event.
SELECT * FROM broadcasts WHERE id = sqlc.arg(id)::uuid FOR UPDATE;

-- name: VoidBroadcast :one
-- DELETE /v1/broadcasts/{id}: the soft delete (R14); voided_by is the staff member.
UPDATE broadcasts SET voided_at = sqlc.arg(voided_at)::timestamptz, voided_by = sqlc.narg(voided_by)::uuid
WHERE id = sqlc.arg(id)::uuid AND voided_at IS NULL
RETURNING *;

-- name: CallerName :one
-- created_by_name of a broadcast: the sender's display name, else its email (sendBroadcast took the Firebase user's
-- displayName || email, fn:chat.ts:163-168). The caller's own users row (p_self_read).
SELECT coalesce(nullif(btrim(display_name), ''), nullif(btrim(email::text), ''), '')::text AS name
FROM users
WHERE id = sqlc.arg(id)::uuid;

-- name: GroupAudience :many
-- The users a tenant recipient group addresses (sendBroadcast took the drivers' authIds from the browser,
-- BroadcastComposer.tsx:56-74; here the server reads them): the linked logins of the drivers of the caller's tenant
-- (own_staff: full-time and part-time, own_subcontractors: employment_type subcontractor) and of the carriers working
-- for it (carriers: contractor reach, R60), when the driver is not inactive, its login active or reset_required and its
-- tenant active. Read under the caller's RLS: a staff member reaches exactly these drivers.
SELECT DISTINCT d.user_id::uuid AS user_id
FROM drivers d
JOIN users u ON u.id = d.user_id
JOIN tenants t ON t.id = d.tenant_id
WHERE d.user_id IS NOT NULL AND d.status <> 'inactive' AND u.status IN ('active', 'reset_required') AND t.status = 'active'
  AND ((d.tenant_id = sqlc.arg(tenant_id)::uuid
        AND ((sqlc.arg(own_staff)::boolean AND d.employment_type <> 'subcontractor')
             OR (sqlc.arg(own_subcontractors)::boolean AND d.employment_type = 'subcontractor')))
       OR (sqlc.arg(carriers)::boolean AND d.tenant_id = ANY (sqlc.arg(subtenant_ids)::uuid[])))
ORDER BY 1;

-- name: SelectedAudience :many
-- The users of recipientUserIds the caller reaches: each must be the linked login of a driver of the caller's tenant
-- or of a carrier working for it (the same rules as GroupAudience); the rest are refused by the service.
SELECT DISTINCT d.user_id::uuid AS user_id
FROM drivers d
JOIN users u ON u.id = d.user_id
JOIN tenants t ON t.id = d.tenant_id
WHERE d.user_id = ANY (sqlc.arg(user_ids)::uuid[])
  AND d.status <> 'inactive' AND u.status IN ('active', 'reset_required') AND t.status = 'active'
  AND (d.tenant_id = sqlc.arg(tenant_id)::uuid OR d.tenant_id = ANY (sqlc.arg(subtenant_ids)::uuid[]))
ORDER BY 1;

-- name: PlatformAudience :many
-- The users of every tenant's drivers (the all_tenants group, stewards only: broadcast_platform_audience, 0022).
SELECT a.user_id::uuid AS user_id FROM broadcast_platform_audience() a ORDER BY 1;

-- name: InsertBroadcast :one
INSERT INTO broadcasts (tenant_id, created_by_user_id, created_by_name, title, message_text, recipient_group,
                        recipient_count, sent_at, created_at)
VALUES (sqlc.narg(tenant_id)::uuid, sqlc.narg(created_by_user_id)::uuid, sqlc.narg(created_by_name)::text,
        sqlc.narg(title)::text, sqlc.arg(message_text)::text, sqlc.arg(recipient_group)::text,
        sqlc.arg(recipient_count)::int, sqlc.arg(sent_at)::timestamptz, sqlc.arg(sent_at)::timestamptz)
RETURNING *;

-- name: InsertRecipients :execrows
-- The recipients of a new broadcast (persisted from the cut-over; legacy rows have none).
INSERT INTO broadcast_recipients (broadcast_id, user_id)
SELECT sqlc.arg(broadcast_id)::uuid, unnest(sqlc.arg(user_ids)::uuid[]);

-- name: DriverBroadcasts :many
-- GET /v1/mobile/broadcasts (broadcast_repository.dart:8-28): the broadcasts addressed to the driver (a recipient
-- row, app_broadcast_recipient) and those without recipients the driver may read (legacy and seeded rows, addressed by
-- their recipient_group: broadcast_unaddressed), never a voided one; newest first, keyset (sent_at, id). read_at is the
-- driver's own read receipt.
SELECT b.id, b.legacy_doc_id, b.created_by_name, b.title, b.message_text, b.sent_at, r.read_at
FROM broadcasts b
LEFT JOIN broadcast_reads r ON r.broadcast_id = b.id AND r.user_id = sqlc.arg(user_id)::uuid
WHERE b.voided_at IS NULL
  AND (app_broadcast_recipient(b.id)
       OR ((b.tenant_id IS NULL OR b.tenant_id = sqlc.arg(tenant_id)::uuid) AND broadcast_unaddressed(b.id)))
  AND (NOT sqlc.arg(has_cursor)::boolean
       OR b.sent_at < sqlc.arg(cursor_at)::timestamptz
       OR (b.sent_at = sqlc.arg(cursor_at)::timestamptz AND b.id < sqlc.arg(cursor_id)::uuid))
ORDER BY b.sent_at DESC, b.id DESC
LIMIT sqlc.arg(row_limit);

-- name: FindDriverBroadcast :one
-- POST /v1/mobile/broadcasts/{id}/read: the broadcast by its uuid or its Firestore document id (legacy_doc_id: the
-- 3.x app knows the document id, which the projection mints as the uuid of a broadcast born in PostgreSQL), among the
-- broadcasts DriverBroadcasts lists for the driver: addressed to it (a recipient row), or without recipients (legacy and
-- seeded rows, addressed by their group); never a voided one. RLS limits the rows a driver sees to its tenant's, the
-- platform-wide ones and those addressed to it, so an unaddressed row here is one of the first two: a broadcast of the
-- driver's own tenant addressed to other drivers is not one the driver may mark read (its readCount would count a
-- non-recipient).
SELECT id, tenant_id FROM broadcasts
WHERE (id = sqlc.narg(id)::uuid OR legacy_doc_id = sqlc.arg(ref)::text) AND voided_at IS NULL
  AND (app_broadcast_recipient(id) OR broadcast_unaddressed(id))
ORDER BY (id = sqlc.narg(id)::uuid) DESC NULLS LAST
LIMIT 1;

-- name: MarkRead :execrows
-- The driver's read receipt; the primary key (broadcast_id, user_id) makes it idempotent: 0 rows = already read.
INSERT INTO broadcast_reads (broadcast_id, user_id, read_at)
VALUES (sqlc.arg(broadcast_id)::uuid, sqlc.arg(user_id)::uuid, sqlc.arg(read_at)::timestamptz)
ON CONFLICT (broadcast_id, user_id) DO NOTHING;
