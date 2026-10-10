-- 0008_platform.sql: Appendix A §A.2.7 plus the Appendix C block of this file (end of the Up section).
-- Follows Appendix A until the P0 deploy applies the baseline to a database that is kept; after that it is
-- never edited and follow-ups take the next free number (as 0001_preamble.sql).
-- Depends on: 0001 (trg_set_updated_at, trg_forbid_mutation, app_* GUC readers, app_rls_* generators),
--             0002 (tenants, users, file_objects), 0003 (drivers). No GRANT/REVOKE here (R66).
-- RLS (Appendix C §C.3.0): security_events, mobile_installations, holidays, leave_requests, leave_request_attachments.
-- Exempt (service layer only, grants in 0009): settings, mobile_app_releases, waitlist, partner_interest,
-- fuel_daily_snapshots, fuel_monthly_snapshots.

-- +goose Up

-- 'YYYY-MM' of a date. to_char() is STABLE, so it cannot back a generated column; this is integer arithmetic only.
CREATE FUNCTION yyyy_mm(d date) RETURNS text
  LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
  RETURN lpad(extract(year FROM d)::int::text, 4, '0') || '-' || lpad(extract(month FROM d)::int::text, 2, '0');

CREATE TABLE settings (                                      -- key/value platform settings (R7: no platform_settings table)
  key        text PRIMARY KEY CHECK (key ~ '^[a-z][a-z0-9_]*$'),   -- 'mobile_app', 'distances_last_calculated'
  value      jsonb NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),
  updated_by uuid REFERENCES users(id)
);
CREATE TRIGGER settings_updated_at BEFORE UPDATE ON settings FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

CREATE TABLE mobile_app_releases (                           -- script-owned half of settings/mobile_app (ADR 0007), one row per release
  id             uuid PRIMARY KEY DEFAULT uuidv7(),
  flavor         text NOT NULL CHECK (flavor IN ('dev','prod')),
  version        text NOT NULL CHECK (version ~ '^[0-9]+\.[0-9]+\.[0-9]+$'),   -- strict semver, no label (pub_semver gate)
  build_number   text NOT NULL CHECK (build_number ~ '^[0-9]+$'),
  apk_file_id    uuid NOT NULL REFERENCES file_objects(id),  -- object in the public bucket under app_releases/
  apk_size_bytes bigint NOT NULL CHECK (apk_size_bytes > 0),
  apk_sha256     text NOT NULL CHECK (apk_sha256 ~ '^[0-9a-f]{64}$'),
  release_notes  text,
  released_at    timestamptz NOT NULL,
  released_by    text NOT NULL,                              -- api_keys name (scope release_publisher, R82) or user email
  created_at     timestamptz NOT NULL DEFAULT now(),
  UNIQUE (flavor, version)
);
CREATE INDEX mobile_app_releases_latest ON mobile_app_releases (flavor, released_at DESC);

