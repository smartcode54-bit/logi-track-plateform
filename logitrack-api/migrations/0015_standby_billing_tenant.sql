-- 0015_standby_billing_tenant.sql. Never edit this file once it is merged; follow-ups take the next number (R31).
-- Appendix A §A.2.13 (issue T37 review). The billing carrier of a priced standby record, stored with its price (R61):
-- trip_billing_snapshots carry their billing tenant, standby_records did not, so the invoice set of a statement derived
-- it from the live pricing source (the standby_rate_entries row, else the party's current 'standby' service fee). Fees
-- are mutable and hard-deletable and ETL rows reach their rate entry only once T24 links it, so a priced standby fell
-- out of every invoice when its fee was deleted or re-typed. Pricing (T38) writes billing_tenant_id with the price: the
-- carrier of billing.LoadPricingTables. A priced row without one (the ETL loads at the P0 schema `up-to 9` + `apply 11`
-- and never names this column, nor does its later sync) belongs to the carrier of the rate entry it names (immutable,
-- never deleted), else to the own fleet, the only billing carrier today: the rule the operations projection already
-- applies (internal/operations.BillingVisible, never through customer_service_fees, whose one standby fee per party may
-- change hands), which the statement's invoice set now applies too. No CHECK, so the P1 load and the P2 sync need no
-- change. Like the other inline billing columns it stays out of scope_standby, the projection for dispatchers and
-- customers (§C.3.7), which lists its columns explicitly.
-- Depends on: 0002 (tenants), 0004 (standby_records), 0005 (standby_rate_entries). No GRANT: the table-level privileges
-- of 0009 cover the new column. Production applies it with the P1 runbook's `migrate up` (T24), after the full load, so
-- the backfill covers the loaded rows; it depends on nothing in 0010-0014.

-- +goose Up
ALTER TABLE standby_records ADD COLUMN billing_tenant_id uuid REFERENCES tenants(id);

-- Priced rows before this file: the carrier of the rate entry they name, else the own fleet (the rule above). The table
-- forces RLS on its owner too, so the backfill runs with the bypass of a system transaction (transaction-local, reset
-- below).
SELECT set_config('app.bypass_tenant', 'on', true);
UPDATE standby_records sr
SET billing_tenant_id = coalesce(
      (SELECT r.tenant_id FROM standby_rate_entries r WHERE r.id = sr.billing_rate_entry_id),
      (SELECT t.id FROM tenants t WHERE t.kind = 'own_fleet'))
WHERE sr.billing_estimate_thb IS NOT NULL AND sr.billing_tenant_id IS NULL;
SELECT set_config('app.bypass_tenant', '', true);

-- The invoice set of a statement: completed standby of one party and billing carrier by ended_at.
CREATE INDEX standby_records_party_billing_tenant ON standby_records (billing_party_id, billing_tenant_id, ended_at)
  WHERE status = 'completed';

-- +goose Down
DROP INDEX standby_records_party_billing_tenant;
ALTER TABLE standby_records DROP COLUMN billing_tenant_id;
