-- 0004_operations.sql: Appendix A §A.2.3 plus the Appendix C block of this file (end of the Up section).
-- Follows Appendix A until the P0 deploy applies the baseline to a database that is kept; after that it is
-- never edited and follow-ups take the next free number (as 0001_preamble.sql).
-- +goose Up

-- Task numbers FM|LH-ddMMyyyy-NNN, global across tenants (R10). Replaces count()+1
-- (logitrack-web/functions/src/triggers.ts:413-417, check_in_page.dart:2017-2022).
-- Reachable only through next_task_seq() (R67): no grant to logitrack_app.
CREATE TABLE task_number_counters (
  task_type  text NOT NULL CHECK (task_type IN ('first_mile','line_haul')),
  plan_date  date NOT NULL,                                -- Bangkok day of tasks.plan_at
  last_seq   int  NOT NULL DEFAULT 0 CHECK (last_seq >= 0),
  PRIMARY KEY (task_type, plan_date)
);
ALTER TABLE task_number_counters ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (platform-only)

-- Allocator (Appendix C §C.3.3, same definition), called inside the task insert transaction; 0009 hands it to
-- logitrack_rls_definer (BYPASSRLS, R66).
-- +goose StatementBegin
CREATE FUNCTION next_task_seq(p_task_type text, p_plan_date date) RETURNS int
  LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
  INSERT INTO task_number_counters AS c (task_type, plan_date, last_seq) VALUES (p_task_type, p_plan_date, 1)
  ON CONFLICT (task_type, plan_date) DO UPDATE SET last_seq = c.last_seq + 1
  RETURNING last_seq
$$;
-- +goose StatementEnd

CREATE TABLE tasks (
  id                          uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id               text,                        -- Firestore auto id (the human number is task_no)
  tenant_id                   uuid NOT NULL REFERENCES tenants(id),
  tenant_source               text NOT NULL CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  task_no                     text,                        -- FM-ddMMyyyy-NNN; legacy values not unique (web padded, mobile not)
  task_type                   text NOT NULL CHECK (task_type IN ('first_mile','line_haul')),
  job_category                text CHECK (job_category IN ('PRIMARY','SUPPLEMENTARY')),   -- NULL = legacy/unknown (ADR 0010), never defaulted
  status                      text NOT NULL CHECK (status IN ('pending','assigned','checked_in','in_transit','completed','cancelled')),
  plan_at                     timestamptz NOT NULL,        -- tasks.date: exact plan instant; plan-basis billing axis (ADR 0027)
  plan_date                   date GENERATED ALWAYS AS (bkk_date(plan_at)) STORED,
  plan_time                   text CHECK (plan_time ~ '^[0-9]{2}:[0-9]{2}$'),
  legacy_date_str             text,                        -- dateStr, ambiguous ddMMyyyy / YYYYMMDD; kept raw, never interpreted
  actual_pickup_at            timestamptz,                 -- ADR 0028 (operations only)
  source_hub_raw              text NOT NULL,               -- verbatim legacy value ('CODE', 'CODE - Name', 'soce')
  source_hub_id               uuid REFERENCES hubs(id),
  source_linked_party_id      uuid REFERENCES billing_parties(id),
  destination_raw             text NOT NULL,
  destination_hub_id          uuid REFERENCES hubs(id),
  destination_soc_key         text,
  destination_linked_party_id uuid REFERENCES billing_parties(id),
  billing_party_id            uuid REFERENCES billing_parties(id),   -- explicit billing customer (ADR 0027/0028)
  truck_id                    uuid REFERENCES trucks(id),
  truck_type                  text CHECK (truck_type IN ('4W','4WJ','6WH','10WH','18WH','VAN')),   -- NULL legal: unknown class -> unpriced no_vehicle_class (R15)
  legacy_truck_type           text,                        -- raw value outside the enum (PICKUP, 4WH, '6 Wheels')
  license_plate_snapshot      text,
  driver_id                   uuid REFERENCES drivers(id),
  legacy_driver_ref           text,                        -- raw legacy driverId (doc id, or auth uid on old rows)
  driver_ref_match            text CHECK (driver_ref_match IN ('doc_id','auth_uid','name','none')),
  helper_driver_id            uuid REFERENCES drivers(id), -- helperDriverIds[0]; ADR 0011 caps helpers at 1
  run_order                   int  CHECK (run_order >= 1), -- per-driver queue position (max + 1 inside the insert tx)
  check_in_at                 timestamptz,
  check_in_photo_file_id      uuid REFERENCES file_objects(id),
  check_in_app_screenshot_file_id uuid REFERENCES file_objects(id),   -- ADR 0019; copied into trip photos as checkin_app
  check_in_lat                double precision,
  check_in_lng                double precision,
  is_multi_delivery           boolean NOT NULL DEFAULT false,
  line_checkin_notified_at    timestamptz,                 -- LINE idempotency flag (notify.line)
  cancelled_at                timestamptz,
  cancel_reason               text,
  client_op_id                uuid,                        -- driver-created task (manual check-in): offline op id (R63)
  created_by                  uuid REFERENCES users(id),
  created_at                  timestamptz NOT NULL DEFAULT now(),
  updated_at                  timestamptz NOT NULL DEFAULT now(),
  CHECK (helper_driver_id IS NULL OR helper_driver_id IS DISTINCT FROM driver_id)
);
CREATE UNIQUE INDEX tasks_legacy          ON tasks (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE UNIQUE INDEX tasks_client_op       ON tasks (driver_id, client_op_id) WHERE client_op_id IS NOT NULL;
CREATE INDEX tasks_task_no                ON tasks (task_no) WHERE task_no IS NOT NULL;
CREATE INDEX tasks_tenant_type_plan       ON tasks (tenant_id, task_type, plan_date DESC, id DESC);
CREATE INDEX tasks_tenant_type_created    ON tasks (tenant_id, task_type, created_at DESC, id DESC);
CREATE INDEX tasks_plan_date              ON tasks (plan_date, task_type);
CREATE INDEX tasks_created                ON tasks (created_at DESC, id DESC);
CREATE INDEX tasks_driver_status          ON tasks (driver_id, status);
CREATE INDEX tasks_driver_run             ON tasks (driver_id, run_order DESC);
CREATE INDEX tasks_driver_plan            ON tasks (driver_id, plan_at);
CREATE INDEX tasks_helper_plan            ON tasks (helper_driver_id, plan_at) WHERE helper_driver_id IS NOT NULL;
CREATE INDEX tasks_active_status          ON tasks (status) WHERE status IN ('checked_in','in_transit');
CREATE INDEX tasks_billing_party          ON tasks (billing_party_id) WHERE billing_party_id IS NOT NULL;
CREATE INDEX tasks_source_party           ON tasks (source_linked_party_id) WHERE source_linked_party_id IS NOT NULL;
CREATE INDEX tasks_destination_party      ON tasks (destination_linked_party_id) WHERE destination_linked_party_id IS NOT NULL;
CREATE INDEX tasks_truck                  ON tasks (truck_id) WHERE truck_id IS NOT NULL;
ALTER TABLE tasks ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant + driver + scope read)

