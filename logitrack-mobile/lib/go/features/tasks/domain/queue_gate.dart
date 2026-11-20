import 'driver_task.dart';

/// Whether [t] holds back the tasks after it in the queue (spec mobile-go-core-loop R11; the port of
/// `taskBlocksSuccessorInQueue`, task_repository.dart:79-98, on the canonical statuses). Completed and cancelled
/// tasks never block; a checked-in task whose trip is delivered does not either; pending, assigned, in transit,
/// an undelivered check-in and unknown statuses do. The server enforces the same gate (409 queue_blocked, R22).
bool taskBlocksSuccessor(DriverTask t) {
  switch (t.status) {
    case TaskStatus.completed:
    case TaskStatus.cancelled:
      return false;
    case TaskStatus.checkedIn:
      return !(t.trip?.delivered ?? false);
    case TaskStatus.pending:
    case TaskStatus.assigned:
    case TaskStatus.inTransit:
    case TaskStatus.unknown:
      return true;
  }
}

/// Whether [task] may be checked in given the queue [ordered] as the server sent it (ADR 0028 order): no task before
/// it blocks. A task missing from the queue is not eligible.
bool isQueueEligibleForCheckIn(DriverTask task, List<DriverTask> ordered) {
  final idx = ordered.indexWhere((x) => x.id == task.id);
  if (idx < 0) return false;
  for (var i = 0; i < idx; i++) {
    if (taskBlocksSuccessor(ordered[i])) return false;
  }
  return true;
}