CREATE TABLE security_events (                               -- append-only audit log
  id               uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id    text,
  created_at       timestamptz NOT NULL DEFAULT now(),
  event_type       text NOT NULL CHECK (event_type ~ '^[a-z][a-z0-9_]*$'),
  severity         text NOT NULL DEFAULT 'info' CHECK (severity IN ('info','warning','critical')),
  summary          text NOT NULL,
  details          jsonb NOT NULL DEFAULT '{}',
  actor_user_id    uuid REFERENCES users(id),
  actor_email      text,
  actor_legacy_uid text,                                     -- legacy actorUid that no users row matched
  target_user_id   uuid REFERENCES users(id),
  tenant_id        uuid REFERENCES tenants(id),              -- NULL = platform-level event (R12)
  request_id       text
);
CREATE UNIQUE INDEX security_events_legacy ON security_events (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX security_events_feed          ON security_events (created_at DESC, id DESC);
CREATE INDEX security_events_type_created  ON security_events (event_type, created_at DESC);
CREATE INDEX security_events_tenant        ON security_events (tenant_id, created_at DESC) WHERE tenant_id IS NOT NULL;
CREATE INDEX security_events_actor         ON security_events (actor_user_id, created_at DESC) WHERE actor_user_id IS NOT NULL;
CREATE INDEX security_events_created_brin  ON security_events USING brin (created_at);
CREATE TRIGGER security_events_immutable BEFORE UPDATE OR DELETE ON security_events
  FOR EACH ROW EXECUTE FUNCTION trg_forbid_mutation();

CREATE TABLE mobile_installations (                          -- drivers/{id}/mobile_installations/{installId} flattened
  driver_id     uuid NOT NULL REFERENCES drivers(id),
  install_id    text NOT NULL,
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  tenant_source text NOT NULL DEFAULT 'driver'
                CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  platform      text NOT NULL CHECK (platform IN ('ios','android','other')),
  app_version   text,
  build_number  text,
  flavor        text NOT NULL DEFAULT 'dev' CHECK (flavor IN ('dev','prod')),
  first_seen_at timestamptz NOT NULL DEFAULT now(),
  last_seen_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (driver_id, install_id)                        -- R33: a guessed install id cannot overwrite another driver's row
);
CREATE INDEX mobile_installations_seen        ON mobile_installations (last_seen_at DESC, install_id);
CREATE INDEX mobile_installations_tenant_seen ON mobile_installations (tenant_id, last_seen_at DESC);
CREATE INDEX mobile_installations_install     ON mobile_installations (install_id);

CREATE TABLE holidays (
  id             uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id  text,
  tenant_id      uuid REFERENCES tenants(id),                -- NULL = public holiday for every tenant (R12)
  holiday_date   date NOT NULL,
  holiday_type   text NOT NULL DEFAULT 'public' CHECK (holiday_type IN ('public','company','other')),
  status         text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','published','archived')),
  name           text NOT NULL,
  name_en        text,
  name_th        text,
  description    text,
  description_en text,
  description_th text,
  is_recurring   boolean NOT NULL DEFAULT false,
  created_by     uuid REFERENCES users(id),
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  CHECK ((holiday_type = 'public') = (tenant_id IS NULL)),
  UNIQUE NULLS NOT DISTINCT (tenant_id, holiday_date, holiday_type)   -- upsert key date|type (holidays.ts:98-113)
);
CREATE UNIQUE INDEX holidays_legacy ON holidays (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX holidays_date          ON holidays (holiday_date);
CREATE TRIGGER holidays_updated_at BEFORE UPDATE ON holidays FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

CREATE TABLE leave_requests (
  id                uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id     text,
  tenant_id         uuid NOT NULL REFERENCES tenants(id),
  tenant_source     text NOT NULL DEFAULT 'driver'
                    CHECK (tenant_source IN ('task','trip','driver','truck','self','form','quarantine')),
  driver_id         uuid REFERENCES drivers(id),
  legacy_driver_ref text,
  driver_ref_match  text CHECK (driver_ref_match IN ('doc_id','auth_uid','none')),
  leave_type        text NOT NULL DEFAULT 'sick' CHECK (leave_type IN ('sick','business')),
  status            text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected','cancelled')),
  start_date        date NOT NULL,
  end_date          date NOT NULL,
  reason            text NOT NULL DEFAULT '',
  approver_user_id  uuid REFERENCES users(id),
  decided_at        timestamptz,                             -- legacy approvedAt (also set on rejected)
  rejection_reason  text,
  client_op_id      uuid,                                    -- offline outbox op id, body `clientOpId` (R63)
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  CHECK (end_date >= start_date),
  CHECK (driver_id IS NOT NULL OR tenant_source = 'quarantine')
);
CREATE UNIQUE INDEX leave_requests_legacy    ON leave_requests (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE UNIQUE INDEX leave_requests_client_op ON leave_requests (driver_id, client_op_id)
  WHERE client_op_id IS NOT NULL;                            -- durable offline replay key (R63)
CREATE INDEX leave_requests_driver        ON leave_requests (driver_id, created_at DESC);
CREATE INDEX leave_requests_tenant        ON leave_requests (tenant_id, status, created_at DESC, id DESC);
CREATE TRIGGER leave_requests_updated_at BEFORE UPDATE ON leave_requests FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

CREATE TABLE leave_request_attachments (
  leave_request_id uuid NOT NULL REFERENCES leave_requests(id) ON DELETE CASCADE,
  file_id          uuid NOT NULL REFERENCES file_objects(id),
  position         int NOT NULL DEFAULT 0,
  PRIMARY KEY (leave_request_id, file_id)
);

CREATE TABLE waitlist (                                      -- anonymous landing form (exempt, R44, R77)
  id            uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id text,
  email         citext NOT NULL,
  name          text NOT NULL,
  country_code  text NOT NULL DEFAULT '+66',
  phone         text,
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX waitlist_legacy ON waitlist (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX waitlist_created       ON waitlist (created_at DESC, id DESC);
CREATE INDEX waitlist_email         ON waitlist (email);

CREATE TABLE partner_interest (                              -- collection 'partner-interest' (exempt, R44, R77)
  id            uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id text,
  payload       jsonb NOT NULL,                              -- UNVERIFIED: field set not in COLLECTIONS or any Zod schema
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX partner_interest_legacy ON partner_interest (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE INDEX partner_interest_created       ON partner_interest (created_at DESC, id DESC);

CREATE TABLE fuel_daily_snapshots (                          -- create-only observation (ADR 0009 §5); insert-only
  day_key     date PRIMARY KEY,
  month_key   text GENERATED ALWAYS AS (yyyy_mm(day_key)) STORED,
  captured_at timestamptz NOT NULL,
  fetched_at  timestamptz,
  source      text NOT NULL,
  locale      text NOT NULL DEFAULT 'th' CHECK (locale IN ('th','en')),
  status      text NOT NULL DEFAULT 'ok' CHECK (status = 'ok'),
  items       jsonb NOT NULL                                 -- [{nameTh, nameEn, price, unit}]
);
CREATE INDEX fuel_daily_snapshots_month ON fuel_daily_snapshots (month_key, day_key DESC);
CREATE TRIGGER fuel_daily_snapshots_immutable BEFORE UPDATE OR DELETE ON fuel_daily_snapshots
  FOR EACH ROW EXECUTE FUNCTION trg_forbid_mutation();

CREATE TABLE fuel_monthly_snapshots (                        -- upserted only on a successful fetch; not a billing input
  month_key     text PRIMARY KEY CHECK (month_key ~ '^[0-9]{4}-[0-9]{2}$'),
  captured_at   timestamptz NOT NULL,
  fetched_at    timestamptz,
  source        text NOT NULL,
  locale        text NOT NULL DEFAULT 'th' CHECK (locale IN ('th','en')),
  status        text NOT NULL CHECK (status IN ('ok','error')),   -- 'error' only on legacy rows
  error_message text,
  items         jsonb NOT NULL DEFAULT '[]',
  updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE TRIGGER fuel_monthly_snapshots_updated_at BEFORE UPDATE ON fuel_monthly_snapshots
  FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

-- Row level security: Appendix C §C.3.0 marks these five tables "yes"; the other six tables of this file are exempt
-- (no ENABLE; exact grants in 0009).
ALTER TABLE security_events           ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- nullable-tenant
ALTER TABLE mobile_installations      ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- tenant
ALTER TABLE holidays                  ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- nullable-tenant
ALTER TABLE leave_requests            ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- tenant
ALTER TABLE leave_request_attachments ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;  -- parent-scoped: leave_requests
-- Appended here, in this file: the Appendix C §C.3.5 "0008_platform" block (generator CALLs and the security-event,
-- holiday and leave-request policies).

-- settings, mobile_app_releases, waitlist, partner_interest, fuel_daily_snapshots, fuel_monthly_snapshots are exempt
-- (no call).
-- +goose StatementBegin
DO $$ BEGIN
  CALL app_rls_platform_table('security_events');    CALL app_rls_platform_table('holidays');
  CALL app_rls_tenant_table('mobile_installations'); CALL app_rls_tenant_table('leave_requests');
  CALL app_rls_child_table('leave_request_attachments', 'leave_requests', 'leave_request_id', driver_writes => true);
  CALL app_rls_driver_rows('mobile_installations', true, true);   -- PK (driver_id, install_id) (R33)
  CALL app_rls_driver_rows('leave_requests', false, false);
END $$;
-- +goose StatementEnd

CREATE POLICY p_staff_read ON security_events FOR SELECT                              -- writes: WithSystem only (C.4.13)
  USING (app_is_staff() AND tenant_id = app_tenant_id());

-- holidays: tenant_id NULL = PUBLIC calendar (stewards, R60), otherwise company / other rows of a tenant
CREATE POLICY p_read  ON holidays FOR SELECT USING (
  app_is_authenticated() AND (tenant_id IS NULL OR tenant_id = app_tenant_id() OR (app_is_staff() AND app_tenant_in_reach(tenant_id))));
CREATE POLICY p_write ON holidays
  USING      ((app_is_staff() AND app_tenant_in_reach(tenant_id)) OR (tenant_id IS NULL AND app_is_steward()))
  WITH CHECK ((app_is_staff() AND app_tenant_in_reach(tenant_id)) OR (tenant_id IS NULL AND app_is_steward()));

CREATE POLICY p_driver_insert ON leave_requests FOR INSERT
  WITH CHECK (app_role() = 'driver' AND driver_id = app_driver_id() AND tenant_id = app_tenant_id() AND status = 'pending');
CREATE POLICY p_driver_update ON leave_requests FOR UPDATE                            -- cancel own pending request
  USING (app_role() = 'driver' AND driver_id = app_driver_id() AND status = 'pending')
  WITH CHECK (app_role() = 'driver' AND driver_id = app_driver_id() AND status IN ('pending','cancelled'));

-- +goose Down
DROP TABLE fuel_monthly_snapshots;
DROP TABLE fuel_daily_snapshots;
DROP TABLE partner_interest;
DROP TABLE waitlist;
DROP TABLE leave_request_attachments;
DROP TABLE leave_requests;
DROP TABLE holidays;
DROP TABLE mobile_installations;
DROP TABLE security_events;
DROP TABLE mobile_app_releases;
DROP TABLE settings;
DROP FUNCTION yyyy_mm(date);
