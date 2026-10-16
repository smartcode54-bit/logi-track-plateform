-- name: SupportLockTenantConsent :one
SELECT support_access_enabled,support_access_version FROM tenants WHERE id=$1 FOR SHARE;
