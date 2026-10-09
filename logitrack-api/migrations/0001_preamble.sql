-- 0001_preamble.sql: Appendix A §A.2.0 plus the Appendix C block of this file (§C.3.5 generators, §C.3.6
-- trg_freeze_tenant_id). No tables. Never edit this file once merged; changes go into 0011 and later.

-- +goose Up
-- Roles come from deploy/postgres-init/00-roles.sql (R66); no migration creates a role or holds a credential.
-- Assert the login and the five roles first, so a wrong URL or a missing role fails before any object exists.
-- +goose StatementBegin
DO $$
DECLARE
  r record;
BEGIN
  IF current_user <> 'logitrack_migrator' THEN
    RAISE EXCEPTION 'migrations run as logitrack_migrator (MIGRATE_DATABASE_URL), not %', current_user
      USING ERRCODE = 'insufficient_privilege';
  END IF;
  FOR r IN SELECT * FROM (VALUES
      ('logitrack_migrator',    true,  false),
      ('logitrack_app',         true,  false),
      ('logitrack_etl',         true,  true),
      ('logitrack_readonly',    true,  false),
      ('logitrack_rls_definer', false, true)) AS v(role_name, can_login, bypass_rls)
  LOOP
    IF NOT EXISTS (SELECT 1 FROM pg_roles
                    WHERE rolname = r.role_name AND rolcanlogin = r.can_login
                      AND rolbypassrls = r.bypass_rls AND NOT rolsuper) THEN
      RAISE EXCEPTION 'role % is missing or has wrong attributes (want LOGIN=%, BYPASSRLS=%, NOSUPERUSER); run deploy/postgres-init/00-roles.sql',
        r.role_name, r.can_login, r.bypass_rls
        USING ERRCODE = 'undefined_object';
    END IF;
  END LOOP;
  -- 0009 hands the SECURITY DEFINER functions to logitrack_rls_definer (ALTER FUNCTION ... OWNER TO needs SET).
  IF NOT pg_has_role('logitrack_migrator', 'logitrack_rls_definer', 'SET') THEN
    RAISE EXCEPTION 'logitrack_migrator must be granted logitrack_rls_definer WITH INHERIT FALSE, SET TRUE (00-roles.sql)'
      USING ERRCODE = 'insufficient_privilege';
  END IF;
END
$$;
-- +goose StatementEnd

CREATE EXTENSION IF NOT EXISTS citext;   -- case-insensitive emails and business codes
CREATE SCHEMA IF NOT EXISTS etl;         -- ETL bookkeeping; tables in 0009

-- No GRANT/REVOKE in 0001-0008: 0009_infra is the single grant site (A.2.8).

-- Bangkok calendar day of an instant (== bangkokDateStrFromMillis, logitrack-web/lib/billingCompute.ts:168-178).
-- Fixed UTC+7, no DST: IMMUTABLE is correct, so it can back STORED generated columns and indexes.
CREATE FUNCTION bkk_date(ts timestamptz) RETURNS date
  LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
  RETURN ((ts AT TIME ZONE 'UTC') + interval '7 hours')::date;

-- 00:00 Asia/Bangkok of a calendar day, as an instant.
CREATE FUNCTION bkk_midnight(d date) RETURNS timestamptz
  LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
  RETURN (d::timestamp - interval '7 hours') AT TIME ZONE 'UTC';

-- RLS context helpers (GUC readers; Appendix C §C.3.3). db.WithPrincipal / db.WithSystem set the GUCs once per
-- transaction with set_config(name, value, true). An unset GUC yields NULL / '' / false, so a forgotten
-- WithPrincipal sees zero rows instead of all rows.
CREATE FUNCTION app_user_id() RETURNS uuid
  LANGUAGE sql STABLE PARALLEL SAFE
  RETURN nullif(current_setting('app.user_id', true), '')::uuid;

CREATE FUNCTION app_tenant_id() RETURNS uuid
  LANGUAGE sql STABLE PARALLEL SAFE
  RETURN nullif(current_setting('app.tenant_id', true), '')::uuid;

CREATE FUNCTION app_role() RETURNS text
  LANGUAGE sql STABLE PARALLEL SAFE
  RETURN coalesce(current_setting('app.role', true), '');

CREATE FUNCTION app_driver_id() RETURNS uuid
  LANGUAGE sql STABLE PARALLEL SAFE
  RETURN nullif(current_setting('app.driver_id', true), '')::uuid;

-- Comma-separated billing_parties.id values (customer and dispatcher scopes, R12).
CREATE FUNCTION app_customer_ids() RETURNS uuid[]
  LANGUAGE sql STABLE PARALLEL SAFE
  RETURN coalesce(string_to_array(nullif(current_setting('app.customer_ids', true), ''), ',')::uuid[], '{}'::uuid[]);

