-- 0027_platform_support_access.sql: ADR 0030, platform-support-access §4.4/§4.9 (P1.2a).
-- Additive storage only: entry stays disabled. Down is for an empty/test rollback; production
-- rollback disables entry and closes sessions while retaining consent, provenance and audit history.
-- +goose Up
SELECT set_config('app.role', 'system', true), set_config('app.bypass_tenant', 'on', true);

ALTER TABLE tenants
  ADD COLUMN support_code_reviewed boolean NOT NULL DEFAULT false,
  ADD COLUMN support_label_version bigint NOT NULL DEFAULT 0 CHECK (support_label_version >= 0),
  ADD COLUMN support_access_enabled boolean NOT NULL DEFAULT false,
  ADD COLUMN support_access_version bigint NOT NULL DEFAULT 0 CHECK (support_access_version >= 0),
  ADD COLUMN support_access_changed_at timestamptz,
  ADD COLUMN support_access_changed_by uuid,
  ADD CONSTRAINT tenants_support_access_quarantine CHECK (kind <> 'quarantine' OR NOT support_access_enabled),
  ADD CONSTRAINT tenants_support_access_actor CHECK
    ((support_access_version = 0 AND NOT support_access_enabled
        AND support_access_changed_at IS NULL AND support_access_changed_by IS NULL)
      OR (support_access_version > 0 AND support_access_changed_at IS NOT NULL AND support_access_changed_by IS NOT NULL));
ALTER TABLE sessions
  ADD COLUMN access_mode text NOT NULL DEFAULT 'member' CHECK (access_mode IN ('member','support')),
  ADD COLUMN context_version bigint NOT NULL DEFAULT 0 CHECK (context_version >= 0),
  ADD COLUMN support_return_tenant_id uuid REFERENCES tenants(id),
  ADD CONSTRAINT sessions_support_tenant CHECK (access_mode <> 'support' OR active_tenant_id IS NOT NULL);
ALTER TABLE users
  ADD COLUMN credential_reset_by_platform_at timestamptz,
  ADD COLUMN credential_self_chosen_at timestamptz;
ALTER TABLE memberships
  ADD COLUMN support_membership_id uuid,
  ADD COLUMN granted_by_platform boolean NOT NULL DEFAULT false,
  ADD COLUMN platform_grantor_user_id uuid REFERENCES users(id),
  ADD COLUMN platform_granted_at timestamptz,
  ADD COLUMN ratified_by_user_id uuid REFERENCES users(id),
  ADD COLUMN ratified_at timestamptz,
  ADD COLUMN grant_version bigint NOT NULL DEFAULT 0 CHECK (grant_version >= 0),
  ADD CONSTRAINT memberships_platform_provenance CHECK
    (granted_by_platform = (platform_grantor_user_id IS NOT NULL AND platform_granted_at IS NOT NULL)
      AND (granted_by_platform OR (platform_grantor_user_id IS NULL AND platform_granted_at IS NULL))),
  ADD CONSTRAINT memberships_ratification CHECK
    ((ratified_at IS NULL) = (ratified_by_user_id IS NULL)
      AND (ratified_at IS NULL OR (granted_by_platform AND ratified_by_user_id <> user_id
        AND ratified_at >= platform_granted_at)));
UPDATE memberships SET support_membership_id = uuidv7();
ALTER TABLE memberships ALTER COLUMN support_membership_id SET NOT NULL,
  ADD CONSTRAINT memberships_support_membership_id_key UNIQUE (support_membership_id);

