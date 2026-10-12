-- 0023_line_deliveries.sql. Never edit this file once it is merged; follow-ups take the next number (R31).
-- Appendix A §A.2.18 (issue T49; the table is §A.2.6). notification_deliveries, the push log of notify.fcm (0007),
-- also logs the LINE cards of notify.line: one row per card a message settled, so "0 duplicate sends" (the P5
-- cut-over criterion, main spec §12.2) can be read back. Kinds line_checkin, line_delivered, line_standby (the legacy
-- events checkin, delivered, standby); status skipped for a card that is not sent and never will be for this message
-- (no LINE group configured, LINE refused it with a 4xx other than 429, the record is not found or belongs to another
-- driver), next to sent and deduplicated (the record was notified already, or another message holds idem:line within
-- 24 h). A LINE row has no user_id and no install_id; its payload names the record (entity, id, card, forced, jobId,
-- photoCount), never the group id or a token.
-- Depends on: 0007 (notification_deliveries). No GRANT: logitrack_app holds SELECT, INSERT, UPDATE on the table (0009).
-- The Down drops the LINE rows (a log) before it restores the 0007 vocabularies.

-- +goose Up
ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_kind_check;
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_kind_check
  CHECK (kind IN ('task_assigned','task_unassigned','task_cancelled','tasks_changed','maintenance_scheduled','chat',
                  'broadcast','leave_decided','session_revoked','line_checkin','line_delivered','line_standby'));
ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_status_check;
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_status_check
  CHECK (status IN ('queued','sent','failed','token_invalid','deduplicated','skipped'));

-- +goose Down
DELETE FROM notification_deliveries WHERE kind IN ('line_checkin','line_delivered','line_standby') OR status = 'skipped';
ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_status_check;
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_status_check
  CHECK (status IN ('queued','sent','failed','token_invalid','deduplicated'));
ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_kind_check;
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_kind_check
  CHECK (kind IN ('task_assigned','task_unassigned','task_cancelled','tasks_changed','maintenance_scheduled','chat',
                  'broadcast','leave_decided','session_revoked'));
