-- 0005_billing.sql: Appendix A §A.2.4 plus the Appendix C block of this file (end of the Up section).
-- Follows Appendix A until the P0 deploy applies the baseline to a database that is kept; after that it is
-- never edited and follow-ups take the next free number (as 0001_preamble.sql).
-- +goose Up

-- Route rate cards: announcement rows, IMMUTABLE, void only (ADR 0009 §1; firestore.rules:142-154).
CREATE TABLE customer_rate_entries (
  id                   uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id        text,
  tenant_id            uuid NOT NULL REFERENCES tenants(id),   -- billing carrier (own fleet today)
  tenant_source        text NOT NULL DEFAULT 'form' CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  billing_party_id     uuid NOT NULL REFERENCES billing_parties(id),
  import_id            text NOT NULL,                        -- 'rc_{ms}' | 'manual_{ms}'
  hub_code             text NOT NULL CHECK (hub_code = upper(hub_code)),   -- extractHubId result
  raw_hub_name         text,
  destination_code     text NOT NULL,                        -- normalizeDestinationCode result as stored (legacy SPK dash collapse kept)
  vehicle_class        text NOT NULL,                        -- normalizeVehicleClass result as stored (legacy '4WJ' defaults kept)
  rate_thb             numeric(14,2) NOT NULL CHECK (rate_thb >= 0),
  distance_km          numeric(9,2),
  job_category         text NOT NULL DEFAULT 'PRIMARY' CHECK (job_category IN ('PRIMARY','SUPPLEMENTARY')),
  effective_from_date  date NOT NULL,                        -- selection key (Bangkok day)
  effective_from_at    timestamptz NOT NULL,                 -- legacy instant; within-day tie sort only
  imported_at          timestamptz,
  created_by           uuid REFERENCES users(id),
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  voided               boolean NOT NULL DEFAULT false,
  voided_at            timestamptz,
  voided_by            uuid REFERENCES users(id),            -- required for new voids by the trigger; may be NULL on legacy voided rows
  voided_reason        text,
  CHECK (NOT voided OR voided_at IS NOT NULL)
);
CREATE UNIQUE INDEX customer_rate_entries_legacy ON customer_rate_entries (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX customer_rate_entries_lookup ON customer_rate_entries
  (billing_party_id, hub_code, destination_code, vehicle_class, job_category, effective_from_date DESC, effective_from_at DESC)
  WHERE NOT voided;
CREATE INDEX customer_rate_entries_party  ON customer_rate_entries (billing_party_id, effective_from_at DESC);
CREATE INDEX customer_rate_entries_import ON customer_rate_entries (billing_party_id, import_id);
CREATE TRIGGER customer_rate_entries_set_updated_at BEFORE UPDATE ON customer_rate_entries
  FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER customer_rate_entries_void_only BEFORE UPDATE ON customer_rate_entries
  FOR EACH ROW EXECUTE FUNCTION trg_announcement_void_only();
CREATE TRIGGER customer_rate_entries_no_delete BEFORE DELETE ON customer_rate_entries
  FOR EACH ROW EXECUTE FUNCTION trg_forbid_mutation();
ALTER TABLE customer_rate_entries ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant, billing carrier)