CREATE FUNCTION app_is_dispatcher() RETURNS boolean
  LANGUAGE sql STABLE PARALLEL SAFE
  RETURN coalesce(current_setting('app.dispatcher', true), '') = 'on';

CREATE FUNCTION app_bypass() RETURNS boolean
  LANGUAGE sql STABLE PARALLEL SAFE
  RETURN coalesce(current_setting('app.bypass_tenant', true), '') = 'on';

CREATE FUNCTION app_is_staff() RETURNS boolean
  LANGUAGE sql STABLE PARALLEL SAFE
  RETURN app_role() IN ('tenant_admin','manager','operation_staff','operator','user');

-- Contractor reach (R60): carrier tenants whose contractor_tenant_id is the active tenant (comma-separated).
CREATE FUNCTION app_subtenant_ids() RETURNS uuid[]
  LANGUAGE sql STABLE PARALLEL SAFE
  RETURN coalesce(string_to_array(nullif(current_setting('app.subtenant_ids', true), ''), ',')::uuid[], '{}'::uuid[]);

-- Steward (R60): staff role in the own-fleet tenant, or platform_admin.
CREATE FUNCTION app_is_steward() RETURNS boolean
  LANGUAGE sql STABLE PARALLEL SAFE
  RETURN coalesce(current_setting('app.steward', true), '') = 'on';

-- Set only by the three audited tenant-move paths (R13 task reassignment, driver move, quarantine re-home).
CREATE FUNCTION app_tenant_move_allowed() RETURNS boolean
  LANGUAGE sql STABLE PARALLEL SAFE
  RETURN coalesce(current_setting('app.tenant_move', true), '') = 'on';

-- Set only by cmd/etl.
CREATE FUNCTION app_etl_load() RETURNS boolean
  LANGUAGE sql STABLE PARALLEL SAFE
  RETURN coalesce(current_setting('app.etl_load', true), '') = 'on';

CREATE FUNCTION app_is_scope() RETURNS boolean
  LANGUAGE sql STABLE PARALLEL SAFE
  RETURN app_role() = 'customer' OR app_is_dispatcher();

CREATE FUNCTION app_is_authenticated() RETURNS boolean
  LANGUAGE sql STABLE PARALLEL SAFE
  RETURN app_user_id() IS NOT NULL OR app_bypass();

CREATE FUNCTION app_tenant_in_reach(t uuid) RETURNS boolean
  LANGUAGE sql STABLE PARALLEL SAFE
  RETURN t = app_tenant_id() OR t = ANY (app_subtenant_ids());

-- The quarantine tenant is structural: 0002 inserts its row with this id (R56).
CREATE FUNCTION app_quarantine_tenant_id() RETURNS uuid
  LANGUAGE sql IMMUTABLE PARALLEL SAFE
  RETURN '00000000-0000-7000-8000-00000000000f'::uuid;

-- +goose StatementBegin
CREATE FUNCTION trg_set_updated_at() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  NEW.updated_at := now();
  RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION trg_forbid_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION '% is append-only (% rejected)', TG_TABLE_NAME, TG_OP
    USING ERRCODE = 'integrity_constraint_violation';
END
$$;
-- +goose StatementEnd

-- Announcement rows (ADR 0009 §1; firestore.rules:142-154): the only legal UPDATE sets voided
-- false -> true plus the void columns; updated_at is whitelisted like firestore.rules:144.
-- +goose StatementBegin
CREATE FUNCTION trg_announcement_void_only() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  o jsonb;
  n jsonb;
BEGIN
  IF OLD.voided THEN
    RAISE EXCEPTION '% row % is already voided', TG_TABLE_NAME, OLD.id
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  IF NEW.voided IS DISTINCT FROM true OR NEW.voided_at IS NULL OR NEW.voided_by IS NULL THEN
    RAISE EXCEPTION '% rows are immutable; insert a new row or void this one', TG_TABLE_NAME
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  o := to_jsonb(OLD) - 'voided' - 'voided_at' - 'voided_by' - 'voided_reason' - 'updated_at';
  n := to_jsonb(NEW) - 'voided' - 'voided_at' - 'voided_by' - 'voided_reason' - 'updated_at';
  IF o IS DISTINCT FROM n THEN
    RAISE EXCEPTION '% void may not change other columns', TG_TABLE_NAME
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

-- Wake-up for the outbox relay (outbox_events is created in 0009).
-- +goose StatementBegin
CREATE FUNCTION trg_outbox_notify() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM pg_notify('outbox_new', NEW.id::text);
  RETURN NEW;
END
$$;
-- +goose StatementEnd

