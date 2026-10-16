-- name: SupportLockConsentVersion :one
SELECT support_access_version FROM tenants WHERE id=$1 AND kind IN ('own_fleet','carrier') FOR SHARE;
