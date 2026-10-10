-- 0009_infra.sql: Appendix A §A.2.8 (no RLS table; single grant site of the baseline).
-- Follows Appendix A until the P0 deploy applies the baseline to a database that is kept; after that it is
-- never edited and follow-ups take the next free number (as 0001_preamble.sql).
-- Depends on: 0001 (schema etl, trg_outbox_notify, role assertion), 0002-0008 (every object granted below).
-- Roles come from the superuser script deploy/postgres-init/00-roles.sql (R66); no migration creates or alters a role.
-- Single grant site (R66): 0001-0008 contain no GRANT/REVOKE; a later migration (0011+) grants its own new tables.
-- Ids: uuid DEFAULT uuidv7(); the only identity column of the schema is outbox_events.id (R57).

-- +goose Up

CREATE TABLE outbox_events (                                 -- transactional outbox -> RabbitMQ + Redis realtime (relay in `scheduler`)
  id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,   -- relay order; AMQP message_id = id::text (R57)
  event_id        uuid NOT NULL DEFAULT uuidv7(),            -- stable envelope id exposed to consumers and SSE payloads
  exchange        text NOT NULL DEFAULT 'lt.events' CHECK (exchange IN ('lt.events','lt.jobs')),   -- relay targets only
  routing_key     text NOT NULL,                             -- 'trip.delivered', 'job.billing.backfill-trips', ...
  aggregate_type  text NOT NULL,                             -- 'trip','task','standby','chat','statement','hub','settings',...
  aggregate_id    text NOT NULL,                             -- text: uuid, settings key, fuel day_key, job id (R14)
  event_type      text NOT NULL,
  tenant_id       uuid REFERENCES tenants(id),               -- NULL = platform-level event (R12)
  payload         jsonb NOT NULL,
  headers         jsonb NOT NULL DEFAULT '{}',               -- traceparent, actor, idempotency key
  realtime_topics text[] NOT NULL DEFAULT '{}',              -- SSE topics the relay XADDs / PUBLISHes (Appendix B §B.4)
  created_at      timestamptz NOT NULL DEFAULT now(),
  published_at    timestamptz,
  attempts        int NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  last_error      text
);
CREATE UNIQUE INDEX outbox_events_event_id  ON outbox_events (event_id);
CREATE INDEX outbox_events_unpublished      ON outbox_events (id) WHERE published_at IS NULL;
CREATE INDEX outbox_events_aggregate        ON outbox_events (aggregate_type, aggregate_id, id);
CREATE INDEX outbox_events_published        ON outbox_events (published_at) WHERE published_at IS NOT NULL;   -- outbox.prune
CREATE TRIGGER outbox_events_notify AFTER INSERT ON outbox_events
  FOR EACH ROW EXECUTE FUNCTION trg_outbox_notify();

CREATE TABLE consumer_inbox (                                -- idempotent consumers: insert in the same tx as the side effect (R14, R17)
  consumer     text NOT NULL,                                -- queue name, e.g. 'billing.compute'
  message_id   text NOT NULL,                                -- AMQP message_id = outbox_events.id::text (events and jobs alike)
  processed_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (consumer, message_id)
);
CREATE INDEX consumer_inbox_processed ON consumer_inbox (processed_at);

CREATE TABLE idempotency_keys (                              -- durable copy of Redis lt:{APP_ENV}:idem:http:* (R53)
  scope         text NOT NULL,                               -- principal user id: key scope (user_id, key), Appendix B §B.1.5
  key           text NOT NULL,                               -- Idempotency-Key header (uuid)
  request_hash  text NOT NULL,                               -- sha256(method + path + body)
  status        text NOT NULL CHECK (status IN ('in_progress','completed')),
  response_code int,
  response_body jsonb,
  created_at    timestamptz NOT NULL DEFAULT now(),
  expires_at    timestamptz NOT NULL,                        -- created_at + IDEMPOTENCY_TTL (default 168h)
  PRIMARY KEY (scope, key),
  CHECK (status <> 'completed' OR response_code IS NOT NULL),
  CHECK (expires_at > created_at)
);
CREATE INDEX idempotency_keys_expires ON idempotency_keys (expires_at);   -- idempotency.prune

