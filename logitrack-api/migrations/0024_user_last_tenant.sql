-- 0024_user_last_tenant.sql. Never edit this file once it is merged; follow-ups take the next number (R31).
-- Appendix A §A.2.1 (issue T62, #139: one user in several organisations). users.last_tenant_id is the tenant the
-- user last worked in: POST /v1/auth/tenant and every sign-in write it, and the next sign-in opens there when it is
-- still an active membership of a tenant that is not suspended (else the earliest such membership). Until now the
-- default came from the newest session's active_tenant_id, which a switch inside an older session did not move.
-- ON DELETE SET NULL: a tenant is never deleted today, but a remembered tenant must not block it.
-- Backfill: each user's most recently seen session that carries a tenant. users and sessions force RLS on their owner
-- too and t_users_self_columns guards the column, so the backfill runs with the bypass of a system transaction
-- (transaction-local, reset below, as 0015). No GRANT: logitrack_app reads and
-- updates users (0009). The Down drops the column (a preference; the sessions still hold the tenants).

-- +goose Up
ALTER TABLE users ADD COLUMN last_tenant_id uuid REFERENCES tenants(id) ON DELETE SET NULL;
SELECT set_config('app.bypass_tenant', 'on', true);
UPDATE users u SET last_tenant_id = s.active_tenant_id
FROM (SELECT DISTINCT ON (user_id) user_id, active_tenant_id
      FROM sessions
      WHERE active_tenant_id IS NOT NULL
      ORDER BY user_id, last_seen_at DESC, created_at DESC, id DESC) s
WHERE s.user_id = u.id;
SELECT set_config('app.bypass_tenant', '', true);

-- +goose Down
ALTER TABLE users DROP COLUMN last_tenant_id;
