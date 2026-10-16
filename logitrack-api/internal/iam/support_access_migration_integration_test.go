//go:build integration

package iam_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/iamdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

func TestSupportAccessMigrationDefaultsAndRoundTrip(t *testing.T) {
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	r := migratetest.Runner(t, d, migrations.FS)
	if _, err := r.UpTo(ctx, 26); err != nil {
		t.Fatal(err)
	}
	pool := d.Pool(t, db.RoleApp)
	tenant, admin, platform, flagged := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error {
		for _, id := range []uuid.UUID{admin, platform, flagged} {
			if _, err := tx.Exec(ctx, `INSERT INTO users (id) VALUES ($1)`, id); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO tenants (id, kind, name_th) VALUES ($1, 'own_fleet', 'Existing tenant')`, tenant); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO user_platform_roles (user_id, role, granted_at)
 VALUES ($1, 'platform_admin', $2)`, platform, now.Add(-2*time.Hour)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO memberships (user_id, tenant_id, role, created_by, created_at)
 VALUES ($1, $2, 'tenant_admin', NULL, $4), ($3, $2, 'manager', $5, $4)`, admin, tenant, flagged, now.Add(-time.Hour), platform); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO security_events (event_type, summary, actor_user_id, target_user_id, created_at)
 VALUES ('user_password_temporary_issued', 'platform-set credential', $1, $2, $3)`, platform, flagged, now); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO sessions (user_id, platform, amr, absolute_expires_at) VALUES ($1, 'web', 'pwd', $2)`, admin, now.Add(time.Hour))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	before, err := d.DumpSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Up(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := d.DumpSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		if err := db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error {
			q := iamdb.New(tx)
			if _, err := q.LockAdminUser(ctx, admin); err != nil {
				return err
			}
			consent, err := q.GetSupportAccess(ctx, tenant)
			if err != nil {
				return err
			}
			if consent.SupportAccessEnabled || consent.SupportAccessVersion != 0 || consent.SupportAccessChangedAt != nil || consent.SupportAccessChangedBy != nil {
				t.Fatalf("consent default = %+v", consent)
			}
			legacy, err := q.GetSupportMembershipAuthority(ctx, iamdb.GetSupportMembershipAuthorityParams{UserID: admin, TenantID: tenant})
			if err != nil {
				return err
			}
			if legacy.GrantedByPlatform || !legacy.CredentialEligible || legacy.FirstAdminSelfChosen || legacy.GrantVersion != 0 || legacy.Role != "tenant_admin" {
				t.Fatalf("no-evidence membership = %+v", legacy)
			}
			if _, err := q.LockAdminUser(ctx, flagged); err != nil {
				return err
			}
			evidenced, err := q.GetSupportMembershipAuthority(ctx, iamdb.GetSupportMembershipAuthorityParams{UserID: flagged, TenantID: tenant})
			if err != nil {
				return err
			}
			if !evidenced.GrantedByPlatform || evidenced.PlatformGrantorUserID == nil || *evidenced.PlatformGrantorUserID != platform || evidenced.CredentialEligible || evidenced.RatifiedAt != nil {
				t.Fatalf("evidenced platform grant = %+v", evidenced)
			}
			var modes bool
			if err := tx.QueryRow(ctx, `SELECT bool_and(access_mode = 'member' AND context_version = 0 AND support_return_tenant_id IS NULL) FROM sessions`).Scan(&modes); err != nil {
				return err
			}
			if !modes {
				t.Fatal("migration changed session mode")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	check()
	if _, err := r.DownTo(ctx, 26); err != nil {
		t.Fatal(err)
	}
	rolledBack, err := d.DumpSchema(ctx)
	if err != nil || before != rolledBack {
		t.Fatalf("0027 Down does not restore the 0026 schema (%v)", err)
	}
	if _, err := r.Up(ctx); err != nil {
		t.Fatal(err)
	}
	reapplied, err := d.DumpSchema(ctx)
	if err != nil || after != reapplied {
		t.Fatalf("0027 Up/Down/Up schema differs (%v)", err)
	}
	check()
}

func TestSupportAccessQueriesAndProvenance(t *testing.T) {
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	pool := d.Pool(t, db.RoleApp)
	tenant, admin, target, platform := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error {
		for _, id := range []uuid.UUID{admin, target, platform} {
			if _, err := tx.Exec(ctx, `INSERT INTO users (id) VALUES ($1)`, id); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO tenants (id, kind, name_th) VALUES ($1, 'own_fleet', 'Tenant')`, tenant); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO memberships (user_id, tenant_id, role, created_at)
 VALUES ($1, $2, 'tenant_admin', $3), ($4, $2, 'manager', $5)`, admin, tenant, now.Add(-time.Hour), target, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error {
		q := iamdb.New(tx)
		if _, err := q.LockSupportAccess(ctx, tenant); err != nil {
			return err
		}
		changed, err := q.CompareAndSwapSupportAccess(ctx, iamdb.CompareAndSwapSupportAccessParams{
			TenantID: tenant, Enabled: true, ExpectedVersion: 0, ChangedAt: now, ActorUserID: admin})
		if err != nil || changed.SupportAccessVersion != 1 || !changed.SupportAccessEnabled {
			t.Fatalf("consent CAS = %+v (%v)", changed, err)
		}
		for _, expected := range []int64{0, 1} {
			if _, err := q.CompareAndSwapSupportAccess(ctx, iamdb.CompareAndSwapSupportAccessParams{
				TenantID: tenant, Enabled: true, ExpectedVersion: expected, ChangedAt: now, ActorUserID: admin}); !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("stale/no-op CAS wrote: %v", err)
			}
		}
		grant, err := q.SetPlatformMembershipGrant(ctx, iamdb.SetPlatformMembershipGrantParams{
			UserID: target, TenantID: tenant, GrantorUserID: platform, GrantedAt: now})
		if err != nil {
			return err
		}
		for _, actor := range []uuid.UUID{target, admin} {
			version := grant.GrantVersion
			if actor == admin {
				version-- // stale grant; self and stale attempts leave provenance unchanged.
			}
			if _, err := q.RatifySupportMembership(ctx, iamdb.RatifySupportMembershipParams{UserID: target, TenantID: tenant,
				ActorUserID: actor, RatifiedAt: now, ExpectedGrantVersion: version}); !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("self/stale ratification wrote: %v", err)
			}
		}
		ratified, err := q.RatifySupportMembership(ctx, iamdb.RatifySupportMembershipParams{UserID: target, TenantID: tenant,
			ActorUserID: admin, RatifiedAt: now, ExpectedGrantVersion: grant.GrantVersion})
		if err != nil || ratified.GrantVersion != grant.GrantVersion+1 {
			t.Fatalf("ratification CAS = %+v (%v)", ratified, err)
		}
		if _, err := q.RecordPlatformCredentialReset(ctx, iamdb.RecordPlatformCredentialResetParams{UserID: target, ResetAt: now}); err != nil {
			return err
		}
		if _, err := q.LockAdminUser(ctx, target); err != nil {
			return err
		}
		blocked, err := q.GetSupportMembershipAuthority(ctx, iamdb.GetSupportMembershipAuthorityParams{UserID: target, TenantID: tenant})
		if err != nil || blocked.CredentialEligible || blocked.RatifiedAt == nil {
			t.Fatalf("platform reset lost ratification or allowed credentials: %+v (%v)", blocked, err)
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET status = 'disabled', disabled_at = $2 WHERE id = $1`, admin, now); err != nil {
			return err
		}
		admins, err := q.ActiveSupportTenantAdmins(ctx, tenant)
		if err != nil || len(admins) != 1 || admins[0] != admin {
			t.Fatalf("disabled active admin did not count: %v (%v)", admins, err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM memberships WHERE user_id = $1 AND tenant_id = $2`, target, tenant); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, 'manager')`, target, tenant); err != nil {
			return err
		}
		recreated, err := q.GetSupportRatificationTarget(ctx, iamdb.GetSupportRatificationTargetParams{UserID: target, TenantID: tenant})
		if err != nil || recreated.GrantVersion <= ratified.GrantVersion || recreated.SupportMembershipID == grant.SupportMembershipID {
			t.Fatalf("recreated grant reused identity/version: %+v (%v)", recreated, err)
		}
		var retained bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM membership_provenance_history
 WHERE membership_id = $1 AND ratified_by_user_id = $2 AND operation = 'DELETE')`, grant.SupportMembershipID, admin).Scan(&retained); err != nil {
			return err
		}
		if !retained {
			t.Fatal("removed membership lost provenance")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Other IAM writes and FK checks can hold KEY SHARE on users. The users-first
	// NO KEY UPDATE lock must stay compatible; the authority read must not upgrade
	// it to FOR UPDATE while also holding a membership lock.
	if err := db.WithSystem(ctx, pool, nil, func(observer pgx.Tx) error {
		var locked uuid.UUID
		if err := observer.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 FOR KEY SHARE`, target).Scan(&locked); err != nil {
			return err
		}
		return db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout='100ms'`); err != nil {
				return err
			}
			q := iamdb.New(tx)
			if _, err := q.LockAdminUser(ctx, target); err != nil {
				return err
			}
			_, err := q.GetSupportMembershipAuthority(ctx, iamdb.GetSupportMembershipAuthorityParams{UserID: target, TenantID: tenant})
			return err
		})
	}); err != nil {
		t.Fatalf("authority upgraded the users lock: %v", err)
	}
}

func TestSupportAccessGrantsAndConstraints(t *testing.T) {
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	pool := d.Pool(t, db.RoleApp)
	for _, table := range []string{"support_visits", "membership_provenance_history"} {
		var enabled, forced, canSelect, canInsert, canUpdate, canDelete, canTruncate bool
		if err := pool.QueryRow(ctx, `SELECT relrowsecurity, relforcerowsecurity,
 has_table_privilege(current_user, oid, 'SELECT'), has_table_privilege(current_user, oid, 'INSERT'),
 has_table_privilege(current_user, oid, 'UPDATE'), has_table_privilege(current_user, oid, 'DELETE'),
 has_table_privilege(current_user, oid, 'TRUNCATE') FROM pg_class WHERE oid = $1::regclass`, table).
			Scan(&enabled, &forced, &canSelect, &canInsert, &canUpdate, &canDelete, &canTruncate); err != nil {
			t.Fatal(err)
		}
		if !enabled || !forced || !canSelect || !canInsert || canUpdate != (table == "support_visits") || canDelete || canTruncate {
			t.Fatalf("%s: RLS=%t/%t SELECT/INSERT/UPDATE/DELETE/TRUNCATE=%t/%t/%t/%t/%t",
				table, enabled, forced, canSelect, canInsert, canUpdate, canDelete, canTruncate)
		}
	}
	for _, sql := range []string{
		`UPDATE tenants SET support_access_version = -1`,
		`UPDATE tenants SET support_access_enabled = true, support_access_version = 1`,
		`INSERT INTO sessions (user_id, platform, amr, absolute_expires_at, access_mode)
 SELECT id, 'web', 'pwd', now() + interval '1 day', 'support' FROM users LIMIT 1`,
	} {
		err := db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `INSERT INTO users DEFAULT VALUES`); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, sql)
			return err
		})
		if sqlState(err) != "23514" {
			t.Fatalf("invalid state: want SQLSTATE 23514, got %v: %s", err, sql)
		}
	}
}

func TestSupportAccessFirstAdminBootstrap(t *testing.T) {
	for _, change := range []string{"disable_enable", "demote_raise", "remove_recreate"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			d := pgtest.NewDatabase(t)
			if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
				t.Fatal(err)
			}
			pool := d.Pool(t, db.RoleApp)
			tenant, holder, platform := uuid.New(), uuid.New(), uuid.New()
			grantAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
			choiceAt := grantAt.Add(time.Minute)
			if err := db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error {
				q := iamdb.New(tx)
				for _, id := range []uuid.UUID{holder, platform} {
					if _, err := tx.Exec(ctx, `INSERT INTO users (id) VALUES ($1)`, id); err != nil {
						return err
					}
				}
				if _, err := tx.Exec(ctx, `INSERT INTO tenants (id, kind, name_th) VALUES ($1, 'own_fleet', 'New tenant')`, tenant); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `INSERT INTO memberships (user_id, tenant_id, role, created_at)
 VALUES ($1, $2, 'tenant_admin', $3)`, holder, tenant, grantAt); err != nil {
					return err
				}
				if _, err := q.SetPlatformMembershipGrant(ctx, iamdb.SetPlatformMembershipGrantParams{UserID: holder,
					TenantID: tenant, GrantorUserID: platform, GrantedAt: grantAt}); err != nil {
					return err
				}
				if n, err := q.RecordFirstSupportOnboardingAdmin(ctx, iamdb.RecordFirstSupportOnboardingAdminParams{
					UserID: holder, TenantID: tenant}); err != nil || n != 1 {
					t.Fatalf("initial onboarding reference = %d (%v)", n, err)
				}
				if _, err := q.RecordPlatformCredentialReset(ctx, iamdb.RecordPlatformCredentialResetParams{UserID: holder, ResetAt: grantAt}); err != nil {
					return err
				}
				authority := func() iamdb.GetSupportMembershipAuthorityRow {
					t.Helper()
					if _, err := q.LockAdminUser(ctx, holder); err != nil {
						t.Fatal(err)
					}
					a, err := q.GetSupportMembershipAuthority(ctx, iamdb.GetSupportMembershipAuthorityParams{UserID: holder, TenantID: tenant})
					if err != nil {
						t.Fatal(err)
					}
					return a
				}
				if a := authority(); a.CredentialEligible || a.FirstAdminSelfChosen {
					t.Fatalf("platform credential acquired authority: %+v", a)
				}
				if _, err := tx.Exec(ctx, `UPDATE users SET must_change_password = false WHERE id = $1`, holder); err != nil {
					return err
				}
				if a := authority(); a.CredentialEligible || a.FirstAdminSelfChosen {
					t.Fatal("clearing must-change alone acquired authority")
				}
				if _, err := q.RecordSupportHolderPasswordChoice(ctx, iamdb.RecordSupportHolderPasswordChoiceParams{UserID: holder, ChosenAt: choiceAt}); err != nil {
					return err
				}
				if a := authority(); !a.CredentialEligible || !a.FirstAdminSelfChosen || !a.GrantedByPlatform || a.RatifiedAt != nil {
					t.Fatalf("holder completion lost bootstrap/provenance: %+v", a)
				}
				consent, err := q.GetSupportAccess(ctx, tenant)
				if err != nil || consent.SupportAccessEnabled || consent.SupportAccessVersion != 0 {
					t.Fatalf("bootstrap changed consent: %+v (%v)", consent, err)
				}
				switch change {
				case "disable_enable":
					if _, err := tx.Exec(ctx, `UPDATE users SET status = 'disabled', disabled_at = $2 WHERE id = $1`, holder, choiceAt); err != nil {
						return err
					}
					if _, err := tx.Exec(ctx, `UPDATE users SET status = 'active' WHERE id = $1`, holder); err != nil {
						return err
					}
				case "demote_raise":
					for _, role := range []string{"manager", "tenant_admin"} {
						if _, err := tx.Exec(ctx, `UPDATE memberships SET role = $3 WHERE user_id = $1 AND tenant_id = $2`, holder, tenant, role); err != nil {
							return err
						}
					}
				case "remove_recreate":
					if _, err := tx.Exec(ctx, `DELETE FROM memberships WHERE user_id = $1 AND tenant_id = $2`, holder, tenant); err != nil {
						return err
					}
					if _, err := tx.Exec(ctx, `INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, 'tenant_admin')`, holder, tenant); err != nil {
						return err
					}
					if _, err := q.SetPlatformMembershipGrant(ctx, iamdb.SetPlatformMembershipGrantParams{UserID: holder,
						TenantID: tenant, GrantorUserID: platform, GrantedAt: grantAt}); err != nil {
						return err
					}
				}
				if authority().FirstAdminSelfChosen {
					t.Fatal("bootstrap was reused after a terminating action")
				}
				if n, err := q.RecordFirstSupportOnboardingAdmin(ctx, iamdb.RecordFirstSupportOnboardingAdminParams{
					UserID: holder, TenantID: tenant}); err != nil || n != 0 {
					t.Fatalf("onboarding reference was replaced: %d (%v)", n, err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSupportAccessMigrationHistoricalPlatformAuthority(t *testing.T) {
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	r := migratetest.Runner(t, d, migrations.FS)
	if _, err := r.UpTo(ctx, 26); err != nil {
		t.Fatal(err)
	}
	pool := d.Pool(t, db.RoleApp)
	now := time.Now().UTC().Truncate(time.Microsecond)
	type evidenceCase struct {
		name, source        string
		actor, user, tenant uuid.UUID
		want                bool
	}
	var cases []evidenceCase
	for _, name := range []string{"revoked", "regranted", "revocation_only", "revoked_gap", "before_grant"} {
		for _, source := range []string{"creator", "audit"} {
			cases = append(cases, evidenceCase{name, source, uuid.New(), uuid.New(), uuid.New(), name == "revoked" || name == "regranted"})
		}
	}
	if err := db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error {
		for _, c := range cases {
			if _, err := tx.Exec(ctx, `INSERT INTO users (id) VALUES ($1), ($2);
 INSERT INTO tenants (id, kind, legal_type, name_th) VALUES ($3, 'carrier', 'company', 'Historical authority')`,
				pgx.QueryExecModeSimpleProtocol, c.actor, c.user, c.tenant); err != nil {
				return err
			}
			if c.name != "revocation_only" {
				if _, err := tx.Exec(ctx, `INSERT INTO security_events (event_type, summary, target_user_id, details, created_at)
 VALUES ('platform_role_granted', 'grant', $1, '{"role":"platform_admin"}', $2)`, c.actor, now.Add(-4*time.Hour)); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `INSERT INTO security_events (event_type, summary, target_user_id, details, created_at)
 VALUES ('platform_role_revoked', 'revoke', $1, '{"role":"platform_admin"}', $2)`, c.actor, now.Add(-2*time.Hour)); err != nil {
				return err
			}
			if c.name == "regranted" || c.name == "revoked_gap" {
				if _, err := tx.Exec(ctx, `INSERT INTO user_platform_roles (user_id, role, granted_at) VALUES ($1, 'platform_admin', $2);
 INSERT INTO security_events (event_type, summary, target_user_id, details, created_at)
 VALUES ('platform_role_granted', 'regrant', $1, '{"role":"platform_admin"}', $2)`, pgx.QueryExecModeSimpleProtocol, c.actor, now.Add(-time.Hour)); err != nil {
					return err
				}
			}
			at := now.Add(-3 * time.Hour)
			if c.name == "revoked_gap" {
				at = now.Add(-90 * time.Minute)
			}
			if c.name == "before_grant" {
				at = now.Add(-5 * time.Hour)
			}
			var creator *uuid.UUID
			if c.source == "creator" {
				creator = &c.actor
			}
			if _, err := tx.Exec(ctx, `INSERT INTO memberships (user_id, tenant_id, role, created_by, created_at)
 VALUES ($1, $2, 'manager', $3, $4)`, c.user, c.tenant, creator, at); err != nil {
				return err
			}
			if c.source == "audit" {
				if _, err := tx.Exec(ctx, `INSERT INTO security_events (event_type, summary, actor_user_id, target_user_id, tenant_id, details, created_at)
 VALUES ('membership_added', 'grant', $1, $2, $3, '{"role":"manager"}', $4)`, c.actor, c.user, c.tenant, at); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `INSERT INTO security_events (event_type, summary, actor_user_id, target_user_id, created_at)
 VALUES ('user_password_temporary_issued', 'credential', $1, $2, $3)`, c.actor, c.user, at); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error {
		for _, c := range cases {
			var grant, reset bool
			if err := tx.QueryRow(ctx, `SELECT m.granted_by_platform, u.credential_reset_by_platform_at IS NOT NULL
 FROM memberships m JOIN users u ON u.id = m.user_id WHERE m.user_id = $1 AND m.tenant_id = $2`, c.user, c.tenant).Scan(&grant, &reset); err != nil {
				return err
			}
			if grant != c.want || reset != c.want {
				t.Errorf("%s/%s: grant=%t reset=%t, want %t", c.name, c.source, grant, reset, c.want)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSupportAccessMigrationLegacyBootstrap(t *testing.T) {
	for _, change := range []string{"original", "disable_enable", "disabled_timestamp", "delete_restore", "demote_raise", "suspend_activate", "remove_recreate", "prior_deleted_admin", "no_admin"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			d := pgtest.NewDatabase(t)
			r := migratetest.Runner(t, d, migrations.FS)
			if _, err := r.UpTo(ctx, 26); err != nil {
				t.Fatal(err)
			}
			pool := d.Pool(t, db.RoleApp)
			tenant, holder, platform := uuid.New(), uuid.New(), uuid.New()
			now := time.Now().UTC().Truncate(time.Microsecond)
			grantAt := now.Add(-5 * time.Hour)
			if err := db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, `INSERT INTO users (id) VALUES ($1), ($2);
 INSERT INTO tenants (id, kind, name_th, created_at) VALUES ($3, 'own_fleet', 'Legacy onboarding', $4);
 INSERT INTO user_platform_roles (user_id, role, granted_at) VALUES ($2, 'platform_admin', $5)`,
					pgx.QueryExecModeSimpleProtocol, holder, platform, tenant, now.Add(-6*time.Hour), now.Add(-8*time.Hour)); err != nil {
					return err
				}
				if change == "no_admin" {
					return nil
				}
				createdAt := grantAt
				if change == "remove_recreate" {
					createdAt = now.Add(-time.Hour)
				}
				if _, err := tx.Exec(ctx, `INSERT INTO memberships (user_id, tenant_id, role, created_by, created_at)
 VALUES ($1, $2, 'tenant_admin', $3, $4)`, holder, tenant, platform, createdAt); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `INSERT INTO security_events (event_type, summary, actor_user_id, target_user_id, tenant_id, details, created_at) VALUES
 ('tenant_created', 'onboarding', $1, NULL, $2, '{}', $3),
 ('user_created', 'first admin', $1, $4, $2, '{"role":"tenant_admin","invite":false}', $5),
 ('password_changed', 'holder choice', $4, $4, NULL, '{}', $6)`, platform, tenant, now.Add(-6*time.Hour), holder, grantAt, now); err != nil {
					return err
				}
				if change == "disabled_timestamp" {
					_, err := tx.Exec(ctx, `UPDATE users SET status='disabled', disabled_at=$2 WHERE id=$1`, holder, now.Add(-3*time.Hour))
					return err
				}
				event, details := "", `{}`
				switch change {
				case "disable_enable":
					event = "user_disabled"
				case "delete_restore":
					event = "user_deleted"
				case "demote_raise":
					event, details = "membership_role_changed", `{"role":"manager","previousRole":"tenant_admin","status":"active"}`
				case "suspend_activate":
					event, details = "membership_role_changed", `{"role":"tenant_admin","status":"suspended"}`
				case "remove_recreate":
					event, details = "membership_removed", `{"previousRole":"tenant_admin"}`
				case "prior_deleted_admin":
					event, details = "user_deleted", fmt.Sprintf(`{"memberships":[{"tenantId":%q,"role":"tenant_admin"}]}`, tenant)
				}
				if event != "" {
					at := now.Add(-3 * time.Hour)
					if change == "prior_deleted_admin" {
						at = grantAt.Add(-time.Minute)
					}
					_, err := tx.Exec(ctx, `INSERT INTO security_events (event_type, summary, target_user_id, tenant_id, details, created_at)
 VALUES ($1, 'terminating history', $2, $3, $4::jsonb, $5)`, event, holder, tenant, details, at)
					return err
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := r.Up(ctx); err != nil {
				t.Fatal(err)
			}
			if err := db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error {
				q := iamdb.New(tx)
				if change == "disabled_timestamp" {
					if _, err := tx.Exec(ctx, `UPDATE users SET status='active' WHERE id=$1`, holder); err != nil {
						return err
					}
				}
				if change == "no_admin" {
					if _, err := tx.Exec(ctx, `INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, 'tenant_admin')`, holder, tenant); err != nil {
						return err
					}
					if _, err := q.SetPlatformMembershipGrant(ctx, iamdb.SetPlatformMembershipGrantParams{UserID: holder, TenantID: tenant, GrantorUserID: platform, GrantedAt: now}); err != nil {
						return err
					}
				}
				if _, err := q.LockAdminUser(ctx, holder); err != nil {
					return err
				}
				a, err := q.GetSupportMembershipAuthority(ctx, iamdb.GetSupportMembershipAuthorityParams{UserID: holder, TenantID: tenant})
				if err != nil {
					return err
				}
				if a.FirstAdminSelfChosen != (change == "original") {
					t.Fatalf("%s: authority = %+v", change, a)
				}
				var original, revoked, consumed bool
				if err := tx.QueryRow(ctx, `SELECT support_first_admin_grant_id IS NOT NULL,
 support_first_admin_revoked_at IS NOT NULL, support_bootstrap_consumed FROM tenants WHERE id=$1`, tenant).Scan(&original, &revoked, &consumed); err != nil {
					return err
				}
				wantOriginal := change != "no_admin" && change != "prior_deleted_admin"
				if original != wantOriginal || revoked != (wantOriginal && change != "original") || consumed != (change != "no_admin") {
					t.Fatalf("%s: original=%t revoked=%t consumed=%t", change, original, revoked, consumed)
				}
				// No admin history preserves initial onboarding; only recording the first grant consumes it.
				if change == "no_admin" {
					if n, err := q.RecordFirstSupportOnboardingAdmin(ctx, iamdb.RecordFirstSupportOnboardingAdminParams{UserID: holder, TenantID: tenant}); err != nil || n != 1 {
						t.Fatalf("legacy adminless bootstrap: %d (%v)", n, err)
					}
					if err := tx.QueryRow(ctx, `SELECT support_bootstrap_consumed, support_first_admin_grant_id IS NOT NULL FROM tenants WHERE id=$1`, tenant).Scan(&consumed, &original); err != nil || !consumed || !original {
						t.Fatalf("first grant not recorded: consumed=%t original=%t (%v)", consumed, original, err)
					}
				}
				if n, err := q.RecordFirstSupportOnboardingAdmin(ctx, iamdb.RecordFirstSupportOnboardingAdminParams{UserID: holder, TenantID: tenant}); err != nil || n != 0 {
					t.Fatalf("legacy bootstrap replaced: %d (%v)", n, err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSupportAccessRequestHistoryAndETLProtection(t *testing.T) {
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	pool := d.Pool(t, db.RoleApp)
	tenant, holder := uuid.New(), uuid.New()
	if err := db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO users (id) VALUES ($1);
 INSERT INTO tenants (id, kind, name_th) VALUES ($2, 'own_fleet', 'Protected tenant');
 INSERT INTO memberships (user_id, tenant_id, role) VALUES ($1, $2, 'tenant_admin')`, pgx.QueryExecModeSimpleProtocol, holder, tenant)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	admin := &authz.Principal{UserID: holder, TenantID: &tenant, TenantRole: authz.TenantAdmin}
	// Prove p_update_own reaches the row, so consent failures exercise the column trigger.
	if err := db.WithPrincipal(ctx, pool, admin, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE tenants SET name_th='Allowed profile edit' WHERE id=$1`, tenant)
		if err == nil && tag.RowsAffected() != 1 {
			t.Fatal("tenant admin cannot reach own tenant")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	state := func(err error, want string) {
		t.Helper()
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.Code != want {
			t.Fatalf("want SQLSTATE %s, got %v", want, err)
		}
	}
	for _, assignment := range []string{"support_access_enabled=true", "support_first_admin_grant_id=uuidv7()", "support_first_admin_revoked_at=now()", "support_bootstrap_consumed=true"} {
		state(db.WithPrincipal(ctx, pool, admin, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE tenants SET `+assignment+` WHERE id=$1`, tenant)
			return err
		}), "42501")
	}
	insertHistory := `INSERT INTO membership_provenance_history (membership_id,user_id,tenant_id,role,status,membership_created_at,granted_by_platform,grant_version,operation)
 VALUES (uuidv7(),$1,$2,'tenant_admin','active',now(),false,0,'INSERT')`
	for _, p := range []*authz.Principal{admin, {UserID: holder}} {
		if err := db.WithPrincipal(ctx, pool, p, func(tx pgx.Tx) error {
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM membership_provenance_history`).Scan(&n); err != nil {
				return err
			}
			if n != 0 {
				t.Fatalf("ordinary/unset context saw %d history rows", n)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		state(db.WithPrincipal(ctx, pool, p, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, insertHistory, holder, tenant)
			return err
		}), "42501")
	}
	// Also exercise a connection with no request GUCs at all.
	var visible int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM membership_provenance_history`).Scan(&visible); err != nil || visible != 0 {
		t.Fatalf("bare history read = %d (%v)", visible, err)
	}
	_, err := pool.Exec(ctx, insertHistory, holder, tenant)
	state(err, "42501")
	for _, sql := range []string{`UPDATE membership_provenance_history SET role='manager'`, `DELETE FROM membership_provenance_history`} {
		state(db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error { _, err := tx.Exec(ctx, sql); return err }), "42501")
		// Owner privileges reach the append-only trigger as well as the app's revoked DML grants.
		state(db.WithSystem(ctx, d.Pool(t, db.RoleMigrator), nil, func(tx pgx.Tx) error { _, err := tx.Exec(ctx, sql); return err }), "23000")
	}
	etl := d.Pool(t, db.RoleETL)
	state(db.WithPrincipal(ctx, etl, admin, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE tenants SET support_access_enabled=true WHERE id=$1`, tenant)
		return err
	}), "42501")
	if err := db.WithSystem(ctx, etl, nil, func(tx pgx.Tx) error {
		// The seed omits this identity column: it must have no random column default, and the
		// BEFORE INSERT trigger must supply it through the ETL login too.
		var defaults int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
 WHERE table_name='memberships' AND column_name='support_membership_id' AND column_default IS NOT NULL`).Scan(&defaults); err != nil {
			return err
		}
		if defaults != 0 {
			t.Fatal("seed membership identity has a column default")
		}
		newUser := uuid.New()
		if _, err := tx.Exec(ctx, `INSERT INTO users (id) VALUES ($1)`, newUser); err != nil {
			return err
		}
		var membershipID uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO memberships (user_id,tenant_id,role)
 VALUES ($1,$2,'manager') RETURNING support_membership_id`, newUser, tenant).Scan(&membershipID); err != nil {
			return err
		}
		if membershipID.Version() != 7 {
			t.Fatalf("ETL insert identity = %s", membershipID)
		}
		if _, err := tx.Exec(ctx, `UPDATE memberships SET role='manager' WHERE user_id=$1 AND tenant_id=$2`, holder, tenant); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM membership_provenance_history WHERE user_id=$1 AND tenant_id=$2 AND operation='UPDATE' AND role='manager'`, holder, tenant).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Fatalf("ETL membership write appended %d snapshots", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
