-- 0018_etl_source_docs_tombstones.sql. Never edit this file once it is merged; follow-ups take the next number (R31).
-- Appendix A §A.2.15 (issue T29 review). Two statuses of etl.source_docs for a document the FS->PG mirror settled
-- because Firestore no longer has it, kept as a tombstone instead of deleting the entry:
--   renamed  the former id of a trip renameTripRecord re-keyed (internal/etl renamedFrom): the row took the new id;
--   removed  a document whose row the mirror removed where the legacy app deletes (internal/etl mirrorRemoved).
-- The tombstone's source_update_time is the instant the document was known gone (the renaming batch's commit, the
-- sweep's read time). A sweep and a listener, or two sweeps, read Firestore before they take the load lock, so a run
-- can still hold a copy of the document read before that instant; with the entry deleted, the update-time guard had
-- nothing to refuse it with and the copy loaded as a new row (a renamed trip twice, with a second trip.delivered; a
-- removed holiday back). With the tombstone the copy is not newer and is skipped; a document created again at the path
-- later is newer and applies as usual (the entry takes its status). Every reader that lists documents (reconcile,
-- reconcile-sync, the Removed pass, quarantine resolve) treats both as settled. Only the mirror's runs write them (they
-- overlap one another); a load deletes the entry as before, and the code checks the CHECK for 'renamed' first, so the
-- P1 load at the P0 schema (up-to 9, apply 11) runs before this file.
-- The table is exempt from RLS and written by the ETL login alone: the CHECK is replaced in place (its scan holds the
-- table for the length of the transaction, which only the mirror and `etl` contend for). No GRANT: the privileges of
-- 0009 cover the rows. The Down deletes the tombstones (the entries the code before this file deleted) and restores
-- the CHECK of 0009.
-- Depends on: 0009 (etl.source_docs). Production applies it with `migrate up` before the mirror runs (T24, T29); it
-- depends on nothing in 0010-0017.

-- +goose Up
ALTER TABLE etl.source_docs DROP CONSTRAINT source_docs_status_check;
ALTER TABLE etl.source_docs ADD CONSTRAINT source_docs_status_check
  CHECK (status IN ('pending','loaded','quarantined','rejected','dropped','renamed','removed'));

-- +goose Down
DELETE FROM etl.source_docs WHERE status IN ('renamed','removed');
ALTER TABLE etl.source_docs DROP CONSTRAINT source_docs_status_check;
ALTER TABLE etl.source_docs ADD CONSTRAINT source_docs_status_check
  CHECK (status IN ('pending','loaded','quarantined','rejected','dropped'));
