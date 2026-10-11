-- 0013_driver_assignment_helpers.sql: Appendix A §A.2.11, Appendix C §C.3.3 (issue T23, review of PR #127). Never
-- edit this file once it is merged; follow-ups take the next number (R31).
-- A truck assignment carries the tenant of its truck (tenant_source 'truck') and binds a driver of that tenant or of
-- one linked to it by contractor_tenant_id (R60), so the staff writing an assignment do not always see the driver's
-- side: a carrier's staff cannot read the own fleet's row that holds their driver, and a carrier revoking the row of
-- its truck that binds an own-fleet driver cannot read that driver. Two SECURITY DEFINER helpers answer the driver's
-- side independently of the caller's RLS. They bypass RLS, so each checks its caller itself; internal/fleet calls
-- them inside the request transaction after Go authorization (fleet:manage_assignments):
--   driver_active_assignment(driver)  the driver's newest active assignment in any tenant, else NULL: the 409
--                                     driver_assigned of POST /v1/truck-assignments. NULL as well unless the caller
--                                     is staff reaching the driver (or the system context).
--   assignment_release_driver(...)    the driver's side of POST /v1/truck-assignments/{id}/revoke, for a revoked
--                                     assignment of the caller's reach: when the driver's current_assignment_id names
--                                     it, a row that is no longer active, or nothing, the pointer moves to the
--                                     driver's newest active row in any tenant or clears, and on_duty without one
--                                     becomes active with its status_history entry; a pointer at another active row
--                                     stays. Returns the driver's tenant (the event's second topic).
-- Rows of the quarantine tenant never count (the helpers bypass p_not_quarantine). Grants: 0009_infra stays the single
-- grant site of the baseline and of logitrack_app's privileges (R66); a SECURITY DEFINER helper added later is handed
-- over in its own file the 0009 way (ACL first, then ownership) with exactly what it reads and writes.
-- Depends on: 0001 (GUC helpers, app_drop_definer_function), 0002 (status_history), 0003 (drivers,
-- truck_assignments), 0009 (logitrack_rls_definer's SELECT on drivers). Production applies it with the P1 runbook's
-- `migrate up` (T24); it depends on nothing in 0010-0012.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION driver_active_assignment(p_driver uuid) RETURNS uuid
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
  SELECT a.id
    FROM truck_assignments a
   WHERE a.driver_id = p_driver AND a.status = 'active' AND a.tenant_id <> app_quarantine_tenant_id()
     AND (app_bypass() OR (app_is_staff() AND EXISTS (
           SELECT 1 FROM drivers d
            WHERE d.id = p_driver AND d.tenant_id <> app_quarantine_tenant_id() AND app_tenant_in_reach(d.tenant_id))))
   ORDER BY a.created_at DESC, a.id DESC
   LIMIT 1
$$;
-- +goose StatementEnd

-- p_user and p_label are the status entry's actor (internal/fleet's actor(): a person's user id, which must be the
-- caller's app.user_id, or NULL and the label of an API key).
-- +goose StatementBegin
CREATE FUNCTION assignment_release_driver(p_assignment uuid, p_user uuid, p_label text, p_reason text,
                                          p_at timestamptz) RETURNS uuid
  LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE
  a      record;
  d      record;
  v_next uuid;
  v_st   text;
BEGIN
  SELECT x.driver_id, x.tenant_id, x.status INTO a FROM truck_assignments x WHERE x.id = p_assignment;
  IF NOT FOUND OR NOT (app_bypass() OR (app_is_staff() AND app_tenant_in_reach(a.tenant_id)
                                         AND a.tenant_id <> app_quarantine_tenant_id())) THEN
    RAISE EXCEPTION 'truck assignment % is outside the caller''s reach', p_assignment
      USING ERRCODE = 'insufficient_privilege';
  END IF;
  IF a.status = 'active' THEN
    RAISE EXCEPTION 'truck assignment % is still active', p_assignment
      USING ERRCODE = 'object_not_in_prerequisite_state';
  END IF;
  IF p_user IS NOT NULL AND p_user IS DISTINCT FROM app_user_id() THEN
    RAISE EXCEPTION 'the status entry must name the caller' USING ERRCODE = 'insufficient_privilege';
  END IF;
  -- The same driver lock as POST /v1/truck-assignments: an assignment and a release of one driver serialise.
  SELECT x.tenant_id, x.status, x.current_assignment_id INTO d FROM drivers x WHERE x.id = a.driver_id FOR UPDATE;
  IF d.current_assignment_id IS NOT NULL AND EXISTS (
       SELECT 1 FROM truck_assignments x WHERE x.id = d.current_assignment_id AND x.status = 'active') THEN
    RETURN d.tenant_id;                    -- the home truck is another assignment that is still active
  END IF;
  SELECT x.id INTO v_next FROM truck_assignments x
   WHERE x.driver_id = a.driver_id AND x.status = 'active' AND x.tenant_id <> app_quarantine_tenant_id()
   ORDER BY x.created_at DESC, x.id DESC
   LIMIT 1;
  v_st := CASE WHEN v_next IS NULL AND d.status = 'on_duty' THEN 'active' ELSE d.status END;
  IF v_next IS DISTINCT FROM d.current_assignment_id OR v_st <> d.status THEN
    UPDATE drivers SET current_assignment_id = v_next, status = v_st WHERE id = a.driver_id;
  END IF;
  IF v_st <> d.status THEN
    INSERT INTO status_history (entity_type, entity_id, status, previous_status, changed_at, changed_by_user_id,
                                changed_by_label, reason)
    VALUES ('driver', a.driver_id, v_st, d.status, p_at, p_user, p_label, p_reason);
  END IF;
  RETURN d.tenant_id;
END $$;
-- +goose StatementEnd

-- What the two helpers read and write (logitrack_rls_definer is BYPASSRLS; 0009 granted it SELECT on drivers).
GRANT SELECT ON truck_assignments TO logitrack_rls_definer;
GRANT UPDATE (current_assignment_id, status) ON drivers TO logitrack_rls_definer;
GRANT INSERT ON status_history TO logitrack_rls_definer;
-- The hand-over of 0009: ACL first, then ownership (the migrator holds logitrack_rls_definer WITH INHERIT FALSE and
-- cannot change the ACL of a function it no longer owns); ALTER ... OWNER TO needs CREATE here for the new owner.
GRANT CREATE ON SCHEMA public TO logitrack_rls_definer;
REVOKE ALL ON FUNCTION driver_active_assignment(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION driver_active_assignment(uuid) TO logitrack_app;
ALTER FUNCTION driver_active_assignment(uuid) OWNER TO logitrack_rls_definer;
REVOKE ALL ON FUNCTION assignment_release_driver(uuid, uuid, text, text, timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION assignment_release_driver(uuid, uuid, text, text, timestamptz) TO logitrack_app;
ALTER FUNCTION assignment_release_driver(uuid, uuid, text, text, timestamptz) OWNER TO logitrack_rls_definer;
REVOKE CREATE ON SCHEMA public FROM logitrack_rls_definer;

-- +goose Down
-- The functions belong to logitrack_rls_definer: dropped as their owner (0001).
CALL app_drop_definer_function('assignment_release_driver(uuid,uuid,text,text,timestamptz)');
CALL app_drop_definer_function('driver_active_assignment(uuid)');
REVOKE INSERT ON status_history FROM logitrack_rls_definer;
REVOKE UPDATE (current_assignment_id, status) ON drivers FROM logitrack_rls_definer;
REVOKE SELECT ON truck_assignments FROM logitrack_rls_definer;
