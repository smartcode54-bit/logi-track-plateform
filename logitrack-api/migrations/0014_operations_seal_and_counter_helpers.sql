-- 0014_operations_seal_and_counter_helpers.sql: Appendix A §A.2.12, Appendix C §C.3.3 (issue T27, review of PR #134).
-- Never edit this file once it is merged; follow-ups take the next number (R31).
-- Two SECURITY DEFINER helpers of the operations data layer (internal/operations); no table change:
--   trip_seal_taken(seal, exclude)        the duplicate-seal check of a trip submit (POST /v1/mobile/trips, 409
--                                         duplicate_seal): whether any trip other than exclude carries the seal, in
--                                         every tenant and every status, as the legacy check read every trip
--                                         (trip_records_repository.dart:59-62; firestore.rules lets any signed-in user
--                                         read trip_records). The submit runs in the driver's request transaction,
--                                         whose RLS shows only the driver's own trips (p_driver_read), so the check
--                                         cannot be a plain query. It answers a boolean only: which trip holds the seal
--                                         the caller reads under its own RLS (TripIDsBySeal), so no other tenant's
--                                         trip number leaks. It takes a per-seal transaction lock first, so the insert
--                                         that follows in the same transaction cannot race another submit of the seal
--                                         (the legacy read-then-write was TOCTOU; the request transactions are READ
--                                         COMMITTED, so the check after the lock sees the trip a finished submit
--                                         committed). Callers: staff (not a dispatcher), a driver, or the system
--                                         context; anyone else gets 42501.
--   bump_task_seq(type, day, seq)         moves the task-number counter of (type, Bangkok day) up to seq, never down,
--                                         when a task keeps a canonical number of record FM|LH-ddMMyyyy-NNN (a legacy or
--                                         mirrored number, CreateTask): the allocator then never mints that number
--                                         again. Like next_task_seq() it takes the counter row lock until COMMIT, so it
--                                         serialises with the allocator (R10, R67).
-- Grants: 0009_infra stays the single grant site of the baseline and of logitrack_app's privileges (R66); a SECURITY
-- DEFINER helper added later is handed over in its own file the 0009 way (ACL first, then ownership). Both read and
-- write only what 0009 already granted logitrack_rls_definer (SELECT on trip_records; SELECT, INSERT, UPDATE on
-- task_number_counters).
-- Depends on: 0001 (GUC helpers, app_drop_definer_function), 0004 (trip_records, task_number_counters), 0009 (the
-- definer's grants). Production applies it with the P1 runbook's `migrate up` (T24); it depends on nothing in
-- 0010-0013.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION trip_seal_taken(p_seal text, p_exclude uuid) RETURNS boolean
  LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  IF NOT (app_bypass() OR (app_is_staff() AND NOT app_is_dispatcher())
          OR (app_role() = 'driver' AND app_driver_id() IS NOT NULL)) THEN
    RAISE EXCEPTION 'the duplicate-seal check needs a staff, driver or system caller'
      USING ERRCODE = 'insufficient_privilege';
  END IF;
  IF p_seal IS NULL OR p_seal = '' THEN
    RETURN false;
  END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('lt-seal:' || p_seal, 0));
  RETURN EXISTS (SELECT 1 FROM trip_records t WHERE t.seal_code = p_seal AND t.id IS DISTINCT FROM p_exclude);
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION bump_task_seq(p_task_type text, p_plan_date date, p_seq int) RETURNS int
  LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
  INSERT INTO task_number_counters AS c (task_type, plan_date, last_seq) VALUES (p_task_type, p_plan_date, p_seq)
  ON CONFLICT (task_type, plan_date) DO UPDATE SET last_seq = GREATEST(c.last_seq, EXCLUDED.last_seq)
  RETURNING last_seq
$$;
-- +goose StatementEnd

-- The hand-over of 0009: ACL first, then ownership (the migrator holds logitrack_rls_definer WITH INHERIT FALSE and
-- cannot change the ACL of a function it no longer owns); ALTER ... OWNER TO needs CREATE here for the new owner.
GRANT CREATE ON SCHEMA public TO logitrack_rls_definer;
REVOKE ALL ON FUNCTION trip_seal_taken(text, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION trip_seal_taken(text, uuid) TO logitrack_app;
ALTER FUNCTION trip_seal_taken(text, uuid) OWNER TO logitrack_rls_definer;
REVOKE ALL ON FUNCTION bump_task_seq(text, date, int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION bump_task_seq(text, date, int) TO logitrack_app;
ALTER FUNCTION bump_task_seq(text, date, int) OWNER TO logitrack_rls_definer;
REVOKE CREATE ON SCHEMA public FROM logitrack_rls_definer;

-- +goose Down
-- The functions belong to logitrack_rls_definer: dropped as their owner (0001).
CALL app_drop_definer_function('bump_task_seq(text,date,integer)');
CALL app_drop_definer_function('trip_seal_taken(text,uuid)');
