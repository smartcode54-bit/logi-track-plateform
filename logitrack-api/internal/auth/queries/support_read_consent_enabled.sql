-- name: SupportReadConsentEnabled :one
SELECT support_access_enabled FROM tenants WHERE id=$1;
