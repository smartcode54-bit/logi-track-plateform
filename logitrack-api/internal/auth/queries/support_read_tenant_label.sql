-- name: SupportReadTenantLabel :one
SELECT name_th,name_en,kind,status,code,
 (kind='carrier' AND legal_type IS DISTINCT FROM 'company'),support_access_enabled,support_access_version FROM tenants WHERE id=$1;
