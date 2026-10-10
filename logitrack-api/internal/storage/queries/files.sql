-- file_objects (R1) as the storage service reads and writes it, always under db.WithSystem (Appendix C §C.3.2):
-- callers' rights are checked in Go before any of these run (a file is readable iff its referencing row is, C.3.5).

-- name: InsertPending :one
-- POST /v1/uploads/presign: a pending upload; storage.gc removes it (and its object) after expires_at.
INSERT INTO file_objects (bucket, object_key, tenant_id, purpose, content_type, size_bytes, sha256, visibility,
                          status, uploaded_by, expires_at, storage_backend)
VALUES (@bucket, @object_key, sqlc.narg(tenant_id)::uuid, @purpose, @content_type::text, @size_bytes::bigint,
        sqlc.narg(sha256)::text, 'private', 'pending', @uploaded_by::uuid, @expires_at::timestamptz, @storage_backend)
RETURNING *;

-- name: GetFileByKey :one
-- Keys are unique per bucket; across the two buckets the same key never occurs in practice (app_releases/ lives
-- only in the public one), and a committed row wins over a pending one.
SELECT * FROM file_objects
 WHERE object_key = @object_key AND deleted_at IS NULL
 ORDER BY (status = 'committed') DESC, created_at DESC, id DESC
 LIMIT 1;

-- name: LockFileByKey :one
-- The commit inside the entity transaction: the row is locked, so storage.gc (SKIP LOCKED) and a second commit
-- wait for this transaction.
SELECT * FROM file_objects
 WHERE object_key = @object_key AND deleted_at IS NULL
 ORDER BY (status = 'committed') DESC, created_at DESC, id DESC
 LIMIT 1
 FOR UPDATE;

-- name: GetFile :one
SELECT * FROM file_objects WHERE id = @id AND deleted_at IS NULL;

-- name: ExtendPending :one
-- Re-signing a pending key (offline retry): the row lives another 24 h from now.
UPDATE file_objects SET expires_at = @expires_at::timestamptz
 WHERE id = @id AND status = 'pending'
RETURNING *;

-- name: CommitFile :exec
UPDATE file_objects
   SET status = 'committed', committed_at = @committed_at::timestamptz, expires_at = NULL,
       owner_kind = @owner_kind::text, owner_id = @owner_id::uuid, content_type = @content_type::text,
       size_bytes = @size_bytes::bigint
 WHERE id = @id AND status = 'pending';

-- name: PendingLocalUpload :one
-- The local upload route accepts bytes only for a pending row of the local backend.
SELECT id, size_bytes, content_type FROM file_objects
 WHERE object_key = @object_key AND storage_backend = 'local' AND status = 'pending' AND deleted_at IS NULL
 LIMIT 1;

-- name: ListExpiredPending :many
-- storage.gc candidates (index file_objects_gc) on the backends this process has.
SELECT id FROM file_objects
 WHERE status = 'pending' AND expires_at < @now::timestamptz AND storage_backend = ANY(@backends::text[])
 ORDER BY expires_at, id
 LIMIT @row_limit::int;

-- name: LockExpiredPending :one
-- One candidate at a time; a row a commit holds is skipped (next run), a re-signed one no longer matches.
SELECT * FROM file_objects
 WHERE id = @id AND status = 'pending' AND expires_at < @now::timestamptz
 FOR UPDATE SKIP LOCKED;

-- name: DeleteFile :exec
DELETE FROM file_objects WHERE id = @id AND status = 'pending';

-- name: IsDirectSubtenant :one
-- Contractor reach (R60): tenant is a carrier working for contractor (one level).
SELECT EXISTS (SELECT 1 FROM tenants WHERE id = @tenant_id AND contractor_tenant_id = @contractor_id::uuid);
