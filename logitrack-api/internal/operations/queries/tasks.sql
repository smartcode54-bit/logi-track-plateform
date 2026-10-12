-- Tasks (Appendix A §A.2.3, 0004_operations): the plan of a job (First Mile / Line Haul), its delivery stops
-- (task_delivery_stops, multi-drop) and its one helper (helper_driver_id, ADR 0011). Tenant-stamped (tenant_source
-- 'driver' | 'form' | 'quarantine'); RLS decides every row (Appendix C §C.3.0): staff of the tenant reach
-- (p_tenant_staff), the assigned driver or helper (p_driver_read / p_driver_update, R28), a driver's own manual task
-- (p_driver_insert), dispatcher and customer scope through scope_tasks (internal/scope). The staff list and detail
-- queries name the reach as well (Appendix C §C.3.8): all_tenants under X-Act-On-Tenant: * or in a system transaction,
-- else tenant_ids (authz.Principal.TenantReach), and reach_driver for a driver principal (own or helper task).
--
-- Indexes -> live query (Appendix A §A.2.3 notes; explain_integration_test.go plans each shape):
-- tasks_tenant_type_created / tasks_tenant_type_plan (the boards without / with a date), tasks_driver_status (check-in
-- gate, driver queue), tasks_helper_plan (helper queue), tasks_driver_run (run_order), tasks_active_status (badges),
-- tasks_task_no, tasks_legacy, tasks_client_op, tasks_truck.

-- --- task numbers (R10) -----------------------------------------------------------------------------------------

-- name: NextTaskSeq :one
-- The next number of (task_type, plan_date), global across tenants (R10), inside the task insert transaction: the
-- counter row stays locked until COMMIT, so two concurrent inserts of one type and day get distinct numbers, and a
-- rolled-back insert leaves a gap, never a duplicate. next_task_seq() is SECURITY DEFINER owned by
-- logitrack_rls_definer (R66); logitrack_app holds no privilege on task_number_counters (R67).
SELECT next_task_seq(sqlc.arg(task_type)::text, sqlc.arg(plan_date)::date)::int AS seq;

-- name: BumpTaskSeq :one
-- A task that keeps a canonical number of record (FM|LH-ddMMyyyy-NNN, padded: a legacy or mirrored number, CreateTask)
-- moves the counter of the (task_type, plan_date) the number names up to its sequence, never down
-- (bump_task_seq(), SECURITY DEFINER, 0014), so NextTaskSeq never mints that number again. It locks the counter row
-- until COMMIT, as NextTaskSeq does.
SELECT bump_task_seq(sqlc.arg(task_type)::text, sqlc.arg(plan_date)::date, sqlc.arg(seq)::int)::int AS last_seq;

-- --- run order (per-driver queue position) ----------------------------------------------------------------------

-- name: LockDriverQueue :exec
-- Serialises the run_order = max + 1 of one driver's queue for the rest of the transaction (a transaction-level
-- advisory lock: a staff writer may not hold UPDATE on a driver of another tenant, and the lock must not wait on the
-- driver row's other writers). Every writer that sets or recomputes a run_order takes it before NextRunOrder (task
-- creation here; the PATCH /v1/tasks/{id} assignment of T31, the import, the mirror): a drivers row lock would not
-- exclude it (developer-spec.md §3.6). Replaces the client-side max read (taskService.ts:51-66, tasks.ts:123-128).
SELECT pg_advisory_xact_lock(hashtextextended('lt-run-order:' || sqlc.arg(driver_id)::uuid::text, 0));

-- name: NextRunOrder :one
-- max(run_order) + 1 over the driver's tasks the writer can read (tasks_driver_run).
SELECT (coalesce(max(run_order), 0) + 1)::int AS run_order FROM tasks WHERE driver_id = sqlc.arg(driver_id)::uuid;

-- --- writes -----------------------------------------------------------------------------------------------------

