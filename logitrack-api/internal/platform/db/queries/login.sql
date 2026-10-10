-- name: CurrentLogin :one
-- The role this connection runs as (cmd/migrate preflight, R66). Catalog only, no tenant rows.
SELECT r.rolname::text                  AS role_name,
       coalesce(r.rolsuper, false)      AS superuser,
       coalesce(r.rolbypassrls, false)  AS bypass_rls
FROM pg_catalog.pg_roles AS r
WHERE r.rolname = current_user;
