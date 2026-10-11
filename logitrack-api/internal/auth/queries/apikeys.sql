-- X-Api-Key authentication (Appendix C §C.4.3 (3), §C.4.11; T32). api_keys is platform-only under RLS, so both
-- statements run inside db.WithSystem; the administration (/v1/api-keys*) lives in internal/iam.

-- name: GetAPIKeyByPrefix :one
-- One indexed read per keyed request (key_prefix is UNIQUE), so a revocation is effective on the next request
-- on every replica. The key's tenant status travels along: a suspended or pending tenant's keys are refused.
SELECT k.id, k.scope, k.key_hash, k.tenant_id, k.capabilities, k.rate_per_min, k.expires_at, k.revoked_at,
       k.last_used_at, t.status AS tenant_status
FROM api_keys k
LEFT JOIN tenants t ON t.id = k.tenant_id
WHERE k.key_prefix = sqlc.arg(key_prefix);

-- name: TouchAPIKey :exec
-- last_used_at, written after the response path at most once a minute per key (never moves backwards).
UPDATE api_keys
SET last_used_at = sqlc.arg(used_at)::timestamptz
WHERE id = sqlc.arg(id) AND (last_used_at IS NULL OR last_used_at < sqlc.arg(used_at)::timestamptz);
