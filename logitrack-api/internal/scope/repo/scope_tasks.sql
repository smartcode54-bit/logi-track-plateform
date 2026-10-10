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