ALTER TABLE drivers ADD CONSTRAINT drivers_active_task_fk
  FOREIGN KEY (active_task_id) REFERENCES tasks(id) ON DELETE SET NULL;

CREATE TABLE task_delivery_stops (                         -- tasks.deliveryStops[] (multi-drop plan)
  id                          uuid PRIMARY KEY DEFAULT uuidv7(),
  task_id                     uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  stop_index                  int  NOT NULL CHECK (stop_index >= 1),
  destination_raw             text NOT NULL,
  destination_hub_id          uuid REFERENCES hubs(id),
  destination_linked_party_id uuid REFERENCES billing_parties(id),
  status                      text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','delivered','failed')),
  delivered_at                timestamptz,
  delivered_lat               double precision,
  delivered_lng               double precision,
  estimated_rate_thb          numeric(14,2),
  drop_fee_thb                numeric(14,2),
  sequence                    int,                         -- CF-only legacy field
  added_at                    timestamptz,
  source_id                   text,
  is_custom                   boolean NOT NULL DEFAULT false,
  UNIQUE (task_id, stop_index),
  UNIQUE (task_id, destination_raw)                        -- taskSchema refine: unique destinations; 409 duplicate_destination
);
CREATE INDEX task_delivery_stops_party ON task_delivery_stops (destination_linked_party_id) WHERE destination_linked_party_id IS NOT NULL;
ALTER TABLE task_delivery_stops ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (parent-scoped via tasks)