CREATE TABLE customer_fuel_rate_adjustments (              -- announcement rows; customer-level, no fallback; skipped for SUPPLEMENTARY
  id                       uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id            text,
  tenant_id                uuid NOT NULL REFERENCES tenants(id),
  tenant_source            text NOT NULL DEFAULT 'form' CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  billing_party_id         uuid NOT NULL REFERENCES billing_parties(id),
  effective_from_date      date NOT NULL,
  effective_from_at        timestamptz NOT NULL,
  rate_multiplier          numeric(10,6) NOT NULL CHECK (rate_multiplier > 0),
  add_thb_per_trip         numeric(14,2) NOT NULL DEFAULT 0, -- signed: discounts allowed
  reference_fuel_price_thb numeric(8,2),                     -- referenceFuelPriceThbPerLitre
  announcement_note        text NOT NULL DEFAULT '',
  fuel_band_enabled        boolean NOT NULL DEFAULT false,
  fuel_band_baseline_floor int,
  fuel_band_thb_per_baht   numeric(10,2),
  created_by               uuid REFERENCES users(id),
  created_at               timestamptz NOT NULL DEFAULT now(),
  updated_at               timestamptz NOT NULL DEFAULT now(),
  voided                   boolean NOT NULL DEFAULT false,
  voided_at                timestamptz,
  voided_by                uuid REFERENCES users(id),
  voided_reason            text,
  CHECK (NOT voided OR voided_at IS NOT NULL),
  CHECK (NOT fuel_band_enabled OR (fuel_band_baseline_floor IS NOT NULL AND fuel_band_thb_per_baht IS NOT NULL))
);
CREATE UNIQUE INDEX customer_fuel_adj_legacy ON customer_fuel_rate_adjustments (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX customer_fuel_adj_lookup ON customer_fuel_rate_adjustments
  (billing_party_id, effective_from_date DESC, effective_from_at DESC) WHERE NOT voided;
CREATE INDEX customer_fuel_adj_party  ON customer_fuel_rate_adjustments (billing_party_id, effective_from_at DESC);
CREATE TRIGGER customer_fuel_adj_set_updated_at BEFORE UPDATE ON customer_fuel_rate_adjustments
  FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER customer_fuel_adj_void_only BEFORE UPDATE ON customer_fuel_rate_adjustments
  FOR EACH ROW EXECUTE FUNCTION trg_announcement_void_only();
CREATE TRIGGER customer_fuel_adj_no_delete BEFORE DELETE ON customer_fuel_rate_adjustments
  FOR EACH ROW EXECUTE FUNCTION trg_forbid_mutation();
ALTER TABLE customer_fuel_rate_adjustments ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant, billing carrier)

CREATE TABLE customer_service_fees (                       -- extra stop, waiting time, standby fallback, ... (mutable, deletable: firestore.rules:172-179)
  id               uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id    text,
  tenant_id        uuid NOT NULL REFERENCES tenants(id),
  tenant_source    text NOT NULL DEFAULT 'form' CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  billing_party_id uuid NOT NULL REFERENCES billing_parties(id),
  fee_type         text NOT NULL CHECK (fee_type IN ('extra_stop','waiting_time','special_handling','service_charge','standby','custom')),
  custom_type_name text,
  amount_thb       numeric(14,2) NOT NULL CHECK (amount_thb >= 0),
  unit             text NOT NULL CHECK (unit IN ('per_trip','per_stop')),
  note             text,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  CHECK (fee_type <> 'custom' OR custom_type_name IS NOT NULL)
);
CREATE UNIQUE INDEX customer_service_fees_legacy ON customer_service_fees (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
-- Non-unique until 0010_d5_unique_constraints.sql (§A.4 D5) adds
--   CREATE UNIQUE INDEX customer_service_fees_one_per_type ON customer_service_fees (billing_party_id, fee_type) WHERE fee_type <> 'custom';
CREATE INDEX customer_service_fees_party_type ON customer_service_fees (billing_party_id, fee_type);
CREATE INDEX customer_service_fees_party      ON customer_service_fees (billing_party_id, created_at DESC);
CREATE TRIGGER customer_service_fees_set_updated_at BEFORE UPDATE ON customer_service_fees
  FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
ALTER TABLE customer_service_fees ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant, billing carrier)

