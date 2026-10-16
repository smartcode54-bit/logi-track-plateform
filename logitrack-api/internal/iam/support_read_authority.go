package iam

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/iamdb"
)

// Read-only counterpart to GetSupportMembershipAuthority. Listing never locks user or membership rows.
func supportMembershipFacts(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]iamdb.GetSupportMembershipAuthorityRow, error) {
	rows, err := tx.Query(ctx, `SELECT m.user_id, m.support_membership_id, m.role, m.status, m.created_at, m.granted_by_platform, m.platform_grantor_user_id,
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
WHERE m.tenant_id = $1 AND m.user_id = ANY($2::uuid[])
`, tenant, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]iamdb.GetSupportMembershipAuthorityRow{}
	for rows.Next() {
		var id uuid.UUID
		var m iamdb.GetSupportMembershipAuthorityRow
		if err := rows.Scan(&id, &m.SupportMembershipID, &m.Role, &m.Status, &m.CreatedAt, &m.GrantedByPlatform, &m.PlatformGrantorUserID, &m.PlatformGrantedAt, &m.RatifiedByUserID, &m.RatifiedAt, &m.GrantVersion, &m.UserStatus, &m.MustChangePassword, &m.CredentialResetByPlatformAt, &m.CredentialSelfChosenAt, &m.HasPlatformRole, &m.CredentialEligible, &m.FirstAdminSelfChosen); err != nil {
			return nil, err
		}
		out[id] = m
	}
	return out, rows.Err()
}