CREATE TABLE trip_records (
  id                           uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id                text,                       -- original Firestore doc id (may differ from trip_no after a rename)
  tenant_id                    uuid NOT NULL REFERENCES tenants(id),
  tenant_source                text NOT NULL CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  trip_no                      text NOT NULL CHECK (trip_no <> '' AND trip_no !~ '/' AND trip_no NOT IN ('.','..')
                                                    AND trip_no !~ '^__.*__$' AND octet_length(trip_no) <= 1500),   -- core/tripDocId.ts:10-18
  status                       text NOT NULL CHECK (status IN ('in_transit','incident','delivered','standby','cancelled')),
  job_type                     text NOT NULL CHECK (job_type IN ('first_mile','line_haul')),
  job_category                 text CHECK (job_category IN ('PRIMARY','SUPPLEMENTARY')),   -- copied from the task (ADR 0010); NULL allowed
  task_id                      uuid REFERENCES tasks(id),
  legacy_task_ref              text,
  task_ref_match               text CHECK (task_ref_match IN ('doc_id','task_no','none')),
  driver_id                    uuid REFERENCES drivers(id),
  legacy_driver_ref            text,                       -- raw legacy driverId (auth uid from mobile)
  driver_ref_match             text CHECK (driver_ref_match IN ('doc_id','auth_uid','none')),
  origin_raw                   text,
  origin_hub_id                uuid REFERENCES hubs(id),   -- resolved through hub_name_aliases only; raw never overwritten
  destination_raw              text,
  destination_hub_id           uuid REFERENCES hubs(id),
  destination_soc_key          text,
  seal_code                    text,
  partner_code                 text,                       -- root partnerCode, else ocrData.partnerCode
  ocr_data                     jsonb,                      -- whole ocrData incl. keys missing from the web Zod schema
  truck_id                     uuid REFERENCES trucks(id),
  truck_license_plate_snapshot text,                       -- never rebuilt from drivers.active_* (lib/truckPlate.ts rule)
  truck_type_raw               text,
  vehicle_class                text CHECK (vehicle_class IN ('4W','4WJ','6WH','10WH','18WH','VAN')),
  distance_km                  numeric(9,2),
  parcel_count                 int,
  seal_time                    timestamptz,                -- 'dd-MM-yyyy HH:mm:ss' parsed as Asia/Bangkok
  total_weight_kg              numeric(12,2),
  loading_lat                  double precision,
  loading_lng                  double precision,
  std                          timestamptz,
  sta                          timestamptz,                -- seal_time + duration_minutes
  ata                          timestamptz,
  duration_minutes             numeric(9,2),
  delivered_at                 timestamptz,                -- deliveredTimestamp: delivered-basis billing axis
  delivered_lat                double precision,
  delivered_lng                double precision,
  delivered_via                text CHECK (delivered_via IN ('mobile','admin_web')),
  is_multi_delivery            boolean NOT NULL DEFAULT false,
  billing_party_id             uuid REFERENCES billing_parties(id),   -- stamped even when unpriced (tripBillingOnDelivered.ts:83-102)
  billing_date                 timestamptz,                -- ADR 0027 plan/delivered axis instant
  billing_axis_date            date GENERATED ALWAYS AS (COALESCE(bkk_date(billing_date), bkk_date(delivered_at))) STORED,
  needs_admin_review           boolean NOT NULL DEFAULT false,
  review_status                text CHECK (review_status IN ('pending_review')),
  review_reason                text,
  resubmitted_at               timestamptz,
  line_delivered_notified_at   timestamptz,                -- LINE idempotency flag
  evidence_token               text,                       -- public gallery key /evidence/{token}; non-expiring (R30)
  evidence_token_revoked_at    timestamptz,                -- POST /v1/trips/{id}/evidence/revoke (R47); next forced LINE send mints a new token
  created_at                   timestamptz NOT NULL DEFAULT now(),   -- legacy: device time at loading (Depart axis); ETL coalesces (R19)
  updated_at                   timestamptz NOT NULL DEFAULT now(),
  CHECK (evidence_token_revoked_at IS NULL OR evidence_token IS NOT NULL)
);
CREATE UNIQUE INDEX trip_records_trip_no        ON trip_records (trip_no);
CREATE UNIQUE INDEX trip_records_legacy         ON trip_records (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE UNIQUE INDEX trip_records_evidence       ON trip_records (evidence_token) WHERE evidence_token IS NOT NULL;
CREATE INDEX trip_records_delivered_driver      ON trip_records (driver_id, delivered_at) WHERE status = 'delivered';
CREATE INDEX trip_records_delivered             ON trip_records (delivered_at) WHERE status = 'delivered';
CREATE INDEX trip_records_party_status_del      ON trip_records (billing_party_id, status, delivered_at);
CREATE INDEX trip_records_party_billdate        ON trip_records (billing_party_id, billing_date) WHERE status = 'delivered';
CREATE INDEX trip_records_party_axis            ON trip_records (billing_party_id, billing_axis_date) WHERE status = 'delivered';
CREATE INDEX trip_records_pending_driver        ON trip_records (driver_id, updated_at DESC) WHERE status = 'in_transit';
CREATE INDEX trip_records_tenant_created        ON trip_records (tenant_id, created_at DESC, id DESC);
CREATE INDEX trip_records_created               ON trip_records (created_at DESC, id DESC);
CREATE INDEX trip_records_created_delivered     ON trip_records (created_at DESC, id DESC) WHERE status = 'delivered';
CREATE INDEX trip_records_driver_created        ON trip_records (driver_id, created_at DESC, id DESC);
CREATE INDEX trip_records_task                  ON trip_records (task_id) WHERE task_id IS NOT NULL;
CREATE INDEX trip_records_seal                  ON trip_records (seal_code) WHERE seal_code IS NOT NULL;
CREATE INDEX trip_records_truck_created         ON trip_records (truck_id, created_at DESC) WHERE truck_id IS NOT NULL;
ALTER TABLE trip_records ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant + driver + scope read)

