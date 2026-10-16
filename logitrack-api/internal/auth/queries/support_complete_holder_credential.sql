-- name: SupportCompleteHolderCredential :exec
UPDATE users SET credential_self_chosen_at = $2 WHERE id = $1;
