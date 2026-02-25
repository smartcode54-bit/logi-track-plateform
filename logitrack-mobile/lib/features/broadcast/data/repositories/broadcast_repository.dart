import 'package:cloud_firestore/cloud_firestore.dart';

class BroadcastRepository {
  final FirebaseFirestore _firestore = FirebaseFirestore.instance;

  /// Stream broadcast messages for drivers, newest first.
  /// Cloud Function writes: sentAt, messageText, createdBy, createdByName, recipientCount, recipientGroup.
  Stream<QuerySnapshot<Map<String, dynamic>>> watchBroadcasts() {
    return _firestore
        .collection('broadcasts')
        .orderBy('sentAt', descending: true)
        .limit(100)
        .snapshots();
  }

  /// Stream of the latest broadcast's sentAt in milliseconds (for "new" badge). Empty → null.
  Stream<int?> watchLatestBroadcastSentAtMs() {
    return _firestore
        .collection('broadcasts')
        .orderBy('sentAt', descending: true)
        .limit(1)
        .snapshots()
        .map((snap) {
          if (snap.docs.isEmpty) return null;
          final sentAt = snap.docs.first.data()['sentAt'] as Timestamp?;
          return sentAt?.millisecondsSinceEpoch;
        });
  }
}
