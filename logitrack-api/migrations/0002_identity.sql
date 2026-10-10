-- 0002_identity.sql: Appendix A §A.2.1 plus the Appendix C block of this file (end of the Up section).
-- Follows Appendix A until the P0 deploy applies the baseline to a database that is kept; after that it is
-- never edited and follow-ups take the next free number (as 0001_preamble.sql).
-- +goose Up

-- Carrier organisations (ADR 0026 semantics: text not on disk; semantics from branch glossary + tenantResolve.ts).
-- One row per legacy subcontractors doc (kind='carrier'), one own_fleet row, one quarantine row (R6/R7/R11).
CREATE TABLE tenants (
  id                uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_doc_id     text,                                  -- subcontractors/{id} (= legacy partnerScopeId value)
  kind              text NOT NULL CHECK (kind IN ('own_fleet','carrier','quarantine')),
  code              citext,                                -- short label (e.g. WRT); UNVERIFIED: subcontractors.code is read by
                                                           -- logitrack-web/functions/src/lineNotify.ts:141 but has no writer
  name_th           text NOT NULL,                         -- subcontractors.name
  name_en           text,
  -- carrier profile folded from subcontractors (no separate subcontractors table, R6)
  legal_type        text CHECK (legal_type IN ('individual','company')),   -- subcontractors.type
  id_card_number    text,                                  -- 13 digits when individual (PII)
  tax_id            text,                                  -- 13 digits when company
  contact_person    text,
  phone             text,
  email             citext,
  website           text,
  address           text,
  designation       text,
  fleet_size        int  NOT NULL DEFAULT 0 CHECK (fleet_size >= 0),
  dispatch_center   text,
  service_regions   text[] NOT NULL DEFAULT '{}',
  vehicle_types     text[] NOT NULL DEFAULT '{}',
  line_group_id     text,                                  -- LINE group id 'C...' (LINE push target)
  contractor_tenant_id uuid REFERENCES tenants(id),        -- contractor reach (R56/R60): the tenant this carrier works for;
                                                           -- own fleet for every legacy subcontractors doc; one level only
  status            text NOT NULL DEFAULT 'active' CHECK (status IN ('active','pending','suspended')),
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  CHECK (kind <> 'carrier' OR legal_type IS NOT NULL),
  CONSTRAINT tenants_contractor_carrier_only CHECK (contractor_tenant_id IS NULL OR kind = 'carrier'),
  CONSTRAINT tenants_contractor_not_self     CHECK (contractor_tenant_id IS DISTINCT FROM id)
);
CREATE UNIQUE INDEX tenants_one_own_fleet  ON tenants (kind) WHERE kind = 'own_fleet';
CREATE UNIQUE INDEX tenants_one_quarantine ON tenants (kind) WHERE kind = 'quarantine';
CREATE UNIQUE INDEX tenants_legacy         ON tenants (legacy_doc_id) WHERE legacy_doc_id IS NOT NULL;
CREATE UNIQUE INDEX tenants_code           ON tenants (code) WHERE code IS NOT NULL;
CREATE INDEX tenants_kind_status           ON tenants (kind, status, name_th);
CREATE INDEX tenants_contractor            ON tenants (contractor_tenant_id) WHERE contractor_tenant_id IS NOT NULL;

-- Structural quarantine tenant (R56, Appendix C §C.1.8), id == app_quarantine_tenant_id(). Inserted before RLS is
-- enabled: a forced table without policies denies the owner too. The own-fleet row is seed/ETL data (R7).
INSERT INTO tenants (id, kind, name_th, name_en, status)
VALUES ('00000000-0000-7000-8000-00000000000f', 'quarantine', 'กักกันข้อมูล', 'Quarantine', 'active');
ALTER TABLE tenants ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (keyed on id)

