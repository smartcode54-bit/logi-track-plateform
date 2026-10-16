//go:build integration

package iam_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/iamdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

func TestGate1LegacyAdminlessBootstrapAndMigrationGrants(t *testing.T) {
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	r := migratetest.Runner(t, d, migrations.FS)
	if _, err := r.UpTo(ctx, 26); err != nil {
		t.Fatal(err)
	}
	pool := d.Pool(t, db.RoleApp)
	empty, removed, holder, actor := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if err := db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO users(id)VALUES($1),($2)`, holder, actor); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO tenants(id,kind,name_th,legal_type)VALUES($1,'carrier','No admin yet','company'),($2,'carrier','Former admin','company')`, empty, removed); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO security_events(event_type,summary,tenant_id,target_user_id,details)VALUES('membership_removed','former admin removed',$1,$2,'{"previousRole":"tenant_admin"}')`, removed, holder)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error {
		for _, id := range []uuid.UUID{empty, removed} {
			var consumed bool
			if err := tx.QueryRow(ctx, `SELECT support_bootstrap_consumed FROM tenants WHERE id=$1`, id).Scan(&consumed); err != nil {
				return err
			}
			if consumed != (id == removed) {
				t.Fatalf("bootstrap for %s consumed=%t", id, consumed)
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO memberships(user_id,tenant_id,role)VALUES($1,$2,'tenant_admin')`, holder, empty); err != nil {
			return err
		}
		q := iamdb.New(tx)
		at := time.Now().UTC()
		if _, err := q.SetPlatformMembershipGrant(ctx, iamdb.SetPlatformMembershipGrantParams{UserID: holder, TenantID: empty, GrantorUserID: actor, GrantedAt: at}); err != nil {
			return err
		}
		if n, err := q.RecordFirstSupportOnboardingAdmin(ctx, iamdb.RecordFirstSupportOnboardingAdminParams{UserID: holder, TenantID: empty}); err != nil || n != 1 {
			t.Fatalf("first legacy onboarding grant=%d (%v)", n, err)
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET must_change_password=false,credential_self_chosen_at=$2 WHERE id=$1`, holder, at.Add(time.Second)); err != nil {
			return err
		}
		m, err := q.GetSupportMembershipAuthority(ctx, iamdb.GetSupportMembershipAuthorityParams{UserID: holder, TenantID: empty})
		if err != nil {
			return err
		}
		if !m.FirstAdminSelfChosen || !m.CredentialEligible || !m.GrantedByPlatform {
			t.Fatalf("legacy bootstrap unusable: %+v", m)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	readonly := d.Pool(t, db.RoleReadonly)
	for _, table := range []string{"support_visits", "membership_provenance_history", "support_request_limits"} {
		var canRead bool
		if err := readonly.QueryRow(ctx, `SELECT has_table_privilege(current_user,$1,'SELECT')`, table).Scan(&canRead); err != nil || !canRead {
			t.Fatalf("readonly SELECT %s=%t (%v)", table, canRead, err)
		}
	}
	var fk bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_constraint c JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=ANY(c.conkey) WHERE c.contype='f' AND c.conrelid='tenants'::regclass AND c.confrelid='users'::regclass AND a.attname='support_access_changed_by')`).Scan(&fk); err != nil || !fk {
		t.Fatalf("consent actor FK missing (%v)", err)
	}
	err := db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE tenants SET support_access_enabled=true,support_access_version=1,support_access_changed_at=now(),support_access_changed_by=$2 WHERE id=$1`, empty, uuid.New())
		return err
	})
	if err == nil {
		t.Fatal("unknown consent actor accepted")
	}
}
