-- 0003_master.sql: Appendix A §A.2.2 plus the Appendix C block of this file (end of the Up section).
-- Follows Appendix A until the P0 deploy applies the baseline to a database that is kept; after that it is
-- never edited and follow-ups take the next free number (as 0001_preamble.sql).
-- +goose Up

CREATE TABLE customers (                                   -- customers collection (global master)
  id                 uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id      text,
  code               citext NOT NULL,                      -- 'SPX', 'TTP', 'CJSF'; key of invoice numbers and driver customer codes
  name               text NOT NULL,
  description        text,
  logo_file_id       uuid REFERENCES file_objects(id),
  address            text,
  tax_id             text,
  branch_type        text CHECK (branch_type IN ('hq','branch')),   -- legacy 'สำนักงานใหญ่' -> hq, 'สาขา' -> branch
  branch_number      text,
  contact_name       text,
  contact_phone      text,
  billing_email      citext,
  payment_terms_days int  CHECK (payment_terms_days >= 0),          -- billing_statements.due_date
  invoice_note       text,
  line_group_id      text,
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX customers_code   ON customers (code);
CREATE UNIQUE INDEX customers_legacy ON customers (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
ALTER TABLE customers ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (global-read-steward-write)

CREATE TABLE customer_driver_id_types (                    -- customers.driverIdTypes[]
  customer_id uuid NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
  key         text NOT NULL,                               -- 'appId', 'workId', ...
  label       text NOT NULL,
  position    int  NOT NULL DEFAULT 0,
  PRIMARY KEY (customer_id, key)
);
ALTER TABLE customer_driver_id_types ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (global-read-steward-write)

-- Single FK target for everything billed to / linked with "a customer OR a carrier tenant" (A.1.3, R6).
CREATE TABLE billing_parties (
  id                 uuid PRIMARY KEY DEFAULT uuidv7(),
  kind               text NOT NULL CHECK (kind IN ('customer','tenant')),
  customer_id        uuid REFERENCES customers(id),
  tenant_id          uuid REFERENCES tenants(id),
  billing_date_basis text NOT NULL DEFAULT 'delivered' CHECK (billing_date_basis IN ('delivered','plan')),   -- ADR 0027
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  CHECK ((kind = 'customer' AND customer_id IS NOT NULL AND tenant_id IS NULL)
      OR (kind = 'tenant'   AND tenant_id   IS NOT NULL AND customer_id IS NULL))
);
CREATE UNIQUE INDEX billing_parties_customer ON billing_parties (customer_id) WHERE customer_id IS NOT NULL;
CREATE UNIQUE INDEX billing_parties_tenant   ON billing_parties (tenant_id)   WHERE tenant_id   IS NOT NULL;
-- billing_parties.tenant_id (kind 'tenant') is a reference to a billed carrier, not a tenancy stamp.
ALTER TABLE billing_parties ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (global-read-steward-write)

CREATE TABLE companies (                                   -- invoice issuer profiles (companies collection)
  id                   uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id        text,
  tenant_id            uuid NOT NULL REFERENCES tenants(id),
  tenant_source        text NOT NULL CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  name_th              text NOT NULL,
  name_en              text,
  short_name           text,                               -- e.g. WRT
  tax_id               text NOT NULL,
  branch_type          text NOT NULL CHECK (branch_type IN ('headquarters','branch')),
  branch_number        text,                               -- 5 digits when branch
  address              text NOT NULL,
  phone                text,
  email                citext,
  logo_file_id         uuid REFERENCES file_objects(id),
  stamp_file_id        uuid REFERENCES file_objects(id),
  signature_file_id    uuid REFERENCES file_objects(id),
  signatory_name       text,
  bank_name            text,
  account_number       text,
  account_name         text,
  withholding_tax_rate numeric(5,2) NOT NULL CHECK (withholding_tax_rate BETWEEN 0 AND 100),   -- percent; no default (main spec §19.2 question 6)
  company_type         text NOT NULL CHECK (company_type IN ('owner','subcontractor')),
  is_active            boolean NOT NULL DEFAULT true,
  max_trucks           int  CHECK (max_trucks > 0),
  max_drivers          int  CHECK (max_drivers > 0),
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX companies_legacy    ON companies (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
-- UNVERIFIED business rule (indexes report: "one owner company" is implied by /app/companies, not stated anywhere).
CREATE UNIQUE INDEX companies_owner_one ON companies (company_type) WHERE company_type = 'owner';
CREATE INDEX companies_type_name        ON companies (company_type, name_th);
CREATE INDEX companies_tenant           ON companies (tenant_id);
ALTER TABLE companies ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant)

-- Customer scope and dispatcher axis of a user (R86: these two kinds only; the legacy companyId claim is never
-- written, so there is no company scope). Placed here (not in 0002) because it references billing_parties;
-- FKs are inline (R6: user_scopes references billing_parties.id).
CREATE TABLE user_scopes (
  id                uuid PRIMARY KEY DEFAULT uuidv7(),
  user_id           uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind              text NOT NULL CHECK (kind IN ('customer','dispatcher')),
  billing_party_id  uuid NOT NULL REFERENCES billing_parties(id),   -- value carried in JWT `cs`
  created_by        uuid REFERENCES users(id),
  created_at        timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT user_scopes_key UNIQUE (user_id, kind, billing_party_id)
);
CREATE INDEX user_scopes_party ON user_scopes (billing_party_id);
ALTER TABLE user_scopes ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (platform-only)

CREATE TABLE hubs (                                        -- hubs collection (pickup points, SOCs); global master
  id                 uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id      text,
  source_id          citext NOT NULL,                      -- business code (ALANG-A, SOCN, SPK890103 ...); the FK used everywhere
  name_th            text NOT NULL,                        -- source_name_th (required since 2026-06-13)
  name_en            text,                                 -- source_name_en (billing label)
  latitude           double precision,
  longitude          double precision,
  station_type       text NOT NULL DEFAULT 'HUB' CHECK (station_type IN ('HUB','SOC')),
  network            text NOT NULL CHECK (network IN ('SPX','SPK')),   -- hubDistanceNetworkGroup, computed at save
  linked_party_id    uuid REFERENCES billing_parties(id),  -- linkedCustomerId + customerLinkKind
  created_by_driver  boolean NOT NULL DEFAULT false,       -- mobile-created hubs (capability mobile:create_hub)
  created_by         uuid REFERENCES users(id),
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX hubs_source_id ON hubs (source_id);
CREATE UNIQUE INDEX hubs_legacy    ON hubs (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX hubs_station_type     ON hubs (station_type, network);
CREATE INDEX hubs_linked_party     ON hubs (linked_party_id) WHERE linked_party_id IS NOT NULL;
ALTER TABLE hubs ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (global-read-steward-write + driver hub insert)

-- nameToCode as data (buildHubMaps, logitrack-web/functions/src/tripBillingOnDelivered.ts:124-141).
-- Direction name -> code ONLY; codeToName is hubs.name_th. The two maps are never merged
-- (shared-docs/.vibe-rules.md Confirmed Patterns, as of commit 4f552099).
CREATE TABLE hub_name_aliases (
  alias      citext PRIMARY KEY,                           -- first writer wins (== `!nameToCode.has(alias)`)
  hub_id     uuid NOT NULL REFERENCES hubs(id) ON DELETE CASCADE,
  source     text NOT NULL CHECK (source IN ('name_th','name_en','legacy_hubName','manual')),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX hub_name_aliases_hub ON hub_name_aliases (hub_id);
ALTER TABLE hub_name_aliases ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (global-read-steward-write + driver alias insert)

CREATE TABLE hub_soc_distances (                           -- hub_soc_distances + soc_hub_distances in one table (R14)
  id              uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id   text,                                    -- `${hub}_${soc}` or `${soc}_${hub}`
  hub_id          uuid NOT NULL REFERENCES hubs(id),
  soc_key         text NOT NULL,                           -- normalizeSocIdToKey (SOCE/SOCN/SOCW or upper source_id)
  direction       text NOT NULL CHECK (direction IN ('hub_to_soc','soc_to_hub')),
  network         text CHECK (network IN ('SPX','SPK')),
  distance_m      int  NOT NULL CHECK (distance_m >= 0),
  distance_km     numeric(9,2) NOT NULL,
  duration_s      int  NOT NULL CHECK (duration_s >= 0),
  duration_min    numeric(9,2) NOT NULL,
  hub_lat         double precision NOT NULL,
  hub_lng         double precision NOT NULL,
  soc_lat         double precision NOT NULL,
  soc_lng         double precision NOT NULL,
  created_by      uuid REFERENCES users(id),
  updated_by      uuid REFERENCES users(id),
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (hub_id, soc_key, direction)
);
CREATE UNIQUE INDEX hub_soc_distances_legacy ON hub_soc_distances (legacy_doc_id, direction) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX hub_soc_distances_soc           ON hub_soc_distances (soc_key, direction);
ALTER TABLE hub_soc_distances ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (global-read-steward-write)

CREATE TABLE drivers (
  id                     uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id          text,                             -- nullable: drivers created in Go have none (R12)
  legacy_auth_uid        text,                             -- drivers.authId / legacy authUid
  tenant_id              uuid NOT NULL REFERENCES tenants(id),
  tenant_source          text NOT NULL CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  user_id                uuid REFERENCES users(id),        -- login link; at most one driver per user
  first_name             text NOT NULL,
  last_name              text NOT NULL,
  full_name_th           text,                             -- whitespace-only legacy values load as NULL
  mobile                 text NOT NULL,
  email                  citext,
  profile_file_id        uuid REFERENCES file_objects(id),
  birth_date             date,
  id_card                text,                             -- 13 digits (PII; readable with drivers:view_pii only)
  id_card_expired_date   date,
  id_card_file_id        uuid REFERENCES file_objects(id),
  truck_license_id       text,
  license_type           text CHECK (license_type IN ('บ.1','บ.2','บ.3','บ.4','ท.1','ท.2','ท.3','ท.4')),
  license_expired_date   date,
  license_file_id        uuid REFERENCES file_objects(id),
  employment_type        text NOT NULL DEFAULT 'full_time' CHECK (employment_type IN ('full_time','subcontractor','part_time')),
  contract_years         numeric(5,2),
  employment_start       date,
  employment_end         date,
  hire_date              date,                             -- SSO base derivation (payroll)
  probation_passed       boolean NOT NULL DEFAULT false,
  status                 text NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive','on_duty')),
  assign_to_project      text,
  current_assignment_id  uuid,                             -- home truck (FK after truck_assignments)
  active_truck_id        uuid,                             -- activeTruck{}: truck responsible right now; set at check-in, cleared at trip end
  active_truck_plate     text,
  active_task_id         uuid,                             -- FK added in 0004 (tasks)
  active_started_at      timestamptz,
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX drivers_user       ON drivers (user_id)         WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX drivers_legacy     ON drivers (legacy_doc_id)   WHERE legacy_doc_id IS NOT NULL;
CREATE UNIQUE INDEX drivers_legacy_uid ON drivers (legacy_auth_uid) WHERE legacy_auth_uid IS NOT NULL;
CREATE INDEX drivers_tenant_status     ON drivers (tenant_id, status);
CREATE INDEX drivers_tenant_created    ON drivers (tenant_id, created_at DESC, id DESC);
CREATE INDEX drivers_active_truck      ON drivers (active_truck_id) WHERE active_truck_id IS NOT NULL;
ALTER TABLE drivers ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant + driver self + scope read)

CREATE TABLE driver_customer_codes (                       -- drivers.customerDriverIds{customerCode:{idTypeKey:value}}
  driver_id    uuid NOT NULL REFERENCES drivers(id) ON DELETE CASCADE,
  customer_id  uuid NOT NULL REFERENCES customers(id),
  id_type_key  text NOT NULL,
  value        text NOT NULL,
  PRIMARY KEY (driver_id, customer_id, id_type_key)
);
ALTER TABLE driver_customer_codes ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (parent-scoped via drivers)

CREATE TABLE trucks (
  id                       uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id            text,
  tenant_id                uuid NOT NULL REFERENCES tenants(id),
  tenant_source            text NOT NULL CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  ownership_type           text NOT NULL DEFAULT 'own' CHECK (ownership_type IN ('own','subcontractor')),
  license_plate            text NOT NULL,
  province                 text NOT NULL,
  vin                      text,
  engine_number            text,
  gps_vehicle_id           text,                           -- Cartrack vehicle_id (GPSVehicleId)
  status                   text NOT NULL CHECK (status IN ('active','inactive','maintenance','insurance_claim','sold')),
  legacy_status            text,                           -- raw value when outside the enum ('Available', 'Active')
  brand                    text NOT NULL,
  model                    text NOT NULL,
  year                     smallint NOT NULL,
  color                    text NOT NULL,
  type_raw                 text NOT NULL,                  -- master full word, e.g. '6 Wheels'
  vehicle_class            text CHECK (vehicle_class IN ('4W','4WJ','6WH','10WH','18WH','VAN')),   -- logitrack-web/lib/truckType.ts map; NULL = unknown, never guessed
  seats                    smallint,
  fuel_type                text,
  engine_capacity          numeric(10,2),
  fuel_capacity            numeric(10,2),
  max_load_weight_kg       numeric(12,2),
  registration_date        date,
  buying_date              date,
  notes                    text,
  tax_expiry_date          date,
  tax_expense_thb          numeric(14,2),
  tax_responsible          text NOT NULL DEFAULT 'Operation Admin',
  tax_renewal_status       text CHECK (tax_renewal_status IN ('pending','in_progress','completed')),
  maintenance_responsible  text NOT NULL DEFAULT 'Driver',
  last_service_date        date,
  next_service_date        date,
  next_service_mileage     int,
  pm_interval_km           int  CHECK (pm_interval_km BETWEEN 1 AND 500000),
  current_mileage          int,
  last_alert_mileage       int,
  active_maintenance_id    uuid,                           -- FK trucks_active_maintenance_fk added in 0006 (maintenance_records)
  insurance_policy_id      text,
  insurance_policy_number  text,
  insurance_company        text,
  insurance_type           text CHECK (insurance_type IN ('1','2','2+','3','3+')),
  insurance_start_date     date,
  insurance_expiry_date    date,
  insurance_premium_thb    numeric(14,2),
  insurance_notes          text,
  insurance_renewal_status text CHECK (insurance_renewal_status IN ('pending','in_progress','completed')),
  payment_method           text CHECK (payment_method IN ('cash','transfer','company_credit')),
  created_by               uuid REFERENCES users(id),
  updated_by               uuid REFERENCES users(id),
  created_at               timestamptz NOT NULL DEFAULT now(),
  updated_at               timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX trucks_plate_per_tenant ON trucks (tenant_id, license_plate);   -- D4 (question 15): per tenant
CREATE UNIQUE INDEX trucks_legacy           ON trucks (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE UNIQUE INDEX trucks_gps              ON trucks (gps_vehicle_id) WHERE gps_vehicle_id IS NOT NULL AND gps_vehicle_id <> '';
CREATE INDEX trucks_tenant_status           ON trucks (tenant_id, ownership_type, status);
CREATE INDEX trucks_tenant_created          ON trucks (tenant_id, created_at DESC, id DESC);
CREATE INDEX trucks_updated                 ON trucks (updated_at DESC);
CREATE INDEX trucks_renewals                ON trucks (tax_expiry_date, insurance_expiry_date);
ALTER TABLE trucks ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant + driver read + scope read)

CREATE TABLE truck_files (                                 -- image*, documentTax/Register, receipts, insuranceDocuments[], legacy images[]
  truck_id  uuid NOT NULL REFERENCES trucks(id) ON DELETE CASCADE,
  file_id   uuid NOT NULL REFERENCES file_objects(id),
  kind      text NOT NULL CHECK (kind IN ('image_front_right','image_front_left','image_back_right','image_back_left',
              'document_tax','document_register','tax_receipt','insurance_receipt','insurance_document','legacy_image','other')),
  position  int  NOT NULL DEFAULT 0,
  PRIMARY KEY (truck_id, file_id)
);
ALTER TABLE truck_files ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (parent-scoped via trucks)

CREATE TABLE truck_assignments (                           -- truckAssignment collection; trucks.currentAssignments[] is derived from status='active' (R14)
  id             uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id  text,
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  tenant_source  text NOT NULL CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  truck_id       uuid NOT NULL REFERENCES trucks(id),
  driver_id      uuid NOT NULL REFERENCES drivers(id),     -- legacy value is the drivers doc id
  status         text NOT NULL CHECK (status IN ('active','revoked','cancelled')),
  admin_user_id  uuid REFERENCES users(id),
  admin_label    text NOT NULL DEFAULT 'System',           -- legacy adminName
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  revoked_at     timestamptz
);
CREATE UNIQUE INDEX truck_assignments_legacy ON truck_assignments (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX truck_assignments_active  ON truck_assignments (tenant_id, created_at DESC, id DESC) WHERE status = 'active';
CREATE INDEX truck_assignments_tenant  ON truck_assignments (tenant_id, created_at DESC, id DESC);
CREATE INDEX truck_assignments_truck   ON truck_assignments (truck_id, created_at DESC);
CREATE INDEX truck_assignments_driver  ON truck_assignments (driver_id, created_at DESC);
ALTER TABLE truck_assignments ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant + driver read)

ALTER TABLE drivers ADD CONSTRAINT drivers_current_assignment_fk
  FOREIGN KEY (current_assignment_id) REFERENCES truck_assignments(id) ON DELETE SET NULL;
ALTER TABLE drivers ADD CONSTRAINT drivers_active_truck_fk
  FOREIGN KEY (active_truck_id) REFERENCES trucks(id) ON DELETE SET NULL;

CREATE TRIGGER customers_set_updated_at         BEFORE UPDATE ON customers         FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER billing_parties_set_updated_at   BEFORE UPDATE ON billing_parties   FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER companies_set_updated_at         BEFORE UPDATE ON companies         FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER hubs_set_updated_at              BEFORE UPDATE ON hubs              FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER hub_soc_distances_set_updated_at BEFORE UPDATE ON hub_soc_distances FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER drivers_set_updated_at           BEFORE UPDATE ON drivers           FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER trucks_set_updated_at            BEFORE UPDATE ON trucks            FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER truck_assignments_set_updated_at BEFORE UPDATE ON truck_assignments FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

-- Driver peer directory (Appendix C §C.3.7 describes it; body printed here): helper picker of a driver principal
-- (GET /v1/mobile/drivers). Drivers never read other drivers' rows; this returns four columns of the non-inactive
-- drivers of the caller's active tenant only. SECURITY DEFINER, handed to logitrack_rls_definer by 0009.
-- +goose StatementBegin
CREATE FUNCTION driver_directory() RETURNS TABLE (id uuid, full_name_th text, first_name text, last_name text)
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
  SELECT d.id, d.full_name_th, d.first_name, d.last_name
    FROM drivers d
   WHERE d.tenant_id = app_tenant_id() AND d.tenant_id <> app_quarantine_tenant_id() AND d.status <> 'inactive'
   ORDER BY d.full_name_th NULLS LAST, d.first_name, d.id
$$;
-- +goose StatementEnd

-- Appendix C block (same file): §C.3.3 app_task_stops_in_scope, app_task_in_scope, app_trip_in_scope,
-- app_recent_work_in_scope; §C.3.5 "0003_master" (DO block + policies of the 13 tables above); §C.3.6
-- trg_driver_self_columns() + t_driver_self_columns, trg_driver_link_membership() + t_driver_link_membership;
-- §C.3.7 scope_drivers, scope_trucks, scope_tenants.

-- §C.3.3 scope helpers: SECURITY DEFINER (handed to logitrack_rls_definer by 0009, BYPASSRLS), so a policy on one
-- table can consult another without the two tables' policies referencing each other; they exclude the quarantine
-- tenant themselves. PL/pgSQL binds tasks, task_delivery_stops and trip_records (0004) at first call.
-- +goose StatementBegin
CREATE FUNCTION app_task_stops_in_scope(p_task_id uuid) RETURNS boolean
  LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  RETURN EXISTS (SELECT 1 FROM task_delivery_stops s
                  WHERE s.task_id = p_task_id AND s.destination_linked_party_id = ANY (app_customer_ids()));
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION app_task_in_scope(p_task_id uuid) RETURNS boolean
  LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  RETURN cardinality(app_customer_ids()) > 0 AND EXISTS (
    SELECT 1 FROM tasks t
     WHERE t.id = p_task_id AND t.tenant_id <> app_quarantine_tenant_id()
       AND (ARRAY[t.billing_party_id, t.source_linked_party_id, t.destination_linked_party_id] && app_customer_ids()
            OR app_task_stops_in_scope(t.id)));
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION app_trip_in_scope(p_trip_id uuid) RETURNS boolean
  LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  RETURN cardinality(app_customer_ids()) > 0 AND EXISTS (
    SELECT 1 FROM trip_records r
     WHERE r.id = p_trip_id AND r.tenant_id <> app_quarantine_tenant_id()
       AND (r.billing_party_id = ANY (app_customer_ids())
            OR (r.task_id IS NOT NULL AND app_task_in_scope(r.task_id))));
END $$;
-- +goose StatementEnd

-- drivers / trucks are visible to scope principals only through recent in-scope work (privacy: 180 days);
-- pass the driver id or the truck id, the other argument NULL
-- +goose StatementBegin
CREATE FUNCTION app_recent_work_in_scope(p_driver_id uuid, p_truck_id uuid) RETURNS boolean
  LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  RETURN cardinality(app_customer_ids()) > 0 AND EXISTS (
    SELECT 1 FROM tasks t
     WHERE (t.driver_id = p_driver_id OR t.helper_driver_id = p_driver_id OR t.truck_id = p_truck_id)
       AND t.plan_at > now() - interval '180 days' AND t.tenant_id <> app_quarantine_tenant_id()
       AND (ARRAY[t.billing_party_id, t.source_linked_party_id, t.destination_linked_party_id] && app_customer_ids()
            OR app_task_stops_in_scope(t.id)));
END $$;
-- +goose StatementEnd

-- §C.3.5 "0003_master"
-- +goose StatementBegin
DO $$ BEGIN
  CALL app_rls_global_table('customers');   CALL app_rls_global_table('customer_driver_id_types');
  CALL app_rls_global_table('billing_parties'); CALL app_rls_global_table('hubs');
  CALL app_rls_global_table('hub_name_aliases'); CALL app_rls_global_table('hub_soc_distances');
  CALL app_rls_tenant_table('companies');   CALL app_rls_tenant_table('drivers');
  CALL app_rls_tenant_table('trucks');      CALL app_rls_tenant_table('truck_assignments');
  CALL app_rls_platform_table('user_scopes');
  CALL app_rls_child_table('driver_customer_codes', 'drivers', 'driver_id', read_staff_only => true);
  CALL app_rls_child_table('truck_files', 'trucks', 'truck_id', read_staff_only => true);
  CALL app_rls_driver_rows('truck_assignments', false, false);
END $$;
-- +goose StatementEnd

CREATE POLICY p_self_read ON user_scopes FOR SELECT USING (user_id = app_user_id());
CREATE POLICY p_driver_hub_insert ON hubs FOR INSERT
  WITH CHECK (app_role() = 'driver' AND created_by_driver);                         -- mobile:create_hub
CREATE POLICY p_driver_hub_alias_insert ON hub_name_aliases FOR INSERT
  WITH CHECK (app_role() = 'driver' AND EXISTS (SELECT 1 FROM hubs h WHERE h.id = hub_id AND h.created_by_driver));

CREATE POLICY p_self_read   ON drivers FOR SELECT USING (app_role() = 'driver' AND id = app_driver_id());
CREATE POLICY p_self_update ON drivers FOR UPDATE                                    -- active_* only (trigger C.3.6)
  USING (app_role() = 'driver' AND id = app_driver_id()) WITH CHECK (app_role() = 'driver' AND id = app_driver_id());
CREATE POLICY p_scope_read  ON drivers FOR SELECT USING (app_is_scope() AND app_recent_work_in_scope(id, NULL));

CREATE POLICY p_driver_read ON trucks FOR SELECT                                     -- own company/partner trucks only
  USING (app_role() = 'driver' AND tenant_id = app_tenant_id());                     -- (.vibe-rules.md "Vehicle identity")
CREATE POLICY p_scope_read  ON trucks FOR SELECT USING (app_is_scope() AND app_recent_work_in_scope(NULL, id));

-- §C.3.6: a driver may change only its live-truck columns (check-in / end of job)
-- +goose StatementBegin
CREATE FUNCTION trg_driver_self_columns() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE allowed text[] := ARRAY['active_truck_id','active_truck_plate','active_task_id','active_started_at','updated_at'];
BEGIN
  IF app_role() = 'driver' AND NOT app_bypass() AND (to_jsonb(NEW) - allowed) IS DISTINCT FROM (to_jsonb(OLD) - allowed) THEN
    RAISE EXCEPTION 'drivers: a driver may only change active_* columns' USING ERRCODE = 'insufficient_privilege';
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER t_driver_self_columns BEFORE UPDATE ON drivers FOR EACH ROW EXECUTE FUNCTION trg_driver_self_columns();

-- §C.3.6: a linked driver row requires a driver membership in the driver's tenant (checked at COMMIT, so the link
-- and the membership can be written in either order inside one transaction). SECURITY DEFINER (handed over by 0009):
-- memberships rows of other users are not visible to the writer under RLS.
-- +goose StatementBegin
CREATE FUNCTION trg_driver_link_membership() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER
  SET search_path = pg_catalog, public AS $$
BEGIN
  IF NEW.user_id IS NOT NULL AND NOT app_etl_load() AND NOT EXISTS (
       SELECT 1 FROM memberships m WHERE m.user_id = NEW.user_id AND m.tenant_id = NEW.tenant_id
                                     AND m.role = 'driver' AND m.status = 'active') THEN
    RAISE EXCEPTION 'driver % is linked to user % without an active driver membership in tenant %',
      NEW.id, NEW.user_id, NEW.tenant_id USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER t_driver_link_membership AFTER INSERT OR UPDATE OF user_id, tenant_id ON drivers
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION trg_driver_link_membership();

-- §C.3.7: column projections for dispatcher and customer-scope principals (security_invoker: the caller's RLS applies).
CREATE VIEW scope_drivers WITH (security_invoker = true) AS
  SELECT id, tenant_id, full_name_th, first_name, last_name, mobile, status, active_truck_id FROM drivers;
                                       -- hidden: id_card*, licence*, birth_date, employment*, documents
CREATE VIEW scope_trucks WITH (security_invoker = true) AS
  SELECT id, tenant_id, license_plate, province, vehicle_class, type_raw, status FROM trucks;
                                       -- hidden: insurance*, tax*, cost fields, documents
CREATE VIEW scope_tenants WITH (security_invoker = true) AS
  SELECT id, kind, code, name_th, name_en FROM tenants;

-- +goose Down
-- Appendix C objects first where they block a table drop (IF EXISTS: runnable with or without them).
DROP VIEW IF EXISTS scope_tenants;                         -- reads tenants (0002)
DROP VIEW IF EXISTS scope_trucks;
DROP VIEW IF EXISTS scope_drivers;
CALL app_drop_definer_function('driver_directory()');
ALTER TABLE drivers DROP CONSTRAINT drivers_active_truck_fk;
ALTER TABLE drivers DROP CONSTRAINT drivers_current_assignment_fk;
DROP TABLE truck_assignments;
DROP TABLE truck_files;
DROP TABLE trucks;
DROP TABLE driver_customer_codes;
DROP TABLE drivers;
DROP TABLE hub_soc_distances;
DROP TABLE hub_name_aliases;
DROP TABLE hubs;
DROP TABLE user_scopes;
DROP TABLE companies;
DROP TABLE billing_parties;
DROP TABLE customer_driver_id_types;
DROP TABLE customers;
-- Functions after the tables whose policies and triggers use them.
DROP FUNCTION IF EXISTS trg_driver_self_columns();
CALL app_drop_definer_function('trg_driver_link_membership()');
CALL app_drop_definer_function('app_recent_work_in_scope(uuid,uuid)');
CALL app_drop_definer_function('app_trip_in_scope(uuid)');
CALL app_drop_definer_function('app_task_in_scope(uuid)');
CALL app_drop_definer_function('app_task_stops_in_scope(uuid)');
