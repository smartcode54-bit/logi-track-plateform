-- 0016_statement_show_actual_pickup.sql. Never edit this file once it is merged; follow-ups take the next number (R31).
-- Appendix A §A.2.14 (issue T39 review). Whether a statement's detail sheet carries the optional วันรับงานจริง column
-- (tasks.actual_pickup_at, ADR 0028). The browser chose it with the Billing Document page's toggle each time it built
-- the ZIP (web:lib/billingDocumentRender.ts:675-683); documents.render draws the documents once, on the server, and a
-- re-download serves the stored file, so the choice is stored with the statement: POST /v1/billing/statements takes it,
-- POST .../documents/regenerate may change it (written in the transaction that succeeds the job, so the column always
-- names what the stored sheet shows). false, the browser's default, for every existing row; the ETL never names it.
-- Depends on: 0005 (billing_statements). No GRANT: the table-level privileges of 0009 cover the new column. Production
-- applies it with the P1 runbook's `migrate up` (T24); it depends on nothing in 0010-0015.

-- +goose Up
ALTER TABLE billing_statements ADD COLUMN show_actual_pickup boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE billing_statements DROP COLUMN show_actual_pickup;
