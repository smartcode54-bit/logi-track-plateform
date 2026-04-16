import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:firebase_auth/firebase_auth.dart';

/// Sort key for driver task queue: [runOrder] ascending when set, else legacy bucket last by [createdAt] ascending.
int compareTasksForDriverQueue(Map<String, dynamic> a, Map<String, dynamic> b) {
  final roA = a['runOrder'];
  final roB = b['runOrder'];
  final hasA = roA is num;
  final hasB = roB is num;
  if (hasA && hasB) {
    final c = roA.toInt().compareTo(roB.toInt());
    if (c != 0) return c;
  } else if (hasA != hasB) {
    return hasA ? -1 : 1;
  }
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