-- Reconstruct authority at the operation, not at migration time. Bound attributable grants by
-- revocations so a later re-grant cannot bridge a revoked interval. A revocation alone cannot
-- prove when authority began. This scratch table disappears on commit.
CREATE TEMPORARY TABLE support_migration_platform_periods ON COMMIT DROP AS
WITH changes AS (
  SELECT user_id, granted_at AS changed_at, true AS granted
  FROM user_platform_roles WHERE role = 'platform_admin'
  UNION
  SELECT target_user_id, created_at, event_type = 'platform_role_granted'
  FROM security_events WHERE event_type IN ('platform_role_granted','platform_role_revoked')
    AND details->>'role' = 'platform_admin' AND target_user_id IS NOT NULL
), starts AS (
  SELECT user_id, changed_at FROM changes WHERE granted
)
SELECT s.user_id, s.changed_at AS starts_at,
  coalesce((SELECT min(c.changed_at) FROM changes c WHERE c.user_id = s.user_id
    AND NOT c.granted AND c.changed_at >= s.changed_at), 'infinity'::timestamptz) AS ends_at
FROM starts s;

-- Missing evidence is not platform provenance.
WITH evidence AS (
  SELECT m.user_id, m.tenant_id, m.created_by AS actor, m.created_at AS granted_at
  FROM memberships m WHERE EXISTS (SELECT 1 FROM support_migration_platform_periods p
    WHERE p.user_id = m.created_by AND m.created_at >= p.starts_at AND m.created_at < p.ends_at)
  UNION ALL
  SELECT m.user_id, m.tenant_id, e.actor_user_id, e.created_at
  FROM memberships m JOIN security_events e ON e.target_user_id = m.user_id AND e.tenant_id = m.tenant_id
    AND e.created_at >= m.created_at AND e.details->>'role' = m.role
    AND (e.event_type IN ('membership_added','user_created')
      OR (e.event_type = 'membership_role_changed' AND e.details->>'previousRole' IS DISTINCT FROM e.details->>'role'))
  WHERE EXISTS (SELECT 1 FROM support_migration_platform_periods p
    WHERE p.user_id = e.actor_user_id AND e.created_at >= p.starts_at AND e.created_at < p.ends_at)
), latest AS (
  SELECT DISTINCT ON (user_id, tenant_id) * FROM evidence ORDER BY user_id, tenant_id, granted_at DESC, actor
)
UPDATE memberships m SET granted_by_platform = true, platform_grantor_user_id = e.actor,
  platform_granted_at = e.granted_at, grant_version = 1
FROM latest e WHERE (m.user_id, m.tenant_id) = (e.user_id, e.tenant_id);
WITH resets AS (
  SELECT e.target_user_id, max(e.created_at) AS reset_at
  FROM security_events e
  WHERE (e.event_type = 'user_password_temporary_issued'
    OR (e.event_type = 'user_created' AND e.details->>'invite' = 'false'))
    AND EXISTS (SELECT 1 FROM support_migration_platform_periods p
      WHERE p.user_id = e.actor_user_id AND e.created_at >= p.starts_at AND e.created_at < p.ends_at)
  GROUP BY e.target_user_id
)
UPDATE users u SET credential_reset_by_platform_at = r.reset_at FROM resets r WHERE u.id = r.target_user_id;
-- An actual holder password submission, not merely clearing must_change_password.
UPDATE users u SET credential_self_chosen_at = e.chosen_at
FROM (SELECT target_user_id, max(created_at) AS chosen_at FROM security_events
      WHERE event_type IN ('password_changed','password_reset_completed') AND actor_user_id = target_user_id
      GROUP BY target_user_id) e
WHERE u.id = e.target_user_id;