CREATE TABLE trip_no_history (                             -- renameTripRecord provenance (renamedFromTripId/At/By); append-only
  id           uuid PRIMARY KEY DEFAULT uuidv7(),          -- R57
  trip_id      uuid NOT NULL REFERENCES trip_records(id),
  old_trip_no  text NOT NULL,
  new_trip_no  text NOT NULL,
  renamed_by   uuid REFERENCES users(id),
  renamed_at   timestamptz NOT NULL DEFAULT now(),
  CHECK (old_trip_no <> new_trip_no)
);
CREATE INDEX trip_no_history_trip   ON trip_no_history (trip_id, renamed_at);
CREATE INDEX trip_no_history_old_no ON trip_no_history (old_trip_no);
CREATE TRIGGER trip_no_history_immutable BEFORE UPDATE OR DELETE ON trip_no_history
  FOR EACH ROW EXECUTE FUNCTION trg_forbid_mutation();
ALTER TABLE trip_no_history ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (parent-scoped via trip_records, staff read)

CREATE TABLE trip_delivery_stops (                         -- trip_records.deliveryStopsProgress[] (actual progress)
  id                 uuid PRIMARY KEY DEFAULT uuidv7(),
  trip_id            uuid NOT NULL REFERENCES trip_records(id) ON DELETE CASCADE,
  stop_index         int  NOT NULL CHECK (stop_index >= 1),
  destination_raw    text NOT NULL,
  destination_hub_id uuid REFERENCES hubs(id),
  source_id          text,
  status             text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','delivered','failed')),
  delivered_at       timestamptz,
  delivered_lat      double precision,
  delivered_lng      double precision,
  completed_seq      int,                                  -- completion order (legacy array order), not plan order
  UNIQUE (trip_id, stop_index)
);
ALTER TABLE trip_delivery_stops ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (parent-scoped via trip_records)

CREATE TABLE trip_photos (                                 -- flat photos[] (ADR 0018: the whole evidence set) + per-stop link
  id               uuid PRIMARY KEY DEFAULT uuidv7(),
  trip_id          uuid NOT NULL REFERENCES trip_records(id) ON DELETE CASCADE,
  stop_id          uuid REFERENCES trip_delivery_stops(id) ON DELETE SET NULL,
  photo_type       text NOT NULL CHECK (photo_type <> ''),   -- free text: unknown legacy types still load (R19)
  photo_type_known boolean GENERATED ALWAYS AS (
                     photo_type IN ('pre_close','closing','seal','runsheet','runsheet_extra_1','runsheet_extra_2','runsheet_extra_3',
                                    'pre_open','opening','empty_container','runsheet_received','checkin_app','truck_release','arrived')
                     OR photo_type ~ '^stop_[0-9]+_(arrived|pre_open|opening|empty_container|runsheet_received)$') STORED,
  file_id          uuid NOT NULL REFERENCES file_objects(id),
  geocode_lat      double precision,
  geocode_lng      double precision,
  geocode_address  text,
  geocoded_at      timestamptz,
  position         int  NOT NULL DEFAULT 0,                -- insertion/replace order
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (trip_id, photo_type)                             -- merge-by-type, last wins => UPSERT
);
CREATE INDEX trip_photos_unknown_type ON trip_photos (trip_id) WHERE NOT photo_type_known;
ALTER TABLE trip_photos ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (parent-scoped via trip_records)

