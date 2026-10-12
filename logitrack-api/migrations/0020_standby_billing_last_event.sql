-- 0020_standby_billing_last_event.sql. Never edit this file once it is merged; follow-ups take the next number (R31).
-- Appendix A §A.2.3 (issue T38). The outbox high-water mark of a standby record's inline billing snapshot, the twin of
-- trip_billing_snapshots.last_event_id (0005): billing.compute locks the record FOR UPDATE and drops a message whose
-- outbox id is not above it (R17, R57, main spec §6.13 "Standby: standby_records FOR UPDATE, same rule"). Producers of
-- the record's events (standby.completed, standby.customer_assigned) append while holding the row lock, so the ids of
-- one record follow commit order and a stale redelivery can never undo a newer price. bigint without FK, like its
-- twin: outbox_events.id is pruned after 7 days. NULL = no event applied yet (every row before this file, and rows the
-- ETL loads). Written only by the billing service, with the other billing_* columns; like them it stays out of
-- scope_standby (§C.3.7), which lists its columns explicitly, and out of every projection for the executing tenant.
-- Depends on: 0004 (standby_records). No GRANT: the table-level privileges of 0009 cover the new column. No backfill.
-- Production applies it with `migrate up` before the worker runs billing.compute (T38); it depends on nothing in
-- 0010-0019.

-- +goose Up
ALTER TABLE standby_records ADD COLUMN billing_last_event_id bigint;

-- +goose Down
ALTER TABLE standby_records DROP COLUMN billing_last_event_id;
