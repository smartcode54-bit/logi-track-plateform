import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:firebase_auth/firebase_auth.dart';

/// The instant a task is dispatched for — the key the driver queue is ordered by.
///
/// [actualPickupAt] (ADR 0028: the real date-time the admin sends the driver to go) when set,
/// otherwise the plan [date] at its stored [time]. The plan date is a billing tag that can sit in a
/// different month from the real work (that is the whole point of ADR 0027/0028), so ordering the
/// driver's queue by it put jobs in an order that did not match when they are actually run.
///
/// The fallback is not a different axis: [time] is itself derived from `actualPickupAt` at assign
/// (`useFirstMileTask` / `useLineHaulTask` / the import), so a task that carries an actual pickup
/// sorts identically either way — the two only diverge when the real pickup DAY differs from the
/// plan day, which is exactly the case this exists to order correctly. A task with neither
/// (a legacy record) returns null and sorts last.
DateTime? taskQueueInstant(Map<String, dynamic> t) {
  final actual = t['actualPickupAt'];
  if (actual is DateTime) return actual;

  final date = t['date'];
  if (date is! DateTime) return null;
  final m = RegExp(r'^(\d{1,2}):(\d{2})$').firstMatch((t['time'] as String? ?? '').trim());
  final hour = m == null ? 0 : int.parse(m.group(1)!);
  final minute = m == null ? 0 : int.parse(m.group(2)!);
  return DateTime(date.year, date.month, date.day, hour, minute);
}

/// Sort key for driver task queue:
/// 1. [taskQueueInstant] ascending — the actual pickup date-time when the admin set one,
///    else the plan date at its stored time. Earlier dispatch comes first.
/// 2. [runOrder] ascending as tiebreaker for tasks dispatched at the same instant.
///    Tasks WITHOUT [runOrder] (legacy records) sort BEFORE those with one.
/// 3. [createdAt] ascending as final fallback.
///
/// Tasks with neither an actual pickup nor a date sort after everything else — they are legacy
/// records with no scheduled pickup time and should be done last.
int compareTasksForDriverQueue(Map<String, dynamic> a, Map<String, dynamic> b) {
  final instantA = taskQueueInstant(a);
  final instantB = taskQueueInstant(b);

  // 1. Both schedulable → compare the dispatch instants (date AND time in one comparison).
  if (instantA != null && instantB != null) {
    final cmp = instantA.compareTo(instantB);
    if (cmp != 0) return cmp;
    // Same instant: fall through to runOrder tiebreaker.
  } else if (instantA != null) {
    return -1; // a is scheduled, b isn't → a sorts first.
  } else if (instantB != null) {
    return 1;  // b is scheduled, a isn't → b sorts first.
  }
  // Neither is schedulable: fall through to runOrder.

  // 2. runOrder tiebreaker; no-runOrder (legacy) sorts BEFORE runOrder-bearing tasks.
  final roA = a['runOrder'];
  final roB = b['runOrder'];
  final hasA = roA is num;
  final hasB = roB is num;
  if (hasA && hasB) {
    final c = roA.toInt().compareTo(roB.toInt());
    if (c != 0) return c;
  } else if (hasA != hasB) {
    return hasA ? 1 : -1;
  }

  // 3. Final fallback: createdAt ascending.
  final at = a['createdAt'] as DateTime?;
  final bt = b['createdAt'] as DateTime?;
  if (at == null && bt == null) return 0;
  if (at == null) return 1;
  if (bt == null) return -1;
  return at.compareTo(bt);
}

void sortTasksForDriverQueue(List<Map<String, dynamic>> list) {
  list.sort(compareTasksForDriverQueue);
}