-- name: InsertTask :one
-- tenant_id / tenant_source come from the caller's stamping (T28: the driver's tenant, else the writer's with 'form',
-- R13); task_no from NextTaskSeq unless the caller carries a number of record (an import row, a legacy number kept
-- verbatim). A driver's manual task (client_op_id set) is idempotent per (driver_id, client_op_id) (R63): a replay
-- inserts nothing and the service returns the stored row.
INSERT INTO tasks (legacy_doc_id, tenant_id, tenant_source, task_no, task_type, job_category, status, plan_at,
                   plan_time, legacy_date_str, actual_pickup_at, source_hub_raw, source_hub_id, source_linked_party_id,
                   destination_raw, destination_hub_id, destination_soc_key, destination_linked_party_id,
                   billing_party_id, truck_id, truck_type, legacy_truck_type, license_plate_snapshot, driver_id,
                   legacy_driver_ref, driver_ref_match, helper_driver_id, run_order, is_multi_delivery, client_op_id,
                   created_by)
VALUES (sqlc.narg(legacy_doc_id), sqlc.arg(tenant_id), sqlc.arg(tenant_source), sqlc.narg(task_no),
        sqlc.arg(task_type), sqlc.narg(job_category), sqlc.arg(status), sqlc.arg(plan_at), sqlc.narg(plan_time),
        sqlc.narg(legacy_date_str), sqlc.narg(actual_pickup_at), sqlc.arg(source_hub_raw), sqlc.narg(source_hub_id),
        sqlc.narg(source_linked_party_id), sqlc.arg(destination_raw), sqlc.narg(destination_hub_id),
        sqlc.narg(destination_soc_key), sqlc.narg(destination_linked_party_id), sqlc.narg(billing_party_id),
        sqlc.narg(truck_id), sqlc.narg(truck_type), sqlc.narg(legacy_truck_type), sqlc.narg(license_plate_snapshot),
        sqlc.narg(driver_id), sqlc.narg(legacy_driver_ref), sqlc.narg(driver_ref_match), sqlc.narg(helper_driver_id),
        sqlc.narg(run_order), sqlc.arg(is_multi_delivery), sqlc.narg(client_op_id), sqlc.narg(created_by))
ON CONFLICT (driver_id, client_op_id) WHERE client_op_id IS NOT NULL DO NOTHING
RETURNING *;

-- name: LockTask :one
-- The write lock of a task update (FOR UPDATE: RLS also applies the UPDATE policies, so only staff of the reach or the
-- assigned driver or helper lock it).
SELECT * FROM tasks WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: UpdateTask :one
-- PATCH writes the merged row (Go merges the request into the locked row; an absent field keeps its value, and status
-- and run_order change only when the caller sets them, fixing tasks.ts:120-137; a cancelled task reopened by a new
-- driver clears its cancellation, the legacy edit's Cancelled -> Assigned). Never tenant_id (frozen,
-- t_freeze_tenant_id; the R13 reassignment is the tenant-move path of T28, iam.TaskMover + tenancy.MoveTask), task_no,
-- task_type or the check-in columns.
UPDATE tasks
SET job_category = sqlc.narg(job_category), status = sqlc.arg(status), plan_at = sqlc.arg(plan_at),
    plan_time = sqlc.narg(plan_time), actual_pickup_at = sqlc.narg(actual_pickup_at),
    source_hub_raw = sqlc.arg(source_hub_raw), source_hub_id = sqlc.narg(source_hub_id),
    source_linked_party_id = sqlc.narg(source_linked_party_id), destination_raw = sqlc.arg(destination_raw),
    destination_hub_id = sqlc.narg(destination_hub_id), destination_soc_key = sqlc.narg(destination_soc_key),
    destination_linked_party_id = sqlc.narg(destination_linked_party_id),
    billing_party_id = sqlc.narg(billing_party_id), truck_id = sqlc.narg(truck_id), truck_type = sqlc.narg(truck_type),
    legacy_truck_type = sqlc.narg(legacy_truck_type), license_plate_snapshot = sqlc.narg(license_plate_snapshot),
    driver_id = sqlc.narg(driver_id), legacy_driver_ref = sqlc.narg(legacy_driver_ref),
    driver_ref_match = sqlc.narg(driver_ref_match), helper_driver_id = sqlc.narg(helper_driver_id),
    run_order = sqlc.narg(run_order), is_multi_delivery = sqlc.arg(is_multi_delivery),
    cancelled_at = sqlc.narg(cancelled_at), cancel_reason = sqlc.narg(cancel_reason)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: CheckInTask :one
