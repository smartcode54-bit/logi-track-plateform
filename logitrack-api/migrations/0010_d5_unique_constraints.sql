-- 0010_d5_unique_constraints.sql: Appendix A §A.4 D5.
-- Follows Appendix A until the P0 deploy applies the baseline to a database that is kept; after that it is
-- never edited and follow-ups take the next free number (as 0001_preamble.sql).
-- +goose NO TRANSACTION
-- Authored in issue T04 with the baseline; applied as a step of the P1 initial full-load runbook (issue T24) after the
-- owner signs off the ETL quarantine report (R59, R88).
-- Production: goose up-to 9 -> ETL -> quarantine sign-off -> goose up.
-- Dev / CI / seed: applied right after 0009 (no legacy duplicates).
-- CREATE INDEX CONCURRENTLY cannot run inside a transaction block (R31); each statement runs on its own.

-- +goose Up
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS vehicle_expenses_fuel_taxinv
  ON vehicle_expenses (driver_id, tax_inv_id) WHERE expense_type = 'fuel' AND tax_inv_id IS NOT NULL;
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS chats_one_open_per_driver
  ON chats (driver_id) WHERE status <> 'closed';
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS customer_service_fees_one_per_type
  ON customer_service_fees (billing_party_id, fee_type) WHERE fee_type <> 'custom';
-- IF NOT EXISTS also accepts an INVALID index left by an earlier failed run: refuse to continue in that case.
-- +goose StatementBegin
DO $$
DECLARE bad text;
BEGIN
  SELECT string_agg(c.relname::text, ', ') INTO bad
    FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
   WHERE c.relname IN ('vehicle_expenses_fuel_taxinv','chats_one_open_per_driver','customer_service_fees_one_per_type')
     AND NOT i.indisvalid;
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'D5 unique index(es) % are INVALID: duplicates survived the sign-off; DROP INDEX CONCURRENTLY, fix the rows, rerun goose up', bad
      USING ERRCODE = 'unique_violation';
  END IF;
END
$$;
-- +goose StatementEnd
DROP INDEX CONCURRENTLY IF EXISTS vehicle_expenses_fuel_taxinv_lookup;   -- superseded by the unique index

-- +goose Down
CREATE INDEX CONCURRENTLY IF NOT EXISTS vehicle_expenses_fuel_taxinv_lookup
  ON vehicle_expenses (driver_id, tax_inv_id) WHERE expense_type = 'fuel' AND tax_inv_id IS NOT NULL;
DROP INDEX CONCURRENTLY IF EXISTS customer_service_fees_one_per_type;
DROP INDEX CONCURRENTLY IF EXISTS chats_one_open_per_driver;
DROP INDEX CONCURRENTLY IF EXISTS vehicle_expenses_fuel_taxinv;