CREATE TABLE users (
  id                     uuid PRIMARY KEY DEFAULT uuidv7(),
  legacy_auth_uid        text,                             -- Firebase Auth uid == users/{uid}; ETL join key and bridge key (R8, R55); kept forever
  email                  citext,                           -- UNVERIFIED: that every Firebase Auth account has an email (export not inspected);
                                                           -- nullable so such an account still loads (it cannot use password login until set)
  email_verified         boolean NOT NULL DEFAULT false,
  display_name           text,
  photo_file_id          uuid,                             -- FK users_photo_file_fk added after file_objects
  password_hash          text,                             -- Argon2id PHC string; NULL until first-login rehash, or Google-only
  password_changed_at    timestamptz,
  must_change_password   boolean NOT NULL DEFAULT false,   -- temporary passwords (R29) and ETL weak-password flag
  legacy_scrypt_hash     bytea,                            -- base64-decoded passwordHash from auth:export; NULLed after rehash
  legacy_scrypt_salt     bytea,                            -- purged 180 days after cut-over (R30)
  status                 text NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled','reset_required','deleted')),
  disabled_at            timestamptz,
  deleted_at             timestamptz,
  auth_version           int  NOT NULL DEFAULT 1 CHECK (auth_version >= 1),   -- JWT `ver`; bumped on role/scope/disable/password/driver-link change
  last_login_at          timestamptz,
  last_login_lat         double precision,
  last_login_lng         double precision,
  last_login_geo_source  text CHECK (last_login_geo_source IN ('gps','ip')),
  last_login_accuracy_m  double precision,
  legacy_auth_created_at timestamptz,                      -- Auth export createdAt
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now(),
  CHECK ((legacy_scrypt_hash IS NULL) = (legacy_scrypt_salt IS NULL)),
  CHECK (status <> 'disabled' OR disabled_at IS NOT NULL),
  CHECK (status <> 'deleted'  OR deleted_at  IS NOT NULL)
);
CREATE UNIQUE INDEX users_email      ON users (email) WHERE email IS NOT NULL AND status <> 'deleted';
CREATE UNIQUE INDEX users_legacy_uid ON users (legacy_auth_uid) WHERE legacy_auth_uid IS NOT NULL;
CREATE INDEX users_last_login        ON users (last_login_at DESC NULLS LAST, id DESC);
ALTER TABLE users ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (platform-only)

-- Registry of every MinIO/S3 object; every *_file_id column points here (R1). Moved from 0009 so the
-- FKs in 0002-0005 resolve. API bodies carry `key`; the service resolves key -> id at commit.
CREATE TABLE file_objects (
  id             uuid PRIMARY KEY DEFAULT uuidv7(),
  bucket         text NOT NULL,                            -- the S3_BUCKET (private) or S3_PUBLIC_BUCKET (app_releases/ only) value
  object_key     text NOT NULL,                            -- legacy Firebase path kept verbatim; new keys use entity uuids
  tenant_id      uuid REFERENCES tenants(id),              -- owning tenant; NULL = platform object (APK, unattributed legacy object)
  purpose        text NOT NULL CHECK (purpose ~ '^[a-z][a-z0-9_]*$'),   -- vocabulary in the notes below
  owner_kind     text CHECK (owner_kind IN ('trip','task','standby','incident','chat','driver','truck','company','customer',
                                            'tenant','maintenance','expense','leave','release','user','statement','report','penalty')),
  owner_id       uuid,                                     -- entity row, linked at commit; NULL while pending
  content_type   text,
  size_bytes     bigint CHECK (size_bytes >= 0),
  sha256         text CHECK (sha256 ~ '^[0-9a-f]{64}$'),
  visibility     text NOT NULL DEFAULT 'private' CHECK (visibility IN ('private','public')),
  status         text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','committed','missing_at_source')),
  legacy_url     text,                                     -- Firebase download URL with the token stripped (ETL only)
  uploaded_by    uuid REFERENCES users(id),
  expires_at     timestamptz,                              -- pending uploads only: storage.gc removes object + row after this
  committed_at   timestamptz,
  created_at     timestamptz NOT NULL DEFAULT now(),
  deleted_at     timestamptz,
  UNIQUE (bucket, object_key),
  CHECK (status <> 'pending'   OR expires_at   IS NOT NULL),
  CHECK (status <> 'committed' OR committed_at IS NOT NULL),
  CHECK ((owner_kind IS NULL) = (owner_id IS NULL)),
  CHECK (visibility = 'private' OR purpose = 'apk')
);
CREATE INDEX file_objects_owner   ON file_objects (owner_kind, owner_id) WHERE owner_id IS NOT NULL;
CREATE INDEX file_objects_gc      ON file_objects (expires_at) WHERE status = 'pending';
CREATE INDEX file_objects_missing ON file_objects (created_at) WHERE status = 'missing_at_source';
CREATE INDEX file_objects_tenant  ON file_objects (tenant_id, created_at DESC) WHERE tenant_id IS NOT NULL;
ALTER TABLE file_objects ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (nullable-tenant)