-- Snapshots survive membership removal/recreation and retain the exact original grant for bootstrap.
CREATE TABLE membership_provenance_history (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  membership_id uuid NOT NULL,
  user_id uuid NOT NULL REFERENCES users(id),
  tenant_id uuid NOT NULL REFERENCES tenants(id),
  role text NOT NULL,
  status text NOT NULL,
  membership_created_at timestamptz NOT NULL,
  granted_by_platform boolean NOT NULL,
  platform_grantor_user_id uuid REFERENCES users(id),
  platform_granted_at timestamptz,
  ratified_by_user_id uuid REFERENCES users(id),
  ratified_at timestamptz,
  grant_version bigint NOT NULL CHECK (grant_version >= 0),
  operation text NOT NULL CHECK (operation IN ('baseline','INSERT','UPDATE','DELETE')),
  recorded_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX membership_provenance_history_member ON membership_provenance_history (user_id, tenant_id, grant_version DESC);
CREATE INDEX membership_provenance_history_grant ON membership_provenance_history (membership_id, grant_version);
CREATE INDEX membership_provenance_history_tenant ON membership_provenance_history (tenant_id, membership_created_at);
ALTER TABLE membership_provenance_history ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY p_system ON membership_provenance_history
  USING (app_role() = 'system' AND app_bypass()) WITH CHECK (app_role() = 'system' AND app_bypass());
CREATE TRIGGER membership_provenance_history_immutable BEFORE UPDATE OR DELETE ON membership_provenance_history
  FOR EACH ROW EXECUTE FUNCTION trg_forbid_mutation();
GRANT SELECT, INSERT ON membership_provenance_history TO logitrack_app;
-- Existing ETL/seed membership writes also append snapshots; ETL retains its own BYPASSRLS login.
GRANT SELECT, INSERT ON membership_provenance_history TO logitrack_etl;

INSERT INTO membership_provenance_history (membership_id, user_id, tenant_id, role, status, membership_created_at,
  granted_by_platform, platform_grantor_user_id, platform_granted_at, ratified_by_user_id, ratified_at, grant_version, operation)
SELECT support_membership_id, user_id, tenant_id, role, status, created_at, granted_by_platform,
  platform_grantor_user_id, platform_granted_at, ratified_by_user_id, ratified_at, grant_version, 'baseline' FROM memberships;
ALTER TABLE tenants
  -- Plain audit references avoid a reverse TRUNCATE CASCADE path from users/history to tenants.
  -- The consent trigger validates the first-grant reference on every subsequent change.
  ADD COLUMN support_first_admin_grant_id uuid,
  ADD COLUMN support_first_admin_revoked_at timestamptz,
  ADD COLUMN support_bootstrap_consumed boolean NOT NULL DEFAULT false,
  ADD CONSTRAINT tenants_support_first_admin_revoked CHECK
    (support_first_admin_revoked_at IS NULL OR support_first_admin_grant_id IS NOT NULL);
-- A legacy tenant cannot acquire a new "first" admin merely because old memberships disappeared.
UPDATE tenants SET support_bootstrap_consumed = true;

-- Retained tenant-creation and original admin-creation audits anchor initial onboarding. Any
-- earlier/conflicting admin evidence makes it ambiguous. Keep an immutable original snapshot,
-- even when subsequent demotion/removal/disable means that grant can never bootstrap again.
WITH initial AS (
  SELECT e.* FROM security_events e JOIN tenants t ON t.id = e.tenant_id
  WHERE e.event_type IN ('user_created','membership_added') AND e.details->>'role' = 'tenant_admin'
    AND coalesce(e.details->>'status', 'active') = 'active' AND e.target_user_id IS NOT NULL
    AND EXISTS (SELECT 1 FROM support_migration_platform_periods p WHERE p.user_id = e.actor_user_id
      AND e.created_at >= p.starts_at AND e.created_at < p.ends_at)
    AND EXISTS (SELECT 1 FROM security_events creation WHERE creation.tenant_id = t.id
      AND creation.event_type = 'tenant_created' AND creation.created_at >= t.created_at
      AND creation.created_at <= e.created_at)
    AND NOT EXISTS (SELECT 1 FROM membership_provenance_history h WHERE h.tenant_id = t.id
      AND h.role = 'tenant_admin' AND h.user_id <> e.target_user_id AND h.membership_created_at <= e.created_at)
    AND NOT EXISTS (SELECT 1 FROM security_events prior WHERE prior.created_at <= e.created_at AND prior.id <> e.id
      AND ((prior.tenant_id = t.id AND (prior.details->>'role' = 'tenant_admin'
        OR prior.details->>'previousRole' = 'tenant_admin'))
        OR (prior.event_type = 'user_deleted' AND prior.details->'memberships' @>
          jsonb_build_array(jsonb_build_object('tenantId', t.id, 'role', 'tenant_admin')))))
), original AS (
  INSERT INTO membership_provenance_history (membership_id, user_id, tenant_id, role, status,
    membership_created_at, granted_by_platform, platform_grantor_user_id, platform_granted_at, grant_version, operation)
  SELECT coalesce((SELECT h.membership_id FROM membership_provenance_history h
      WHERE h.user_id = e.target_user_id AND h.tenant_id = e.tenant_id
        AND h.membership_created_at <= e.created_at LIMIT 1), uuidv7()),
    e.target_user_id, e.tenant_id, 'tenant_admin', 'active', e.created_at, true, e.actor_user_id, e.created_at, 0, 'baseline'
  FROM initial e RETURNING *
)
UPDATE tenants t SET support_first_admin_grant_id = h.id,
  support_first_admin_revoked_at = (SELECT min(ended_at) FROM (
    SELECT e.created_at AS ended_at FROM security_events e
      WHERE e.target_user_id = h.user_id AND e.created_at >= h.platform_granted_at
        AND (e.event_type IN ('user_disabled','user_deleted')
          OR (e.tenant_id = h.tenant_id AND (e.event_type = 'membership_removed'
            OR (e.event_type = 'membership_role_changed' AND (e.details->>'role' IS DISTINCT FROM 'tenant_admin'
              OR e.details->>'status' IS DISTINCT FROM 'active')))))
    UNION ALL SELECT u.disabled_at FROM users u WHERE u.id = h.user_id AND u.disabled_at >= h.platform_granted_at
    UNION ALL SELECT u.deleted_at FROM users u WHERE u.id = h.user_id AND u.deleted_at >= h.platform_granted_at
  ) ended)
FROM original h WHERE t.id = h.tenant_id;

-- +goose StatementBegin
CREATE FUNCTION trg_support_consent_columns() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.code IS DISTINCT FROM OLD.code THEN NEW.support_code_reviewed := false; END IF;
  IF (NEW.kind, NEW.legal_type, NEW.code, NEW.name_th, NEW.name_en, NEW.support_code_reviewed, NEW.support_access_enabled) IS DISTINCT FROM
     (OLD.kind, OLD.legal_type, OLD.code, OLD.name_th, OLD.name_en, OLD.support_code_reviewed, OLD.support_access_enabled) THEN
    NEW.support_label_version := OLD.support_label_version + 1;
  ELSE NEW.support_label_version := OLD.support_label_version;
  END IF;
  IF (NEW.support_access_enabled, NEW.support_access_version, NEW.support_access_changed_at,
      NEW.support_access_changed_by, NEW.support_first_admin_grant_id, NEW.support_first_admin_revoked_at,
      NEW.support_bootstrap_consumed, NEW.support_code_reviewed) IS DISTINCT FROM
     (OLD.support_access_enabled, OLD.support_access_version, OLD.support_access_changed_at,
      OLD.support_access_changed_by, OLD.support_first_admin_grant_id, OLD.support_first_admin_revoked_at,
      OLD.support_bootstrap_consumed, OLD.support_code_reviewed) THEN
    IF app_role() IS DISTINCT FROM 'system' OR NOT app_bypass() THEN
      RAISE EXCEPTION 'support consent requires an authorized system transaction' USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF OLD.support_first_admin_grant_id IS NOT NULL AND NEW.support_first_admin_grant_id IS DISTINCT FROM OLD.support_first_admin_grant_id THEN
      RAISE EXCEPTION 'first onboarding grant is immutable' USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    IF OLD.support_bootstrap_consumed AND NOT NEW.support_bootstrap_consumed THEN
      RAISE EXCEPTION 'onboarding bootstrap cannot be restored' USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    IF OLD.support_first_admin_revoked_at IS NOT NULL AND NEW.support_first_admin_revoked_at IS DISTINCT FROM OLD.support_first_admin_revoked_at THEN
      RAISE EXCEPTION 'first onboarding eligibility cannot be restored' USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    IF NEW.support_first_admin_grant_id IS DISTINCT FROM OLD.support_first_admin_grant_id AND NOT EXISTS (
      SELECT 1 FROM membership_provenance_history h WHERE h.id = NEW.support_first_admin_grant_id
        AND h.tenant_id = NEW.id AND h.role = 'tenant_admin' AND h.status = 'active' AND h.granted_by_platform) THEN
      RAISE EXCEPTION 'first onboarding reference requires its tenant admin grant' USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.support_access_enabled IS DISTINCT FROM OLD.support_access_enabled THEN
      IF NEW.support_access_version <> OLD.support_access_version + 1 OR NEW.support_access_changed_at IS NULL
          OR NEW.support_access_changed_by IS NULL THEN
        RAISE EXCEPTION 'consent change requires next version, actor and time' USING ERRCODE = 'check_violation';
      END IF;
    ELSIF (NEW.support_access_version, NEW.support_access_changed_at, NEW.support_access_changed_by) IS DISTINCT FROM
          (OLD.support_access_version, OLD.support_access_changed_at, OLD.support_access_changed_by) THEN
      RAISE EXCEPTION 'unchanged consent preserves version, actor and time' USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER t_support_consent_columns BEFORE UPDATE ON tenants FOR EACH ROW EXECUTE FUNCTION trg_support_consent_columns();

-- Disabling/deleting the first holder permanently consumes bootstrap, even after audit retention.
-- +goose StatementBegin
CREATE FUNCTION trg_support_first_admin_status() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  UPDATE tenants t SET support_first_admin_revoked_at = clock_timestamp()
  FROM membership_provenance_history h WHERE h.id = t.support_first_admin_grant_id
    AND h.user_id = NEW.id AND t.support_first_admin_revoked_at IS NULL;
  RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER t_support_first_admin_status AFTER UPDATE OF status ON users
  FOR EACH ROW WHEN (OLD.status IS DISTINCT FROM NEW.status AND NEW.status IN ('disabled','deleted'))
  EXECUTE FUNCTION trg_support_first_admin_status();

-- +goose StatementBegin
CREATE FUNCTION trg_membership_provenance_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    IF NEW.support_membership_id IS NULL THEN NEW.support_membership_id := uuidv7(); END IF;
    NEW.grant_version := greatest(NEW.grant_version, coalesce((SELECT max(grant_version) + 1
      FROM membership_provenance_history WHERE user_id = NEW.user_id AND tenant_id = NEW.tenant_id), 0));
  ELSE
    IF (NEW.support_membership_id, NEW.user_id, NEW.tenant_id) IS DISTINCT FROM
       (OLD.support_membership_id, OLD.user_id, OLD.tenant_id) THEN
      RAISE EXCEPTION 'membership identity is immutable' USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    IF (NEW.role, NEW.status, NEW.granted_by_platform, NEW.platform_grantor_user_id, NEW.platform_granted_at,
        NEW.ratified_by_user_id, NEW.ratified_at) IS DISTINCT FROM
       (OLD.role, OLD.status, OLD.granted_by_platform, OLD.platform_grantor_user_id, OLD.platform_granted_at,
        OLD.ratified_by_user_id, OLD.ratified_at) THEN
      NEW.grant_version := OLD.grant_version + 1;
    ELSIF NEW.grant_version <> OLD.grant_version THEN
      RAISE EXCEPTION 'unchanged grant preserves version' USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER t_membership_provenance_version BEFORE INSERT OR UPDATE ON memberships
  FOR EACH ROW EXECUTE FUNCTION trg_membership_provenance_version();

-- +goose StatementBegin
CREATE FUNCTION trg_membership_provenance_history() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE m memberships;
BEGIN
  IF TG_OP = 'DELETE' THEN m := OLD; m.grant_version := m.grant_version + 1;
  ELSE
    m := NEW;
    IF TG_OP = 'UPDATE' AND NEW.grant_version = OLD.grant_version THEN RETURN NULL; END IF;
  END IF;
  INSERT INTO membership_provenance_history (membership_id, user_id, tenant_id, role, status, membership_created_at,
    granted_by_platform, platform_grantor_user_id, platform_granted_at, ratified_by_user_id, ratified_at, grant_version, operation)
  VALUES (m.support_membership_id, m.user_id, m.tenant_id, m.role, m.status, m.created_at, m.granted_by_platform,
    m.platform_grantor_user_id, m.platform_granted_at, m.ratified_by_user_id, m.ratified_at, m.grant_version, TG_OP);
  RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER t_membership_provenance_history AFTER INSERT OR UPDATE OR DELETE ON memberships
  FOR EACH ROW EXECUTE FUNCTION trg_membership_provenance_history();

CREATE TABLE support_visits (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  session_id uuid REFERENCES sessions(id) ON DELETE SET NULL,
  actor_user_id uuid NOT NULL REFERENCES users(id),
  tenant_id uuid NOT NULL REFERENCES tenants(id),
  started_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  ended_at timestamptz,
  end_reason text CHECK (end_reason IN ('leave','switch','logout','revoked','role_removed','expired')),
  end_recorded_at timestamptz,
  consent_version_at_entry bigint NOT NULL CHECK (consent_version_at_entry >= 0),
  CONSTRAINT support_visits_lifecycle CHECK ((ended_at IS NULL) = (end_reason IS NULL)
    AND (ended_at IS NULL) = (end_recorded_at IS NULL) AND (ended_at IS NOT NULL OR session_id IS NOT NULL)
    AND (ended_at IS NULL OR ended_at >= started_at) AND (end_recorded_at IS NULL OR end_recorded_at >= ended_at)),
  CONSTRAINT support_visits_not_quarantine CHECK (tenant_id <> '00000000-0000-7000-8000-00000000000f')
);
CREATE UNIQUE INDEX support_visits_session_open ON support_visits (session_id) WHERE ended_at IS NULL;
CREATE INDEX support_visits_tenant_started ON support_visits (tenant_id, started_at DESC, id DESC);
CREATE INDEX support_visits_actor ON support_visits (actor_user_id);
CREATE INDEX support_visits_session ON support_visits (session_id) WHERE session_id IS NOT NULL;
ALTER TABLE support_visits ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY p_system ON support_visits
  USING (app_role() = 'system' AND app_bypass()) WITH CHECK (app_role() = 'system' AND app_bypass());
GRANT SELECT, INSERT, UPDATE ON support_visits TO logitrack_app;
GRANT SELECT ON support_visits TO logitrack_etl;

-- +goose StatementBegin
CREATE FUNCTION trg_support_visit_lifecycle() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    IF NOT EXISTS (SELECT 1 FROM tenants WHERE id = NEW.tenant_id AND kind IN ('own_fleet','carrier')) THEN
      RAISE EXCEPTION 'support destination is unavailable' USING ERRCODE = 'check_violation';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM sessions WHERE id = NEW.session_id AND user_id = NEW.actor_user_id) THEN
      RAISE EXCEPTION 'support visit session does not belong to actor' USING ERRCODE = 'check_violation';
    END IF;
  ELSE
    IF (NEW.id, NEW.actor_user_id, NEW.tenant_id, NEW.started_at, NEW.consent_version_at_entry) IS DISTINCT FROM
       (OLD.id, OLD.actor_user_id, OLD.tenant_id, OLD.started_at, OLD.consent_version_at_entry)
       OR (NEW.session_id IS DISTINCT FROM OLD.session_id AND NOT (OLD.ended_at IS NOT NULL AND NEW.session_id IS NULL))
       OR (OLD.ended_at IS NOT NULL AND (NEW.ended_at, NEW.end_reason, NEW.end_recorded_at) IS DISTINCT FROM
                                      (OLD.ended_at, OLD.end_reason, OLD.end_recorded_at)) THEN
      RAISE EXCEPTION 'support visit history is immutable' USING ERRCODE = 'integrity_constraint_violation';
    END IF;
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER t_support_visit_lifecycle BEFORE INSERT OR UPDATE ON support_visits
  FOR EACH ROW EXECUTE FUNCTION trg_support_visit_lifecycle();