/// True if successors must wait for this task to clear the queue.
bool taskBlocksSuccessorInQueue(
  Map<String, dynamic> t,
  Set<String> deliveredTaskIds,
) {
  final st = t['status'] as String? ?? '';
  if (st == 'Completed' || st == 'Cancelled' || st == 'Delivered') {
    return false;
  }
  final taskDocId = t['id'] as String? ?? '';
  final altTaskId = t['taskId'] as String? ??
      t['LineHaulTaskId'] as String? ??
      t['FirstMileTaskId'] as String? ??
      '';
  final isDeliveredByTrip = deliveredTaskIds.contains(taskDocId) ||
      (altTaskId.isNotEmpty && deliveredTaskIds.contains(altTaskId));
  if (st == 'Checked in' && isDeliveredByTrip) return false;
  // Pending, Assigned, undelivered Checked in, In-Transit, unknown — block successors.
  return true;
}

/// Whether [task] may start check-in given the full list [sortedAllTasks] (same order as UI queue).
bool isQueueEligibleForCheckIn({
  required Map<String, dynamic> task,
  required List<Map<String, dynamic>> sortedAllTasks,
  required Set<String> deliveredTaskIds,
}) {
  final id = task['id'] as String?;
  if (id == null) return false;
  final idx = sortedAllTasks.indexWhere((x) => x['id'] == id);
  if (idx < 0) return false;
  for (var i = 0; i < idx; i++) {
    if (taskBlocksSuccessorInQueue(sortedAllTasks[i], deliveredTaskIds)) {
      return false;
    }
  }
  return true;
}

/// Next [runOrder] for a new task for this driver (max existing + 1).
Future<int> getNextRunOrderForDriver(String driverId) async {
  if (driverId.isEmpty) return 1;
  final snap = await FirebaseFirestore.instance
      .collection('tasks')
      .where('driverId', isEqualTo: driverId)
      .get();
  var max = 0;
  for (final d in snap.docs) {
    final ro = d.data()['runOrder'];
    if (ro is int && ro > max) max = ro;
    if (ro is num && ro.toInt() > max) max = ro.toInt();
  }
  return max + 1;
}

/// Streams tasks assigned to the given driver (real-time from Firestore).
Stream<List<Map<String, dynamic>>> streamTasksForDriver(String driverId) {
  if (driverId.isEmpty) {
    return Stream.value([]);
  }
  return FirebaseFirestore.instance
      .collection('tasks')
      .where('driverId', isEqualTo: driverId)
      .snapshots()
      .map((snap) {
        final list = snap.docs.map((doc) {
          final data = Map<String, dynamic>.from(doc.data());
          data['id'] = doc.id;
          _convertTimestamp(data, 'date');
          // Ordering key for the queue (ADR 0028) — must be a DateTime before the sort runs.
          _convertTimestamp(data, 'actualPickupAt');
          _convertTimestamp(data, 'createdAt');
          _convertTimestamp(data, 'updatedAt');
          _convertTimestamp(data, 'checkInAt');
          return data;
        }).toList();
        sortTasksForDriverQueue(list);
        return list;
      });
}

/// Returns true if the driver has at least one task with status "Checked in".
/// Used to enforce step order: Check in → Loading → Deliver + incident.
///
/// Queries by Firestore driver document ID first (standard format). If no result is found,
/// also tries the current Auth UID as a fallback for legacy tasks where driverId stores Auth UID.
Future<bool> hasCheckedInTask(String driverId) async {
  if (driverId.isEmpty) return false;
  try {
    final snapshot = await FirebaseFirestore.instance
        .collection('tasks')
        .where('driverId', isEqualTo: driverId)
        .where('status', isEqualTo: 'Checked in')
        .limit(1)
        .get();
    if (snapshot.docs.isNotEmpty) return true;

    // Fallback: some tasks may store Auth UID as driverId (legacy or web-assigned tasks).
    final authUid = FirebaseAuth.instance.currentUser?.uid;
    if (authUid != null && authUid.isNotEmpty && authUid != driverId) {
      final snap2 = await FirebaseFirestore.instance
          .collection('tasks')
          .where('driverId', isEqualTo: authUid)
          .where('status', isEqualTo: 'Checked in')
          .limit(1)
          .get();
      return snap2.docs.isNotEmpty;
    }
    return false;
  } catch (_) {
    return false;
  }
}

void _convertTimestamp(Map<String, dynamic> data, String key) {
  final v = data[key];
  if (v is Timestamp) data[key] = v.toDate();
}
