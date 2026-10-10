-- name: IdempotencyClaim :one
-- Durable Idempotency-Key records (Appendix B §B.1.5, R53; idempotency_keys is RLS-exempt and only the
-- httpx/idempotency middleware touches it). Claims (scope, key) for one execution: inserts an in_progress
-- row, or takes over a row that expired (IDEMPOTENCY_TTL) or an in_progress row older than the in-flight
-- lock (its holder died). The returned created_at is the claim's fencing token for Complete and Release.
-- No row: the key is completed or held by a live request; read it with IdempotencyGet.
INSERT INTO idempotency_keys AS k (scope, key, request_hash, status, expires_at)
VALUES (@scope, @key, @request_hash, 'in_progress', now() + make_interval(secs => @ttl_seconds::float8))
ON CONFLICT (scope, key) DO UPDATE
   SET request_hash  = EXCLUDED.request_hash,
       status        = 'in_progress',
       response_code = NULL,
       response_body = NULL,
       created_at    = now(),
       expires_at    = EXCLUDED.expires_at
 WHERE k.expires_at <= now()
    OR (k.status = 'in_progress' AND k.created_at < now() - make_interval(secs => @lock_seconds::float8))
RETURNING k.created_at;

-- name: IdempotencyGet :one
-- The live (not expired) record of (scope, key).
SELECT request_hash, status, response_code, response_body, created_at, expires_at
FROM idempotency_keys
WHERE scope = @scope AND key = @key AND expires_at > now();

-- name: IdempotencyComplete :execrows
-- Stores the response of the claim made at claimed_at.
UPDATE idempotency_keys
   SET status = 'completed', response_code = @response_code::int, response_body = @response_body::jsonb
 WHERE scope = @scope AND key = @key AND status = 'in_progress' AND created_at = @claimed_at;

-- name: IdempotencyRelease :execrows
-- Drops the claim made at claimed_at: the request failed and committed nothing, so a retry with the
-- same key runs again.
DELETE FROM idempotency_keys
 WHERE scope = @scope AND key = @key AND status = 'in_progress' AND created_at = @claimed_at;

-- name: IdempotencyPrune :execrows
-- idempotency.prune (scheduler, Appendix B §B.5.6): deletes up to batch_size expired rows. Rows a live
-- claim is taking over are skipped (SKIP LOCKED), and the expiry is tested on the target row as well:
-- after a lock wait PostgreSQL rechecks only the outer WHERE against the newest row version, so a row
-- re-claimed meanwhile (expires_at moved forward) is never deleted.
DELETE FROM idempotency_keys
 WHERE (scope, key) IN (SELECT i.scope, i.key FROM idempotency_keys AS i
                         WHERE i.expires_at <= now() ORDER BY i.expires_at LIMIT @batch_size::int
                         FOR UPDATE SKIP LOCKED)
   AND expires_at <= now();