CREATE TABLE standby_rate_entries (                        -- fixed rate per standby event; mutable, soft delete only (R20)
  id                  uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id       text,
  tenant_id           uuid NOT NULL REFERENCES tenants(id),
  tenant_source       text NOT NULL DEFAULT 'form' CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  billing_party_id    uuid NOT NULL REFERENCES billing_parties(id),
  rate_thb            numeric(14,2) NOT NULL CHECK (rate_thb >= 0),
  effective_from_date date NOT NULL,
  effective_from_at   timestamptz NOT NULL,                  -- legacy: browser-local midnight
  note                text,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  voided_at           timestamptz,
  voided_by           uuid REFERENCES users(id),
  voided_reason       text,
  CHECK ((voided_at IS NULL) = (voided_by IS NULL))
);
CREATE UNIQUE INDEX standby_rate_entries_legacy ON standby_rate_entries (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX standby_rate_entries_lookup ON standby_rate_entries
  (billing_party_id, effective_from_date DESC, effective_from_at DESC) WHERE voided_at IS NULL;
CREATE TRIGGER standby_rate_entries_set_updated_at BEFORE UPDATE ON standby_rate_entries
  FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER standby_rate_entries_no_delete BEFORE DELETE ON standby_rate_entries
  FOR EACH ROW EXECUTE FUNCTION trg_forbid_mutation();
ALTER TABLE standby_rate_entries ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant, billing carrier)

ALTER TABLE standby_records ADD CONSTRAINT standby_records_rate_fk
  FOREIGN KEY (billing_rate_entry_id) REFERENCES standby_rate_entries(id);

-- 1:1 price snapshot of a trip. Separate from trip_records because it is carrier-internal (dispatcher and
-- the executing carrier never read it), written only by the billing service, deleted as a whole on reset
-- (reset-multidrop-billing.js semantics) and gated by period lock + freeze. The axis columns
-- (billing_party_id, billing_date) stay on trip_records.
CREATE TABLE trip_billing_snapshots (
  trip_id                   uuid PRIMARY KEY REFERENCES trip_records(id) ON DELETE CASCADE,
  tenant_id                 uuid NOT NULL REFERENCES tenants(id),   -- billing carrier that priced the trip (may differ from trip_records.tenant_id)
  tenant_source             text NOT NULL DEFAULT 'form' CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  estimate_thb              numeric(14,2),                 -- NULL = stamped but unpriced
  unpriced_reason           text CHECK (unpriced_reason IN ('no_customer','no_rate','no_vehicle_class','no_billing_date')),
  base_rate_thb             numeric(14,2),                 -- single: raw card price; multi-drop: fuel-adjusted base stop
  stop_charge_thb           numeric(14,2),                 -- multi-drop surcharge (stops 2+ flat fee, or legacy per-route)
  rate_entry_id             uuid REFERENCES customer_rate_entries(id),
  rate_import_id            text,                          -- legacy billingRateImportId / 'manual'
  lookup_hub_code           text,
  lookup_destination_code   text,
  fuel_adjustment_id        uuid REFERENCES customer_fuel_rate_adjustments(id),
  rate_multiplier           numeric(10,6) NOT NULL DEFAULT 1,
  add_thb_per_trip          numeric(14,2) NOT NULL DEFAULT 0,
  fuel_effective_from_date  date,                          -- billingEffectiveFromDateStr
  round_effective_from_date date,                          -- billingRoundEffectiveFromDateStr (ADR 0009)
  fuel_band_lower_thb       numeric(8,2),
  fuel_band_upper_thb       numeric(8,2),
  reference_fuel_price_thb  numeric(8,2),
  is_multi_delivery         boolean NOT NULL DEFAULT false,
  manual_override           boolean NOT NULL DEFAULT false, -- EditBillingDialog / PUT /v1/trips/{id}/billing/manual
  job_category_at_pricing   text CHECK (job_category_at_pricing IN ('PRIMARY','SUPPLEMENTARY')),
  computed_by               text NOT NULL CHECK (computed_by IN ('mobile_delivery','scheduler','admin_force','set_job_category','manual_edit','etl')),
  compute_version           int  NOT NULL DEFAULT 1,       -- 0 = loaded by ETL from the legacy snapshot
  last_event_id             bigint,                        -- outbox_events.id high-water mark; older events are dropped (R17)
  computed_at               timestamptz NOT NULL DEFAULT now(),
  updated_at                timestamptz NOT NULL DEFAULT now(),
  CHECK (unpriced_reason IS NULL OR estimate_thb IS NULL)
);
CREATE INDEX trip_billing_snapshots_rate     ON trip_billing_snapshots (rate_entry_id) WHERE rate_entry_id IS NOT NULL;
CREATE INDEX trip_billing_snapshots_fuel     ON trip_billing_snapshots (fuel_adjustment_id) WHERE fuel_adjustment_id IS NOT NULL;
CREATE INDEX trip_billing_snapshots_unpriced ON trip_billing_snapshots (tenant_id, unpriced_reason) WHERE estimate_thb IS NULL;
CREATE INDEX trip_billing_snapshots_manual   ON trip_billing_snapshots (tenant_id) WHERE manual_override;
CREATE TRIGGER trip_billing_snapshots_set_updated_at BEFORE UPDATE ON trip_billing_snapshots
  FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