CREATE TABLE standby_records (
  id                          uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id               text,
  tenant_id                   uuid NOT NULL REFERENCES tenants(id),
  tenant_source               text NOT NULL CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  driver_id                   uuid REFERENCES drivers(id),
  legacy_driver_ref           text,
  driver_ref_match            text CHECK (driver_ref_match IN ('doc_id','auth_uid','none')),
  task_id                     uuid REFERENCES tasks(id),
  trip_id                     uuid REFERENCES trip_records(id),
  customer_party_id           uuid REFERENCES billing_parties(id),   -- standby.customerId (pricing input)
  customer_resolved           boolean NOT NULL DEFAULT false,
  customer_resolved_from      text CHECK (customer_resolved_from IN ('task','origin_hub','manual')),
  job_category                text CHECK (job_category IN ('PRIMARY','SUPPLEMENTARY')),   -- label only
  start_location_raw          text,
  end_location_raw            text,
  started_at                  timestamptz,
  ended_at                    timestamptz,                 -- billing axis; NULL => unpriced no_ended_at (never derived)
  billing_axis_date           date GENERATED ALWAYS AS (bkk_date(COALESCE(ended_at, started_at, created_at))) STORED,
  duration_minutes            int,
  note                        text,
  status                      text NOT NULL DEFAULT 'completed' CHECK (status IN ('completed')),
  lat                         double precision,
  lng                         double precision,
  truck_id                    uuid REFERENCES trucks(id),
  truck_license_plate_snapshot text,
  backfilled_by_admin         boolean NOT NULL DEFAULT false,
  migrated_from_trip_no       text,                        -- migrate-standby-trips.js provenance
  -- inline billing snapshot (small; written only by the billing service)
  billing_estimate_thb        numeric(14,2),
  billing_party_id            uuid REFERENCES billing_parties(id),
  billing_rate_source         text CHECK (billing_rate_source IN ('standby_rate','service_fee')),
  billing_rate_entry_id       uuid,                        -- FK standby_records_rate_fk added in 0005
  billing_effective_from_date date,
  billing_unpriced_reason     text CHECK (billing_unpriced_reason IN ('no_customer','no_rate','no_ended_at')),
  billing_computed_at         timestamptz,
  line_notified_at            timestamptz,
  evidence_token              text,                        -- non-expiring (R30)
  evidence_token_revoked_at   timestamptz,                 -- POST /v1/standby/{id}/evidence/revoke (R47)
  client_op_id                uuid,                        -- offline op id (R63)
  created_at                  timestamptz NOT NULL DEFAULT now(),
  updated_at                  timestamptz NOT NULL DEFAULT now(),
  CHECK (billing_unpriced_reason IS NULL OR billing_estimate_thb IS NULL),
  CHECK (billing_rate_source IS DISTINCT FROM 'service_fee' OR billing_rate_entry_id IS NULL),
  CHECK (evidence_token_revoked_at IS NULL OR evidence_token IS NOT NULL)
);
CREATE UNIQUE INDEX standby_records_legacy   ON standby_records (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE UNIQUE INDEX standby_records_evidence ON standby_records (evidence_token) WHERE evidence_token IS NOT NULL;
CREATE UNIQUE INDEX standby_records_client_op ON standby_records (driver_id, client_op_id) WHERE client_op_id IS NOT NULL;
CREATE INDEX standby_records_ended           ON standby_records (ended_at) WHERE status = 'completed';
CREATE INDEX standby_records_created         ON standby_records (created_at DESC, id DESC);
CREATE INDEX standby_records_tenant_created  ON standby_records (tenant_id, created_at DESC, id DESC);
CREATE INDEX standby_records_driver          ON standby_records (driver_id, created_at DESC, id DESC);
CREATE INDEX standby_records_party_axis      ON standby_records (billing_party_id, billing_axis_date);
CREATE INDEX standby_records_customer_axis   ON standby_records (customer_party_id, billing_axis_date);
CREATE INDEX standby_records_task            ON standby_records (task_id) WHERE task_id IS NOT NULL;
CREATE INDEX standby_records_trip            ON standby_records (trip_id) WHERE trip_id IS NOT NULL;
ALTER TABLE standby_records ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant + driver + scope read)

CREATE TABLE standby_photos (
  id           uuid PRIMARY KEY DEFAULT uuidv7(),
  standby_id   uuid NOT NULL REFERENCES standby_records(id) ON DELETE CASCADE,
  photo_type   text NOT NULL CHECK (photo_type IN ('customer_worksheet','site_photo')),
  file_id      uuid NOT NULL REFERENCES file_objects(id),
  overlay_text text,
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (standby_id, photo_type)
);
ALTER TABLE standby_photos ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (parent-scoped via standby_records)

