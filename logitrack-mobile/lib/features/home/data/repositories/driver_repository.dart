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

  /// Record the truck this driver is responsible for RIGHT NOW (the truck of the task they just
  /// checked in on). Read by the fuel/other-expense forms and by the maintenance Firestore rule,
  /// so a driver running a vehicle other than their home binding still books expenses — and sees
  /// maintenance — against the truck they are actually driving.
  Future<void> setActiveTruck({
    required String driverId,
    required String truckId,
    required String truckPlate,
    required String taskId,
  }) async {
    if (driverId.isEmpty || truckId.isEmpty) return;
    try {
      await _firestore.collection('drivers').doc(driverId).set(
        {
          'activeTruck': {
            'truckId': truckId,
            'truckPlate': truckPlate,
            'taskId': taskId,
            'startedAt': FieldValue.serverTimestamp(),
          },
          'updatedAt': FieldValue.serverTimestamp(),
        },
        SetOptions(merge: true),
      );
    } catch (e) {
      // Non-fatal: the task itself already carries the truck. Losing this only means the
      // expense forms fall back to the home binding.
      throw Exception('Failed to set active truck: $e');
    }
  }

  /// Clear the active truck when the job is done, so an idle driver falls back to their home truck.
  /// Deletes the field rather than writing null — the maintenance rule reads it as a map.
  Future<void> clearActiveTruck(String driverId) async {
    if (driverId.isEmpty) return;
    try {
      await _firestore.collection('drivers').doc(driverId).update({
        'activeTruck': FieldValue.delete(),
        'updatedAt': FieldValue.serverTimestamp(),
      });
    } catch (e) {
      throw Exception('Failed to clear active truck: $e');
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