ALTER TABLE users ADD CONSTRAINT users_photo_file_fk
  FOREIGN KEY (photo_file_id) REFERENCES file_objects(id) ON DELETE SET NULL;

CREATE TABLE tenant_files (                                -- subcontractors.documents[]
  tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  file_id     uuid NOT NULL REFERENCES file_objects(id),
  kind        text NOT NULL CHECK (kind IN ('id_card','company_doc','other')),
  position    int  NOT NULL DEFAULT 0 CHECK (position >= 0),
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, file_id)
);
ALTER TABLE tenant_files ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (tenant)

CREATE TABLE auth_identities (                             -- external identity links: Google OIDC `sub`; legacy Firebase uid (ETL, §A.3.1)
  id               uuid PRIMARY KEY DEFAULT uuidv7(),
  user_id          uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  provider         text NOT NULL CHECK (provider IN ('google','firebase_legacy')),   -- widen when another IdP is added
  provider_subject text NOT NULL,                          -- Google `sub` (providerUserInfo[google.com].rawId); Firebase uid for firebase_legacy
  email_at_link    citext,
  linked_at        timestamptz NOT NULL DEFAULT now(),
  last_used_at     timestamptz,
  UNIQUE (provider, provider_subject),
  UNIQUE (user_id, provider)
);
ALTER TABLE auth_identities ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (platform-only)

CREATE TABLE sessions (                                    -- one per login on a device/browser (R2); JWT `sid`
  id                  uuid PRIMARY KEY DEFAULT uuidv7(),
  user_id             uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  platform            text NOT NULL CHECK (platform IN ('web','android','ios','script')),
  amr                 text NOT NULL CHECK (amr IN ('pwd','google')),
  active_tenant_id    uuid REFERENCES tenants(id),         -- JWT `tid`; written at login and by POST /v1/auth/tenant, re-issued on refresh (R83)
  install_id          text,                                -- mobile install id (API field installId, R83); NULL on web
  device_label        text,
  app_version         text,
  ip                  inet,
  user_agent          text,
  absolute_expires_at timestamptz NOT NULL,                -- web: created_at + 30 d; mobile: + REFRESH_TOKEN_TTL_MOBILE (90 d) (Appendix C §C.4.4)
  created_at          timestamptz NOT NULL DEFAULT now(),
  last_seen_at        timestamptz NOT NULL DEFAULT now(),
  revoked_at          timestamptz,
  revoked_by          uuid REFERENCES users(id),
  revoked_reason      text CHECK (revoked_reason IN ('logout','logout_all','admin_revoke','disabled','password_changed',
                                                     'password_reset','refresh_reuse','device_relogin','expired')),   -- Appendix C §C.4.4
  CHECK ((revoked_at IS NULL) = (revoked_reason IS NULL))
);
CREATE INDEX sessions_user_active ON sessions (user_id, last_seen_at DESC) WHERE revoked_at IS NULL;
CREATE INDEX sessions_expiry      ON sessions (absolute_expires_at);
-- One live session per (user, install): a re-login on the same device revokes the older one first (device_relogin, R83).
CREATE UNIQUE INDEX sessions_user_install_live ON sessions (user_id, install_id) WHERE install_id IS NOT NULL AND revoked_at IS NULL;
ALTER TABLE sessions ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (platform-only)

