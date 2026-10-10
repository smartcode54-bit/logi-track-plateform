-- name: InsertJob :one
-- The id is minted in Go (uuidv7) so the Redis job lock can hold it before the row commits.
INSERT INTO jobs (id, type, owner_user_id, tenant_id, params)
VALUES (@id, @type, @owner_user_id, @tenant_id, @params)
RETURNING *;

-- name: GetJob :one
SELECT * FROM jobs WHERE id = @id;

-- name: ListJobs :many
-- GET /v1/jobs: keyset on (created_at DESC, id DESC); owner NULL = every job (platform callers).
SELECT * FROM jobs
 WHERE (sqlc.narg(owner_user_id)::uuid IS NULL OR owner_user_id = sqlc.narg(owner_user_id)::uuid)
   AND (sqlc.narg(job_type)::text IS NULL OR type = sqlc.narg(job_type)::text)
   AND (sqlc.narg(before_created_at)::timestamptz IS NULL
        OR (created_at, id) < (sqlc.narg(before_created_at)::timestamptz, sqlc.narg(before_id)::uuid))
 ORDER BY created_at DESC, id DESC
 LIMIT @row_limit::int;

-- name: StartJob :one
UPDATE jobs SET status = 'running', started_at = coalesce(started_at, now())
 WHERE id = @id AND status IN ('queued', 'running')
RETURNING *;

-- name: SetJobProgress :one
UPDATE jobs SET progress = @progress WHERE id = @id AND status = 'running'
RETURNING *;

-- name: FinishJob :one
UPDATE jobs SET status = @status, result = @result, error = @error, finished_at = now(),
                started_at = coalesce(started_at, now())
 WHERE id = @id AND status IN ('queued', 'running')
RETURNING *;

-- name: ClaimQueuedJob :one
-- Jobs the scheduler runs itself (queue.replay): the oldest queued one nobody else holds.
SELECT * FROM jobs
 WHERE type = @type AND status = 'queued'
 ORDER BY created_at, id
 LIMIT 1
   FOR UPDATE SKIP LOCKED;

-- name: PruneJobs :execrows
-- jobs.prune (Appendix B §B.5.6): rows created before the cutoff, one batch per call.
DELETE FROM jobs
 WHERE id IN (SELECT j.id FROM jobs AS j WHERE j.created_at < @cutoff::timestamptz ORDER BY j.created_at LIMIT @batch_size::int);

