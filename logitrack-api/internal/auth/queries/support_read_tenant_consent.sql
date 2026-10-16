-- name: SupportReadTenantConsent :one
SELECT kind, support_access_enabled, support_access_version FROM tenants WHERE id=$1 AND kind IN ('own_fleet','carrier');
