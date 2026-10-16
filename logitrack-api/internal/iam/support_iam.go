package iam

import (
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/iamdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// supportIAM runs after locking the target user and before changing any membership or credential.
// Disabled accounts and suspended target memberships cannot erase the pre-operation tenant reach.
func (a *Admin) supportIAM(c call, tx pgx.Tx, user uuid.UUID, extra []uuid.UUID, action string, general bool) error {
	if err := authz.RequireTenantWrite(c.p); err != nil {
		return err
	}
	tenants := slices.Clone(extra)
	rows, err := tx.Query(c.ctx, `SELECT tenant_id FROM memberships WHERE user_id=$1 AND ($2 OR EXISTS (SELECT 1 FROM memberships admin WHERE admin.user_id=$1 AND admin.role='tenant_admin')) ORDER BY tenant_id`, user, general)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		tenants = append(tenants, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	slices.SortFunc(tenants, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	tenants = slices.Compact(tenants)
	return a.supportIAMTenants(c, tx, &user, nil, tenants, action)
}

// Explicit tenant targets share the D2 lock, audit and notification boundary with account actions.
func (a *Admin) supportIAMTenants(c call, tx pgx.Tx, user, key *uuid.UUID, tenants []uuid.UUID, action string) error {
	if err := authz.RequireTenantWrite(c.p); err != nil {
		return err
	}
	if err := liveIAMContext(c, tx); err != nil {
		return err
	}
	platform := c.p.IsPlatform() && !c.p.IsMachine()
	q := iamdb.New(tx)
	for _, tenant := range tenants {
		t, err := q.LockSupportAccess(c.ctx, tenant)
		if err != nil {
			return err
		}
		if !platform {
			continue
		}
		admins, err := q.ActiveSupportTenantAdmins(c.ctx, tenant)
		if err != nil {
			return err
		}
		if len(admins) > 0 && !t.SupportAccessEnabled {
			return httpx.NewError(403, "support_access_required", "tenant consent is required for platform IAM")
		}
		if action == "" {
			continue
		}
		basis := "onboarding"
		if len(admins) > 0 {
			basis = "consent"
		}
		event := "platform_admin_account_changed"
		switch action {
		case "membership_set", "membership_add", "user_create":
			event = "platform_membership_granted"
		case "temporary_password":
			event = "platform_account_reset"
		case "invite":
			event = "platform_account_invited"
		}
		details := map[string]any{"action": action, "basis": basis, "outcome": "allowed", "grantorUserId": c.p.UserID, "consentVersion": t.SupportAccessVersion}
		if key != nil {
			details["apiKeyId"] = *key
		}
		if err := a.audit(c, tx, event, "platform access administration", user, &tenant, details); err != nil {
			return err
		}
		for _, recipient := range admins {
			if err := emit(c, tx, "tenant.support_admin_action", "tenant", tenant.String(), &tenant, map[string]any{"tenantId": tenant, "actorUserId": c.p.UserID, "targetUserId": user, "targetApiKeyId": key, "action": action, "basis": basis, "recipientUserIds": []uuid.UUID{recipient}}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *Admin) platformCredential(c call, tx pgx.Tx, user uuid.UUID) error {
	if !c.p.IsPlatform() || c.p.IsMachine() {
		return nil
	}
	_, err := tx.Exec(c.ctx, `UPDATE users SET credential_reset_by_platform_at=greatest($2::timestamptz,credential_reset_by_platform_at+interval '1 microsecond',credential_self_chosen_at+interval '1 microsecond'),must_change_password=true WHERE id=$1`, user, a.clock())
	return err
}

func (a *Admin) platformGrant(c call, tx pgx.Tx, user, tenant uuid.UUID) error {
	if !c.p.IsPlatform() || c.p.IsMachine() {
		return nil
	}
	q := iamdb.New(tx)
	if _, err := q.SetPlatformMembershipGrant(c.ctx, iamdb.SetPlatformMembershipGrantParams{UserID: user, TenantID: tenant, GrantorUserID: c.p.UserID, GrantedAt: a.clock()}); err != nil {
		return err
	}
	// A previous native admin tenure is history too. Only partial native snapshots created in this
	// same platform-grant transaction can precede the recorded first grant.
	var previousNativeAdmin bool
	if err := tx.QueryRow(c.ctx, `SELECT EXISTS(SELECT 1 FROM membership_provenance_history WHERE tenant_id=$1 AND role='tenant_admin' AND NOT granted_by_platform AND xmin::text::numeric<>mod(pg_current_xact_id()::text::numeric,4294967296))`, tenant).Scan(&previousNativeAdmin); err != nil {
		return err
	}
	if previousNativeAdmin {
		return nil
	}
	n, err := q.RecordFirstSupportOnboardingAdmin(c.ctx, iamdb.RecordFirstSupportOnboardingAdminParams{UserID: user, TenantID: tenant})
	if err != nil {
		return err
	}
	if n > 0 {
		_, err = tx.Exec(c.ctx, `UPDATE users SET must_change_password=true WHERE id=$1`, user)
	}
	return err
}
