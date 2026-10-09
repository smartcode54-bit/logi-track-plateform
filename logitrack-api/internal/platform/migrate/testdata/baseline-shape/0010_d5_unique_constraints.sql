-- Test fixture with the file names of the Appendix A chain (T03: up-to 9, status, round trip).
-- Not a migration of the product; the real chain is logitrack-api/migrations (T04).
-- Like the real 0010 (R88): applied after ETL and the owner's sign-off, so it builds the index
-- CONCURRENTLY outside a transaction.
-- +goose NO TRANSACTION
-- +goose Up
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS shape_chats_one_open_per_driver
  ON shape_chats (driver_id) WHERE NOT closed;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS shape_chats_one_open_per_driver;