REVOKE ALL ON FUNCTION trg_support_consent_columns(), trg_membership_provenance_version(),
  trg_membership_provenance_history(), trg_support_visit_lifecycle(), trg_support_first_admin_status() FROM PUBLIC;
SELECT set_config('app.role', '', true), set_config('app.bypass_tenant', '', true);


CREATE TABLE support_request_limits (
  bucket text NOT NULL CHECK (bucket IN ('support_options','support_consent')),
  subject text NOT NULL,
  theoretical_at timestamptz NOT NULL,
  PRIMARY KEY (bucket,subject)
);
ALTER TABLE support_request_limits ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY p_system ON support_request_limits
  USING (app_role() = 'system' AND app_bypass()) WITH CHECK (app_role() = 'system' AND app_bypass());
GRANT SELECT, INSERT, UPDATE ON support_request_limits TO logitrack_app;
GRANT SELECT ON support_request_limits TO logitrack_etl;

-- Safe pagination IDs for the platform projection; device identifiers never enter cursors.
ALTER TABLE mobile_installations ADD COLUMN support_page_id uuid NOT NULL DEFAULT uuidv7();
CREATE UNIQUE INDEX mobile_installations_support_page_id ON mobile_installations(support_page_id);
CREATE INDEX mobile_installations_support_seen ON mobile_installations(last_seen_at DESC,driver_id DESC,support_page_id DESC);

