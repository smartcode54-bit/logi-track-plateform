//go:build integration

package iam

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
	"testing"
	"time"
)

func TestSupportProvisioningAndLifecycleServiceExceptions(t *testing.T) {
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	if _, err := migratetest.Runner(t, d, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	pool := d.Pool(t, db.RoleApp)
	actor := uuid.New()
	if err := db.WithSystem(ctx, pool, nil, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO users(id,email)VALUES($1,'platform-control@example.test')`, actor)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	a := &Admin{pool: pool, now: time.Now}
	c := call{ctx: ctx, p: &authz.Principal{UserID: actor, Platform: []authz.PlatformRole{authz.PlatformAdmin}, TenantRole: authz.SupportReadOnly}}
	tenant, err := a.CreateTenant(c, CreateTenantInput{Kind: "carrier", Code: "CONTROL", NameTh: "Control Company", LegalType: "company", TaxID: "0105536000313"})
	if err != nil {
		t.Fatal(err)
	}
	lifecycle := PatchTenantInput{Status: Field[string]{Set: true, V: "suspended"}, Reason: Field[string]{Set: true, V: "review"}}
	out, err := a.PatchTenant(c, tenant.ID, lifecycle)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "suspended" {
		t.Fatal("lifecycle was not applied")
	}
	for _, in := range []PatchTenantInput{{NameTh: Field[string]{Set: true, V: "Changed"}}, {Status: lifecycle.Status, Reason: lifecycle.Reason, NameTh: Field[string]{Set: true, V: "Changed"}}} {
		if _, err := a.PatchTenant(c, tenant.ID, in); err == nil {
			t.Fatal("support changed tenant profile")
		} else if e, ok := err.(*httpx.Error); !ok || e.Code != httpx.CodeSupportReadOnly {
			t.Fatalf("profile guard: %v", err)
		}
	}
}