-- Down-only helper (R66). After 0009 hands a SECURITY DEFINER function to logitrack_rls_definer, the migrator
-- (member WITH INHERIT FALSE, SET TRUE) is not its owner; this drops it as the owner (transaction-local SET ROLE,
-- reset before returning). Migrator-owned functions are dropped directly, missing ones ignored.
-- +goose StatementBegin
CREATE PROCEDURE app_drop_definer_function(sig text) LANGUAGE plpgsql AS $$
DECLARE
  fn       regprocedure := to_regprocedure(sig);
  fn_owner regrole;
BEGIN
  IF fn IS NULL THEN
    RETURN;
  END IF;
  SELECT proowner::regrole INTO fn_owner FROM pg_proc WHERE oid = fn;
  IF fn_owner = 'logitrack_rls_definer'::regrole THEN
    EXECUTE 'SET LOCAL ROLE logitrack_rls_definer';
    EXECUTE format('DROP FUNCTION %s', fn);
    EXECUTE 'RESET ROLE';
  ELSE
    EXECUTE format('DROP FUNCTION %s', fn);
  END IF;
END
$$;
-- +goose StatementEnd

-- Appendix C block (same file): §C.3.6 trg_freeze_tenant_id(); §C.3.5 generators app_rls_tenant_table,
-- app_rls_child_table, app_rls_global_table, app_rls_platform_table, app_rls_driver_rows. Each table that uses a
-- generator names it in an explicit DO block of its own migration file (0002-0008).

-- tenant_id is frozen at write time (glossary `tenantId`); moves only through the explicit paths.
-- +goose StatementBegin
CREATE FUNCTION trg_freeze_tenant_id() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id AND NOT app_tenant_move_allowed() AND NOT app_etl_load() THEN
    RAISE EXCEPTION '%.tenant_id is frozen at write time', TG_TABLE_NAME USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd

