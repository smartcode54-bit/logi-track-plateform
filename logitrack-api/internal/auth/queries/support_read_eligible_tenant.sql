-- name: SupportReadEligibleTenant :one
SELECT id FROM tenants WHERE id=$1 AND kind IN ('own_fleet','carrier');