CREATE TABLE refresh_tokens (                              -- rotation chain; presenting a rotated token revokes the family (R2)
  id              uuid PRIMARY KEY DEFAULT uuidv7(),
  session_id      uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  family_id       uuid NOT NULL,
  token_hash      bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),   -- sha256(token); the token is never stored
  issued_at       timestamptz NOT NULL DEFAULT now(),
  expires_at      timestamptz NOT NULL,                    -- web: sliding 7 d, capped by sessions.absolute_expires_at
  rotated_at      timestamptz,
  replaced_by     uuid REFERENCES refresh_tokens(id),
  revoked_at      timestamptz,
  revoked_reason  text,
  CHECK ((rotated_at IS NULL) = (replaced_by IS NULL))
);
CREATE INDEX refresh_tokens_family  ON refresh_tokens (family_id);
CREATE INDEX refresh_tokens_session ON refresh_tokens (session_id);
CREATE INDEX refresh_tokens_expiry  ON refresh_tokens (expires_at);
ALTER TABLE refresh_tokens ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (platform-only)

CREATE TABLE password_reset_tokens (                       -- reset and invite links; passwords are never emailed (R29)
  id            uuid PRIMARY KEY DEFAULT uuidv7(),
  user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  purpose       text NOT NULL DEFAULT 'reset' CHECK (purpose IN ('reset','invite')),
  token_hash    bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
  expires_at    timestamptz NOT NULL,                      -- now() + PASSWORD_RESET_TTL
  used_at       timestamptz,
  requested_ip  inet,
  requested_by  uuid REFERENCES users(id),                 -- admin who sent an invite; NULL for self-service forgot-password
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX password_reset_tokens_user   ON password_reset_tokens (user_id, created_at DESC);
CREATE INDEX password_reset_tokens_expiry ON password_reset_tokens (expires_at) WHERE used_at IS NULL;
ALTER TABLE password_reset_tokens ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (platform-only)

CREATE TABLE api_keys (                                    -- machine principals: scripts, release CLI, callable shims (R25, R45)
  id            uuid PRIMARY KEY DEFAULT uuidv7(),
  name          text NOT NULL,
  scope         text NOT NULL CHECK (scope IN ('integration','script','cf_shim','release_publisher')),   -- R82
  key_prefix    text NOT NULL UNIQUE,                      -- first 8 characters, shown in the UI
  key_hash      bytea NOT NULL CHECK (octet_length(key_hash) = 32),   -- sha256(secret || API_KEY_PEPPER)
  tenant_id     uuid REFERENCES tenants(id),               -- NULL = platform-level key (only these may hold global-class and mobile:* keys; checked in Go)
  capabilities  text[] NOT NULL CHECK (cardinality(capabilities) > 0),
  rate_per_min  int  NOT NULL DEFAULT 600 CHECK (rate_per_min > 0),
  created_by    uuid NOT NULL REFERENCES users(id),
  expires_at    timestamptz,
  last_used_at  timestamptz,
  revoked_at    timestamptz,
  revoked_by    uuid REFERENCES users(id),
  created_at    timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT api_keys_cf_shim_platform CHECK (scope <> 'cf_shim' OR tenant_id IS NULL)   -- shim keys are platform keys (R45)
);
CREATE INDEX api_keys_tenant ON api_keys (tenant_id, created_at DESC) WHERE revoked_at IS NULL;
CREATE INDEX api_keys_scope  ON api_keys (scope) WHERE revoked_at IS NULL;
ALTER TABLE api_keys ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (platform-only)

CREATE TABLE memberships (                                 -- tenant role per (user, tenant) (R6)
  user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  role        text NOT NULL CONSTRAINT memberships_tenant_role_check
                CHECK (role IN ('tenant_admin','manager','operation_staff','operator','user','driver')),
  status      text NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended')),
  created_by  uuid REFERENCES users(id),
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, tenant_id)
);
CREATE INDEX memberships_by_tenant_role ON memberships (tenant_id, role);
ALTER TABLE memberships ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (platform-only)