-- Tenant table: platform bypass + quarantine invisibility (restrictive, AND-ed) + tenant staff reach + frozen tenant_id.
-- +goose StatementBegin
CREATE PROCEDURE app_rls_tenant_table(tbl regclass) LANGUAGE plpgsql AS $$
BEGIN
  EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY', tbl);
  EXECUTE format('ALTER TABLE %s FORCE ROW LEVEL SECURITY', tbl);
  EXECUTE format('CREATE POLICY p_bypass ON %s USING (app_bypass()) WITH CHECK (app_bypass())', tbl);
  EXECUTE format('CREATE POLICY p_not_quarantine ON %s AS RESTRICTIVE
                    USING (app_bypass() OR tenant_id IS DISTINCT FROM app_quarantine_tenant_id())
                    WITH CHECK (app_bypass() OR tenant_id IS DISTINCT FROM app_quarantine_tenant_id())', tbl);
  EXECUTE format('CREATE POLICY p_tenant_staff ON %s
                    USING (app_is_staff() AND app_tenant_in_reach(tenant_id))
                    WITH CHECK (app_is_staff() AND app_tenant_in_reach(tenant_id))', tbl);
  EXECUTE format('CREATE TRIGGER t_freeze_tenant_id BEFORE UPDATE OF tenant_id ON %s
                    FOR EACH ROW EXECUTE FUNCTION trg_freeze_tenant_id()', tbl);
END $$;
-- +goose StatementEnd

-- Child table without tenant_id: readable when the parent row is visible (the EXISTS runs under the parent's RLS);
-- writable only when the parent is in the writer's tenant reach and the writer is staff (or the owning driver when allowed).
-- +goose StatementBegin
CREATE PROCEDURE app_rls_child_table(tbl regclass, parent regclass, fk name, parent_key name DEFAULT 'id',
                                     read_staff_only boolean DEFAULT false, driver_writes boolean DEFAULT false)
LANGUAGE plpgsql AS $$
DECLARE vis text := format('EXISTS (SELECT 1 FROM %s p WHERE p.%I = %s.%I)', parent, parent_key, tbl, fk);
        wr  text := CASE WHEN driver_writes THEN 'OR app_role() = ''driver''' ELSE '' END;
BEGIN
  EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY', tbl);
  EXECUTE format('ALTER TABLE %s FORCE ROW LEVEL SECURITY', tbl);
  EXECUTE format('CREATE POLICY p_bypass ON %s USING (app_bypass()) WITH CHECK (app_bypass())', tbl);
  EXECUTE format('CREATE POLICY p_parent_read ON %s FOR SELECT USING (%s %s)', tbl,
                 CASE WHEN read_staff_only THEN 'app_is_staff() AND' ELSE '' END, vis);
  EXECUTE format('CREATE POLICY p_parent_write ON %s
                    USING (EXISTS (SELECT 1 FROM %s p WHERE p.%I = %s.%I AND app_tenant_in_reach(p.tenant_id)
                                   AND (app_is_staff() %s)))
                    WITH CHECK (EXISTS (SELECT 1 FROM %s p WHERE p.%I = %s.%I AND app_tenant_in_reach(p.tenant_id)
                                   AND (app_is_staff() %s)))',
                 tbl, parent, parent_key, tbl, fk, wr, parent, parent_key, tbl, fk, wr);
END $$;
-- +goose StatementEnd

-- Global master table (no tenancy stamp): any authenticated principal reads; stewards write (R60).
-- +goose StatementBegin
CREATE PROCEDURE app_rls_global_table(tbl regclass) LANGUAGE plpgsql AS $$
BEGIN
  EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY', tbl);
  EXECUTE format('ALTER TABLE %s FORCE ROW LEVEL SECURITY', tbl);
  EXECUTE format('CREATE POLICY p_bypass ON %s USING (app_bypass()) WITH CHECK (app_bypass())', tbl);
  EXECUTE format('CREATE POLICY p_read ON %s FOR SELECT USING (app_is_authenticated())', tbl);
  EXECUTE format('CREATE POLICY p_steward_write ON %s USING (app_is_steward()) WITH CHECK (app_is_steward())', tbl);
END $$;
-- +goose StatementEnd

-- Platform-only / nullable-tenant base: only WithSystem transactions, plus the policies added per table.
-- +goose StatementBegin
CREATE PROCEDURE app_rls_platform_table(tbl regclass) LANGUAGE plpgsql AS $$
BEGIN
  EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY', tbl);
  EXECUTE format('ALTER TABLE %s FORCE ROW LEVEL SECURITY', tbl);
  EXECUTE format('CREATE POLICY p_bypass ON %s USING (app_bypass()) WITH CHECK (app_bypass())', tbl);
END $$;
-- +goose StatementEnd

-- Driver self rows on a tenant table (added to G-tenant): p_driver_read always; p_driver_insert (own row in the
-- driver's current tenant) and p_driver_update (own row; tenant_id cannot change, t_freeze_tenant_id) on request.
-- +goose StatementBegin
CREATE PROCEDURE app_rls_driver_rows(tbl regclass, can_insert boolean, can_update boolean) LANGUAGE plpgsql AS $$
DECLARE own text := 'app_role() = ''driver'' AND driver_id = app_driver_id()';
BEGIN
  EXECUTE format('CREATE POLICY p_driver_read ON %s FOR SELECT USING (%s)', tbl, own);
  IF can_insert THEN
    EXECUTE format('CREATE POLICY p_driver_insert ON %s FOR INSERT WITH CHECK (%s AND tenant_id = app_tenant_id())', tbl, own);
  END IF;
  IF can_update THEN
    EXECUTE format('CREATE POLICY p_driver_update ON %s FOR UPDATE USING (%s) WITH CHECK (%s)', tbl, own, own);
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Appendix C additions first (IF EXISTS keeps this Down runnable with or without them).
DROP PROCEDURE IF EXISTS app_rls_driver_rows(regclass, boolean, boolean);
DROP PROCEDURE IF EXISTS app_rls_platform_table(regclass);
DROP PROCEDURE IF EXISTS app_rls_global_table(regclass);
DROP PROCEDURE IF EXISTS app_rls_child_table(regclass, regclass, name, name, boolean, boolean);
DROP PROCEDURE IF EXISTS app_rls_tenant_table(regclass);
DROP FUNCTION IF EXISTS trg_freeze_tenant_id();
DROP PROCEDURE app_drop_definer_function(text);
DROP FUNCTION trg_outbox_notify();
DROP FUNCTION trg_announcement_void_only();
DROP FUNCTION trg_forbid_mutation();
DROP FUNCTION trg_set_updated_at();
-- SQL-standard bodies record dependencies: drop the composed helpers before the readers they call.
DROP FUNCTION app_quarantine_tenant_id();
DROP FUNCTION app_tenant_in_reach(uuid);
DROP FUNCTION app_is_authenticated();
DROP FUNCTION app_is_scope();
DROP FUNCTION app_etl_load();
DROP FUNCTION app_tenant_move_allowed();
DROP FUNCTION app_is_steward();
DROP FUNCTION app_subtenant_ids();
DROP FUNCTION app_is_staff();
DROP FUNCTION app_bypass();
DROP FUNCTION app_is_dispatcher();
DROP FUNCTION app_customer_ids();
DROP FUNCTION app_driver_id();
DROP FUNCTION app_role();
DROP FUNCTION app_tenant_id();
DROP FUNCTION app_user_id();
DROP FUNCTION bkk_midnight(date);
DROP FUNCTION bkk_date(timestamptz);
DROP SCHEMA etl;
-- Not reverted on purpose: the citext extension (may serve other schemas; re-Up uses IF NOT EXISTS)
-- and the five roles (created by deploy/postgres-init/00-roles.sql, cluster-wide, never by a migration).
