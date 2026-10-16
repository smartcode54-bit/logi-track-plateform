-- name: SupportReadUserState :one
SELECT status, auth_version FROM users WHERE id=$1;
