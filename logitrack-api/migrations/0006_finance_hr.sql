-- 0006_finance_hr.sql: Appendix A §A.2.5 plus the Appendix C block of this file (end of the Up section).
-- Follows Appendix A until the P0 deploy applies the baseline to a database that is kept; after that it is
-- never edited and follow-ups take the next free number (as 0001_preamble.sql).
-- Depends on: 0001 (bkk_date, trg_set_updated_at, trg_forbid_mutation, app_* GUC readers, app_rls_* generators),
--             0002 (tenants, users, file_objects), 0003 (drivers, trucks, truck_assignments; trucks.active_maintenance_id).
-- Ids: uuid DEFAULT uuidv7() (PostgreSQL 18 native, R57). Money: NUMERIC(14,2) (R20).
-- tenant_source: inline CHECK, vocabulary task|trip|driver|truck|self|form|quarantine (R11, R58).
-- No GRANT/REVOKE here: 0009_infra is the single grant site (R66). RLS: all eleven tables (Appendix C §C.3.0).

-- +goose Up

CREATE TABLE vehicle_expenses (                              -- fuel + other expenses (mobile, admin, toll import)
  id                           uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id                text,
  tenant_id                    uuid NOT NULL REFERENCES tenants(id),
  tenant_source                text NOT NULL
                               CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  driver_id                    uuid REFERENCES drivers(id),
  legacy_driver_ref            text,                           -- raw legacy driverId (auth uid or doc id), R12
  driver_ref_match             text CHECK (driver_ref_match IN ('doc_id','auth_uid','none')),
  truck_id                     uuid REFERENCES trucks(id),
  truck_license_plate_snapshot text,                           -- display string, never normalised
  expense_type                 text NOT NULL CHECK (expense_type IN ('fuel','other')),
  category                     text CHECK (category IN ('tire_repair','maintenance','toll','parking','other')),
  expense_at                   timestamptz NOT NULL,           -- legacy `date` (Timestamp)
  expense_date                 date GENERATED ALWAYS AS (bkk_date(expense_at)) STORED,
  amount_thb                   numeric(14,2) NOT NULL,
  status                       text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
  volume_liters                numeric(10,3),
  price_per_liter              numeric(14,2),
  odometer_km                  int CHECK (odometer_km >= 0),
  station_tax_id               text,                           -- legacy alias gasStation
  tax_inv_id                   text,
  refill_lat                   double precision,
  refill_lng                   double precision,
  receipt_file_id              uuid REFERENCES file_objects(id),
  odometer_file_id             uuid REFERENCES file_objects(id),
  note                         text,
  description                  text,
  admin_note                   text,
  distance_km                  numeric(9,2),
  toll_import_sequence         int,
  toll_location                text,
  toll_lane                    text,
  toll_source_type             text,
  client_op_id                 uuid,                           -- offline outbox op id, body `clientOpId` (R63)
  created_by_user_id           uuid REFERENCES users(id),
  created_at                   timestamptz NOT NULL DEFAULT now(),   -- ETL supplies the legacy value
  updated_at                   timestamptz NOT NULL DEFAULT now(),
  CHECK (driver_id IS NOT NULL OR truck_id IS NOT NULL OR tenant_source = 'quarantine')
);
CREATE UNIQUE INDEX vehicle_expenses_legacy      ON vehicle_expenses (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE UNIQUE INDEX vehicle_expenses_client_op   ON vehicle_expenses (driver_id, client_op_id)
  WHERE client_op_id IS NOT NULL;                             -- durable offline replay key (R63)
CREATE INDEX vehicle_expenses_driver_date        ON vehicle_expenses (driver_id, expense_at DESC);
CREATE INDEX vehicle_expenses_type_date          ON vehicle_expenses (tenant_id, expense_type, expense_at DESC, id DESC);
CREATE INDEX vehicle_expenses_status_date        ON vehicle_expenses (tenant_id, status, expense_at DESC, id DESC);
CREATE INDEX vehicle_expenses_pending            ON vehicle_expenses (tenant_id) WHERE status = 'pending';
CREATE INDEX vehicle_expenses_truck_date         ON vehicle_expenses (truck_id, expense_at DESC);
CREATE INDEX vehicle_expenses_fuel_taxinv_lookup ON vehicle_expenses (driver_id, tax_inv_id)
  WHERE expense_type = 'fuel' AND tax_inv_id IS NOT NULL;     -- UNIQUE version is created by 0010 (D5, R31, R59)
CREATE TRIGGER vehicle_expenses_updated_at BEFORE UPDATE ON vehicle_expenses
  FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

CREATE TABLE maintenance_records (                           -- collection `maintenance`
  id                           uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id                text,
  tenant_id                    uuid NOT NULL REFERENCES tenants(id),
  tenant_source                text NOT NULL DEFAULT 'truck'
                               CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  truck_id                     uuid REFERENCES trucks(id),
  truck_license_plate_snapshot text,
  truck_brand_snapshot         text,
  maintenance_type             text NOT NULL CHECK (maintenance_type IN ('PM','CM')),   -- domain codes, kept upper
  service_type                 text NOT NULL,
  start_date                   date NOT NULL,
  end_date                     date,
  status                       text NOT NULL
                               CHECK (status IN ('pm_booking','scheduled','in_progress','completed','cancelled')),
  appointment_time             text CHECK (appointment_time ~ '^[0-9]{2}:[0-9]{2}$'),
  pickup_appointment_at        timestamptz,
  cost_labor_thb               numeric(14,2),
  cost_parts_thb               numeric(14,2),
  total_cost_thb               numeric(14,2),
  invoice_amount_thb           numeric(14,2),                  -- driver receipt total (maintenanceDisplayCost fallback)
  provider                     text,
  provider_lat                 double precision,
  provider_lng                 double precision,
  payment_method               text CHECK (payment_method IN ('cash','credit_card','billing','transfer','insurance_claim')),
  current_mileage              int,
  next_service_mileage         int,
  driver_submitted             boolean NOT NULL DEFAULT false,
  check_in_at                  timestamptz,
  check_out_at                 timestamptz,
  notes                        text,
  created_by                   uuid REFERENCES users(id),
  updated_by                   uuid REFERENCES users(id),
  created_at                   timestamptz NOT NULL DEFAULT now(),
  updated_at                   timestamptz NOT NULL DEFAULT now(),
  CHECK (truck_id IS NOT NULL OR tenant_source = 'quarantine')
);
CREATE UNIQUE INDEX maintenance_records_legacy ON maintenance_records (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX maintenance_truck_created         ON maintenance_records (truck_id, created_at DESC);
CREATE INDEX maintenance_truck_status          ON maintenance_records (truck_id, status);
CREATE INDEX maintenance_tenant_created        ON maintenance_records (tenant_id, created_at DESC, id DESC);
CREATE INDEX maintenance_open                  ON maintenance_records (tenant_id, status)
  WHERE status IN ('pm_booking','scheduled','in_progress');
CREATE TRIGGER maintenance_records_updated_at BEFORE UPDATE ON maintenance_records
  FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
ALTER TABLE trucks ADD CONSTRAINT trucks_active_maintenance_fk
  FOREIGN KEY (active_maintenance_id) REFERENCES maintenance_records(id) ON DELETE SET NULL;

CREATE TABLE maintenance_files (                             -- images[], receipts[], invoiceUrl
  maintenance_id uuid NOT NULL REFERENCES maintenance_records(id) ON DELETE CASCADE,
  file_id        uuid NOT NULL REFERENCES file_objects(id),
  kind           text NOT NULL CHECK (kind IN ('image','receipt','invoice')),
  position       int  NOT NULL DEFAULT 0,
  PRIMARY KEY (maintenance_id, file_id)
);

CREATE TABLE transactions (                                  -- append-only ledger; two legacy shapes (renewal, payout)
  id                   uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id        text,
  tenant_id            uuid NOT NULL REFERENCES tenants(id),
  tenant_source        text NOT NULL
                       CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  tx_type              text NOT NULL CHECK (tx_type IN ('tax','insurance','driver_payout')),
  sub_type             text,
  amount_thb           numeric(14,2) NOT NULL,
  payment_method       text,
  tx_date              date NOT NULL,
  tx_date_source       text NOT NULL DEFAULT 'bangkok' CHECK (tx_date_source IN ('bangkok','legacy_utc')),
  truck_id             uuid REFERENCES trucks(id),
  driver_id            uuid REFERENCES drivers(id),
  legacy_driver_ref    text,
  driver_ref_match     text CHECK (driver_ref_match IN ('doc_id','auth_uid','none')),
  payroll_run_id       uuid,                                   -- FK added after payroll_runs (deferrable, circular)
  pay_period           text CHECK (pay_period ~ '^[0-9]{4}-[0-9]{2}$'),
  pay_round            text CHECK (pay_round IN ('R1','R2')),
  receipt_file_id      uuid REFERENCES file_objects(id),
  performed_by_user_id uuid REFERENCES users(id),
  performed_by_label   text,                                   -- legacy displayName/email string
  notes                text,
  created_at           timestamptz NOT NULL DEFAULT now(),
  CHECK (tx_type <> 'driver_payout' OR (pay_period IS NOT NULL AND pay_round IS NOT NULL))
);
CREATE UNIQUE INDEX transactions_legacy ON transactions (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX transactions_tenant_date   ON transactions (tenant_id, tx_date DESC, id DESC);
CREATE INDEX transactions_truck         ON transactions (truck_id, tx_date DESC) WHERE truck_id IS NOT NULL;
CREATE INDEX transactions_payroll       ON transactions (payroll_run_id) WHERE payroll_run_id IS NOT NULL;
CREATE TRIGGER transactions_immutable BEFORE UPDATE OR DELETE ON transactions
  FOR EACH ROW EXECUTE FUNCTION trg_forbid_mutation();

CREATE TABLE driver_compensation_configs (                   -- append-only versions; latest effective_from <= date wins
  id                             uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id                  text,
  tenant_id                      uuid NOT NULL REFERENCES tenants(id),
  tenant_source                  text NOT NULL DEFAULT 'form'
                                 CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  effective_from_at              timestamptz NOT NULL,
  effective_from_date            date GENERATED ALWAYS AS (bkk_date(effective_from_at)) STORED,
  weekday_rate_thb               numeric(14,2) NOT NULL DEFAULT 300
                                 CHECK (weekday_rate_thb >= 0 AND weekday_rate_thb = trunc(weekday_rate_thb)),
  holiday_rate_thb               numeric(14,2) NOT NULL DEFAULT 350
                                 CHECK (holiday_rate_thb >= 0 AND holiday_rate_thb = trunc(holiday_rate_thb)),
  pay_standby                    boolean NOT NULL DEFAULT false,
  helper_day_rate_thb            numeric(14,2) NOT NULL DEFAULT 400 CHECK (helper_day_rate_thb >= 0),
  fuel_incentive_tiers           jsonb NOT NULL DEFAULT '[]',   -- [{minKmPerLitre, amountThb}]
  fuel_min_refuels_per_month     int NOT NULL DEFAULT 5,
  trip_volume_tiers              jsonb NOT NULL DEFAULT '[]',   -- [{minTrips, amountThb}]
  sso_rate_percent               numeric(5,2) NOT NULL DEFAULT 5,
  sso_base_existing_thb          numeric(14,2) NOT NULL DEFAULT 15000,
  sso_base_new_thb               numeric(14,2) NOT NULL DEFAULT 12000,
  sso_existing_hired_before_year int NOT NULL DEFAULT 2026,
  sso_max_age_inclusive          int NOT NULL DEFAULT 55,
  sso_probation_months           int NOT NULL DEFAULT 3,
  created_by                     uuid REFERENCES users(id),
  created_at                     timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX driver_compensation_configs_legacy ON driver_compensation_configs (legacy_doc_id)
  WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX driver_compensation_configs_eff ON driver_compensation_configs (tenant_id, effective_from_at DESC, id DESC);

CREATE TABLE penalty_types (                                 -- config.penaltyTypes[] per config version
  config_id          uuid NOT NULL REFERENCES driver_compensation_configs(id) ON DELETE CASCADE,
  code               text NOT NULL,
  name_en            text NOT NULL,
  name_th            text NOT NULL,
  default_amount_thb numeric(14,2) NOT NULL DEFAULT 0 CHECK (default_amount_thb >= 0),
  PRIMARY KEY (config_id, code)
);

CREATE TABLE driver_penalties (
  id                 uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id      text,
  tenant_id          uuid NOT NULL REFERENCES tenants(id),
  tenant_source      text NOT NULL DEFAULT 'driver'
                     CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  driver_id          uuid REFERENCES drivers(id),
  legacy_driver_ref  text,
  driver_ref_match   text CHECK (driver_ref_match IN ('doc_id','auth_uid','none')),
  type_code          text NOT NULL,
  type_name_snapshot text,
  total_thb          numeric(14,2) NOT NULL CHECK (total_thb >= 0 AND total_thb = trunc(total_thb)),
  remaining_thb      numeric(14,2) NOT NULL CHECK (remaining_thb >= 0 AND remaining_thb <= total_thb),
  installments_total int NOT NULL DEFAULT 1 CHECK (installments_total > 0),
  installments_paid  int NOT NULL DEFAULT 0 CHECK (installments_paid >= 0 AND installments_paid <= installments_total),
  reason             text,
  evidence_file_id   uuid REFERENCES file_objects(id),
  status             text NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending','partially_deducted','cleared','cancelled')),
  incurred_at        timestamptz NOT NULL,
  created_by         uuid REFERENCES users(id),
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  CHECK (driver_id IS NOT NULL OR tenant_source = 'quarantine')
);
CREATE UNIQUE INDEX driver_penalties_legacy ON driver_penalties (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX driver_penalties_open          ON driver_penalties (driver_id) WHERE status IN ('pending','partially_deducted');
CREATE INDEX driver_penalties_incurred      ON driver_penalties (tenant_id, incurred_at DESC, id DESC);
CREATE TRIGGER driver_penalties_updated_at BEFORE UPDATE ON driver_penalties
  FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

CREATE TABLE payroll_runs (                                  -- legacy payroll/{authUid}_{YYYY-MM}_{R1|R2}; keyed by driver_id
  id                    uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id         text,
  tenant_id             uuid NOT NULL REFERENCES tenants(id),
  tenant_source         text NOT NULL DEFAULT 'driver'
                        CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  driver_id             uuid REFERENCES drivers(id),
  legacy_driver_ref     text,
  driver_ref_match      text CHECK (driver_ref_match IN ('doc_id','auth_uid','none')),
  pay_period            text NOT NULL CHECK (pay_period ~ '^[0-9]{4}-[0-9]{2}$'),
  pay_round             text NOT NULL CHECK (pay_round IN ('R1','R2')),    -- R1 = days 1-15, R2 = 16-end (Bangkok)
  period_start          timestamptz NOT NULL,                  -- Bangkok midnight bounds
  period_end            timestamptz NOT NULL,
  status                text NOT NULL DEFAULT 'draft'
                        CHECK (status IN ('draft','pending_approval','approved','paid','cancelled')),
  total_earnings_thb    numeric(14,2) NOT NULL DEFAULT 0,
  total_deductions_thb  numeric(14,2) NOT NULL DEFAULT 0,
  net_pay_thb           numeric(14,2) NOT NULL DEFAULT 0,
  currency              text NOT NULL DEFAULT 'THB' CHECK (currency = 'THB'),
  payment_date          timestamptz,
  payment_method        text,
  remarks               text,
  approved_by           uuid REFERENCES users(id),
  approved_at           timestamptz,
  ledger_transaction_id uuid REFERENCES transactions(id),
  created_at            timestamptz NOT NULL DEFAULT now(),
  updated_at            timestamptz NOT NULL DEFAULT now(),
  CHECK (period_end > period_start),
  CHECK (driver_id IS NOT NULL OR tenant_source = 'quarantine')
);
CREATE UNIQUE INDEX payroll_runs_legacy        ON payroll_runs (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE UNIQUE INDEX payroll_runs_driver_period ON payroll_runs (driver_id, pay_period, pay_round);
CREATE INDEX payroll_runs_period_end           ON payroll_runs (tenant_id, period_end DESC, id DESC);
CREATE INDEX payroll_runs_period_status        ON payroll_runs (tenant_id, pay_period, pay_round, status);
CREATE TRIGGER payroll_runs_updated_at BEFORE UPDATE ON payroll_runs
  FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
ALTER TABLE transactions ADD CONSTRAINT transactions_payroll_fk
  FOREIGN KEY (payroll_run_id) REFERENCES payroll_runs(id) DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE payroll_line_items (                            -- frozen once the run leaves draft (ADR 0013)
  id             uuid PRIMARY KEY DEFAULT uuidv7(),
  payroll_run_id uuid NOT NULL REFERENCES payroll_runs(id) ON DELETE CASCADE,
  line_no        int  NOT NULL CHECK (line_no >= 0),
  item_type      text NOT NULL CHECK (item_type IN ('earning','deduction')),
  category       text NOT NULL,      -- ADR 0013 code as written by the engine: TRIP_COMMISSION, HELPER_PAY, FUEL_INCENTIVE,
                                     -- TRIP_VOLUME_INCENTIVE, SOCIAL_SECURITY, PENALTY, CASH_ADVANCE, BASE_SALARY, TAX
  name           text NOT NULL,
  amount_thb     numeric(14,2) NOT NULL,
  description    text,
  reference_id   text,
  quantity       numeric(12,3),
  unit_rate_thb  numeric(14,2),
  meta           jsonb,
  UNIQUE (payroll_run_id, line_no)
);
-- +goose StatementBegin
CREATE FUNCTION trg_payroll_lines_frozen() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE s text;
BEGIN
  SELECT status INTO s FROM payroll_runs WHERE id = COALESCE(NEW.payroll_run_id, OLD.payroll_run_id);
  -- s IS NULL when the parent row is being deleted (FK cascade): allowed.
  IF s IS NOT NULL AND s <> 'draft' THEN
    RAISE EXCEPTION 'payroll run % is %, line items are frozen', COALESCE(NEW.payroll_run_id, OLD.payroll_run_id), s
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN COALESCE(NEW, OLD);
END $$;
-- +goose StatementEnd
CREATE TRIGGER payroll_line_items_frozen BEFORE INSERT OR UPDATE OR DELETE ON payroll_line_items
  FOR EACH ROW EXECUTE FUNCTION trg_payroll_lines_frozen();

CREATE TABLE payroll_penalty_applications (                  -- penaltyApplications[]; committed to driver_penalties on approve
  payroll_run_id uuid NOT NULL REFERENCES payroll_runs(id) ON DELETE CASCADE,
  penalty_id     uuid NOT NULL REFERENCES driver_penalties(id),
  applied_thb    numeric(14,2) NOT NULL CHECK (applied_thb >= 0),
  PRIMARY KEY (payroll_run_id, penalty_id)
);
CREATE INDEX payroll_penalty_applications_penalty ON payroll_penalty_applications (penalty_id);

CREATE TABLE driver_advances (                               -- ADR 0014 cash advance; table shipped, UI deferred (R30)
  id             uuid PRIMARY KEY DEFAULT uuidv7(),
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  tenant_source  text NOT NULL DEFAULT 'driver'
                 CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  driver_id      uuid NOT NULL REFERENCES drivers(id),
  amount_thb     numeric(14,2) NOT NULL CHECK (amount_thb > 0),
  withdrawn_at   timestamptz NOT NULL,
  deduct_period  text NOT NULL CHECK (deduct_period ~ '^[0-9]{4}-[0-9]{2}$'),
  deduct_round   text NOT NULL CHECK (deduct_round IN ('R1','R2')),
  status         text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','deducted','cancelled')),
  reason         text,
  payroll_run_id uuid REFERENCES payroll_runs(id),
  created_by     uuid REFERENCES users(id),
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  CHECK (status <> 'deducted' OR payroll_run_id IS NOT NULL)
);
CREATE INDEX driver_advances_open ON driver_advances (driver_id, deduct_period, deduct_round) WHERE status = 'pending';
CREATE INDEX driver_advances_tenant ON driver_advances (tenant_id, withdrawn_at DESC, id DESC);
CREATE TRIGGER driver_advances_updated_at BEFORE UPDATE ON driver_advances
  FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

-- Row level security: Appendix C §C.3.0 marks all eleven tables "yes" (family in the trailing comment).
ALTER TABLE vehicle_expenses             ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- tenant
ALTER TABLE maintenance_records          ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- tenant
ALTER TABLE maintenance_files            ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- parent-scoped: maintenance_records
ALTER TABLE transactions                 ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- tenant
ALTER TABLE driver_compensation_configs  ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- tenant
ALTER TABLE penalty_types                ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- parent-scoped: driver_compensation_configs
ALTER TABLE driver_penalties             ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- tenant
ALTER TABLE payroll_runs                 ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- tenant
ALTER TABLE payroll_line_items           ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- parent-scoped: payroll_runs
ALTER TABLE payroll_penalty_applications ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- parent-scoped: payroll_runs
ALTER TABLE driver_advances              ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- tenant
-- Appended here, in this file: the Appendix C §C.3.5 "0006_finance_hr" block (generator CALLs, p_driver_update_pending,
-- app_driver_truck_ids(), the maintenance driver policies). ENABLE/FORCE above are idempotent with the generators,
-- so no table of this file is ever deny-all between migrations.

-- +goose StatementBegin
DO $$ BEGIN
  CALL app_rls_tenant_table('vehicle_expenses');      CALL app_rls_tenant_table('maintenance_records');
  CALL app_rls_tenant_table('transactions');          CALL app_rls_tenant_table('driver_compensation_configs');
  CALL app_rls_tenant_table('driver_penalties');      CALL app_rls_tenant_table('payroll_runs');
  CALL app_rls_tenant_table('driver_advances');
  CALL app_rls_child_table('maintenance_files', 'maintenance_records', 'maintenance_id', driver_writes => true);
  CALL app_rls_child_table('penalty_types', 'driver_compensation_configs', 'config_id', read_staff_only => true);
  CALL app_rls_child_table('payroll_line_items', 'payroll_runs', 'payroll_run_id');
  CALL app_rls_child_table('payroll_penalty_applications', 'payroll_runs', 'payroll_run_id');
  CALL app_rls_driver_rows('vehicle_expenses', true, false);
  CALL app_rls_driver_rows('payroll_runs', false, false);
END $$;
-- +goose StatementEnd

CREATE POLICY p_driver_update_pending ON vehicle_expenses FOR UPDATE
  USING (app_role() = 'driver' AND driver_id = app_driver_id() AND status = 'pending')
  WITH CHECK (app_role() = 'driver' AND driver_id = app_driver_id() AND status = 'pending');

-- maintenance gate (replaces firestore.rules:381-418 and the activeTruck denormalisation): the truck the driver is
-- responsible for now, or the home truck of an active assignment, in the driver's active tenant only. Invoker rights:
-- the lookups run under the driver's own RLS branches on drivers, truck_assignments and trucks (own tenant). Neither
-- drivers.active_truck_id nor truck_assignments.truck_id is tenant-checked by its FK, and an assignment left in a
-- former tenant stays visible to the driver (self-scope by driver_id), so the helper and both policies bind the
-- gate to app_tenant_id().
CREATE FUNCTION app_driver_truck_ids() RETURNS uuid[] LANGUAGE sql STABLE PARALLEL SAFE
  RETURN ARRAY(SELECT t.id FROM drivers d JOIN trucks t ON t.id = d.active_truck_id AND t.tenant_id = app_tenant_id()
                WHERE d.id = app_driver_id()
               UNION
               SELECT t.id FROM truck_assignments a JOIN trucks t ON t.id = a.truck_id AND t.tenant_id = app_tenant_id()
                WHERE a.driver_id = app_driver_id() AND a.status = 'active' AND a.tenant_id = app_tenant_id());
CREATE POLICY p_driver_read ON maintenance_records FOR SELECT
  USING (app_role() = 'driver' AND tenant_id = app_tenant_id() AND truck_id = ANY (app_driver_truck_ids()));
CREATE POLICY p_driver_update ON maintenance_records FOR UPDATE
  USING      (app_role() = 'driver' AND tenant_id = app_tenant_id() AND truck_id = ANY (app_driver_truck_ids()))
  WITH CHECK (app_role() = 'driver' AND tenant_id = app_tenant_id() AND truck_id = ANY (app_driver_truck_ids()));

-- +goose Down
DROP TABLE driver_advances;
DROP TABLE payroll_penalty_applications;
DROP TABLE payroll_line_items;
DROP FUNCTION trg_payroll_lines_frozen();
ALTER TABLE transactions DROP CONSTRAINT transactions_payroll_fk;
DROP TABLE payroll_runs;
DROP TABLE driver_penalties;
DROP TABLE penalty_types;
DROP TABLE driver_compensation_configs;
DROP TABLE transactions;
DROP TABLE maintenance_files;
ALTER TABLE trucks DROP CONSTRAINT trucks_active_maintenance_fk;
DROP TABLE maintenance_records;                               -- drops the policies that call app_driver_truck_ids()
DROP FUNCTION IF EXISTS app_driver_truck_ids();               -- created by the appended Appendix C block
DROP TABLE vehicle_expenses;