CREATE TABLE user_platform_roles (                         -- platform axis, separate from tenant roles (ADR 0026 semantics)
  user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role        text NOT NULL CHECK (role IN ('platform_admin','support')),
  granted_by  uuid REFERENCES users(id),                   -- NULL = bootstrap by cmd/seed from PLATFORM_ADMIN_EMAILS
  granted_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, role)
);
ALTER TABLE user_platform_roles ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (platform-only)

-- Per-tenant capability overrides; replaces permissions_config/{role}. The catalog (81 keys = 77 tenant/scope keys
-- incl. mobile:create_hub + 4 platform keys, colon form) is Go code (R5, R73); the API validates `capability` against it.
CREATE TABLE role_capability_overrides (
  id          uuid PRIMARY KEY DEFAULT uuidv7(),
  tenant_id   uuid REFERENCES tenants(id),                 -- NULL = platform-wide default override
  role        text NOT NULL CHECK (role IN ('tenant_admin','manager','operation_staff','operator','user','driver')),
  capability  text NOT NULL CHECK (capability ~ '^[a-z]+:[a-z_]+$'),
  allowed     boolean NOT NULL,
  updated_by  uuid NOT NULL REFERENCES users(id),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT rco_not_grantable           CHECK (capability !~ '^(platform|dispatch):'),
  CONSTRAINT rco_assign_role_platform    CHECK (tenant_id IS NULL OR capability <> 'users:assign_role'),
  CONSTRAINT rco_key UNIQUE NULLS NOT DISTINCT (tenant_id, role, capability)
);
ALTER TABLE role_capability_overrides ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (platform-only)

-- statusHistory[] of drivers, trucks, subcontractors (tenants), truck assignments and users. Append-only.
CREATE TABLE status_history (
  id                  uuid PRIMARY KEY DEFAULT uuidv7(),    -- R57: uuidv7 like every table (time-ordered)
  entity_type         text NOT NULL CHECK (entity_type IN ('driver','truck','tenant','truck_assignment','user')),
  entity_id           uuid NOT NULL,
  status              text NOT NULL,                       -- free text: legacy truck entries carry values such as 'Tax Renewed'
  previous_status     text,
  changed_at          timestamptz NOT NULL,
  changed_by_user_id  uuid REFERENCES users(id),
  changed_by_label    text,                                -- trucks shape: displayName/email string; 'system'
  reason              text,
  legacy_raw          jsonb
);
CREATE INDEX status_history_entity ON status_history (entity_type, entity_id, changed_at);
CREATE TRIGGER status_history_immutable BEFORE UPDATE OR DELETE ON status_history
  FOR EACH ROW EXECUTE FUNCTION trg_forbid_mutation();
ALTER TABLE status_history ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;   -- policies: Appendix C §C.3.5 (parent-scoped, polymorphic)

CREATE TRIGGER tenants_set_updated_at     BEFORE UPDATE ON tenants     FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER users_set_updated_at       BEFORE UPDATE ON users       FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER memberships_set_updated_at BEFORE UPDATE ON memberships FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();
CREATE TRIGGER rco_set_updated_at         BEFORE UPDATE ON role_capability_overrides FOR EACH ROW EXECUTE FUNCTION trg_set_updated_at();

