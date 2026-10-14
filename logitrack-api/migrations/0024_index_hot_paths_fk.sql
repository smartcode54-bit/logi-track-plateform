-- 0024_index_hot_paths_fk.sql. Never edit this file once it is merged; follow-ups take the next number (R31).
-- Appendix A §A.2.19 (issue T61, first pass: M1 master data and the load-scale M3 tables). Index-only file, built
-- CONCURRENTLY so a large table keeps taking writes while it builds (R31); each statement runs on its own.
--   * Keyset lists of the two largest tables of the load profile (T16): the own-fleet staff list reaches the own fleet
--     and its subtenants (tenant_id = ANY(...)), which the tenant-leading indexes of 0005/0006 serve only as a bitmap
--     scan plus a sort of every reachable row. A global index on the sort key lets the first page stop after LIMIT rows
--     (as tasks_created / trip_records_created / security_events_feed do).
--   * Foreign-key columns whose parent rows are deleted at run time: storage.gc deletes expired pending
--     file_objects (one DELETE per file, every referencing FK checked), the trip stop editor and the ETL mirror delete
--     trip_delivery_stops (trip_photos.stop_id ON DELETE SET NULL), auth.token-cleanup deletes refresh_tokens
--     (self reference replaced_by). Without an index each check reads the whole child table.
-- Every other foreign key without an index has its reason in migrations/index_integration_test.go (fkWithoutIndex).
-- Depends on: 0002-0007 (the tables). No GRANT: an index needs none.
-- +goose NO TRANSACTION

-- +goose Up
CREATE INDEX CONCURRENTLY IF NOT EXISTS customer_rate_entries_keyset
  ON customer_rate_entries (effective_from_at DESC, import_id COLLATE "C" DESC, id DESC);
CREATE INDEX CONCURRENTLY IF NOT EXISTS vehicle_expenses_keyset
  ON vehicle_expenses (expense_at DESC, id DESC);
CREATE INDEX CONCURRENTLY IF NOT EXISTS vehicle_expenses_receipt_file
  ON vehicle_expenses (receipt_file_id) WHERE receipt_file_id IS NOT NULL;
CREATE INDEX CONCURRENTLY IF NOT EXISTS vehicle_expenses_odometer_file
  ON vehicle_expenses (odometer_file_id) WHERE odometer_file_id IS NOT NULL;
CREATE INDEX CONCURRENTLY IF NOT EXISTS trip_photos_file
  ON trip_photos (file_id);
CREATE INDEX CONCURRENTLY IF NOT EXISTS trip_photos_stop
  ON trip_photos (stop_id) WHERE stop_id IS NOT NULL;
CREATE INDEX CONCURRENTLY IF NOT EXISTS standby_photos_file
  ON standby_photos (file_id);
CREATE INDEX CONCURRENTLY IF NOT EXISTS tasks_check_in_photo_file
  ON tasks (check_in_photo_file_id) WHERE check_in_photo_file_id IS NOT NULL;
CREATE INDEX CONCURRENTLY IF NOT EXISTS tasks_check_in_screenshot_file
  ON tasks (check_in_app_screenshot_file_id) WHERE check_in_app_screenshot_file_id IS NOT NULL;
CREATE INDEX CONCURRENTLY IF NOT EXISTS incident_reports_map_photo_file
  ON incident_reports (map_photo_file_id) WHERE map_photo_file_id IS NOT NULL;
CREATE INDEX CONCURRENTLY IF NOT EXISTS incident_reports_situation1_file
  ON incident_reports (situation1_photo_file_id) WHERE situation1_photo_file_id IS NOT NULL;
CREATE INDEX CONCURRENTLY IF NOT EXISTS incident_reports_situation2_file
  ON incident_reports (situation2_photo_file_id) WHERE situation2_photo_file_id IS NOT NULL;
CREATE INDEX CONCURRENTLY IF NOT EXISTS maintenance_files_file
  ON maintenance_files (file_id);
CREATE INDEX CONCURRENTLY IF NOT EXISTS transactions_receipt_file
  ON transactions (receipt_file_id) WHERE receipt_file_id IS NOT NULL;
CREATE INDEX CONCURRENTLY IF NOT EXISTS chat_messages_image_file
  ON chat_messages (image_file_id) WHERE image_file_id IS NOT NULL;
CREATE INDEX CONCURRENTLY IF NOT EXISTS refresh_tokens_replaced_by
  ON refresh_tokens (replaced_by) WHERE replaced_by IS NOT NULL;
-- IF NOT EXISTS also accepts an INVALID index left by an interrupted build: stop instead of carrying it on.
-- +goose StatementBegin
DO $$
DECLARE bad text;
BEGIN
  SELECT string_agg(c.relname::text, ', ') INTO bad
    FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
   WHERE c.relname IN ('customer_rate_entries_keyset', 'vehicle_expenses_keyset', 'vehicle_expenses_receipt_file',
                       'vehicle_expenses_odometer_file', 'trip_photos_file', 'trip_photos_stop', 'standby_photos_file',
                       'tasks_check_in_photo_file', 'tasks_check_in_screenshot_file', 'incident_reports_map_photo_file',
                       'incident_reports_situation1_file', 'incident_reports_situation2_file', 'maintenance_files_file',
                       'transactions_receipt_file', 'chat_messages_image_file', 'refresh_tokens_replaced_by')
     AND NOT i.indisvalid;
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'index(es) % are INVALID after an interrupted build: DROP INDEX CONCURRENTLY each, then rerun goose up', bad;
  END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS refresh_tokens_replaced_by;
DROP INDEX CONCURRENTLY IF EXISTS chat_messages_image_file;
DROP INDEX CONCURRENTLY IF EXISTS transactions_receipt_file;
DROP INDEX CONCURRENTLY IF EXISTS maintenance_files_file;
DROP INDEX CONCURRENTLY IF EXISTS incident_reports_situation2_file;
DROP INDEX CONCURRENTLY IF EXISTS incident_reports_situation1_file;
DROP INDEX CONCURRENTLY IF EXISTS incident_reports_map_photo_file;
DROP INDEX CONCURRENTLY IF EXISTS tasks_check_in_screenshot_file;
DROP INDEX CONCURRENTLY IF EXISTS tasks_check_in_photo_file;
DROP INDEX CONCURRENTLY IF EXISTS standby_photos_file;
DROP INDEX CONCURRENTLY IF EXISTS trip_photos_stop;
DROP INDEX CONCURRENTLY IF EXISTS trip_photos_file;
DROP INDEX CONCURRENTLY IF EXISTS vehicle_expenses_odometer_file;
DROP INDEX CONCURRENTLY IF EXISTS vehicle_expenses_receipt_file;
DROP INDEX CONCURRENTLY IF EXISTS vehicle_expenses_keyset;
DROP INDEX CONCURRENTLY IF EXISTS customer_rate_entries_keyset;
