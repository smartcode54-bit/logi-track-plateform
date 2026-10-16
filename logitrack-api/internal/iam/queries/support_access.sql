-- P1.2a storage handoff (ADR 0030). The future API must authorize member mode, exact tenant,
-- live membership/credentials and a holder action, then append audit/outbox in this transaction.
-- These queries do not enable support entry or replace service authorization.

-- name: GetSupportAccess :one
SELECT id, support_access_enabled, support_access_version, support_access_changed_at, support_access_changed_by
FROM tenants WHERE id = sqlc.arg(tenant_id) AND kind <> 'quarantine';

-- name: LockSupportAccess :one
-- Serialize consent and all D2 membership/admin-count mutations on the affected tenants in ID order.
SELECT id, support_access_enabled, support_access_version, support_access_changed_at, support_access_changed_by
FROM tenants WHERE id = sqlc.arg(tenant_id) AND kind <> 'quarantine' FOR UPDATE;

-- name: CompareAndSwapSupportAccess :one
-- Read/lock first: matching version + unchanged boolean is a no-op; no row here means no write.
UPDATE tenants SET support_access_enabled = sqlc.arg(enabled), support_access_version = support_access_version + 1,
  support_access_changed_at = sqlc.arg(changed_at)::timestamptz, support_access_changed_by = sqlc.arg(actor_user_id)::uuid
WHERE id = sqlc.arg(tenant_id) AND kind <> 'quarantine'
  AND support_access_version = sqlc.arg(expected_version) AND support_access_enabled <> sqlc.arg(enabled)
RETURNING id, support_access_enabled, support_access_version, support_access_changed_at, support_access_changed_by;

-- name: ActiveSupportTenantAdmins :many
-- Disabled users still count for D2 and remain pre-operation notification recipients.
SELECT user_id FROM memberships WHERE tenant_id = sqlc.arg(tenant_id) AND role = 'tenant_admin' AND status = 'active'
ORDER BY user_id;

-- name: GetSupportMembershipAuthority :one
-- Facts for the live Go eligibility decision, never a substitute for authenticated member-mode checks.
-- LockAdminUser (FOR NO KEY UPDATE) must run first in this transaction, as on other IAM paths:
-- users before memberships. Do not let a multi-table locking SELECT choose the lock order.
SELECT m.support_membership_id, m.role, m.status, m.created_at, m.granted_by_platform, m.platform_grantor_user_id,
  m.platform_granted_at, m.ratified_by_user_id, m.ratified_at, m.grant_version, u.status AS user_status,
  u.must_change_password, u.credential_reset_by_platform_at, u.credential_self_chosen_at,
  EXISTS (SELECT 1 FROM user_platform_roles p WHERE p.user_id = m.user_id)::boolean AS has_platform_role,
  coalesce(NOT u.must_change_password AND (u.credential_reset_by_platform_at IS NULL
    OR u.credential_self_chosen_at > u.credential_reset_by_platform_at), false)::boolean AS credential_eligible,
  EXISTS (SELECT 1 FROM tenants t JOIN membership_provenance_history h ON h.id = t.support_first_admin_grant_id
    WHERE t.id = m.tenant_id AND t.support_first_admin_revoked_at IS NULL AND h.membership_id = m.support_membership_id
      AND h.granted_by_platform AND h.role = 'tenant_admin' AND h.status = 'active'
      AND h.platform_granted_at = m.platform_granted_at AND u.credential_self_chosen_at > h.platform_granted_at
      AND NOT EXISTS (SELECT 1 FROM membership_provenance_history later WHERE later.membership_id = h.membership_id
        AND later.grant_version > h.grant_version
        AND (later.role <> 'tenant_admin' OR later.status <> 'active' OR later.operation = 'DELETE')))::boolean AS first_admin_self_chosen
FROM memberships m JOIN users u ON u.id = m.user_id
WHERE m.user_id = sqlc.arg(user_id) AND m.tenant_id = sqlc.arg(tenant_id)
FOR UPDATE OF m;

-- name: ListSupportMembershipProvenance :many
SELECT user_id, support_membership_id, role, status, created_at, granted_by_platform,
  platform_grantor_user_id, platform_granted_at, ratified_by_user_id, ratified_at, grant_version
FROM memberships WHERE tenant_id = sqlc.arg(tenant_id) ORDER BY created_at, user_id;

