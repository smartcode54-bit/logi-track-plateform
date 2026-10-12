-- 0022_broadcast_audience.sql: Appendix A §A.2.6, Appendix C §C.3.3, §C.3.5 (issue T48). Never edit this file once it
-- is merged; follow-ups take the next number (R31).
-- The audience of a broadcast (POST /v1/broadcasts persists its recipients in broadcast_recipients) reaches across
-- tenants in two ways the 0007 policies cannot express on their own:
--   * own-fleet staff address the drivers of the carriers working for them (recipient groups all_driver and
--     subcontractor, R60), and a steward the drivers of every tenant (all_tenants, tenant_id NULL); a driver of a
--     carrier reads the own fleet's broadcast addressed to it (GET /v1/mobile/broadcasts) and marks it read (POST
--     /v1/mobile/broadcasts/{id}/read: broadcast_reads.p_write checks that the broadcast is visible). broadcasts.p_read
--     gains that clause. A policy on broadcasts cannot read broadcast_recipients directly (its own p_read reads
--     broadcasts: infinite recursion), so the policy helper app_broadcast_recipient(broadcast) answers it.
--   * the platform-wide audience (every tenant's drivers) is outside any principal's RLS reach.
-- Three SECURITY DEFINER helpers (logitrack_rls_definer is BYPASSRLS, so each checks its caller itself):
--   app_broadcast_recipient(broadcast)   whether the caller (app.user_id) is a persisted recipient: the policy helper.
--   broadcast_unaddressed(broadcast)     whether the broadcast has no persisted recipient at all (the legacy rows the
--                                        ETL loads and the seeded ones: addressed by their recipient_group, Appendix A
--                                        §A.3.10), so the driver list shows them to every driver who may read them.
--   broadcast_platform_audience()        the users of every tenant's drivers a broadcast reaches (a linked login that
--                                        is active or reset_required, a driver that is not inactive, an active tenant,
--                                        never the quarantine tenant); stewards (R60) and the system context only,
--                                        anyone else gets 42501.
-- The first two answer only about the caller or about the existence of recipient rows; neither names a user.
-- Grants: 0009_infra stays the single grant site of the baseline and of logitrack_app's privileges (R66); a SECURITY
-- DEFINER helper added later is handed over in its own file the 0009 way (ACL first, then ownership) with exactly what
-- it reads (SELECT on broadcast_recipients, users, tenants; drivers is granted by 0009). The policy helper is also
-- executable by the roles that evaluate policies (logitrack_readonly, pg_database_owner), as 0009 does for app_*.
-- Depends on: 0001 (GUC helpers, app_drop_definer_function), 0002 (users, tenants), 0003 (drivers), 0007 (broadcasts,
-- broadcast_recipients, broadcasts.p_read), 0009 (logitrack_rls_definer's SELECT on drivers). Production applies it
-- with `migrate up` before the broadcasts routes serve (T48); it depends on nothing in 0010-0021.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION app_broadcast_recipient(p_broadcast uuid) RETURNS boolean
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
  SELECT EXISTS (SELECT 1 FROM broadcast_recipients r WHERE r.broadcast_id = p_broadcast AND r.user_id = app_user_id())
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION broadcast_unaddressed(p_broadcast uuid) RETURNS boolean
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
  SELECT NOT EXISTS (SELECT 1 FROM broadcast_recipients r WHERE r.broadcast_id = p_broadcast)
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION broadcast_platform_audience() RETURNS TABLE (user_id uuid)
  LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  IF NOT (app_bypass() OR app_is_steward()) THEN
    RAISE EXCEPTION 'the platform-wide broadcast audience needs a steward' USING ERRCODE = 'insufficient_privilege';
  END IF;
  RETURN QUERY
    SELECT DISTINCT d.user_id
      FROM drivers d
      JOIN users u ON u.id = d.user_id
      JOIN tenants t ON t.id = d.tenant_id
     WHERE d.status <> 'inactive' AND u.status IN ('active', 'reset_required') AND t.status = 'active'
       AND d.tenant_id <> app_quarantine_tenant_id()
     ORDER BY d.user_id;
END $$;
-- +goose StatementEnd

-- What the helpers read (logitrack_rls_definer is BYPASSRLS; 0009 granted it SELECT on drivers).
GRANT SELECT ON broadcast_recipients, users, tenants TO logitrack_rls_definer;
-- The hand-over of 0009: ACL first, then ownership (the migrator holds logitrack_rls_definer WITH INHERIT FALSE and
-- cannot change the ACL of a function it no longer owns); ALTER ... OWNER TO needs CREATE here for the new owner.
GRANT CREATE ON SCHEMA public TO logitrack_rls_definer;
REVOKE ALL ON FUNCTION app_broadcast_recipient(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app_broadcast_recipient(uuid) TO logitrack_app, logitrack_readonly, pg_database_owner;
ALTER FUNCTION app_broadcast_recipient(uuid) OWNER TO logitrack_rls_definer;
REVOKE ALL ON FUNCTION broadcast_unaddressed(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION broadcast_unaddressed(uuid) TO logitrack_app;
ALTER FUNCTION broadcast_unaddressed(uuid) OWNER TO logitrack_rls_definer;
REVOKE ALL ON FUNCTION broadcast_platform_audience() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION broadcast_platform_audience() TO logitrack_app;
ALTER FUNCTION broadcast_platform_audience() OWNER TO logitrack_rls_definer;
REVOKE CREATE ON SCHEMA public FROM logitrack_rls_definer;

-- broadcasts.p_read of 0007 plus the driver's broadcasts addressed to it in another tenant (a carrier's driver reached
-- by the own fleet). Voided rows stay hidden from drivers; staff and stewards read as before.
ALTER POLICY p_read ON broadcasts USING (
     (app_is_staff() AND (tenant_id IS NULL OR app_tenant_in_reach(tenant_id)))
  OR (app_role() = 'driver' AND voided_at IS NULL
      AND (tenant_id IS NULL OR tenant_id = app_tenant_id() OR app_broadcast_recipient(id)))
  OR (tenant_id IS NULL AND app_is_steward()));

-- +goose Down
-- The 0007 expression first: the policy depends on the helper.
ALTER POLICY p_read ON broadcasts USING (
     (app_is_staff() AND (tenant_id IS NULL OR app_tenant_in_reach(tenant_id)))
  OR (app_role() = 'driver' AND voided_at IS NULL AND (tenant_id IS NULL OR tenant_id = app_tenant_id()))
  OR (tenant_id IS NULL AND app_is_steward()));
-- The functions belong to logitrack_rls_definer: dropped as their owner (0001).
CALL app_drop_definer_function('broadcast_platform_audience()');
CALL app_drop_definer_function('broadcast_unaddressed(uuid)');
CALL app_drop_definer_function('app_broadcast_recipient(uuid)');
REVOKE SELECT ON broadcast_recipients, users, tenants FROM logitrack_rls_definer;
