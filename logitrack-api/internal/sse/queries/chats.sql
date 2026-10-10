-- name: ChatAudience :one
-- The tenant and the driver of a chat, for an explicit chat:{id} topic of GET /v1/events and
-- GET /v1/mobile/events (T12). It runs in the request principal's WithPrincipal transaction, so RLS
-- decides whether the row is returned at all: staff see the chats of their tenant reach, a driver its
-- own chat (Appendix C §C.3.5).
SELECT tenant_id, driver_id
FROM chats
WHERE id = sqlc.arg(id);
