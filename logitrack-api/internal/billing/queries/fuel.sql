-- Bangchak retail price snapshots (P3, R70; Appendix B §B.2.14): fuel_daily_snapshots is insert-only (ADR 0009 §5,
-- trg_forbid_mutation, UPDATE and DELETE revoked) and keyed by the Bangkok day; fuel_monthly_snapshots is upserted only
-- after a successful fetch (fixes fn:core/persistFuelMonthlySnapshot.ts:88-96, which overwrote it with an error). Both
-- are RLS-exempt (service layer only, grants in 0009): the reads need accounting:view_fuel, the writes are the
-- bangchak.snapshot consumer's.

-- name: InsertDailySnapshot :execrows
-- Create only: a day already captured keeps its first prices (0 rows).
INSERT INTO fuel_daily_snapshots (day_key, captured_at, fetched_at, source, locale, status, items)
VALUES (sqlc.arg(day_key)::date, sqlc.arg(captured_at), sqlc.arg(fetched_at), sqlc.arg(source), sqlc.arg(locale), 'ok',
        sqlc.arg(items))
ON CONFLICT (day_key) DO NOTHING;

-- name: UpsertMonthlySnapshot :exec
-- The month's row after a successful fetch: replaced in place (status ok, no error).
INSERT INTO fuel_monthly_snapshots (month_key, captured_at, fetched_at, source, locale, status, error_message, items)
VALUES (sqlc.arg(month_key), sqlc.arg(captured_at), sqlc.arg(fetched_at), sqlc.arg(source), sqlc.arg(locale), 'ok', NULL,
        sqlc.arg(items))
ON CONFLICT (month_key) DO UPDATE
SET captured_at = EXCLUDED.captured_at, fetched_at = EXCLUDED.fetched_at, source = EXCLUDED.source,
    locale = EXCLUDED.locale, status = 'ok', error_message = NULL, items = EXCLUDED.items;

-- name: ListDailySnapshots :many
-- GET /v1/fuel/daily: days from ?from (inclusive), newest first, keyset on day_key.
SELECT day_key, month_key, captured_at, fetched_at, source, locale, status, items
FROM fuel_daily_snapshots
WHERE (sqlc.narg(from_day)::date IS NULL OR day_key >= sqlc.narg(from_day)::date)
  AND (sqlc.narg(before_day)::date IS NULL OR day_key < sqlc.narg(before_day)::date)
ORDER BY day_key DESC
LIMIT sqlc.arg(row_limit);

-- name: GetDailySnapshot :one
SELECT day_key, month_key, captured_at, fetched_at, source, locale, status, items
FROM fuel_daily_snapshots
WHERE day_key = sqlc.arg(day_key)::date;

-- name: ListMonthlySnapshots :many
-- GET /v1/fuel/monthly: newest month first, keyset on month_key.
SELECT month_key, captured_at, fetched_at, source, locale, status, error_message, items, updated_at
FROM fuel_monthly_snapshots
WHERE (sqlc.narg(before_month)::text IS NULL OR month_key < sqlc.narg(before_month)::text)
ORDER BY month_key DESC
LIMIT sqlc.arg(row_limit);

-- name: GetMonthlySnapshot :one
SELECT month_key, captured_at, fetched_at, source, locale, status, error_message, items, updated_at
FROM fuel_monthly_snapshots
WHERE month_key = sqlc.arg(month_key);