CREATE TABLE jobs (                                          -- long-running admin and scheduler jobs (R14, R64)
  id            uuid PRIMARY KEY DEFAULT uuidv7(),
  type          text NOT NULL CHECK (type ~ '^[a-z]+(\.[a-z0-9-]+)+$'),   -- 'billing.backfill-trips', 'payroll.run', ...
  status        text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','succeeded','failed')),
  owner_user_id uuid REFERENCES users(id),                   -- NULL = enqueued by the scheduler
  tenant_id     uuid REFERENCES tenants(id),                 -- NULL = platform-wide job
  params        jsonb NOT NULL DEFAULT '{}',                 -- input parameters as accepted at enqueue (R64)
  progress      jsonb NOT NULL DEFAULT '{}',                 -- {"done":n,"total":m}
  result        jsonb,                                       -- job-specific summary
  error         text,
  created_at    timestamptz NOT NULL DEFAULT now(),
  started_at    timestamptz,
  finished_at   timestamptz,
  CHECK (jsonb_typeof(params) = 'object'),
  CHECK (finished_at IS NULL OR status IN ('succeeded','failed')),
  CHECK (status <> 'running' OR started_at IS NOT NULL)
);
CREATE INDEX jobs_owner_created ON jobs (owner_user_id, created_at DESC, id DESC);
CREATE INDEX jobs_type_created  ON jobs (type, created_at DESC, id DESC);
CREATE INDEX jobs_tenant_created ON jobs (tenant_id, created_at DESC, id DESC) WHERE tenant_id IS NOT NULL;
CREATE INDEX jobs_active        ON jobs (type) WHERE status IN ('queued','running');

-- ETL bookkeeping (schema etl from 0001; owned by logitrack_migrator like every object; DML for logitrack_etl,
-- SELECT for logitrack_readonly, nothing for logitrack_app)
CREATE TABLE etl.source_docs (                               -- every dumped Firestore doc verbatim; also the legacy-id -> row map
  collection         text NOT NULL,
  doc_path           text NOT NULL,                          -- full path incl. parents (drivers/{id}/mobile_installations/{iid})
  doc_id             text NOT NULL,
  raw                jsonb NOT NULL,                         -- type-tagged NDJSON fields ({"$ts":..}, {"$geo":..}, {"$ref":..})
  source_create_time timestamptz,                            -- Firestore createTime (trip created_at fallback, R68)
  source_update_time timestamptz NOT NULL,                   -- Firestore updateTime; loads apply only when newer
  exported_at        timestamptz NOT NULL,
  imported_at        timestamptz,
  target_table       text,
  target_id          text,                                   -- uuid, or composite key 'driver_id/install_id' for composite-PK tables
  status             text NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending','loaded','quarantined','rejected','dropped')),
  PRIMARY KEY (collection, doc_path)
);
CREATE INDEX etl_source_docs_status ON etl.source_docs (status, collection);
CREATE INDEX etl_source_docs_target ON etl.source_docs (target_table, target_id);

CREATE TABLE etl.quarantine (                                -- field- and row-level findings (§A.3.0)
  id           uuid PRIMARY KEY DEFAULT uuidv7(),
  collection   text NOT NULL,
  doc_path     text NOT NULL,
  field        text,                                         -- NULL = whole-row finding
  reason_code  text NOT NULL CHECK (reason_code IN (         -- the canonical list of §A.3.0 (R68); synonyms never written
                 'tenant_unresolved','driver_unresolved','user_unresolved','truck_unresolved','task_unresolved',
                 'task_ambiguous','trip_unresolved','party_unresolved','customer_unresolved','hub_unresolved',
                 'tenant_mismatch','helper_overflow','helper_tenant_mismatch','missing_required',
                 'bad_timestamp','bad_number','negative_money','status_out_of_vocab','vehicle_class_out_of_enum',
                 'photo_type_unknown','file_missing_at_source','url_unparseable',
                 'duplicate_natural_key','duplicate_service_fee','duplicate_hub_source_id',
                 'created_at_derived','missing_delivered_at','missing_billing_date','missing_ended_at',
                 'billing_date_locked','trip_no_mismatch','multidrop_priced_as_single','link_blanket_default',
                 'legacy_standby_trip','unknown_capability_key','unknown_collection')),
  detail       text,
  raw_value    jsonb,
  created_at   timestamptz NOT NULL DEFAULT now(),
  resolved_at  timestamptz,
  resolved_by  text,
  resolution   text CHECK (resolution IN ('retried','accepted','rehomed','skipped','fixed_at_source')),
  CHECK ((resolved_at IS NULL) = (resolution IS NULL))
);
CREATE INDEX etl_quarantine_open ON etl.quarantine (reason_code, collection) WHERE resolved_at IS NULL;
CREATE INDEX etl_quarantine_doc  ON etl.quarantine (collection, doc_path);

