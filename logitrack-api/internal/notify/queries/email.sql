-- name: GetRecipient :one
-- notify.email reads the address server-side: recipients are never taken from a message (main spec §7.6).
SELECT id, coalesce(email::text, '')::text AS email, display_name, status FROM users WHERE id = @id;