-- Column guard on tenants (Appendix C §C.3.6 describes it; body printed here): without bypass (a tenant_admin
-- through p_update_own) only profile columns change; these five are platform-only (platform:manage_tenants, WithSystem).
-- +goose StatementBegin
CREATE FUNCTION trg_tenant_admin_columns() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT app_bypass() AND (NEW.kind, NEW.status, NEW.code, NEW.legacy_doc_id, NEW.contractor_tenant_id)
       IS DISTINCT FROM (OLD.kind, OLD.status, OLD.code, OLD.legacy_doc_id, OLD.contractor_tenant_id) THEN
    RAISE EXCEPTION 'tenants: kind, status, code, legacy_doc_id and contractor_tenant_id are platform-only'
      USING ERRCODE = 'insufficient_privilege';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER t_tenant_admin_columns BEFORE UPDATE ON tenants FOR EACH ROW EXECUTE FUNCTION trg_tenant_admin_columns();

-- Appendix C block (same file): §C.3.5 "0002_identity" (generator DO block, policies of the 13 tables above,
-- app_status_entity_visible(text, uuid, boolean) + p_entity), then §C.3.6 trg_users_self_columns() + t_users_self_columns.

-- +goose StatementBegin
DO $$ BEGIN
  CALL app_rls_platform_table('tenants');            CALL app_rls_tenant_table('tenant_files');
  CALL app_rls_platform_table('users');              CALL app_rls_platform_table('file_objects');
  CALL app_rls_platform_table('auth_identities');    CALL app_rls_platform_table('sessions');
  CALL app_rls_platform_table('refresh_tokens');     CALL app_rls_platform_table('password_reset_tokens');
  CALL app_rls_platform_table('api_keys');           CALL app_rls_platform_table('memberships');
  CALL app_rls_platform_table('user_platform_roles'); CALL app_rls_platform_table('role_capability_overrides');
  CALL app_rls_platform_table('status_history');
END $$;
-- +goose StatementEnd

-- tenants: own tenant, sub-tenants and (for stewards and scope principals) carrier names; quarantine only via bypass
CREATE POLICY p_read ON tenants FOR SELECT USING (
  kind <> 'quarantine' AND (app_tenant_in_reach(id) OR app_is_steward() OR app_is_scope()));
CREATE POLICY p_update_own ON tenants FOR UPDATE                     -- profile fields only (trigger t_tenant_admin_columns)
  USING (app_role() = 'tenant_admin' AND id = app_tenant_id())
  WITH CHECK (app_role() = 'tenant_admin' AND id = app_tenant_id());
CREATE POLICY p_steward ON tenant_files USING (app_is_steward()) WITH CHECK (app_is_steward());

-- users: self, and staff of a tenant the user belongs to; writes other than self-profile go through iam (WithSystem)
CREATE POLICY p_self_read   ON users FOR SELECT USING (id = app_user_id());
CREATE POLICY p_self_update ON users FOR UPDATE USING (id = app_user_id()) WITH CHECK (id = app_user_id());
CREATE POLICY p_staff_read  ON users FOR SELECT USING (app_is_staff() AND EXISTS (
  SELECT 1 FROM memberships m WHERE m.user_id = users.id AND app_tenant_in_reach(m.tenant_id)));
CREATE POLICY p_self_read  ON memberships FOR SELECT USING (user_id = app_user_id());
CREATE POLICY p_staff_read ON memberships FOR SELECT USING (app_is_staff() AND app_tenant_in_reach(tenant_id));
CREATE POLICY p_self_read ON auth_identities     FOR SELECT USING (user_id = app_user_id());
CREATE POLICY p_self_read ON user_platform_roles FOR SELECT USING (user_id = app_user_id());
CREATE POLICY p_self_read ON sessions            FOR SELECT USING (user_id = app_user_id());
CREATE POLICY p_staff_read ON role_capability_overrides FOR SELECT USING (app_is_staff() AND tenant_id = app_tenant_id());
CREATE POLICY p_staff_read ON api_keys                  FOR SELECT USING (app_is_staff() AND tenant_id = app_tenant_id());