-- The driver's check-in (POST /v1/mobile/tasks/{id}/check-in, P7a): status checked_in, the evidence files (committed in
-- the same transaction), the position and the truck of this job (the vehicle-identity rule: the truck of one job,
-- never drivers.current_assignment_id). The helper is set only when the request names one.
UPDATE tasks
SET status = 'checked_in', check_in_at = sqlc.arg(check_in_at)::timestamptz, check_in_photo_file_id = sqlc.narg(photo_file_id),
    check_in_app_screenshot_file_id = sqlc.narg(app_screenshot_file_id), check_in_lat = sqlc.narg(lat),
    check_in_lng = sqlc.narg(lng), truck_id = sqlc.narg(truck_id), truck_type = sqlc.narg(truck_type),
    license_plate_snapshot = sqlc.narg(license_plate_snapshot),
    helper_driver_id = coalesce(sqlc.narg(helper_driver_id)::uuid, helper_driver_id)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: SetTaskStatus :one
-- A lifecycle step written by another aggregate's transaction: in_transit at loading, completed at delivery or standby.
UPDATE tasks SET status = sqlc.arg(status) WHERE id = sqlc.arg(id) RETURNING *;

-- name: CancelTask :one
-- POST /v1/tasks/{id}/cancel; a task already cancelled is not rewritten (no row).
UPDATE tasks SET status = 'cancelled', cancelled_at = sqlc.arg(cancelled_at)::timestamptz, cancel_reason = sqlc.narg(reason)
WHERE id = sqlc.arg(id) AND status <> 'cancelled'
RETURNING *;

-- name: SetTaskMultiDelivery :exec
-- The first extra stop upgrades a single-stop task (addDeliveryStop, multiDeliveryTrips.ts:420-424).
UPDATE tasks SET is_multi_delivery = true WHERE id = sqlc.arg(id) AND NOT is_multi_delivery;

-- --- delivery stops (task_delivery_stops: parent-scoped, the parent's reach writes, the assigned driver too) -------

-- name: InsertTaskStop :one
-- UNIQUE (task_id, stop_index) and UNIQUE (task_id, destination_raw): a repeated destination is 409
-- duplicate_destination (taskSchema refine; the service checks the normalised form first).
INSERT INTO task_delivery_stops (task_id, stop_index, destination_raw, destination_hub_id, destination_linked_party_id,
                                 sequence, added_at, source_id, is_custom)
VALUES (sqlc.arg(task_id), sqlc.arg(stop_index), sqlc.arg(destination_raw), sqlc.narg(destination_hub_id),
        sqlc.narg(destination_linked_party_id), sqlc.narg(sequence), sqlc.narg(added_at), sqlc.narg(source_id),
        sqlc.arg(is_custom))
RETURNING *;

-- name: TaskStops :many
SELECT * FROM task_delivery_stops WHERE task_id = sqlc.arg(task_id) ORDER BY stop_index;

-- name: TaskStopsOf :many
-- The stops of a page of tasks (one round trip instead of one per row).
SELECT * FROM task_delivery_stops WHERE task_id = ANY (sqlc.arg(task_ids)::uuid[]) ORDER BY task_id, stop_index;

-- name: SetTaskStopStatus :one
-- A planned stop's outcome (delivered / failed) when its trip stop is delivered.
UPDATE task_delivery_stops
SET status = sqlc.arg(status), delivered_at = sqlc.narg(delivered_at), delivered_lat = sqlc.narg(lat),
    delivered_lng = sqlc.narg(lng)
WHERE task_id = sqlc.arg(task_id) AND stop_index = sqlc.arg(stop_index)
RETURNING *;

-- --- reads ------------------------------------------------------------------------------------------------------

-- name: GetTask :one
-- GET /v1/tasks/{id} (staff, or the driver or helper of the task: driver:self, R33).
SELECT * FROM tasks t
WHERE t.id = sqlc.arg(id)
  AND (sqlc.arg(all_tenants)::boolean OR t.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[])
       OR t.driver_id = sqlc.narg(reach_driver)::uuid OR t.helper_driver_id = sqlc.narg(reach_driver)::uuid);

