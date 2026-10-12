-- Chats, messages and read state (T47, issue #67; Appendix A §A.2.6, Appendix B §B.2.16, §B.2.21). Every query runs in
-- the caller's request transaction (db.WithPrincipal), so RLS decides the rows: staff read and write the chats of their
-- tenant reach (G-tenant, contractor reach included), a driver its own (p_driver_read / p_driver_update), messages and
-- read state follow their chat (p_parent_read; p_own_write: a sender writes its own message and its own read state).
-- The staff queries also name the reach (authz.Principal.TenantReach, Appendix C §C.3.8): RLS is the safety net.

-- name: ListChats :many
-- GET /v1/chats (ConversationsPanel.tsx:127-163): queue = unassigned and not closed, mine = assigned to the caller and
-- not closed, all = every chat of the reach; sort last_message_at DESC (a chat without one last), tie-break id DESC.
-- unread is the server-side count of the other side's messages after the caller's read state (the legacy client
-- compared lastMessageAt with lastReadByAdmin.{uid} itself).
SELECT sqlc.embed(c),
       coalesce(nullif(btrim(d.full_name_th), ''), nullif(btrim(d.first_name || ' ' || d.last_name), ''), '')::text
         AS driver_name,
       d.user_id AS driver_user_id,
       coalesce(d.legacy_auth_uid, '')::text AS driver_legacy_uid,
       au.display_name AS assigned_admin_name,
       rs.last_read_at AS my_last_read_at,
       (SELECT count(*) FROM chat_messages m
         WHERE m.chat_id = c.id AND m.sender_role = sqlc.arg(other_role)::text
           AND m.created_at > coalesce(rs.last_read_at, '-infinity'::timestamptz))::bigint AS unread
FROM chats c
LEFT JOIN drivers d ON d.id = c.driver_id
LEFT JOIN users au ON au.id = c.assigned_admin_user_id
LEFT JOIN chat_read_state rs ON rs.chat_id = c.id AND rs.user_id = sqlc.arg(user_id)::uuid
WHERE (sqlc.arg(all_tenants)::boolean OR c.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND (sqlc.arg(filter)::text <> 'queue' OR (c.assigned_admin_user_id IS NULL AND c.status <> 'closed'))
  AND (sqlc.arg(filter)::text <> 'mine' OR (c.assigned_admin_user_id = sqlc.arg(user_id)::uuid AND c.status <> 'closed'))
  AND (sqlc.narg(status)::text IS NULL OR c.status = sqlc.narg(status)::text)
  AND (sqlc.narg(driver_id)::uuid IS NULL OR c.driver_id = sqlc.narg(driver_id)::uuid)
  AND (NOT sqlc.arg(has_cursor)::boolean
       OR (sqlc.narg(cursor_at)::timestamptz IS NOT NULL
           AND (c.last_message_at < sqlc.narg(cursor_at)::timestamptz
                OR (c.last_message_at = sqlc.narg(cursor_at)::timestamptz AND c.id < sqlc.arg(cursor_id)::uuid)
                OR c.last_message_at IS NULL))
       OR (sqlc.narg(cursor_at)::timestamptz IS NULL AND c.last_message_at IS NULL AND c.id < sqlc.arg(cursor_id)::uuid))
ORDER BY c.last_message_at DESC NULLS LAST, c.id DESC
LIMIT sqlc.arg(row_limit);

-- name: GetChatView :one
-- GET /v1/chats/{id}, the answer of every chat write, and GET /v1/mobile/chat: the chat as ListChats shows it.
SELECT sqlc.embed(c),
       coalesce(nullif(btrim(d.full_name_th), ''), nullif(btrim(d.first_name || ' ' || d.last_name), ''), '')::text
         AS driver_name,
       d.user_id AS driver_user_id,
       coalesce(d.legacy_auth_uid, '')::text AS driver_legacy_uid,
       au.display_name AS assigned_admin_name,
       rs.last_read_at AS my_last_read_at,
       (SELECT count(*) FROM chat_messages m
         WHERE m.chat_id = c.id AND m.sender_role = sqlc.arg(other_role)::text
           AND m.created_at > coalesce(rs.last_read_at, '-infinity'::timestamptz))::bigint AS unread
FROM chats c
LEFT JOIN drivers d ON d.id = c.driver_id
LEFT JOIN users au ON au.id = c.assigned_admin_user_id
LEFT JOIN chat_read_state rs ON rs.chat_id = c.id AND rs.user_id = sqlc.arg(user_id)::uuid
WHERE c.id = sqlc.arg(id)::uuid;

-- name: ChatReadStates :many
-- The read state of every reader of a chat (lastReadByAdmin{uid: ts} + lastReadByDriver): the read receipts of the
-- room. is_driver marks the row of the chat's driver (drivers.user_id).
SELECT rs.user_id, rs.last_read_at, (d.user_id IS NOT NULL AND rs.user_id = d.user_id)::boolean AS is_driver
FROM chat_read_state rs
JOIN chats c ON c.id = rs.chat_id
LEFT JOIN drivers d ON d.id = c.driver_id
WHERE rs.chat_id = sqlc.arg(chat_id)::uuid
ORDER BY rs.last_read_at DESC, rs.user_id;

-- name: LockChat :one
-- Every chat write locks its chat first, so the writes of one chat (and the ids of its outbox events) follow commit
-- order.
SELECT * FROM chats WHERE id = sqlc.arg(id)::uuid FOR UPDATE;

-- name: OpenChatOfDriver :one
-- The driver's chat that is not closed (one per driver: chats_one_open_per_driver, 0010; until that index is built the
-- oldest wins, as the legacy query took the first document).
SELECT * FROM chats
WHERE driver_id = sqlc.arg(driver_id)::uuid AND status <> 'closed'
ORDER BY created_at, id
LIMIT 1
FOR UPDATE;

-- name: OpenChatID :one
-- The driver's chat that is not closed, read without a lock (GET /v1/mobile/chat*; the writes lock it themselves).
SELECT id FROM chats
WHERE driver_id = sqlc.arg(driver_id)::uuid AND status <> 'closed'
ORDER BY created_at, id
LIMIT 1;

-- name: OtherOpenChat :one
-- Another chat of the driver that is not closed (the reopen guard, R14).
SELECT id FROM chats
WHERE driver_id = sqlc.arg(driver_id)::uuid AND status <> 'closed' AND id <> sqlc.arg(id)::uuid
LIMIT 1;

-- name: LockDriverChats :exec
-- Serialises the get-or-create and the reopen of one driver's chat (a transaction-scoped advisory lock), so two
-- concurrent calls never both create an open chat, with or without the 0010 index.
SELECT pg_advisory_xact_lock(hashtext('chat-open:' || sqlc.arg(driver_id)::uuid::text));

-- name: GetDriver :one
-- The driver a chat is opened with (RLS: staff reach, or the driver itself).
SELECT id, tenant_id, user_id, coalesce(legacy_doc_id, '')::text AS legacy_doc_id,
       coalesce(legacy_auth_uid, '')::text AS legacy_auth_uid,
       coalesce(nullif(btrim(full_name_th), ''), nullif(btrim(first_name || ' ' || last_name), ''), '')::text AS name
FROM drivers
WHERE id = sqlc.arg(id)::uuid;

-- name: InsertChat :one
-- A new open chat of a driver in the driver's tenant (tenant_source driver, Appendix C §C.3.10). last_message_at starts
-- at the creation, as the legacy document did (lastMessageAt: serverTimestamp()), so the chat sorts into the lists.
INSERT INTO chats (id, legacy_doc_id, tenant_id, tenant_source, driver_id, status, priority, last_message_at)
VALUES (sqlc.arg(id)::uuid, sqlc.narg(legacy_doc_id)::text, sqlc.arg(tenant_id)::uuid, 'driver', sqlc.arg(driver_id)::uuid,
        'open', 'normal', now())
RETURNING *;

-- name: SetChat :one
-- The state of a chat after a write: status, assignment, priority and the last message, computed by the service from
-- the locked row.
UPDATE chats
SET status                  = sqlc.arg(status)::text,
    assigned_admin_user_id  = sqlc.narg(assigned_admin_user_id)::uuid,
    assigned_at             = sqlc.narg(assigned_at)::timestamptz,
    closed_at               = sqlc.narg(closed_at)::timestamptz,
    priority                = sqlc.arg(priority)::text,
    last_message_preview    = sqlc.narg(last_message_preview)::text,
    last_message_at         = sqlc.narg(last_message_at)::timestamptz,
    last_message_by_user_id = sqlc.narg(last_message_by_user_id)::uuid,
    last_message_type       = sqlc.narg(last_message_type)::text
WHERE id = sqlc.arg(id)::uuid
RETURNING *;

-- name: MessageByClientID :one
-- An offline replay of a send: the message stored under the same clientMessageId (unique per chat, R63).
SELECT * FROM chat_messages
WHERE chat_id = sqlc.arg(chat_id)::uuid AND client_message_id = sqlc.arg(client_message_id)::uuid;

-- name: InsertMessage :one
-- A new message; a concurrent replay of the same clientMessageId inserts nothing (no row returned).
INSERT INTO chat_messages (id, legacy_doc_id, chat_id, sender_user_id, sender_role, text, image_file_id, message_type,
                           client_message_id)
VALUES (sqlc.arg(id)::uuid, sqlc.narg(legacy_doc_id)::text, sqlc.arg(chat_id)::uuid, sqlc.arg(sender_user_id)::uuid,
        sqlc.arg(sender_role)::text, sqlc.narg(text)::text, sqlc.narg(image_file_id)::uuid, 'normal',
        sqlc.arg(client_message_id)::uuid)
ON CONFLICT (chat_id, client_message_id) WHERE client_message_id IS NOT NULL DO NOTHING
RETURNING *;

-- name: ListMessages :many
-- GET /v1/chats/{id}/messages and GET /v1/mobile/chat/messages: keyset DESC on (created_at, id), newest first (W9, 50
-- per page; chat_messages_chat_created). sender_name is the staff's display name (a driver reads no other user: null).
SELECT sqlc.embed(m), u.display_name AS sender_name
FROM chat_messages m
LEFT JOIN users u ON u.id = m.sender_user_id
WHERE m.chat_id = sqlc.arg(chat_id)::uuid
  AND (sqlc.narg(before_at)::timestamptz IS NULL
       OR (m.created_at, m.id) < (sqlc.narg(before_at)::timestamptz, sqlc.arg(before_id)::uuid))
ORDER BY m.created_at DESC, m.id DESC
LIMIT sqlc.arg(row_limit);

-- name: GetMessage :one
SELECT sqlc.embed(m), u.display_name AS sender_name
FROM chat_messages m
LEFT JOIN users u ON u.id = m.sender_user_id
WHERE m.id = sqlc.arg(id)::uuid;

-- name: ReadStateOf :one
SELECT last_read_at FROM chat_read_state
WHERE chat_id = sqlc.arg(chat_id)::uuid AND user_id = sqlc.arg(user_id)::uuid;

-- name: UpsertReadState :one
-- POST /v1/chats/{id}/read (and a sender's own message): the read state only moves forward, and never past the
-- database clock (the clock of every message's created_at; no lastReadAt = now). Returns the stored value.
INSERT INTO chat_read_state (chat_id, user_id, last_read_at)
VALUES (sqlc.arg(chat_id)::uuid, sqlc.arg(user_id)::uuid,
        least(coalesce(sqlc.narg(last_read_at)::timestamptz, now()), now()))
ON CONFLICT (chat_id, user_id) DO UPDATE
SET last_read_at = greatest(chat_read_state.last_read_at, EXCLUDED.last_read_at)
RETURNING last_read_at;

-- name: StaffMember :one
-- POST /v1/chats/{id}/assign to another user: an active staff member of the chat's tenant (memberships p_staff_read:
-- the caller sees the members of its reach).
SELECT EXISTS (
  SELECT 1 FROM memberships m JOIN users u ON u.id = m.user_id
  WHERE m.user_id = sqlc.arg(user_id)::uuid AND m.tenant_id = sqlc.arg(tenant_id)::uuid AND m.status = 'active'
    AND m.role IN ('tenant_admin', 'manager', 'operation_staff', 'operator', 'user') AND u.status = 'active'
)::boolean AS ok;

-- name: DriverUnread :one
-- GET /v1/mobile/chat/unread-count (main_layout.dart:156-173 counted it in the app): the driver's chats that are not
-- closed with staff messages after the driver's read state, and those messages.
SELECT count(*) FILTER (WHERE x.unread > 0)::bigint AS chats, coalesce(sum(x.unread), 0)::bigint AS messages
FROM (SELECT (SELECT count(*) FROM chat_messages m
               WHERE m.chat_id = c.id AND m.sender_role = 'admin'
                 AND m.created_at > coalesce(rs.last_read_at, '-infinity'::timestamptz)) AS unread
      FROM chats c
      LEFT JOIN chat_read_state rs ON rs.chat_id = c.id AND rs.user_id = sqlc.arg(user_id)::uuid
      WHERE c.driver_id = sqlc.arg(driver_id)::uuid AND c.status <> 'closed') x;

-- name: UserRefs :many
-- The ids the 3.x app and the legacy web know for users (Firebase uid = legacy_auth_uid, else the uuid the bridge
-- gives a user born in Go): the senderId, assignedAdminId, lastMessageBy and lastReadByAdmin keys of the write-back.
SELECT id, coalesce(legacy_auth_uid, id::text)::text AS ref FROM users WHERE id = ANY (sqlc.arg(ids)::uuid[]);

-- name: DriverRef :one
-- The id the 3.x app keys a driver's chats by (chats.driverId, Q1): the driver's Firebase uid (drivers.legacy_auth_uid,
-- else its user's legacy_auth_uid, else the uuid the bridge gives a user born in Go), for the write-back.
SELECT coalesce(d.legacy_auth_uid, u.legacy_auth_uid, u.id::text, '')::text AS ref
FROM drivers d
LEFT JOIN users u ON u.id = d.user_id
WHERE d.id = sqlc.arg(id)::uuid;

-- name: ChatIDByLegacyDoc :one
-- The row the mirror made of a chat document the write-back created.
SELECT id FROM chats WHERE legacy_doc_id = sqlc.arg(legacy_doc_id)::text;

-- name: MessageByLegacyDoc :one
-- The row the mirror made of a message document the write-back created.
SELECT * FROM chat_messages
WHERE chat_id = sqlc.arg(chat_id)::uuid AND legacy_doc_id = sqlc.arg(legacy_doc_id)::text;

-- name: TxNow :one
-- The transaction's clock: the clock of every timestamp the chat writes store.
SELECT now()::timestamptz AS now;