-- status_history: visible / insertable exactly when the entity is. Invoker-rights PL/pgSQL, so the lookups run under
-- the caller's RLS and bind drivers / trucks / truck_assignments (created in 0003) at first call.
-- +goose StatementBegin
CREATE FUNCTION app_status_entity_visible(p_type text, p_id uuid, p_for_write boolean) RETURNS boolean
  LANGUAGE plpgsql STABLE AS $$
BEGIN
  RETURN CASE p_type
    WHEN 'driver'           THEN EXISTS (SELECT 1 FROM drivers d           WHERE d.id = p_id)
    WHEN 'truck'            THEN EXISTS (SELECT 1 FROM trucks t            WHERE t.id = p_id)
    WHEN 'truck_assignment' THEN EXISTS (SELECT 1 FROM truck_assignments a WHERE a.id = p_id)
    WHEN 'tenant'           THEN app_is_staff() AND app_tenant_in_reach(p_id)
    WHEN 'user'             THEN NOT p_for_write AND p_id = app_user_id()
    ELSE false END;
END $$;
-- +goose StatementEnd
CREATE POLICY p_entity ON status_history
  USING (app_status_entity_visible(entity_type, entity_id, false))
  WITH CHECK (app_is_staff() AND app_status_entity_visible(entity_type, entity_id, true));

-- file_objects (R1 columns): a principal sees its own uploads, its tenant's files and public files. Cross-tenant
-- readers (scope principals, evidence viewers) never read this table: the storage service resolves the key under
-- WithSystem only after the referencing row was read under the principal ("a file is readable iff its referencing
-- row is readable").
CREATE POLICY p_read ON file_objects FOR SELECT USING (
     visibility = 'public' OR uploaded_by = app_user_id()
  OR (app_is_staff() AND app_tenant_in_reach(tenant_id)));
CREATE POLICY p_upload ON file_objects FOR INSERT WITH CHECK (
  uploaded_by = app_user_id() AND status = 'pending'
  AND (tenant_id = app_tenant_id() OR (tenant_id IS NULL AND app_is_steward())));
CREATE POLICY p_commit ON file_objects FOR UPDATE
  USING (uploaded_by = app_user_id() OR (app_is_staff() AND app_tenant_in_reach(tenant_id)))
  WITH CHECK (uploaded_by = app_user_id() OR (app_is_staff() AND app_tenant_in_reach(tenant_id)));

-- Appendix C §C.3.6: self-service profile columns only (PATCH /v1/me); role, status, password and auth_version
-- change only through iam/auth under WithSystem.
-- +goose StatementBegin
CREATE FUNCTION trg_users_self_columns() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE allowed text[] := ARRAY['display_name','photo_file_id','last_login_at','last_login_lat','last_login_lng',
                                'last_login_geo_source','last_login_accuracy_m','updated_at'];
BEGIN
  IF NOT app_bypass() AND (to_jsonb(NEW) - allowed) IS DISTINCT FROM (to_jsonb(OLD) - allowed) THEN
    RAISE EXCEPTION 'users: only profile columns are self-service' USING ERRCODE = 'insufficient_privilege';
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER t_users_self_columns BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION trg_users_self_columns();

-- +goose Down
-- Appendix C objects (IF EXISTS: runnable with or without them).
DROP POLICY IF EXISTS p_staff_read ON users;               -- reads memberships; would block its DROP
DROP TABLE status_history;
DROP TABLE role_capability_overrides;
DROP TABLE user_platform_roles;
DROP TABLE memberships;
DROP TABLE api_keys;
DROP TABLE password_reset_tokens;
DROP TABLE refresh_tokens;
DROP TABLE sessions;
DROP TABLE auth_identities;
DROP TABLE tenant_files;
ALTER TABLE users DROP CONSTRAINT users_photo_file_fk;
DROP TABLE file_objects;
DROP TABLE users;
DROP TABLE tenants;                                        -- removes the quarantine row with it
DROP FUNCTION trg_tenant_admin_columns();
DROP FUNCTION IF EXISTS app_status_entity_visible(text, uuid, boolean);
DROP FUNCTION IF EXISTS trg_users_self_columns();
