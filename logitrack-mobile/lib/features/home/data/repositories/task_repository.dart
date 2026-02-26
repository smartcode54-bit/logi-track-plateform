import 'package:cloud_firestore/cloud_firestore.dart';

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
        // Sort by createdAt descending (no composite index required)
        list.sort((a, b) {
          final aAt = a['createdAt'] as DateTime?;
          final bAt = b['createdAt'] as DateTime?;
          if (aAt == null && bAt == null) return 0;
          if (aAt == null) return 1;
          if (bAt == null) return -1;
          return bAt.compareTo(aAt);
        });
        return list;
      });
}

void _convertTimestamp(Map<String, dynamic> data, String key) {
  final v = data[key];
  if (v is Timestamp) data[key] = v.toDate();
}