-- name: SetPlatformMembershipGrant :one
-- Call after the D2 gate and membership upsert in the same system transaction. A new/elevated
-- grant clears ratification; the trigger advances grant_version and preserves the old snapshot.
UPDATE memberships SET granted_by_platform = true, platform_grantor_user_id = sqlc.arg(grantor_user_id)::uuid,
  platform_granted_at = sqlc.arg(granted_at)::timestamptz, ratified_by_user_id = NULL, ratified_at = NULL
WHERE user_id = sqlc.arg(user_id) AND tenant_id = sqlc.arg(tenant_id)
RETURNING support_membership_id, grant_version;

-- name: RecordFirstSupportOnboardingAdmin :execrows
-- Explicit initial-onboarding evidence only. Historical admin rows, including deleted ones, prevent
-- a replacement from acquiring bootstrap. The reference points to an immutable snapshot, never a live row.
-- Migration consumes bootstrap for every legacy tenant and separately backfills evidenced original grants.
UPDATE tenants t SET support_first_admin_grant_id = h.id, support_bootstrap_consumed = true
FROM membership_provenance_history h JOIN memberships m ON m.support_membership_id = h.membership_id
  AND m.grant_version = h.grant_version
WHERE t.id = sqlc.arg(tenant_id) AND t.id = m.tenant_id AND m.user_id = sqlc.arg(user_id)
  AND t.support_first_admin_grant_id IS NULL AND NOT t.support_bootstrap_consumed AND m.granted_by_platform
  AND m.role = 'tenant_admin' AND m.status = 'active' AND h.operation <> 'DELETE'
  AND NOT EXISTS (SELECT 1 FROM membership_provenance_history previous
    WHERE previous.tenant_id = t.id AND previous.role = 'tenant_admin'
      AND (previous.membership_id <> m.support_membership_id OR previous.operation = 'baseline'
        OR previous.operation = 'DELETE' OR previous.platform_granted_at IS DISTINCT FROM m.platform_granted_at
          AND previous.granted_by_platform
        OR NOT previous.granted_by_platform
          AND previous.xmin::text::numeric <> mod(pg_current_xact_id()::text::numeric, 4294967296)));

-- name: GetSupportRatificationTarget :one
SELECT support_membership_id, role, status, created_at, granted_by_platform, platform_granted_at,
  ratified_at, grant_version
FROM memberships WHERE user_id = sqlc.arg(user_id) AND tenant_id = sqlc.arg(tenant_id) FOR UPDATE;

-- name: RatifySupportMembership :one
-- The caller locks/rechecks GetSupportMembershipAuthority for a distinct eligible predecessor first.
UPDATE memberships m SET ratified_by_user_id = sqlc.arg(actor_user_id)::uuid, ratified_at = sqlc.arg(ratified_at)::timestamptz
WHERE m.user_id = sqlc.arg(user_id) AND m.tenant_id = sqlc.arg(tenant_id)
  AND m.user_id <> sqlc.arg(actor_user_id) AND m.granted_by_platform AND m.ratified_at IS NULL
  AND m.grant_version = sqlc.arg(expected_grant_version)
  AND EXISTS (SELECT 1 FROM memberships actor WHERE actor.user_id = sqlc.arg(actor_user_id)
    AND actor.tenant_id = m.tenant_id AND actor.role = 'tenant_admin' AND actor.status = 'active'
    AND actor.created_at < m.platform_granted_at)
RETURNING support_membership_id, grant_version, ratified_by_user_id, ratified_at;

-- name: RecordPlatformCredentialReset :execrows
-- Called together with the credential write and its normal invalidation/audit path, including invites.
UPDATE users SET credential_reset_by_platform_at = greatest(sqlc.arg(reset_at)::timestamptz,
  credential_reset_by_platform_at + interval '1 microsecond', credential_self_chosen_at + interval '1 microsecond'), must_change_password = true
WHERE id = sqlc.arg(user_id);

-- name: RecordSupportHolderPasswordChoice :execrows
-- P1.1 calls only from an actual holder password submission, never an administrative credential write.
UPDATE users SET credential_self_chosen_at = sqlc.arg(chosen_at)::timestamptz
WHERE id = sqlc.arg(user_id) AND NOT must_change_password
  AND (credential_self_chosen_at IS NULL OR credential_self_chosen_at < sqlc.arg(chosen_at));