-- +goose Down
ALTER TABLE mobile_installations DROP COLUMN support_page_id;
DROP TABLE support_request_limits;
ALTER TABLE tenants DROP COLUMN support_code_reviewed, DROP COLUMN support_label_version;
DROP TABLE support_visits;
DROP FUNCTION trg_support_visit_lifecycle();
DROP TRIGGER t_membership_provenance_history ON memberships;
DROP FUNCTION trg_membership_provenance_history();
DROP TRIGGER t_membership_provenance_version ON memberships;
DROP FUNCTION trg_membership_provenance_version();
DROP TRIGGER t_support_consent_columns ON tenants;
DROP FUNCTION trg_support_consent_columns();
DROP TRIGGER t_support_first_admin_status ON users;
DROP FUNCTION trg_support_first_admin_status();
ALTER TABLE tenants DROP COLUMN support_first_admin_grant_id, DROP COLUMN support_first_admin_revoked_at,
  DROP COLUMN support_bootstrap_consumed;
DROP TABLE membership_provenance_history;
ALTER TABLE memberships DROP COLUMN support_membership_id, DROP COLUMN granted_by_platform,
  DROP COLUMN platform_grantor_user_id, DROP COLUMN platform_granted_at, DROP COLUMN ratified_by_user_id,
  DROP COLUMN ratified_at, DROP COLUMN grant_version;
ALTER TABLE users DROP COLUMN credential_reset_by_platform_at, DROP COLUMN credential_self_chosen_at;
ALTER TABLE sessions DROP COLUMN access_mode, DROP COLUMN context_version, DROP COLUMN support_return_tenant_id;
ALTER TABLE tenants DROP COLUMN support_access_enabled, DROP COLUMN support_access_version,
  DROP COLUMN support_access_changed_at, DROP COLUMN support_access_changed_by;
