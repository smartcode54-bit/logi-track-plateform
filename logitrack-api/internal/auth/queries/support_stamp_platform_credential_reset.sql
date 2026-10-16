-- name: SupportStampPlatformCredentialReset :exec
UPDATE users SET credential_reset_by_platform_at=$2 WHERE id=$1 AND EXISTS (SELECT 1 FROM user_platform_roles WHERE user_id=$3);
