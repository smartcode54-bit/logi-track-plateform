-- Dispatcher and customer-scope reads of tasks: the scope_tasks projection only (Appendix C §C.3.7, R85).
-- Rows come from p_scope_read under the caller's RLS (security_invoker); every file named scope_*.sql
-- reads scope_* views only, and sqlc vet (rule scope-views-only) fails on a base table.

-- name: ListScopeTasks :many
SELECT * FROM scope_tasks
 WHERE (sqlc.narg(before_plan_at)::timestamptz IS NULL
        OR (plan_at, id) < (sqlc.narg(before_plan_at)::timestamptz, sqlc.narg(before_id)::uuid))
 ORDER BY plan_at DESC, id DESC
 LIMIT sqlc.arg(row_limit);

-- name: GetScopeTask :one
SELECT * FROM scope_tasks WHERE id = sqlc.arg(id);

-- name: ListScopeTasksByCreated :many
-- The boards without a date for a scope principal (T30): the filters of the staff list (the source and destination
-- place buckets of T34 included), newest first, keyset (created_at, id). driver_id matches the driver or the helper;
-- q is a substring of the number, the places or the plate.
SELECT * FROM scope_tasks
 WHERE (sqlc.narg(task_type)::text IS NULL OR task_type = sqlc.narg(task_type)::text)
   AND (sqlc.narg(statuses)::text[] IS NULL OR status = ANY (sqlc.narg(statuses)::text[]))
   AND (sqlc.narg(driver_id)::uuid IS NULL OR driver_id = sqlc.narg(driver_id)::uuid
        OR helper_driver_id = sqlc.narg(driver_id)::uuid)
   AND (sqlc.narg(truck_id)::uuid IS NULL OR truck_id = sqlc.narg(truck_id)::uuid)
   AND (sqlc.narg(q)::text IS NULL
        OR strpos(lower(concat_ws(' ', task_no, source_hub_raw, destination_raw, license_plate_snapshot)),
                  lower(sqlc.narg(q)::text)) > 0)
   AND (NOT sqlc.arg(source_on)::boolean
        OR source_hub_id = ANY (sqlc.arg(source_hubs)::uuid[])
        OR (source_hub_id IS NULL
            AND CASE WHEN upper(btrim(source_hub_raw, sqlc.arg(trim_chars)::text)) ~ '^(SOCE|SOCN|SOCW)($|[ (])'
                     THEN left(upper(btrim(source_hub_raw, sqlc.arg(trim_chars)::text)), 4) END = sqlc.narg(source_soc)::text)
        OR (source_hub_id IS NULL AND upper(btrim(source_hub_raw, sqlc.arg(trim_chars)::text)) !~ '^(SOCE|SOCN|SOCW)($|[ (])'
            AND btrim(source_hub_raw, sqlc.arg(trim_chars)::text) = sqlc.narg(source_raw)::text)
        OR (sqlc.arg(source_none)::boolean AND source_hub_id IS NULL AND btrim(source_hub_raw, sqlc.arg(trim_chars)::text) = ''))
   AND (NOT sqlc.arg(destination_on)::boolean
        OR destination_hub_id = ANY (sqlc.arg(destination_hubs)::uuid[])
        OR (destination_hub_id IS NULL
            AND coalesce(destination_soc_key,
                         CASE WHEN upper(btrim(destination_raw, sqlc.arg(trim_chars)::text)) ~ '^(SOCE|SOCN|SOCW)($|[ (])'
                              THEN left(upper(btrim(destination_raw, sqlc.arg(trim_chars)::text)), 4) END) = sqlc.narg(destination_soc)::text)
        OR (destination_hub_id IS NULL AND destination_soc_key IS NULL
            AND upper(btrim(destination_raw, sqlc.arg(trim_chars)::text)) !~ '^(SOCE|SOCN|SOCW)($|[ (])'
            AND btrim(destination_raw, sqlc.arg(trim_chars)::text) = sqlc.narg(destination_raw)::text)
        OR (sqlc.arg(destination_none)::boolean AND destination_hub_id IS NULL AND destination_soc_key IS NULL
            AND btrim(destination_raw, sqlc.arg(trim_chars)::text) = ''))
   AND (sqlc.narg(after_created)::timestamptz IS NULL
        OR (created_at, id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
 ORDER BY created_at DESC, id DESC
 LIMIT sqlc.arg(row_limit);

-- name: ListScopeTasksByPlan :many
-- The boards on a plan day or range for a scope principal (T30): the Bangkok plan days of the range, latest day first,
-- keyset (plan_date, id).
SELECT * FROM scope_tasks
 WHERE plan_date >= sqlc.arg(from_date)::date AND plan_date <= sqlc.arg(to_date)::date
   AND (sqlc.narg(task_type)::text IS NULL OR task_type = sqlc.narg(task_type)::text)
   AND (sqlc.narg(statuses)::text[] IS NULL OR status = ANY (sqlc.narg(statuses)::text[]))
   AND (sqlc.narg(driver_id)::uuid IS NULL OR driver_id = sqlc.narg(driver_id)::uuid
        OR helper_driver_id = sqlc.narg(driver_id)::uuid)
   AND (sqlc.narg(truck_id)::uuid IS NULL OR truck_id = sqlc.narg(truck_id)::uuid)
   AND (sqlc.narg(q)::text IS NULL
        OR strpos(lower(concat_ws(' ', task_no, source_hub_raw, destination_raw, license_plate_snapshot)),
                  lower(sqlc.narg(q)::text)) > 0)
   AND (NOT sqlc.arg(source_on)::boolean
        OR source_hub_id = ANY (sqlc.arg(source_hubs)::uuid[])
        OR (source_hub_id IS NULL
            AND CASE WHEN upper(btrim(source_hub_raw, sqlc.arg(trim_chars)::text)) ~ '^(SOCE|SOCN|SOCW)($|[ (])'
                     THEN left(upper(btrim(source_hub_raw, sqlc.arg(trim_chars)::text)), 4) END = sqlc.narg(source_soc)::text)
        OR (source_hub_id IS NULL AND upper(btrim(source_hub_raw, sqlc.arg(trim_chars)::text)) !~ '^(SOCE|SOCN|SOCW)($|[ (])'
            AND btrim(source_hub_raw, sqlc.arg(trim_chars)::text) = sqlc.narg(source_raw)::text)
        OR (sqlc.arg(source_none)::boolean AND source_hub_id IS NULL AND btrim(source_hub_raw, sqlc.arg(trim_chars)::text) = ''))
   AND (NOT sqlc.arg(destination_on)::boolean
        OR destination_hub_id = ANY (sqlc.arg(destination_hubs)::uuid[])
        OR (destination_hub_id IS NULL
            AND coalesce(destination_soc_key,
                         CASE WHEN upper(btrim(destination_raw, sqlc.arg(trim_chars)::text)) ~ '^(SOCE|SOCN|SOCW)($|[ (])'
                              THEN left(upper(btrim(destination_raw, sqlc.arg(trim_chars)::text)), 4) END) = sqlc.narg(destination_soc)::text)
        OR (destination_hub_id IS NULL AND destination_soc_key IS NULL
            AND upper(btrim(destination_raw, sqlc.arg(trim_chars)::text)) !~ '^(SOCE|SOCN|SOCW)($|[ (])'
            AND btrim(destination_raw, sqlc.arg(trim_chars)::text) = sqlc.narg(destination_raw)::text)
        OR (sqlc.arg(destination_none)::boolean AND destination_hub_id IS NULL AND destination_soc_key IS NULL
            AND btrim(destination_raw, sqlc.arg(trim_chars)::text) = ''))
   AND (sqlc.narg(after_plan)::date IS NULL
        OR (plan_date, id) < (sqlc.narg(after_plan)::date, sqlc.arg(after_id)::uuid))
 ORDER BY plan_date DESC, id DESC
 LIMIT sqlc.arg(row_limit);

-- name: ScopeTasksByNumber :many
-- The lookup by task number for a scope principal (T30): every match, the number verbatim.
SELECT * FROM scope_tasks WHERE task_no = sqlc.arg(task_no)::text ORDER BY created_at, id;

-- name: ScopeTaskStopsOf :many
-- The planned stops of a page of the projection (scope_task_stops, 0017: no linked party, no price).
SELECT * FROM scope_task_stops WHERE task_id = ANY (sqlc.arg(task_ids)::uuid[]) ORDER BY task_id, stop_index;