ALTER TABLE trip_billing_snapshots ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant, billing carrier)

CREATE TABLE trip_billing_stop_breakdown (                 -- billingMultiDeliveryBreakdown[]
  trip_id          uuid NOT NULL REFERENCES trip_billing_snapshots(trip_id) ON DELETE CASCADE,
  stop_index       int  NOT NULL CHECK (stop_index >= 1),
  destination_code text NOT NULL,
  base_rate_thb    numeric(14,2) NOT NULL,
  final_rate_thb   numeric(14,2) NOT NULL,
  PRIMARY KEY (trip_id, stop_index)
);
ALTER TABLE trip_billing_stop_breakdown ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (parent-scoped via trip_billing_snapshots)

CREATE TABLE billing_counters (                            -- billing_counters/{customerId}_{YYYYMM}; written only by next_invoice_seq()
  billing_party_id uuid NOT NULL REFERENCES billing_parties(id),
  period_year      int  NOT NULL CHECK (period_year BETWEEN 2000 AND 2999),
  period_month     int  NOT NULL CHECK (period_month BETWEEN 1 AND 12),
  tenant_id        uuid NOT NULL REFERENCES tenants(id),   -- billing carrier
  tenant_source    text NOT NULL DEFAULT 'form' CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  last_seq         int  NOT NULL DEFAULT 0 CHECK (last_seq >= 0),
  customer_code    text NOT NULL,                          -- prefix of the invoice number
  updated_at       timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (billing_party_id, period_year, period_month)
);
CREATE TRIGGER billing_counters_set_updated_at BEFORE UPDATE ON billing_counters
  FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
ALTER TABLE billing_counters ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (platform-only)

-- Allocator (Appendix C §C.3.3, same definition), called inside the statement insert transaction (no gap). The
-- counter carries the billing carrier (R61): another carrier's counter returns NULL and the transaction fails.
-- +goose StatementBegin
CREATE FUNCTION next_invoice_seq(p_tenant uuid, p_party uuid, p_year int, p_month int, p_code text) RETURNS int
  LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
  INSERT INTO billing_counters AS c (billing_party_id, period_year, period_month, tenant_id, tenant_source,
                                     last_seq, customer_code)
  VALUES (p_party, p_year, p_month, p_tenant, 'form', 1, p_code)
  ON CONFLICT (billing_party_id, period_year, period_month)
    DO UPDATE SET last_seq = c.last_seq + 1 WHERE c.tenant_id = p_tenant
  RETURNING last_seq
$$;
-- +goose StatementEnd