CREATE TABLE incident_reports (                            -- collection `incidentReport`
  id                           uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id                text,
  tenant_id                    uuid NOT NULL REFERENCES tenants(id),
  tenant_source                text NOT NULL CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  driver_id                    uuid REFERENCES drivers(id),
  legacy_driver_ref            text,
  driver_ref_match             text CHECK (driver_ref_match IN ('doc_id','auth_uid','none')),
  trip_id                      uuid REFERENCES trip_records(id),
  legacy_trip_ref              text,                       -- kept when the trip could not be resolved
  delay_cause                  text,                       -- i18n key incident_cause_*
  description                  text,
  lat                          double precision,
  lng                          double precision,
  truck_id                     uuid REFERENCES trucks(id),
  truck_license_plate_snapshot text,                       -- mobile truckLicensePlate or admin truckPlate
  map_photo_file_id            uuid REFERENCES file_objects(id),
  situation1_photo_file_id     uuid REFERENCES file_objects(id),
  situation2_photo_file_id     uuid REFERENCES file_objects(id),
  reported_by_kind             text NOT NULL DEFAULT 'driver' CHECK (reported_by_kind IN ('driver','admin')),
  reported_by_user_id          uuid REFERENCES users(id),
  client_op_id                 uuid,                       -- offline op id (R63)
  created_at                   timestamptz NOT NULL DEFAULT now(),
  updated_at                   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX incident_reports_legacy ON incident_reports (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE UNIQUE INDEX incident_reports_client_op ON incident_reports (driver_id, client_op_id) WHERE client_op_id IS NOT NULL;
CREATE INDEX incident_reports_trip           ON incident_reports (trip_id) WHERE trip_id IS NOT NULL;
CREATE INDEX incident_reports_driver         ON incident_reports (driver_id, created_at DESC);
CREATE INDEX incident_reports_tenant_created ON incident_reports (tenant_id, created_at DESC, id DESC);
CREATE INDEX incident_reports_created        ON incident_reports (created_at DESC, id DESC);
ALTER TABLE incident_reports ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant + driver + scope read)

CREATE TABLE vehicle_locations (                           -- latest Cartrack position per truck (sync every 3 min); latest only
  truck_id             uuid PRIMARY KEY REFERENCES trucks(id) ON DELETE CASCADE,
  tenant_id            uuid NOT NULL REFERENCES tenants(id),
  tenant_source        text NOT NULL CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  gps_vehicle_id       text NOT NULL,
  license_plate        text,
  lat                  double precision NOT NULL,
  lng                  double precision NOT NULL,
  speed_kmh            numeric(6,1),
  heading              numeric(5,1),
  engine_on            boolean,
  position_description text,
  driver_name_raw      text,
  last_gps_at          timestamptz,
  odometer_km          numeric(12,1),
  fuel_level           numeric(8,2),
  fuel_percentage      numeric(5,2),
  updated_at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX vehicle_locations_tenant ON vehicle_locations (tenant_id, updated_at DESC);
ALTER TABLE vehicle_locations ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant + dispatcher read)

-- updated_at maintenance
CREATE TRIGGER tasks_set_updated_at             BEFORE UPDATE ON tasks             FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER trip_records_set_updated_at      BEFORE UPDATE ON trip_records      FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER trip_photos_set_updated_at       BEFORE UPDATE ON trip_photos       FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER standby_records_set_updated_at   BEFORE UPDATE ON standby_records   FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER incident_reports_set_updated_at  BEFORE UPDATE ON incident_reports  FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER vehicle_locations_set_updated_at BEFORE UPDATE ON vehicle_locations FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

-- Tenant consistency (Appendix C §C.3.6): a child keeps its parent's tenant, following the resolver order of
-- tenantResolve.ts (trip <- task; standby <- task, else trip; incident <- trip). Deferred to COMMIT so a
-- re-home or an R13 cross-tenant reassignment can move parent and children in one transaction.
-- Functions re-read the current row because a deferred event's NEW may be stale by commit time.
-- +goose StatementBegin
CREATE FUNCTION assert_tenant_matches(child_table text, child_id uuid, child_tenant uuid,
                                      parent_table text, parent_id uuid, parent_tenant uuid) RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
  IF parent_tenant IS NULL THEN
    RAISE EXCEPTION '% % references % % which is not visible to this session', child_table, child_id, parent_table, parent_id
      USING ERRCODE = 'insufficient_privilege';
  ELSIF parent_tenant <> child_tenant THEN
    RAISE EXCEPTION '% % has tenant % but its parent % % has tenant %',
      child_table, child_id, child_tenant, parent_table, parent_id, parent_tenant
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION trg_trip_tenant_check() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE c_tenant uuid; c_task uuid; p_tenant uuid;
BEGIN
  SELECT tenant_id, task_id INTO c_tenant, c_task FROM trip_records WHERE id = NEW.id;
  IF NOT FOUND OR c_task IS NULL THEN RETURN NULL; END IF;
  SELECT tenant_id INTO p_tenant FROM tasks WHERE id = c_task;
  PERFORM assert_tenant_matches('trip_records', NEW.id, c_tenant, 'tasks', c_task, p_tenant);
  RETURN NULL;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION trg_standby_tenant_check() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE c_tenant uuid; c_task uuid; c_trip uuid; p_tenant uuid;
BEGIN
  SELECT tenant_id, task_id, trip_id INTO c_tenant, c_task, c_trip FROM standby_records WHERE id = NEW.id;
  IF NOT FOUND THEN RETURN NULL; END IF;
  IF c_task IS NOT NULL THEN
    SELECT tenant_id INTO p_tenant FROM tasks WHERE id = c_task;
    PERFORM assert_tenant_matches('standby_records', NEW.id, c_tenant, 'tasks', c_task, p_tenant);
  ELSIF c_trip IS NOT NULL THEN
    SELECT tenant_id INTO p_tenant FROM trip_records WHERE id = c_trip;
    PERFORM assert_tenant_matches('standby_records', NEW.id, c_tenant, 'trip_records', c_trip, p_tenant);
  END IF;
  RETURN NULL;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION trg_incident_tenant_check() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE c_tenant uuid; c_trip uuid; p_tenant uuid;
BEGIN
  SELECT tenant_id, trip_id INTO c_tenant, c_trip FROM incident_reports WHERE id = NEW.id;
  IF NOT FOUND OR c_trip IS NULL THEN RETURN NULL; END IF;
  SELECT tenant_id INTO p_tenant FROM trip_records WHERE id = c_trip;
  PERFORM assert_tenant_matches('incident_reports', NEW.id, c_tenant, 'trip_records', c_trip, p_tenant);
  RETURN NULL;
END
$$;
-- +goose StatementEnd

-- A parent that changes tenant must take its children along in the same transaction.
-- +goose StatementBegin
CREATE FUNCTION trg_task_tenant_move_check() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE cur uuid; child uuid;
BEGIN
  SELECT tenant_id INTO cur FROM tasks WHERE id = NEW.id;
  IF NOT FOUND THEN RETURN NULL; END IF;
  SELECT id INTO child FROM trip_records WHERE task_id = NEW.id AND tenant_id <> cur LIMIT 1;
  IF FOUND THEN
    RAISE EXCEPTION 'task % is on tenant % but trip_records % is not', NEW.id, cur, child
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  SELECT id INTO child FROM standby_records WHERE task_id = NEW.id AND tenant_id <> cur LIMIT 1;
  IF FOUND THEN
    RAISE EXCEPTION 'task % is on tenant % but standby_records % is not', NEW.id, cur, child
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN NULL;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION trg_trip_tenant_move_check() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE cur uuid; child uuid;
BEGIN
  SELECT tenant_id INTO cur FROM trip_records WHERE id = NEW.id;
  IF NOT FOUND THEN RETURN NULL; END IF;
  SELECT id INTO child FROM incident_reports WHERE trip_id = NEW.id AND tenant_id <> cur LIMIT 1;
  IF FOUND THEN
    RAISE EXCEPTION 'trip % is on tenant % but incident_reports % is not', NEW.id, cur, child
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  SELECT id INTO child FROM standby_records WHERE trip_id = NEW.id AND task_id IS NULL AND tenant_id <> cur LIMIT 1;
  IF FOUND THEN
    RAISE EXCEPTION 'trip % is on tenant % but standby_records % is not', NEW.id, cur, child
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER trip_records_tenant_check AFTER INSERT OR UPDATE OF tenant_id, task_id ON trip_records
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION trg_trip_tenant_check();
CREATE CONSTRAINT TRIGGER standby_records_tenant_check AFTER INSERT OR UPDATE OF tenant_id, task_id, trip_id ON standby_records
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION trg_standby_tenant_check();
CREATE CONSTRAINT TRIGGER incident_reports_tenant_check AFTER INSERT OR UPDATE OF tenant_id, trip_id ON incident_reports
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION trg_incident_tenant_check();
CREATE CONSTRAINT TRIGGER tasks_tenant_move_check AFTER UPDATE OF tenant_id ON tasks
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION trg_task_tenant_move_check();
CREATE CONSTRAINT TRIGGER trip_records_tenant_move_check AFTER UPDATE OF tenant_id ON trip_records
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION trg_trip_tenant_move_check();

-- Appendix C block (same file): §C.3.5 "0004_operations" (DO block + policies of the 11 tables above); §C.3.7
-- scope_tasks, scope_trips, scope_standby, scope_incidents.

-- +goose StatementBegin
DO $$ BEGIN
  CALL app_rls_platform_table('task_number_counters');
  CALL app_rls_tenant_table('tasks');        CALL app_rls_tenant_table('trip_records');
  CALL app_rls_tenant_table('standby_records'); CALL app_rls_tenant_table('incident_reports');
  CALL app_rls_tenant_table('vehicle_locations');
  CALL app_rls_child_table('task_delivery_stops', 'tasks', 'task_id', driver_writes => true);
  CALL app_rls_child_table('trip_delivery_stops', 'trip_records', 'trip_id', driver_writes => true);
  CALL app_rls_child_table('trip_photos', 'trip_records', 'trip_id', driver_writes => true);
  CALL app_rls_child_table('trip_no_history', 'trip_records', 'trip_id', read_staff_only => true);
  CALL app_rls_child_table('standby_photos', 'standby_records', 'standby_id', driver_writes => true);
  CALL app_rls_driver_rows('trip_records', true, true);
  CALL app_rls_driver_rows('standby_records', true, false);
  CALL app_rls_driver_rows('incident_reports', true, true);
END $$;
-- +goose StatementEnd

-- tasks: own or helper task (R28 ownership; helper_driver_id is the scalar ADR 0011 cap-1 column)
CREATE POLICY p_driver_read ON tasks FOR SELECT
  USING (app_role() = 'driver' AND (driver_id = app_driver_id() OR helper_driver_id = app_driver_id()));
CREATE POLICY p_driver_update ON tasks FOR UPDATE                                    -- check-in, truck confirmation
  USING      (app_role() = 'driver' AND (driver_id = app_driver_id() OR helper_driver_id = app_driver_id()))
  WITH CHECK (app_role() = 'driver' AND (driver_id = app_driver_id() OR helper_driver_id = app_driver_id()));
CREATE POLICY p_driver_insert ON tasks FOR INSERT                                    -- manual check-in creates its own task
  WITH CHECK (app_role() = 'driver' AND driver_id = app_driver_id() AND tenant_id = app_tenant_id());
CREATE POLICY p_scope_read ON tasks FOR SELECT USING (app_is_scope() AND (
     ARRAY[billing_party_id, source_linked_party_id, destination_linked_party_id] && app_customer_ids()
  OR app_task_stops_in_scope(id)));

CREATE POLICY p_scope_read ON trip_records FOR SELECT USING (app_is_scope() AND (
     billing_party_id = ANY (app_customer_ids())                                     -- useDriverMonitor.ts:217 definition
  OR (task_id IS NOT NULL AND app_task_in_scope(task_id))));                         -- first-mile/page.tsx:262-266 definition

CREATE POLICY p_scope_read ON standby_records FOR SELECT USING (app_is_scope() AND (
     ARRAY[billing_party_id, customer_party_id] && app_customer_ids()
  OR (task_id IS NOT NULL AND app_task_in_scope(task_id))
  OR (trip_id IS NOT NULL AND app_trip_in_scope(trip_id))));

CREATE POLICY p_scope_read ON incident_reports FOR SELECT                            -- incidents of visible trips only
  USING (app_is_scope() AND trip_id IS NOT NULL AND app_trip_in_scope(trip_id));     -- (fixes firestore.rules:93-96)

CREATE POLICY p_dispatch_read ON vehicle_locations FOR SELECT                        -- fleet:view_live_map for dispatchers
  USING (app_is_dispatcher() AND app_recent_work_in_scope(NULL, truck_id));

-- §C.3.7: column projections for dispatcher and customer-scope principals (security_invoker: the caller's RLS applies).
CREATE VIEW scope_tasks WITH (security_invoker = true) AS
  SELECT id, tenant_id, task_no, task_type, job_category, status, plan_at, plan_date, plan_time, actual_pickup_at,
         source_hub_raw, source_hub_id, destination_raw, destination_hub_id, destination_soc_key,
         truck_id, truck_type, license_plate_snapshot, driver_id, helper_driver_id, run_order,
         check_in_at, check_in_photo_file_id, check_in_app_screenshot_file_id, check_in_lat, check_in_lng,
         is_multi_delivery, created_at, updated_at
    FROM tasks;                       -- hidden: billing/linked party ids, line_checkin_notified_at, created_by, legacy_*
CREATE VIEW scope_trips WITH (security_invoker = true) AS
  SELECT id, tenant_id, trip_no, status, job_type, job_category, task_id, driver_id,
         origin_raw, origin_hub_id, destination_raw, destination_hub_id, destination_soc_key, seal_code, partner_code,
         truck_id, truck_license_plate_snapshot, vehicle_class, parcel_count, total_weight_kg,
         loading_lat, loading_lng, std, sta, ata, delivered_at, delivered_lat, delivered_lng, is_multi_delivery,
         created_at, updated_at
    FROM trip_records;                -- hidden: billing_party_id, billing_date, evidence_token, review fields, ocr_data, line flags
CREATE VIEW scope_standby WITH (security_invoker = true) AS
  SELECT id, tenant_id, driver_id, task_id, trip_id, job_category, start_location_raw, end_location_raw,
         started_at, ended_at, duration_minutes, note, status, lat, lng, truck_id, truck_license_plate_snapshot, created_at
    FROM standby_records;             -- hidden: billing_*, customer_party_id, evidence_token
CREATE VIEW scope_incidents WITH (security_invoker = true) AS
  SELECT id, tenant_id, trip_id, driver_id, delay_cause, description, lat, lng, truck_id, truck_license_plate_snapshot,
         map_photo_file_id, situation1_photo_file_id, situation2_photo_file_id, reported_by_kind, created_at
    FROM incident_reports;

-- +goose Down
-- Appendix C views first: they read the tables below (IF EXISTS: runnable with or without them).
DROP VIEW IF EXISTS scope_incidents;
DROP VIEW IF EXISTS scope_standby;
DROP VIEW IF EXISTS scope_trips;
DROP VIEW IF EXISTS scope_tasks;
ALTER TABLE drivers DROP CONSTRAINT drivers_active_task_fk;
DROP TABLE vehicle_locations;
DROP TABLE incident_reports;
DROP TABLE standby_photos;
DROP TABLE standby_records;
DROP TABLE trip_photos;
DROP TABLE trip_delivery_stops;
DROP TABLE trip_no_history;
DROP TABLE trip_records;
DROP TABLE task_delivery_stops;
DROP TABLE tasks;
CALL app_drop_definer_function('next_task_seq(text,date)');
DROP TABLE task_number_counters;
DROP FUNCTION trg_trip_tenant_move_check();
DROP FUNCTION trg_task_tenant_move_check();
DROP FUNCTION trg_incident_tenant_check();
DROP FUNCTION trg_standby_tenant_check();
DROP FUNCTION trg_trip_tenant_check();
DROP FUNCTION assert_tenant_matches(text, uuid, uuid, text, uuid, uuid);