CREATE TABLE etl.watermarks (                                -- FS->PG mirror / etl.sync position per collection
  collection       text PRIMARY KEY,
  last_update_time timestamptz NOT NULL,
  last_doc_path    text,                                     -- tie-break inside one updateTime (collection-group safe)
  updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE etl.reconciliation_runs (                       -- `etl reconcile` results (counts + money sums)
  id          uuid PRIMARY KEY DEFAULT uuidv7(),
  period      text CHECK (period ~ '^[0-9]{4}-[0-9]{2}$'),
  started_at  timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz,
  outcome     text CHECK (outcome IN ('match','mismatch','error')),
  report      jsonb NOT NULL DEFAULT '{}'
);

-- ===== Grants: the single grant site of the baseline (R66; exact privileges of Appendix C §C.3.2) =====

-- Guard (R67): 70 tables with ENABLE + FORCE and exactly the 11 exempt tables without RLS (Appendix C §C.3.0).
-- goose's own goose_db_version (public, no RLS, §A.2.0 notes) is not an Appendix A table and is skipped by name.
-- +goose StatementBegin
DO $$
DECLARE
  exempt text[] := ARRAY['notification_deliveries','settings','mobile_app_releases','waitlist','partner_interest',
                         'fuel_daily_snapshots','fuel_monthly_snapshots','outbox_events','consumer_inbox',
                         'idempotency_keys','jobs'];
  n_rls  int;
  bad    text;
BEGIN
  SELECT count(*) INTO n_rls
    FROM pg_class c JOIN pg_namespace s ON s.oid = c.relnamespace
   WHERE s.nspname = 'public' AND c.relkind = 'r' AND c.relrowsecurity AND c.relforcerowsecurity;
  SELECT string_agg(c.relname::text, ', ' ORDER BY c.relname) INTO bad
    FROM pg_class c JOIN pg_namespace s ON s.oid = c.relnamespace
   WHERE s.nspname = 'public' AND c.relkind = 'r' AND c.relname <> 'goose_db_version'
     AND (c.relrowsecurity AND c.relforcerowsecurity) = (c.relname::text = ANY (exempt));
  IF n_rls <> 70 OR bad IS NOT NULL THEN
    RAISE EXCEPTION 'RLS layout differs from Appendix C §C.3.0: % tables with ENABLE + FORCE (want 70), mismatched: %',
      n_rls, coalesce(bad, 'none') USING ERRCODE = 'invalid_object_definition';
  END IF;
END
$$;
-- +goose StatementEnd

-- logitrack_app (api, worker, scheduler): DML on the RLS tables (policies decide the rows) except the two counters,
-- which only the SECURITY DEFINER allocators touch (R10, R67).
GRANT USAGE ON SCHEMA public TO logitrack_app;
-- +goose StatementBegin
DO $$
DECLARE t regclass;
BEGIN
  FOR t IN SELECT c.oid::regclass
             FROM pg_class c JOIN pg_namespace s ON s.oid = c.relnamespace
            WHERE s.nspname = 'public' AND c.relkind = 'r' AND c.relrowsecurity AND c.relforcerowsecurity
              AND c.relname::text NOT IN ('task_number_counters','billing_counters')
            ORDER BY c.relname
  LOOP
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %s TO logitrack_app', t);
  END LOOP;
END
$$;
-- +goose StatementEnd
-- Exempt tables: exactly the privileges listed in Appendix C §C.3.2, nothing else.
GRANT SELECT, INSERT, UPDATE, DELETE ON outbox_events, idempotency_keys, jobs       TO logitrack_app;   -- jobs: jobs.prune (T10)
GRANT SELECT, INSERT, DELETE         ON consumer_inbox, waitlist                    TO logitrack_app;
GRANT SELECT, INSERT, UPDATE         ON notification_deliveries, settings,
                                        fuel_monthly_snapshots                      TO logitrack_app;
GRANT SELECT, INSERT                 ON mobile_app_releases, partner_interest,
                                        fuel_daily_snapshots                        TO logitrack_app;
-- Projection views for dispatcher and customer-scope principals (security_invoker, Appendix C §C.3.7).
GRANT SELECT ON scope_tenants, scope_drivers, scope_trucks, scope_tasks, scope_trips, scope_standby, scope_incidents
  TO logitrack_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO logitrack_app;     -- the outbox_events identity (R57)
-- /startupz compares the applied goose version with the version the binary needs (Appendix B), so the API login
-- reads goose's bookkeeping table; goose creates it before 0001 and it is absent only outside goose.
-- +goose StatementBegin
DO $$ BEGIN
  IF to_regclass('public.goose_db_version') IS NOT NULL THEN
    GRANT SELECT ON public.goose_db_version TO logitrack_app;
  END IF;
END $$;
-- +goose StatementEnd

-- logitrack_etl (cmd/etl and cmd/seed writes, R87; BYPASSRLS): DML on both schemas; no TRUNCATE (skips row triggers).
GRANT USAGE ON SCHEMA public, etl TO logitrack_etl;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO logitrack_etl;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA etl    TO logitrack_etl;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public, etl TO logitrack_etl;
-- The blanket grant also reached goose's bookkeeping table: the ETL login must never rewrite migration state.
-- +goose StatementBegin
DO $$ BEGIN
  IF to_regclass('public.goose_db_version') IS NOT NULL THEN
    REVOKE INSERT, UPDATE, DELETE ON public.goose_db_version FROM logitrack_etl;
  END IF;
END $$;
-- +goose StatementEnd

-- logitrack_readonly (reporting tools, operator-held): SELECT on both schemas; RLS still applies (Appendix C §C.3.2).
GRANT USAGE ON SCHEMA public, etl TO logitrack_readonly;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO logitrack_readonly;
GRANT SELECT ON ALL TABLES IN SCHEMA etl    TO logitrack_readonly;

-- Append-only and void-only tables (§A.1.8): revoked after the grants above, from both writing roles.
REVOKE UPDATE, DELETE ON status_history, trip_no_history, billing_statement_lines, transactions,
                         security_events, fuel_daily_snapshots
  FROM logitrack_app, logitrack_etl;
REVOKE DELETE ON customer_rate_entries, customer_fuel_rate_adjustments, standby_rate_entries
  FROM logitrack_app, logitrack_etl;

-- SECURITY DEFINER functions (Appendix C §C.3.3, §C.3.6, §C.3.7; allocators R10, R61): ACL first, then ownership,
-- because logitrack_migrator holds logitrack_rls_definer WITH INHERIT FALSE and cannot change the ACL of a function
-- it no longer owns (Notes 0009). Functions the definer already owns are skipped (re-run after Down).
GRANT CREATE ON SCHEMA public TO logitrack_rls_definer;      -- ALTER ... OWNER TO: the new owner needs CREATE here
-- +goose StatementBegin
DO $$
DECLARE
  f       regprocedure;
  fname   name;
  f_owner oid;
BEGIN
  FOREACH f IN ARRAY ARRAY['app_task_stops_in_scope(uuid)', 'app_task_in_scope(uuid)', 'app_trip_in_scope(uuid)',
                           'app_recent_work_in_scope(uuid,uuid)', 'driver_directory()',
                           'trg_driver_link_membership()', 'next_task_seq(text,date)',
                           'next_invoice_seq(uuid,uuid,int,int,text)']::regprocedure[]
  LOOP
    SELECT p.proname, p.proowner INTO fname, f_owner FROM pg_proc p WHERE p.oid = f;
    CONTINUE WHEN f_owner = 'logitrack_rls_definer'::regrole::oid;
    EXECUTE format('REVOKE ALL ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO logitrack_app', f);
    IF starts_with(fname::text, 'app_') THEN   -- policy helpers: every role that evaluates policies (Notes 0009)
      EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO logitrack_readonly, pg_database_owner', f);
    ELSIF starts_with(fname::text, 'next_') THEN
      EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO logitrack_etl', f);   -- cmd/seed numbers tasks and invoices (R87)
    END IF;
    EXECUTE format('ALTER FUNCTION %s OWNER TO logitrack_rls_definer', f);
  END LOOP;
END
$$;
-- +goose StatementEnd
REVOKE CREATE ON SCHEMA public FROM logitrack_rls_definer;
-- What the definer functions read and write (logitrack_rls_definer is BYPASSRLS, so no policy applies to it).
GRANT SELECT ON tasks, task_delivery_stops, trip_records, drivers, memberships TO logitrack_rls_definer;
GRANT SELECT, INSERT, UPDATE ON task_number_counters, billing_counters TO logitrack_rls_definer;

-- +goose Down
-- Function ownership and ACLs stay with logitrack_rls_definer (the migrator cannot take ownership back under INHERIT
-- FALSE); the Downs of 0003-0005 drop those functions as their owner through app_drop_definer_function() (0001).
REVOKE ALL ON ALL TABLES    IN SCHEMA etl    FROM logitrack_etl, logitrack_readonly;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA etl    FROM logitrack_etl;
REVOKE ALL ON ALL TABLES    IN SCHEMA public FROM logitrack_app, logitrack_etl, logitrack_readonly, logitrack_rls_definer;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA public FROM logitrack_app, logitrack_etl;
REVOKE USAGE ON SCHEMA etl    FROM logitrack_etl, logitrack_readonly;
REVOKE USAGE ON SCHEMA public FROM logitrack_app, logitrack_etl, logitrack_readonly;
DROP TABLE etl.reconciliation_runs;
DROP TABLE etl.watermarks;
DROP TABLE etl.quarantine;
DROP TABLE etl.source_docs;
DROP TABLE jobs;
DROP TABLE idempotency_keys;
DROP TABLE consumer_inbox;
DROP TABLE outbox_events;