CREATE TABLE billing_statements (
  id                     uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id          text,
  tenant_id              uuid NOT NULL REFERENCES tenants(id),   -- billing carrier
  tenant_source          text NOT NULL DEFAULT 'form' CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  billing_party_id       uuid NOT NULL REFERENCES billing_parties(id),
  issuer_company_id      uuid REFERENCES companies(id),    -- invoice issuer profile used for the documents
  invoice_number         text NOT NULL,                    -- {CODE}-{YYYYMM}-{SEQ:03}; legacy invoiceNumber ?? doc id
  customer_name_snapshot text,
  customer_code_snapshot text,
  period_year            int  NOT NULL CHECK (period_year BETWEEN 2000 AND 2999),
  period_month           int  NOT NULL CHECK (period_month BETWEEN 1 AND 12),
  total_amount           numeric(14,2) NOT NULL,
  withholding_tax_rate   numeric(5,4) NOT NULL CHECK (withholding_tax_rate BETWEEN 0 AND 1),   -- fraction; snapshot (R18); legacy 0.0100
  withholding_tax        numeric(14,2) NOT NULL,
  net_amount             numeric(14,2) NOT NULL,
  trip_count             int  NOT NULL DEFAULT 0,
  trip_only_count        int  NOT NULL DEFAULT 0,
  trip_subtotal          numeric(14,2) NOT NULL DEFAULT 0,
  standby_count          int  NOT NULL DEFAULT 0,
  standby_subtotal       numeric(14,2) NOT NULL DEFAULT 0,
  multi_drop_count       int  NOT NULL DEFAULT 0,
  multi_drop_subtotal    numeric(14,2) NOT NULL DEFAULT 0,
  status                 text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','sent','paid','cancelled')),
  generated_at           timestamptz NOT NULL,
  sent_at                timestamptz,
  paid_at                timestamptz,
  cancelled_at           timestamptz,
  due_date               timestamptz,                      -- generated_at + customers.payment_terms_days
  note                   text,
  generated_by           uuid REFERENCES users(id),
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX billing_statements_invoice_no ON billing_statements (invoice_number);
CREATE UNIQUE INDEX billing_statements_legacy     ON billing_statements (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX billing_statements_party_gen    ON billing_statements (billing_party_id, generated_at DESC);
CREATE INDEX billing_statements_status_gen   ON billing_statements (status, generated_at DESC);
CREATE INDEX billing_statements_party_status ON billing_statements (billing_party_id, status, generated_at DESC);
CREATE INDEX billing_statements_period_lock  ON billing_statements (billing_party_id, period_year, period_month)
  WHERE status IN ('sent','paid');
CREATE INDEX billing_statements_tenant_gen   ON billing_statements (tenant_id, generated_at DESC, id DESC);
CREATE TRIGGER billing_statements_set_updated_at BEFORE UPDATE ON billing_statements
  FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
ALTER TABLE billing_statements ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant, billing carrier)

-- +goose StatementBegin
CREATE FUNCTION trg_billing_statement_draft_only_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status <> 'draft' THEN
    RAISE EXCEPTION 'billing statement % is %, only a draft may be deleted', OLD.invoice_number, OLD.status
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN OLD;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER billing_statements_draft_delete BEFORE DELETE ON billing_statements
  FOR EACH ROW EXECUTE FUNCTION trg_billing_statement_draft_only_delete();

-- Which rows an invoice was built from (ADR 0008 follow-up "billedOnInvoiceNumber"). Empty for legacy statements.
CREATE TABLE billing_statement_lines (
  id            uuid PRIMARY KEY DEFAULT uuidv7(),
  statement_id  uuid NOT NULL REFERENCES billing_statements(id) ON DELETE CASCADE,
  row_type      text NOT NULL CHECK (row_type IN ('trip','multidrop_stop','standby')),
  trip_id       uuid REFERENCES trip_records(id),
  stop_index    int  CHECK (stop_index >= 1),
  standby_id    uuid REFERENCES standby_records(id),
  amount_thb    numeric(14,2) NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  CHECK ((row_type = 'standby') = (standby_id IS NOT NULL)),
  CHECK ((row_type = 'standby') = (trip_id IS NULL)),
  CHECK ((row_type = 'multidrop_stop') = (stop_index IS NOT NULL))
);
CREATE UNIQUE INDEX billing_statement_lines_row ON billing_statement_lines
  (statement_id, row_type, COALESCE(trip_id, standby_id), COALESCE(stop_index, 0));
CREATE INDEX billing_statement_lines_trip    ON billing_statement_lines (trip_id)    WHERE trip_id IS NOT NULL;
CREATE INDEX billing_statement_lines_standby ON billing_statement_lines (standby_id) WHERE standby_id IS NOT NULL;

-- UPDATE always rejected; DELETE only while the parent statement is a draft (or already gone, i.e. the
-- ON DELETE CASCADE of a draft statement).
-- +goose StatementBegin
CREATE FUNCTION trg_statement_lines_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'UPDATE' THEN
    RAISE EXCEPTION 'billing_statement_lines are immutable' USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  IF EXISTS (SELECT 1 FROM billing_statements s WHERE s.id = OLD.statement_id AND s.status <> 'draft') THEN
    RAISE EXCEPTION 'lines of statement % are frozen (statement is not a draft)', OLD.statement_id
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN OLD;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER billing_statement_lines_guard BEFORE UPDATE OR DELETE ON billing_statement_lines
  FOR EACH ROW EXECUTE FUNCTION trg_statement_lines_guard();
ALTER TABLE billing_statement_lines ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (parent-scoped via billing_statements)

-- Rendered documents per statement (R14; queue documents.render). Keys:
-- documents/statements/{statement_id}/{invoice_summary.pdf|invoice_detail.xlsx|receipt.pdf|bundle.zip}
CREATE TABLE statement_documents (
  id            uuid PRIMARY KEY DEFAULT uuidv7(),
  statement_id  uuid NOT NULL REFERENCES billing_statements(id) ON DELETE CASCADE,
  kind          text NOT NULL CHECK (kind IN ('invoice_summary_pdf','invoice_detail_xlsx','receipt_pdf','bundle_zip')),
  status        text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','rendering','ready','failed')),
  file_id       uuid REFERENCES file_objects(id),
  job_id        uuid,                                      -- jobs.id (0009) of the render job; no FK, jobs are pruned after 30 days
  error         text,
  rendered_at   timestamptz,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (statement_id, kind),
  CHECK (status <> 'ready' OR (file_id IS NOT NULL AND rendered_at IS NOT NULL))
);
CREATE INDEX statement_documents_pending ON statement_documents (created_at) WHERE status IN ('pending','rendering','failed');
CREATE TRIGGER statement_documents_set_updated_at BEFORE UPDATE ON statement_documents
  FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
ALTER TABLE statement_documents ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (parent-scoped via billing_statements)

-- Appendix C block (same file): §C.3.5 "0005_billing" generator DO block for the 10 tables above.

-- Carrier-internal: no driver, scope or dispatcher branch.
-- +goose StatementBegin
DO $$ BEGIN
  CALL app_rls_tenant_table('customer_rate_entries');  CALL app_rls_tenant_table('customer_fuel_rate_adjustments');
  CALL app_rls_tenant_table('customer_service_fees');  CALL app_rls_tenant_table('standby_rate_entries');
  CALL app_rls_tenant_table('trip_billing_snapshots'); CALL app_rls_tenant_table('billing_statements');
  CALL app_rls_platform_table('billing_counters');
  CALL app_rls_child_table('trip_billing_stop_breakdown', 'trip_billing_snapshots', 'trip_id',
                           parent_key => 'trip_id', read_staff_only => true);
  CALL app_rls_child_table('billing_statement_lines', 'billing_statements', 'statement_id', read_staff_only => true);
  CALL app_rls_child_table('statement_documents', 'billing_statements', 'statement_id', read_staff_only => true);
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE statement_documents;
DROP TABLE billing_statement_lines;
DROP TABLE billing_statements;
CALL app_drop_definer_function('next_invoice_seq(uuid,uuid,integer,integer,text)');
DROP TABLE billing_counters;
DROP TABLE trip_billing_stop_breakdown;
DROP TABLE trip_billing_snapshots;
ALTER TABLE standby_records DROP CONSTRAINT standby_records_rate_fk;
DROP TABLE standby_rate_entries;
DROP TABLE customer_service_fees;
DROP TABLE customer_fuel_rate_adjustments;
DROP TABLE customer_rate_entries;
DROP FUNCTION trg_statement_lines_guard();
DROP FUNCTION trg_billing_statement_draft_only_delete();