-- name: TaskByLegacyDocID :one
-- The shim targets accept a Firestore task doc id (Appendix B §B.2.21: resolved through legacy_doc_id).
SELECT * FROM tasks t
WHERE t.legacy_doc_id = sqlc.arg(legacy_doc_id)::text
  AND (sqlc.arg(all_tenants)::boolean OR t.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[])
       OR t.driver_id = sqlc.narg(reach_driver)::uuid OR t.helper_driver_id = sqlc.narg(reach_driver)::uuid);

-- name: TasksByNumber :many
-- GET /v1/tasks/by-number/{taskNo}: an array, because legacy numbers are not unique (web padded, mobile not); the
-- number is matched verbatim (an unpadded legacy FM-01102026-7 is not FM-01102026-007).
SELECT * FROM tasks t
WHERE t.task_no = sqlc.arg(task_no)::text
  AND (sqlc.arg(all_tenants)::boolean OR t.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[])
       OR t.driver_id = sqlc.narg(reach_driver)::uuid OR t.helper_driver_id = sqlc.narg(reach_driver)::uuid)
ORDER BY t.created_at, t.id;

-- name: TaskByClientOp :one
-- The stored task of a replayed manual check-in (R63).
SELECT * FROM tasks WHERE driver_id = sqlc.arg(driver_id)::uuid AND client_op_id = sqlc.arg(client_op_id)::uuid;

