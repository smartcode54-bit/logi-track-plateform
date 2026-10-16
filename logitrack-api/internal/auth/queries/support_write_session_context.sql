-- name: SupportWriteSessionContext :one
UPDATE sessions SET access_mode=$2, active_tenant_id=$3,
 support_return_tenant_id=$4, context_version=context_version+1 WHERE id=$1 RETURNING context_version;
