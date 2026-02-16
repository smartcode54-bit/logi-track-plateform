import 'package:cloud_firestore/cloud_firestore.dart';

class DriverRepository {
  final FirebaseFirestore _firestore = FirebaseFirestore.instance;

  /// Returns driver data including document [id] (for task queries and FCM token update).
  Future<Map<String, dynamic>?> getCurrentDriver(String authId) async {
    try {
      final querySnapshot = await _firestore
          .collection('drivers')
          .where('authId', isEqualTo: authId)
          .limit(1)
          .get();

      if (querySnapshot.docs.isNotEmpty) {
        final doc = querySnapshot.docs.first;
        final data = doc.data();
        data['id'] = doc.id;
        return data;
      }
      return null;
    } catch (e) {
      throw Exception('Failed to fetch driver data: $e');
    }
  }

  /// Update driver's FCM token for push notifications.
  Future<void> updateFcmToken(String driverId, String? token) async {
    if (driverId.isEmpty) return;
    try {
      await _firestore.collection('drivers').doc(driverId).set(
            {'fcmToken': token, 'updatedAt': FieldValue.serverTimestamp()},
            SetOptions(merge: true),
          );
    } catch (e) {
      throw Exception('Failed to update FCM token: $e');
    }
  }
}