-- name: ListTasksByCreated :many
-- GET /v1/tasks without a date: newest first, keyset (created_at, id) (tasks_tenant_type_created; tasks_created under
-- X-Act-On-Tenant: *). driver_id matches the driver or the helper; q is a substring of the number, the places or the
-- plate.
SELECT * FROM tasks t
WHERE (sqlc.arg(all_tenants)::boolean OR t.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND (sqlc.narg(task_type)::text IS NULL OR t.task_type = sqlc.narg(task_type)::text)
  AND (sqlc.narg(statuses)::text[] IS NULL OR t.status = ANY (sqlc.narg(statuses)::text[]))
  AND (sqlc.narg(driver_id)::uuid IS NULL OR t.driver_id = sqlc.narg(driver_id)::uuid
       OR t.helper_driver_id = sqlc.narg(driver_id)::uuid)
  AND (sqlc.narg(truck_id)::uuid IS NULL OR t.truck_id = sqlc.narg(truck_id)::uuid)
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(concat_ws(' ', t.task_no, t.source_hub_raw, t.destination_raw, t.license_plate_snapshot)),
                 lower(sqlc.narg(q)::text)) > 0)
  AND (sqlc.narg(after_created)::timestamptz IS NULL
       OR (t.created_at, t.id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY t.created_at DESC, t.id DESC
LIMIT sqlc.arg(row_limit);

-- name: ListTasksByPlan :many
-- GET /v1/tasks?date | from&to: the plan days (Bangkok) of the range, latest day first, keyset (plan_date, id)
-- (tasks_tenant_type_plan; tasks_plan_date under X-Act-On-Tenant: *).
SELECT * FROM tasks t
WHERE (sqlc.arg(all_tenants)::boolean OR t.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND t.plan_date >= sqlc.arg(from_date)::date AND t.plan_date <= sqlc.arg(to_date)::date
  AND (sqlc.narg(task_type)::text IS NULL OR t.task_type = sqlc.narg(task_type)::text)
  AND (sqlc.narg(statuses)::text[] IS NULL OR t.status = ANY (sqlc.narg(statuses)::text[]))
  AND (sqlc.narg(driver_id)::uuid IS NULL OR t.driver_id = sqlc.narg(driver_id)::uuid
       OR t.helper_driver_id = sqlc.narg(driver_id)::uuid)
  AND (sqlc.narg(truck_id)::uuid IS NULL OR t.truck_id = sqlc.narg(truck_id)::uuid)
  AND (sqlc.narg(q)::text IS NULL
       OR strpos(lower(concat_ws(' ', t.task_no, t.source_hub_raw, t.destination_raw, t.license_plate_snapshot)),
                 lower(sqlc.narg(q)::text)) > 0)
  AND (sqlc.narg(after_plan)::date IS NULL
       OR (t.plan_date, t.id) < (sqlc.narg(after_plan)::date, sqlc.arg(after_id)::uuid))
ORDER BY t.plan_date DESC, t.id DESC
LIMIT sqlc.arg(row_limit);

-- name: CountTasksOnDay :many
-- The day counts of the boards (taskService.ts:77-83 counted every tenant's tasks; the reach is named now).
SELECT t.task_type, t.status, count(*)::int AS n
FROM tasks t
WHERE (sqlc.arg(all_tenants)::boolean OR t.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND t.plan_date = sqlc.arg(plan_date)::date
GROUP BY t.task_type, t.status
ORDER BY t.task_type, t.status;

-- name: DriverTasks :many
-- The driver's queue (GET /v1/mobile/tasks, P7a): own and helper tasks (R28), optionally by status, in the queue order
-- of ADR 0028 (compareTasksForDriverQueue, task_repository.dart:16-70), applied here before the LIMIT: the dispatch
-- instant queue_at (taskQueueInstant: actual_pickup_at, else the Bangkok plan day at plan_time, 00:00 without one),
-- then run_order with the tasks that have none first (run_order >= 1, so NULL keys as 0), then created_at, then id.
-- A keyset page on that key (after_*: the last row of the previous page); the service asks for limit + 1 rows, so a
-- longer queue gets a next cursor, never a silent cut (TW8). tasks_driver_status + tasks_helper_plan.
SELECT sqlc.embed(t), k.queue_at::timestamptz AS queue_at
FROM tasks t
CROSS JOIN LATERAL (VALUES (coalesce(t.actual_pickup_at,
                                     bkk_midnight(t.plan_date)
                                     + coalesce(split_part(t.plan_time, ':', 1)::int, 0) * interval '1 hour'
                                     + coalesce(split_part(t.plan_time, ':', 2)::int, 0) * interval '1 minute'))) AS k(queue_at)
WHERE (t.driver_id = sqlc.arg(driver_id)::uuid OR t.helper_driver_id = sqlc.arg(driver_id)::uuid)
  AND (sqlc.narg(statuses)::text[] IS NULL OR t.status = ANY (sqlc.narg(statuses)::text[]))
  AND (sqlc.narg(after_queue_at)::timestamptz IS NULL
       OR (k.queue_at, coalesce(t.run_order, 0), t.created_at, t.id)
          > (sqlc.narg(after_queue_at)::timestamptz, sqlc.arg(after_run_order)::int, sqlc.arg(after_created_at)::timestamptz,
             sqlc.arg(after_id)::uuid))
ORDER BY k.queue_at, coalesce(t.run_order, 0), t.created_at, t.id
LIMIT sqlc.arg(row_limit);

-- name: DriverHasTaskInStatus :one
-- The check-in / loading gate (task_repository.dart:166-183, loading_phase_page.dart:189-194; tasks_driver_status).
SELECT EXISTS (SELECT 1 FROM tasks t WHERE t.driver_id = sqlc.arg(driver_id)::uuid AND t.status = sqlc.arg(status)::text) AS found;

-- name: ActiveTaskDrivers :many
-- The "On run" badges (taskService.ts:50-55; tasks_active_status): drivers with a checked-in or in-transit task.
SELECT t.id, t.driver_id, t.status, t.task_type, t.task_no, t.check_in_at
FROM tasks t
WHERE t.status IN ('checked_in', 'in_transit')
  AND (sqlc.arg(all_tenants)::boolean OR t.tenant_id = ANY (sqlc.arg(tenant_ids)::uuid[]))
  AND t.driver_id IS NOT NULL
ORDER BY t.driver_id, t.check_in_at DESC NULLS LAST, t.id DESC;
