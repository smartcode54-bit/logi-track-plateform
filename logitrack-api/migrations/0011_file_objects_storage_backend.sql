-- 0011_file_objects_storage_backend.sql: Appendix A §A.2.9 (issue T11, owner addition of 2026-10-10).
-- The first deployment stores uploads on the server's disk until an S3-compatible service exists, so every
-- registered object records where it lives: 'local' (files under LOCAL_MEDIA_DIR) or 's3' (S3_BUCKET /
-- S3_PUBLIC_BUCKET through minio-go). STORAGE_BACKEND picks the backend of new uploads; reads always dispatch on
-- this column, so objects written under 'local' keep downloading after the switch to 's3' (main spec §9.11).
-- Every image or file reference of 0002-0008 is a *_file_id foreign key to file_objects (table list in
-- Appendix A §A.2.9), so no other table needs a backend column.
-- Depends on: 0002 (file_objects, trg_file_objects_commit_columns). No GRANT/REVOKE: no new table (R66).
-- Production order (R59, R88): P0 runs `migrate up-to 9` then `migrate apply 11`, so this file ships ahead of
-- the held 0010_d5_unique_constraints (it touches no table of 0010); the P1 runbook's `migrate up` applies 0010
-- after the quarantine sign-off. The ETL therefore always loads into a schema that has the column and names
-- storage_backend = 's3' on every copied object (Appendix A §A.2.9).

-- +goose Up
-- Rows that exist when this runs (none in production, which applies it before the ETL; a dev database loaded
-- before T11) are MinIO objects: 's3'. The default exists only for that backfill and is dropped at once, so
-- every later INSERT names its backend (NOT NULL, no default).
ALTER TABLE file_objects ADD COLUMN storage_backend text NOT NULL DEFAULT 's3'
  CONSTRAINT file_objects_storage_backend_check CHECK (storage_backend IN ('local','s3'));
ALTER TABLE file_objects ALTER COLUMN storage_backend DROP DEFAULT;

-- Appendix C §C.3.6 with storage_backend among the columns fixed at upload: outside WithSystem (and cmd/etl) an
-- update may only commit. Moving an object between backends (an optional copy job) runs under WithSystem.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION trg_file_objects_commit_columns() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT app_bypass() AND NOT app_etl_load() AND (
       (NEW.bucket, NEW.object_key, NEW.tenant_id, NEW.purpose, NEW.visibility, NEW.uploaded_by, NEW.legacy_url, NEW.created_at,
        NEW.storage_backend)
         IS DISTINCT FROM (OLD.bucket, OLD.object_key, OLD.tenant_id, OLD.purpose, OLD.visibility, OLD.uploaded_by,
                           OLD.legacy_url, OLD.created_at, OLD.storage_backend)
    OR (NEW.status IS DISTINCT FROM OLD.status AND NOT (OLD.status = 'pending' AND NEW.status = 'committed'))) THEN
    RAISE EXCEPTION 'file_objects: identity, tenant and exposure columns are fixed at upload; status moves only pending -> committed'
      USING ERRCODE = 'insufficient_privilege';
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION trg_file_objects_commit_columns() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT app_bypass() AND NOT app_etl_load() AND (
       (NEW.bucket, NEW.object_key, NEW.tenant_id, NEW.purpose, NEW.visibility, NEW.uploaded_by, NEW.legacy_url, NEW.created_at)
         IS DISTINCT FROM (OLD.bucket, OLD.object_key, OLD.tenant_id, OLD.purpose, OLD.visibility, OLD.uploaded_by,
                           OLD.legacy_url, OLD.created_at)
    OR (NEW.status IS DISTINCT FROM OLD.status AND NOT (OLD.status = 'pending' AND NEW.status = 'committed'))) THEN
    RAISE EXCEPTION 'file_objects: identity, tenant and exposure columns are fixed at upload; status moves only pending -> committed'
      USING ERRCODE = 'insufficient_privilege';
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
ALTER TABLE file_objects DROP COLUMN storage_backend;
