-- The R13 task reassignment (T28, Appendix C §C.1.7, §C.3.6): the reads of iam.TaskMover in its system transaction,
-- after Go authorization. The task itself is locked and written through internal/operations (operationsdb.LockTask,
-- UpdateTask, LockDriverQueue, NextRunOrder) and moved by tenancy.MoveTask.

-- name: TaskMoveTarget :one
-- The target tenant of a reassignment (FOR KEY SHARE, as a membership write takes it): it must exist, not be the
-- quarantine tenant, and be active; contractor_tenant_id decides which helpers and trucks may stay on the task.
SELECT kind, status, contractor_tenant_id FROM tenants WHERE id = sqlc.arg(id) FOR KEY SHARE;

-- name: TaskMoveDriver :one
-- The tenant and status of a driver the reassignment assigns or keeps (the driver, the helper).
SELECT tenant_id, status FROM drivers WHERE id = sqlc.arg(id);

-- name: TaskMoveTruck :one
-- The tenant of the truck the task names.
SELECT tenant_id FROM trucks WHERE id = sqlc.arg(id);

-- name: TenantsLinked :one
-- Whether two tenants are one or linked by contractor_tenant_id in either direction (R60): a helper or truck of such a
-- tenant may serve the task.
SELECT (sqlc.arg(a)::uuid = sqlc.arg(b)::uuid OR EXISTS (
  SELECT 1 FROM tenants t
  WHERE (t.id = sqlc.arg(a)::uuid AND t.contractor_tenant_id = sqlc.arg(b)::uuid)
     OR (t.id = sqlc.arg(b)::uuid AND t.contractor_tenant_id = sqlc.arg(a)::uuid)))::boolean AS linked;

-- name: TaskPartyIDs :many
-- The billing parties of a task (its billing party, its linked source and destination, its stops' linked
-- destinations): the dispatch:{partyId}:tasks topics of its events (Appendix B §B.4.2).
SELECT DISTINCT p::uuid AS party_id FROM (
  SELECT unnest(ARRAY[t.billing_party_id, t.source_linked_party_id, t.destination_linked_party_id]) AS p
  FROM tasks t WHERE t.id = sqlc.arg(id)
  UNION ALL
  SELECT s.destination_linked_party_id FROM task_delivery_stops s WHERE s.task_id = sqlc.arg(id)
) x
WHERE p IS NOT NULL
ORDER BY 1;
