//go:build integration

// Acceptance tests of the Appendix A baseline (issue T04) on postgres:18-alpine: the R66 roles, the
// catalog after `migrate up`, the immutability and void-only rules, the SECURITY DEFINER allocators
// and the D5 guard of 0010. The RLS layout and grants are checked by internal/platform/db
// (rls_catalog_test.go, Appendix C §C.3.8), policy behaviour as principals by rls_integration_test.go.
package migrations_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db/pgtest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/migrate/migratetest"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/migrations"
)

func TestMain(m *testing.M) { os.Exit(pgtest.Main(m)) }

const quarantineTenant = "00000000-0000-7000-8000-00000000000f"

// tenantSource is the inline vocabulary of every tenant-stamped table (R11, R58).
var tenantSource = []string{"form", "quarantine", "self", "task", "driver", "trip", "truck"}

// migrated is a fresh database with the whole embedded chain applied (dev / CI order: 0001-0010).
func migrated(t *testing.T) (*pgtest.Database, *migrate.Runner) {
	t.Helper()
	d := pgtest.NewDatabase(t)
	r := migratetest.Runner(t, d, migrations.FS)
	if _, err := r.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	return d, r
}

// connect opens one session as role and sets session GUCs (name, value pairs), e.g. app.bypass_tenant
// the way db.WithSystem will (T07).
func connect(t *testing.T, d *pgtest.Database, role string, gucs ...string) *pgx.Conn {
	t.Helper()
	ctx := context.Background()
	c, err := pgx.Connect(ctx, d.URL(role))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	for i := 0; i+1 < len(gucs); i += 2 {
		if _, err := c.Exec(ctx, "SELECT set_config($1, $2, false)", gucs[i], gucs[i+1]); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func exec(t *testing.T, c *pgx.Conn, sql string, args ...any) {
	t.Helper()
	if _, err := c.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func scalar[T any](t *testing.T, c *pgx.Conn, sql string, args ...any) T {
	t.Helper()
	var v T
	if err := c.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return v
}

func strs(t *testing.T, c *pgx.Conn, sql string, args ...any) []string {
	t.Helper()
	rows, err := c.Query(context.Background(), sql, args...)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return out
}

// wantSQLState runs sql and requires it to fail with code (and, if given, a message fragment).
func wantSQLState(t *testing.T, c *pgx.Conn, code, fragment, sql string, args ...any) {
	t.Helper()
	_, err := c.Exec(context.Background(), sql, args...)
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.Code != code || !strings.Contains(pgErr.Message, fragment) {
		t.Fatalf("%s\nerr = %v, want SQLSTATE %s containing %q", sql, err, code, fragment)
	}
}

func sameSet(t *testing.T, what string, got, want []string) {
	t.Helper()
	g, w := slices.Clone(got), slices.Clone(want)
	sort.Strings(g)
	sort.Strings(w)
	if !slices.Equal(g, w) {
		var missing, extra []string
		for _, x := range w {
			if !slices.Contains(g, x) {
				missing = append(missing, x)
			}
		}
		for _, x := range g {
			if !slices.Contains(w, x) {
				extra = append(extra, x)
			}
		}
		t.Errorf("%s: missing %v, unexpected %v", what, missing, extra)
	}
}

// specFile reads a document of shared-docs/ (next to logitrack-api/ in the repository).
func specFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "shared-docs", rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// appendixATables lists every CREATE TABLE of the sql blocks of Appendix A (schema-qualified for etl).
func appendixATables(t *testing.T) []string {
	t.Helper()
	doc := specFile(t, "specs/mv-go/A-data-model.md")
	block := regexp.MustCompile("(?s)```sql\n(.*?)```")
	create := regexp.MustCompile(`(?m)^CREATE TABLE ([a-z_.]+)`)
	var out []string
	for _, b := range block.FindAllStringSubmatch(doc, -1) {
		for _, m := range create.FindAllStringSubmatch(b[1], -1) {
			out = append(out, m[1])
		}
	}
	return out
}

// On a fresh volume deploy/postgres-init/00-roles.sql creates the five roles of R66 (pgtest mounts it
// as an init script exactly like compose).
func TestRolesScriptCreatesTheFiveRoles(t *testing.T) {
	d := pgtest.NewDatabase(t)
	c := connect(t, d, db.RoleReadonly)
	got := strs(t, c, `SELECT format('%s login=%s bypassrls=%s super=%s', rolname, rolcanlogin, rolbypassrls, rolsuper)
		FROM pg_roles WHERE rolname LIKE 'logitrack%' ORDER BY rolname`)
	want := []string{
		"logitrack_app login=t bypassrls=f super=f",
		"logitrack_etl login=t bypassrls=t super=f",
		"logitrack_migrator login=t bypassrls=f super=f",
		"logitrack_readonly login=t bypassrls=f super=f",
		"logitrack_rls_definer login=f bypassrls=t super=f",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("roles:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// The migrator may SET ROLE logitrack_rls_definer (0009 hand-over, Down helper) but does not inherit
	// its BYPASSRLS-owned functions' rights; it is the only member.
	members := strs(t, c, `SELECT format('%s inherit=%s set=%s admin=%s', m.member::regrole, m.inherit_option, m.set_option, m.admin_option)
		FROM pg_auth_members m WHERE m.roleid IN (SELECT oid FROM pg_roles WHERE rolname LIKE 'logitrack%')`)
	if !slices.Equal(members, []string{"logitrack_migrator inherit=f set=t admin=f"}) {
		t.Fatalf("role memberships = %v", members)
	}
	if owner := scalar[string](t, c, `SELECT pg_get_userbyid(datdba)::text FROM pg_database WHERE datname = 'logitrack'`); owner != db.RoleMigrator {
		t.Fatalf("the init database belongs to %s, want %s", owner, db.RoleMigrator)
	}
}

func TestUpAppliesTheWholeBaseline(t *testing.T) {
	d := pgtest.NewDatabase(t)
	r := migratetest.Runner(t, d, migrations.FS)
	res, err := r.Up(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, x := range res {
		names = append(names, x.Name)
	}
	want := []string{"0001_preamble.sql", "0002_identity.sql", "0003_master.sql", "0004_operations.sql", "0005_billing.sql",
		"0006_finance_hr.sql", "0007_comms.sql", "0008_platform.sql", "0009_infra.sql", "0010_d5_unique_constraints.sql",
		"0011_file_objects_storage_backend.sql"}
	if !slices.Equal(names, want) {
		t.Fatalf("up applied %v", names)
	}
	if v, err := r.Version(context.Background()); err != nil || v != 11 {
		t.Fatalf("version = %d (%v), want 11", v, err)
	}
}

func TestSchemaCatalog(t *testing.T) {
	d, _ := migrated(t)
	c := connect(t, d, db.RoleMigrator)

	t.Run("tables equal the DDL of Appendix A", func(t *testing.T) {
		got := strs(t, c, `SELECT CASE n.nspname WHEN 'public' THEN '' ELSE n.nspname || '.' END || c.relname
			FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname IN ('public', 'etl') AND c.relkind IN ('r', 'p') AND c.relname <> 'goose_db_version'`)
		want := appendixATables(t)
		if len(want) != 85 {
			t.Fatalf("Appendix A declares %d tables, main spec §3.3 counts 85", len(want))
		}
		sameSet(t, "tables", got, want)
	})

	t.Run("no draft name of Appendix A §A.2.R and §A.2.S exists", func(t *testing.T) {
		drafts := []string{"auth_sessions", "billing_statement_rows", "capabilities", "role_capabilities", "roles",
			"tenant_memberships", "storage_objects", "media_objects", "subcontractors", "task_helpers", "legacy_driver_refs",
			"soc_hub_distances", "job_runs", "etl_id_map", "etl_quarantine", "platform_settings", "trips", "checkin"}
		found := strs(t, c, `SELECT n.nspname || '.' || c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast') AND c.relname = ANY ($1)`, drafts)
		if len(found) > 0 {
			t.Errorf("draft relations exist: %v", found)
		}
		columns := []string{"users.legacy_firebase_uid", "users.google_sub", "users.photo_url", "users.legacy_password",
			"users.disabled", "users.force_logout_at", "users.revocation_epoch", "users.is_platform_admin", "tenants.name",
			"tenants.legacy_subcontractor_id", "file_objects.source_status", "notification_deliveries.device_token_id",
			"chats.driver_user_id", "trucks.current_assignments", "device_tokens.invalidated_at", "device_tokens.id",
			"user_scopes.customer_id", "user_scopes.subcontractor_id", "user_scopes.company_id"}
		found = strs(t, c, `SELECT table_name || '.' || column_name FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name || '.' || column_name = ANY ($1)`, columns)
		if len(found) > 0 {
			t.Errorf("draft columns exist: %v", found)
		}
	})

	t.Run("ids are uuidv7, the only identity is outbox_events.id", func(t *testing.T) {
		bad := strs(t, c, `SELECT format('%s.%s %s default=%s pk=%s', a.attrelid::regclass, a.attname, format_type(a.atttypid, a.atttypmod),
				pg_get_expr(ad.adbin, ad.adrelid), EXISTS (SELECT 1 FROM pg_constraint k WHERE k.conrelid = a.attrelid
				AND k.contype = 'p' AND k.conkey = ARRAY[a.attnum]))
			FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid JOIN pg_namespace n ON n.oid = c.relnamespace
			LEFT JOIN pg_attrdef ad ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum
			WHERE n.nspname IN ('public', 'etl') AND c.relkind = 'r' AND c.relname <> 'goose_db_version'
			  AND a.attname = 'id' AND NOT a.attisdropped
			  AND NOT (c.relname = 'outbox_events' AND a.attidentity = 'a' AND a.atttypid = 'bigint'::regtype)
			  AND NOT (a.atttypid = 'uuid'::regtype AND pg_get_expr(ad.adbin, ad.adrelid) = 'uuidv7()'
			           AND EXISTS (SELECT 1 FROM pg_constraint k WHERE k.conrelid = a.attrelid AND k.contype = 'p'
			                       AND k.conkey = ARRAY[a.attnum]))`)
		if len(bad) > 0 {
			t.Errorf("id columns that are not uuid PRIMARY KEY DEFAULT uuidv7():\n%s", strings.Join(bad, "\n"))
		}
		gen := strs(t, c, `SELECT format('%s.%s', ad.adrelid::regclass, a.attname) FROM pg_attrdef ad
			JOIN pg_attribute a ON a.attrelid = ad.adrelid AND a.attnum = ad.adnum
			WHERE pg_get_expr(ad.adbin, ad.adrelid) ~* 'gen_random_uuid|uuid_generate'`)
		if len(gen) > 0 {
			t.Errorf("defaults from gen_random_uuid / uuid-ossp: %v", gen)
		}
		ident := strs(t, c, `SELECT format('%s.%s:%s', a.attrelid::regclass, a.attname, a.attidentity)
			FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname IN ('public', 'etl') AND a.attidentity <> '' AND c.relname <> 'goose_db_version'`)
		if !slices.Equal(ident, []string{"outbox_events.id:a"}) {
			t.Errorf("identity columns = %v, want only outbox_events.id GENERATED ALWAYS (R57)", ident)
		}
		if ext := strs(t, c, `SELECT extname FROM pg_extension WHERE extname <> 'plpgsql' ORDER BY 1`); !slices.Equal(ext, []string{"citext"}) {
			t.Errorf("extensions = %v, want citext only (uuidv7 is native)", ext)
		}
	})

	t.Run("generated columns are STORED", func(t *testing.T) {
		got := strs(t, c, `SELECT format('%s.%s:%s', a.attrelid::regclass, a.attname, a.attgenerated)
			FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname IN ('public', 'etl') AND a.attgenerated <> '' AND NOT a.attisdropped`)
		sameSet(t, "generated columns (main spec §3.2)", got, []string{
			"tasks.plan_date:s", "trip_records.billing_axis_date:s", "standby_records.billing_axis_date:s",
			"trip_photos.photo_type_known:s", "vehicle_expenses.expense_date:s",
			"driver_compensation_configs.effective_from_date:s", "fuel_daily_snapshots.month_key:s",
		})
	})

	t.Run("tenant_source is an inline CHECK, no DOMAIN or ENUM", func(t *testing.T) {
		stamped := strs(t, c, `SELECT table_name FROM information_schema.columns
			WHERE table_schema = 'public' AND column_name = 'tenant_source'`)
		sameSet(t, "tenant-stamped tables", stamped, []string{
			"companies", "drivers", "trucks", "truck_assignments", "tasks", "trip_records", "standby_records",
			"incident_reports", "vehicle_locations", "customer_rate_entries", "customer_fuel_rate_adjustments",
			"customer_service_fees", "standby_rate_entries", "trip_billing_snapshots", "billing_counters",
			"billing_statements", "vehicle_expenses", "maintenance_records", "transactions", "driver_compensation_configs",
			"driver_penalties", "payroll_runs", "driver_advances", "chats", "mobile_installations", "leave_requests",
		})
		for _, tbl := range stamped {
			typ := scalar[string](t, c, `SELECT format('%s notnull=%s', format_type(atttypid, atttypmod), attnotnull)
				FROM pg_attribute WHERE attrelid = $1::regclass AND attname = 'tenant_source'`, tbl)
			if typ != "text notnull=t" {
				t.Errorf("%s.tenant_source is %s, want text NOT NULL", tbl, typ)
			}
			sameSet(t, tbl+".tenant_source CHECK", checkValues(t, c, tbl, "tenant_source"), tenantSource)
			if tid := scalar[bool](t, c, `SELECT attnotnull FROM pg_attribute WHERE attrelid = $1::regclass AND attname = 'tenant_id'`, tbl); !tid {
				t.Errorf("%s.tenant_id is nullable", tbl)
			}
		}
		if n := scalar[int](t, c, `SELECT count(*) FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
			WHERE n.nspname IN ('public', 'etl') AND t.typtype IN ('d', 'e')`); n != 0 {
			t.Errorf("%d DOMAIN/ENUM types exist (A.1.7, R58)", n)
		}
	})

	t.Run("vocabularies", func(t *testing.T) {
		for _, v := range []struct {
			table, column string
			values        []string
		}{
			{"users", "status", []string{"active", "disabled", "reset_required", "deleted"}},                                         // R55
			{"tenants", "kind", []string{"own_fleet", "carrier", "quarantine"}},                                                      // R56
			{"api_keys", "scope", []string{"integration", "script", "cf_shim", "release_publisher"}},                                 // R82
			{"user_scopes", "kind", []string{"customer", "dispatcher"}},                                                              // R86
			{"file_objects", "status", []string{"pending", "committed", "missing_at_source"}},                                        // R1
			{"file_objects", "storage_backend", []string{"local", "s3"}},                                                             // 0011, T11
			{"trip_billing_snapshots", "unpriced_reason", []string{"no_customer", "no_rate", "no_vehicle_class", "no_billing_date"}}, // R62
			{"standby_records", "billing_unpriced_reason", []string{"no_customer", "no_rate", "no_ended_at"}},                        // R62
			{"jobs", "status", []string{"queued", "running", "succeeded", "failed"}},                                                 // R64
			{"notification_deliveries", "kind", []string{"task_assigned", "task_unassigned", "task_cancelled", "tasks_changed",
				"maintenance_scheduled", "chat", "broadcast", "leave_decided", "session_revoked"}}, // R84
		} {
			sameSet(t, v.table+"."+v.column+" CHECK", checkValues(t, c, v.table, v.column), v.values)
		}
	})

	// T11 (Appendix A §A.2.9): every stored image or file is reached through a *_file_id foreign key to file_objects,
	// whose storage_backend says where it lives; no table keeps an object reference of its own.
	t.Run("file references", func(t *testing.T) {
		cols := strs(t, c, `SELECT format('%s.%s', a.attrelid::regclass, a.attname)
			FROM pg_attribute a JOIN pg_class r ON r.oid = a.attrelid JOIN pg_namespace n ON n.oid = r.relnamespace
			WHERE n.nspname = 'public' AND r.relkind = 'r' AND a.attnum > 0 AND NOT a.attisdropped
			  AND (a.attname = 'file_id' OR a.attname LIKE '%\_file\_id')
			  AND NOT EXISTS (SELECT 1 FROM pg_constraint k WHERE k.conrelid = a.attrelid AND k.contype = 'f'
			                    AND k.confrelid = 'file_objects'::regclass AND k.conkey = ARRAY[a.attnum])`)
		if len(cols) > 0 {
			t.Errorf("file columns without a file_objects foreign key: %v", cols)
		}
		refs := strs(t, c, `SELECT DISTINCT conrelid::regclass::text FROM pg_constraint
			WHERE contype = 'f' AND confrelid = 'file_objects'::regclass`)
		sameSet(t, "tables referencing file_objects", refs, []string{"users", "tenant_files", "customers", "companies",
			"drivers", "truck_files", "tasks", "trip_photos", "standby_photos", "incident_reports", "statement_documents",
			"vehicle_expenses", "maintenance_files", "transactions", "driver_penalties", "chat_messages",
			"mobile_app_releases", "leave_request_attachments"})
		if nn := scalar[bool](t, c, `SELECT attnotnull AND NOT atthasdef FROM pg_attribute
			WHERE attrelid = 'file_objects'::regclass AND attname = 'storage_backend'`); !nn {
			t.Error("file_objects.storage_backend must be NOT NULL without a default (every writer names its backend)")
		}
	})

	t.Run("columns", func(t *testing.T) {
		for _, col := range []string{
			// R55
			"users.legacy_auth_uid text", "users.legacy_scrypt_hash bytea", "users.legacy_scrypt_salt bytea",
			"users.status text", "users.auth_version integer", "users.must_change_password boolean",
			"users.password_changed_at timestamp with time zone", "drivers.legacy_auth_uid text",
			// R56, R83
			"tenants.name_th text", "tenants.name_en text", "tenants.kind text", "tenants.contractor_tenant_id uuid",
			"sessions.active_tenant_id uuid", "sessions.install_id text",
			// R1
			"file_objects.status text", "file_objects.purpose text", "file_objects.uploaded_by uuid",
			"file_objects.tenant_id uuid", "file_objects.expires_at timestamp with time zone", "file_objects.storage_backend text",
			// R14, R18, R20, R47, R64
			"broadcasts.voided_at timestamp with time zone", "billing_statements.withholding_tax_rate numeric(5,4)",
			"standby_rate_entries.voided_at timestamp with time zone", "statement_documents.status text",
			"jobs.params jsonb", "trip_records.evidence_token_revoked_at timestamp with time zone",
			"standby_records.evidence_token_revoked_at timestamp with time zone",
			// R57
			"outbox_events.id bigint", "trip_billing_snapshots.last_event_id bigint",
		} {
			name, typ, _ := strings.Cut(col, " ")
			tbl, attr, _ := strings.Cut(name, ".")
			got := strs(t, c, `SELECT format_type(atttypid, atttypmod) FROM pg_attribute
				WHERE attrelid = to_regclass($1) AND attname = $2 AND NOT attisdropped`, tbl, attr)
			if len(got) != 1 || got[0] != typ {
				t.Errorf("%s: %v, want %s", name, got, typ)
			}
		}
		if def := scalar[string](t, c, `SELECT format('%s notnull=%s', pg_get_expr(adbin, adrelid), a.attnotnull) FROM pg_attrdef
			JOIN pg_attribute a ON a.attrelid = adrelid AND a.attnum = adnum WHERE adrelid = 'jobs'::regclass AND a.attname = 'params'`); def != "'{}'::jsonb notnull=t" {
			t.Errorf("jobs.params: %s", def)
		}
		// Money is NUMERIC(14,2) (R20); fuel prices NUMERIC(8,2), the THB-per-baht band factor NUMERIC(10,2) (A.1.5).
		money := strs(t, c, `SELECT format('%s.%s %s', a.attrelid::regclass, a.attname, format_type(a.atttypid, a.atttypmod))
			FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = 'public' AND c.relkind = 'r' AND a.attname LIKE '%\_thb' AND NOT a.attisdropped
			  AND format_type(a.atttypid, a.atttypmod) <> 'numeric(14,2)'`)
		sameSet(t, "THB columns other than numeric(14,2)", money, []string{
			"customer_fuel_rate_adjustments.reference_fuel_price_thb numeric(8,2)",
			"trip_billing_snapshots.fuel_band_lower_thb numeric(8,2)",
			"trip_billing_snapshots.fuel_band_upper_thb numeric(8,2)",
			"trip_billing_snapshots.reference_fuel_price_thb numeric(8,2)",
		})
	})

	t.Run("indexes and keys", func(t *testing.T) {
		for name, want := range map[string]string{
			"sessions_user_install_live": "CREATE UNIQUE INDEX sessions_user_install_live ON public.sessions USING btree (user_id, install_id) WHERE ((install_id IS NOT NULL) AND (revoked_at IS NULL))",
			"tenants_one_own_fleet":      "CREATE UNIQUE INDEX tenants_one_own_fleet ON public.tenants USING btree (kind) WHERE (kind = 'own_fleet'::text)",
			"tenants_one_quarantine":     "CREATE UNIQUE INDEX tenants_one_quarantine ON public.tenants USING btree (kind) WHERE (kind = 'quarantine'::text)",
			// R63: durable offline replay keys
			"tasks_client_op":            "CREATE UNIQUE INDEX tasks_client_op ON public.tasks USING btree (driver_id, client_op_id) WHERE (client_op_id IS NOT NULL)",
			"standby_records_client_op":  "CREATE UNIQUE INDEX standby_records_client_op ON public.standby_records USING btree (driver_id, client_op_id) WHERE (client_op_id IS NOT NULL)",
			"incident_reports_client_op": "CREATE UNIQUE INDEX incident_reports_client_op ON public.incident_reports USING btree (driver_id, client_op_id) WHERE (client_op_id IS NOT NULL)",
			"vehicle_expenses_client_op": "CREATE UNIQUE INDEX vehicle_expenses_client_op ON public.vehicle_expenses USING btree (driver_id, client_op_id) WHERE (client_op_id IS NOT NULL)",
			"leave_requests_client_op":   "CREATE UNIQUE INDEX leave_requests_client_op ON public.leave_requests USING btree (driver_id, client_op_id) WHERE (client_op_id IS NOT NULL)",
			"chat_messages_client_id":    "CREATE UNIQUE INDEX chat_messages_client_id ON public.chat_messages USING btree (chat_id, client_message_id) WHERE (client_message_id IS NOT NULL)",
			// D5 (0010, R88)
			"vehicle_expenses_fuel_taxinv":       "CREATE UNIQUE INDEX vehicle_expenses_fuel_taxinv ON public.vehicle_expenses USING btree (driver_id, tax_inv_id) WHERE ((expense_type = 'fuel'::text) AND (tax_inv_id IS NOT NULL))",
			"chats_one_open_per_driver":          "CREATE UNIQUE INDEX chats_one_open_per_driver ON public.chats USING btree (driver_id) WHERE (status <> 'closed'::text)",
			"customer_service_fees_one_per_type": "CREATE UNIQUE INDEX customer_service_fees_one_per_type ON public.customer_service_fees USING btree (billing_party_id, fee_type) WHERE (fee_type <> 'custom'::text)",
		} {
			got := strs(t, c, `SELECT pg_get_indexdef(i.indexrelid) FROM pg_index i WHERE i.indexrelid = to_regclass($1) AND i.indisvalid`, name)
			if len(got) != 1 || got[0] != want {
				t.Errorf("%s = %v\nwant %s", name, got, want)
			}
		}
		if n := scalar[int](t, c, `SELECT count(*) FROM pg_class WHERE relname = 'vehicle_expenses_fuel_taxinv_lookup'`); n != 0 {
			t.Error("0010 keeps the superseded vehicle_expenses_fuel_taxinv_lookup")
		}
		for _, fk := range []string{"tenants.contractor_tenant_id tenants", "sessions.active_tenant_id tenants", "file_objects.uploaded_by users",
			"file_objects.tenant_id tenants", "user_scopes.billing_party_id billing_parties"} {
			col, ref, _ := strings.Cut(fk, " ")
			tbl, attr, _ := strings.Cut(col, ".")
			got := strs(t, c, `SELECT k.confrelid::regclass::text FROM pg_constraint k
				WHERE k.conrelid = $1::regclass AND k.contype = 'f'
				  AND k.conkey = ARRAY[(SELECT attnum FROM pg_attribute WHERE attrelid = $1::regclass AND attname = $2)]`, tbl, attr)
			if !slices.Equal(got, []string{ref}) {
				t.Errorf("FK of %s = %v, want %s", col, got, ref)
			}
		}
	})

	// /startupz reads the applied version as logitrack_app; no runtime login may rewrite migration state.
	t.Run("goose_db_version is read-only for the API and ETL logins", func(t *testing.T) {
		app := connect(t, d, db.RoleApp)
		if v := scalar[int64](t, app, `SELECT max(version_id) FROM goose_db_version`); v != 11 {
			t.Errorf("logitrack_app reads version %d, want 11", v)
		}
		for _, role := range []string{db.RoleApp, db.RoleETL} {
			wantSQLState(t, connect(t, d, role), "42501", "permission denied", `DELETE FROM goose_db_version`)
		}
	})

	t.Run("the quarantine tenant row", func(t *testing.T) {
		etl := connect(t, d, db.RoleETL)
		got := strs(t, etl, `SELECT format('%s %s %s %s', id, kind, name_en, status) FROM tenants`)
		if !slices.Equal(got, []string{quarantineTenant + " quarantine Quarantine active"}) {
			t.Fatalf("tenants after the chain = %v", got)
		}
		if id := scalar[string](t, etl, `SELECT app_quarantine_tenant_id()::text`); id != quarantineTenant {
			t.Fatalf("app_quarantine_tenant_id() = %s", id)
		}
	})
}

// checkValues returns the literals of the single-column CHECK constraint "column IN (...)" of table.
func checkValues(t *testing.T, c *pgx.Conn, table, column string) []string {
	t.Helper()
	defs := strs(t, c, `SELECT pg_get_constraintdef(k.oid) FROM pg_constraint k
		WHERE k.conrelid = $1::regclass AND k.contype = 'c'
		  AND k.conkey = ARRAY[(SELECT attnum FROM pg_attribute WHERE attrelid = $1::regclass AND attname = $2)]
		  AND pg_get_constraintdef(k.oid) LIKE '%= ANY%'`, table, column)
	if len(defs) != 1 {
		t.Fatalf("%s.%s has %d IN (...) CHECKs: %v", table, column, len(defs), defs)
	}
	var out []string
	for _, m := range regexp.MustCompile(`'([^']*)'::text`).FindAllStringSubmatch(defs[0], -1) {
		out = append(out, m[1])
	}
	return out
}

// tenants: one own fleet, one quarantine row, contractor reach only for carriers (R7, R56, R60).
func TestTenantKindRules(t *testing.T) {
	d, _ := migrated(t)
	etl := connect(t, d, db.RoleETL)
	own := scalar[string](t, etl, `INSERT INTO tenants (kind, name_th) VALUES ('own_fleet', 'กองรถ') RETURNING id::text`)
	wantSQLState(t, etl, "23505", "tenants_one_own_fleet", `INSERT INTO tenants (kind, name_th) VALUES ('own_fleet', 'สอง')`)
	wantSQLState(t, etl, "23505", "tenants_one_quarantine", `INSERT INTO tenants (kind, name_th) VALUES ('quarantine', 'สอง')`)
	wantSQLState(t, etl, "23514", "tenants_kind_check", `INSERT INTO tenants (kind, name_th) VALUES ('subcontractor', 'x')`)
	exec(t, etl, `INSERT INTO tenants (kind, name_th, legal_type, contractor_tenant_id) VALUES ('carrier', 'ผู้รับเหมา', 'company', $1)`, own)
	wantSQLState(t, etl, "23503", "", `INSERT INTO tenants (kind, name_th, legal_type, contractor_tenant_id) VALUES ('carrier', 'x', 'company', uuidv7())`)
	wantSQLState(t, etl, "23514", "tenants_contractor_carrier_only",
		`INSERT INTO tenants (kind, name_th, contractor_tenant_id) VALUES ('own_fleet', 'x', $1)`, own)
	if v := scalar[int](t, etl, `SELECT uuid_extract_version(id) FROM tenants WHERE id = $1`, own); v != 7 {
		t.Fatalf("tenant id version = %d, want 7 (uuidv7)", v)
	}
}

// Append-only tables (A.1.8): REVOKE UPDATE, DELETE from logitrack_app and logitrack_etl at the 0009
// grant site, and a raising trigger that stops the owner too.
func TestAppendOnlyTablesRefuseUpdateAndDelete(t *testing.T) {
	d, _ := migrated(t)
	owner := connect(t, d, db.RoleMigrator, "app.bypass_tenant", "on") // FORCE RLS: the owner needs the bypass
	trip := scalar[string](t, owner, `INSERT INTO trip_records (tenant_id, tenant_source, trip_no, status, job_type)
		VALUES (app_quarantine_tenant_id(), 'quarantine', 'TRIP-1', 'delivered', 'first_mile') RETURNING id::text`)
	cust := scalar[string](t, owner, `INSERT INTO customers (code, name) VALUES ('CJSF', 'CJ') RETURNING id::text`)
	party := scalar[string](t, owner, `INSERT INTO billing_parties (kind, customer_id) VALUES ('customer', $1) RETURNING id::text`, cust)
	stmt := scalar[string](t, owner, `INSERT INTO billing_statements (tenant_id, billing_party_id, invoice_number, period_year,
		period_month, total_amount, withholding_tax_rate, withholding_tax, net_amount, generated_at)
		VALUES (app_quarantine_tenant_id(), $1, 'CJSF-202610-001', 2026, 10, 100, 0.01, 1, 99, now()) RETURNING id::text`, party)
	for _, sql := range []string{
		`INSERT INTO status_history (entity_type, entity_id, status, changed_at) VALUES ('user', uuidv7(), 'active', now())`,
		`INSERT INTO trip_no_history (trip_id, old_trip_no, new_trip_no) VALUES ('` + trip + `', 'TRIP-0', 'TRIP-1')`,
		`INSERT INTO billing_statement_lines (statement_id, row_type, trip_id, amount_thb) VALUES ('` + stmt + `', 'trip', '` + trip + `', 100)`,
		`INSERT INTO transactions (tenant_id, tenant_source, tx_type, amount_thb, tx_date) VALUES (app_quarantine_tenant_id(), 'form', 'tax', 10, current_date)`,
		`INSERT INTO security_events (event_type, summary) VALUES ('user_created', 'x')`,
		`INSERT INTO fuel_daily_snapshots (day_key, captured_at, source, items) VALUES (current_date, now(), 'bangchak', '[]')`,
	} {
		exec(t, owner, sql)
	}
	exec(t, owner, `UPDATE billing_statements SET status = 'sent', sent_at = now() WHERE id = $1`, stmt)

	tables := map[string]string{
		"status_history": "reason", "trip_no_history": "new_trip_no", "billing_statement_lines": "amount_thb",
		"transactions": "notes", "security_events": "summary", "fuel_daily_snapshots": "source",
	}
	app := connect(t, d, db.RoleApp, "app.bypass_tenant", "on")
	etl := connect(t, d, db.RoleETL)
	for tbl, col := range tables {
		for _, c := range []*pgx.Conn{app, etl} {
			wantSQLState(t, c, "42501", "permission denied", "UPDATE "+tbl+" SET "+col+" = "+col)
			wantSQLState(t, c, "42501", "permission denied", "DELETE FROM "+tbl)
		}
		// The owner holds every privilege; the BEFORE trigger refuses.
		wantSQLState(t, owner, "23000", "", "UPDATE "+tbl+" SET "+col+" = "+col)
		wantSQLState(t, owner, "23000", "", "DELETE FROM "+tbl)
	}
	// A sent statement cannot be deleted, so its lines stay (the draft cascade: TestDraftStatementDeleteCascadesItsLines).
	wantSQLState(t, owner, "23000", "only a draft may be deleted", "DELETE FROM billing_statements")
}

// A draft statement's lines go with its ON DELETE CASCADE (A.1.8): the line guard lets the cascade
// through, so a draft invoice can be discarded or regenerated by logitrack_app.
func TestDraftStatementDeleteCascadesItsLines(t *testing.T) {
	d, _ := migrated(t)
	owner := connect(t, d, db.RoleMigrator, "app.bypass_tenant", "on")
	trip := scalar[string](t, owner, `INSERT INTO trip_records (tenant_id, tenant_source, trip_no, status, job_type)
		VALUES (app_quarantine_tenant_id(), 'quarantine', 'TRIP-1', 'delivered', 'first_mile') RETURNING id::text`)
	cust := scalar[string](t, owner, `INSERT INTO customers (code, name) VALUES ('CJSF', 'CJ') RETURNING id::text`)
	party := scalar[string](t, owner, `INSERT INTO billing_parties (kind, customer_id) VALUES ('customer', $1) RETURNING id::text`, cust)
	app := connect(t, d, db.RoleApp, "app.bypass_tenant", "on") // WithSystem (T07)
	stmt := scalar[string](t, app, `INSERT INTO billing_statements (tenant_id, billing_party_id, invoice_number, period_year,
		period_month, total_amount, withholding_tax_rate, withholding_tax, net_amount, generated_at)
		VALUES (app_quarantine_tenant_id(), $1, 'CJSF-202610-001', 2026, 10, 100, 0.01, 1, 99, now()) RETURNING id::text`, party)
	exec(t, app, `INSERT INTO billing_statement_lines (statement_id, row_type, trip_id, amount_thb) VALUES ($1, 'trip', $2, 100)`, stmt, trip)
	if s := scalar[string](t, app, `SELECT status FROM billing_statements WHERE id = $1`, stmt); s != "draft" {
		t.Fatalf("a new statement is %q, want draft", s)
	}
	exec(t, app, `DELETE FROM billing_statements WHERE id = $1`, stmt)
	if n := scalar[int](t, owner, `SELECT count(*)::int FROM billing_statement_lines WHERE statement_id = $1`, stmt); n != 0 {
		t.Fatalf("deleting the draft left %d lines", n)
	}
}

// Announcement rows (ADR 0009 §1, A.1.8): only voided false -> true plus the void columns and
// updated_at change; no DELETE. standby_rate_entries is soft-delete only (R20).
func TestAnnouncementRowsAreVoidOnly(t *testing.T) {
	d, _ := migrated(t)
	app := connect(t, d, db.RoleApp, "app.bypass_tenant", "on") // WithSystem (T07)
	own := scalar[string](t, app, `INSERT INTO tenants (kind, name_th) VALUES ('own_fleet', 'กองรถ') RETURNING id::text`)
	user := scalar[string](t, app, `INSERT INTO users (email) VALUES ('ops@example.test') RETURNING id::text`)
	cust := scalar[string](t, app, `INSERT INTO customers (code, name) VALUES ('SPX', 'Shopee') RETURNING id::text`)
	party := scalar[string](t, app, `INSERT INTO billing_parties (kind, customer_id) VALUES ('customer', $1) RETURNING id::text`, cust)
	rate := scalar[string](t, app, `INSERT INTO customer_rate_entries (tenant_id, billing_party_id, import_id, hub_code,
		destination_code, vehicle_class, rate_thb, effective_from_date, effective_from_at)
		VALUES ($1, $2, 'manual_1', 'SPK-GW', 'SPK890103', '4WJ', 1500, '2026-10-01', '2026-09-30T17:00:00Z') RETURNING id::text`, own, party)
	fuel := scalar[string](t, app, `INSERT INTO customer_fuel_rate_adjustments (tenant_id, billing_party_id, effective_from_date,
		effective_from_at, rate_multiplier) VALUES ($1, $2, '2026-10-01', '2026-09-30T17:00:00Z', 1.05) RETURNING id::text`, own, party)
	standby := scalar[string](t, app, `INSERT INTO standby_rate_entries (tenant_id, billing_party_id, rate_thb, effective_from_date,
		effective_from_at) VALUES ($1, $2, 500, '2026-10-01', '2026-09-30T17:00:00Z') RETURNING id::text`, own, party)

	for _, a := range []struct{ table, id, change string }{
		{"customer_rate_entries", rate, "rate_thb = 1600"},
		{"customer_fuel_rate_adjustments", fuel, "rate_multiplier = 1.10"},
	} {
		wantSQLState(t, app, "23000", "rows are immutable", "UPDATE "+a.table+" SET "+a.change+" WHERE id = $1", a.id)
		wantSQLState(t, app, "23000", "rows are immutable",
			"UPDATE "+a.table+" SET voided = true, voided_at = now() WHERE id = $1", a.id) // voided_by is required
		wantSQLState(t, app, "23000", "void may not change other columns",
			"UPDATE "+a.table+" SET voided = true, voided_at = now(), voided_by = $2, "+a.change+" WHERE id = $1", a.id, user)
		exec(t, app, "UPDATE "+a.table+" SET voided = true, voided_at = now(), voided_by = $2, voided_reason = 'superseded', updated_at = now() WHERE id = $1", a.id, user)
		wantSQLState(t, app, "23000", "is already voided", "UPDATE "+a.table+" SET voided_reason = 'again' WHERE id = $1", a.id)
		wantSQLState(t, app, "42501", "permission denied", "DELETE FROM "+a.table+" WHERE id = $1", a.id)
	}
	exec(t, app, `UPDATE standby_rate_entries SET voided_at = now(), voided_by = $2, voided_reason = 'retired' WHERE id = $1`, standby, user)
	wantSQLState(t, app, "42501", "permission denied", `DELETE FROM standby_rate_entries WHERE id = $1`, standby)
	owner := connect(t, d, db.RoleMigrator, "app.bypass_tenant", "on")
	for _, tbl := range []string{"customer_rate_entries", "customer_fuel_rate_adjustments", "standby_rate_entries"} {
		wantSQLState(t, owner, "23000", "append-only", "DELETE FROM "+tbl)
	}
}

// Counters are reachable only through the SECURITY DEFINER allocators (R10, R61, R66, R67).
func TestSecurityDefinerAllocators(t *testing.T) {
	d, _ := migrated(t)
	owner := connect(t, d, db.RoleMigrator)
	for _, fn := range []string{"next_task_seq(text,date)", "next_invoice_seq(uuid,uuid,integer,integer,text)",
		"app_task_stops_in_scope(uuid)", "app_task_in_scope(uuid)", "app_trip_in_scope(uuid)",
		"app_recent_work_in_scope(uuid,uuid)", "driver_directory()", "trg_driver_link_membership()"} {
		got := scalar[string](t, owner, `SELECT format('owner=%s secdef=%s public=%s', pg_get_userbyid(proowner), prosecdef,
			proacl IS NULL OR EXISTS (SELECT 1 FROM aclexplode(proacl) a WHERE a.grantee = 0))
			FROM pg_proc WHERE oid = $1::regprocedure`, fn)
		if got != "owner=logitrack_rls_definer secdef=t public=f" {
			t.Errorf("%s: %s", fn, got)
		}
	}
	for _, p := range []struct {
		role, fn string
		want     bool
	}{
		{db.RoleApp, "next_task_seq(text,date)", true},
		{db.RoleETL, "next_task_seq(text,date)", true},
		{db.RoleReadonly, "next_task_seq(text,date)", false},
		{db.RoleReadonly, "next_invoice_seq(uuid,uuid,integer,integer,text)", false},
		{db.RoleReadonly, "app_task_in_scope(uuid)", true},
		{db.RoleETL, "app_task_in_scope(uuid)", false},
		{db.RoleETL, "driver_directory()", false},
	} {
		if got := scalar[bool](t, owner, `SELECT has_function_privilege($1, $2, 'EXECUTE')`, p.role, p.fn); got != p.want {
			t.Errorf("%s EXECUTE %s = %t, want %t", p.role, p.fn, got, p.want)
		}
	}

	app := connect(t, d, db.RoleApp)
	for i, want := range []int{1, 2} {
		if got := scalar[int](t, app, `SELECT next_task_seq('first_mile', '2026-10-10')`); got != want {
			t.Fatalf("call %d: next_task_seq = %d, want %d", i+1, got, want)
		}
	}
	if got := scalar[int](t, app, `SELECT next_task_seq('line_haul', '2026-10-10')`); got != 1 {
		t.Fatalf("line_haul starts at %d", got)
	}
	wantSQLState(t, app, "42501", "permission denied", `SELECT * FROM task_number_counters`)
	wantSQLState(t, app, "42501", "permission denied", `UPDATE billing_counters SET last_seq = 0`)
	wantSQLState(t, connect(t, d, db.RoleReadonly), "42501", "permission denied", `SELECT next_task_seq('first_mile', '2026-10-10')`)

	etl := connect(t, d, db.RoleETL)
	own := scalar[string](t, etl, `INSERT INTO tenants (kind, name_th) VALUES ('own_fleet', 'กองรถ') RETURNING id::text`)
	carrier := scalar[string](t, etl, `INSERT INTO tenants (kind, name_th, legal_type) VALUES ('carrier', 'ผู้รับเหมา', 'company') RETURNING id::text`)
	cust := scalar[string](t, etl, `INSERT INTO customers (code, name) VALUES ('CJSF', 'CJ') RETURNING id::text`)
	party := scalar[string](t, etl, `INSERT INTO billing_parties (kind, customer_id) VALUES ('customer', $1) RETURNING id::text`, cust)
	for i, want := range []int{1, 2} {
		if got := scalar[int](t, app, `SELECT next_invoice_seq($1, $2, 2026, 10, 'CJSF')`, own, party); got != want {
			t.Fatalf("call %d: next_invoice_seq = %d, want %d", i+1, got, want)
		}
	}
	// Another billing carrier's counter for the same party and month answers NULL (R61).
	if got := scalar[*int](t, app, `SELECT next_invoice_seq($1, $2, 2026, 10, 'CJSF')`, carrier, party); got != nil {
		t.Fatalf("next_invoice_seq for another carrier = %d, want NULL", *got)
	}
}

// 0010 refuses to finish while a D5 duplicate survives (R59, R88): the first run fails on the
// unique build and leaves an INVALID index, a rerun stops at the guard until an operator drops it
// and fixes the rows.
func TestD5ConstraintsStopOnDuplicates(t *testing.T) {
	ctx := context.Background()
	d := pgtest.NewDatabase(t)
	r := migratetest.Runner(t, d, migrations.FS)
	if _, err := r.UpTo(ctx, 9); err != nil {
		t.Fatal(err)
	}
	etl := connect(t, d, db.RoleETL)
	own := scalar[string](t, etl, `INSERT INTO tenants (kind, name_th) VALUES ('own_fleet', 'กองรถ') RETURNING id::text`)
	driver := scalar[string](t, etl, `INSERT INTO drivers (tenant_id, tenant_source, first_name, last_name, mobile)
		VALUES ($1, 'self', 'สมชาย', 'ใจดี', '0800000000') RETURNING id::text`, own)
	for range 2 {
		exec(t, etl, `INSERT INTO vehicle_expenses (tenant_id, tenant_source, driver_id, expense_type, expense_at, amount_thb, tax_inv_id)
			VALUES ($1, 'driver', $2, 'fuel', now(), 1200, 'TAXINV-1')`, own, driver)
	}

	_, err := r.Up(ctx)
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "23505" || !strings.Contains(pgErr.Message, "vehicle_expenses_fuel_taxinv") {
		t.Fatalf("first up: %v, want a unique violation building vehicle_expenses_fuel_taxinv", err)
	}
	_, err = r.Up(ctx)
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "23505" ||
		!strings.Contains(pgErr.Message, "D5 unique index(es) vehicle_expenses_fuel_taxinv are INVALID") {
		t.Fatalf("rerun: %v, want the INVALID-index guard", err)
	}
	if v, _ := r.Version(ctx); v != 9 {
		t.Fatalf("version = %d after the refused 0010, want 9", v)
	}

	// The runbook: drop the invalid index, fix the rows, run up again.
	owner := connect(t, d, db.RoleMigrator)
	exec(t, owner, `DROP INDEX CONCURRENTLY vehicle_expenses_fuel_taxinv`)
	exec(t, etl, `DELETE FROM vehicle_expenses WHERE id = (SELECT id FROM vehicle_expenses ORDER BY id DESC LIMIT 1)`)
	if _, err := r.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if v, _ := r.Version(ctx); v != 11 { // 0010, then 0011 (T11) right after it
		t.Fatalf("version = %d, want 11", v)
	}
	if n := scalar[int](t, owner, `SELECT count(*) FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid WHERE i.indisvalid
		AND c.relname IN ('vehicle_expenses_fuel_taxinv','chats_one_open_per_driver','customer_service_fees_one_per_type')`); n != 3 {
		t.Fatalf("%d valid D5 indexes, want 3", n)
	}
}
