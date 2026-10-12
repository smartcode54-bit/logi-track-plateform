-- 0020_task_helper_payroll_lock.sql: Appendix B §B.2.11 (PATCH /v1/tasks/{id}: "helper change checks payroll lock
-- (409 payroll_locked)"), Appendix C §C.3.3 (issue T31, review of PR #160). Never edit this file once it is merged;
-- follow-ups take the next number (R31).
-- One SECURITY DEFINER helper of the operations write routes (internal/operations); no table change:
--   task_helper_payroll_locks(drivers, period, round)
--                                     the payroll runs of drivers for one pay period (YYYY-MM) and round (R1 | R2) that
--                                     are approved or paid: the payroll lock of a task's helper change (the legacy
--                                     EditTripDetailsDialog refused it in the browser when the payout of the old or the
--                                     new helper for the task's Bangkok period and round was APPROVED or PAID). The task
--                                     write runs in the caller's request transaction, whose RLS hides payroll_runs from
--                                     staff without hr:view_payroll and a carrier helper's run of another tenant, so the
--                                     check cannot be a plain query. It answers only the driver ids the caller named and
--                                     their run status. Callers: staff or the system context; anyone else gets 42501.
-- Rows of the quarantine tenant never count. Grants: 0009_infra stays the single grant site of the baseline and of
-- logitrack_app's privileges (R66); a SECURITY DEFINER helper added later is handed over in its own file the 0009 way
-- (ACL first, then ownership) with exactly what it reads (SELECT on payroll_runs).
-- Depends on: 0001 (GUC helpers, app_drop_definer_function), 0006 (payroll_runs). Production applies it with
-- `migrate up` before the operations write routes serve; it depends on nothing in 0010-0019.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION task_helper_payroll_locks(p_drivers uuid[], p_period text, p_round text)
  RETURNS TABLE (driver_id uuid, status text)
  LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  IF NOT (app_bypass() OR app_is_staff()) THEN
    RAISE EXCEPTION 'the helper payroll lock needs a staff or system caller' USING ERRCODE = 'insufficient_privilege';
  END IF;
  RETURN QUERY
    SELECT r.driver_id, r.status
      FROM payroll_runs r
     WHERE r.driver_id = ANY (p_drivers) AND r.pay_period = p_period AND r.pay_round = p_round
       AND r.status IN ('approved', 'paid') AND r.tenant_id <> app_quarantine_tenant_id()
     ORDER BY r.driver_id, r.status;
END $$;
-- +goose StatementEnd

-- What the helper reads (logitrack_rls_definer is BYPASSRLS).
GRANT SELECT ON payroll_runs TO logitrack_rls_definer;
-- The hand-over of 0009: ACL first, then ownership (the migrator holds logitrack_rls_definer WITH INHERIT FALSE and
-- cannot change the ACL of a function it no longer owns); ALTER ... OWNER TO needs CREATE here for the new owner.
GRANT CREATE ON SCHEMA public TO logitrack_rls_definer;
REVOKE ALL ON FUNCTION task_helper_payroll_locks(uuid[], text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION task_helper_payroll_locks(uuid[], text, text) TO logitrack_app;
ALTER FUNCTION task_helper_payroll_locks(uuid[], text, text) OWNER TO logitrack_rls_definer;
REVOKE CREATE ON SCHEMA public FROM logitrack_rls_definer;

-- +goose Down
-- The function belongs to logitrack_rls_definer: dropped as its owner (0001).
CALL app_drop_definer_function('task_helper_payroll_locks(uuid[],text,text)');
REVOKE SELECT ON payroll_runs FROM logitrack_rls_definer;
