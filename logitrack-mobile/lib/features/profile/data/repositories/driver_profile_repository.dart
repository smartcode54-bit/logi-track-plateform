import 'package:cloud_firestore/cloud_firestore.dart';

/// Repository for driver profile data.
/// Follows the same structure as web sources: feature-specific data layer.
class DriverProfileRepository {
  final FirebaseFirestore _firestore = FirebaseFirestore.instance;

  static const String _assignmentsCollection = 'truckAssignment';

  Future<Map<String, dynamic>?> getProfileByAuthId(String authId) async {
    try {
      final querySnapshot = await _firestore
          .collection('drivers')
          .where('authId', isEqualTo: authId)
          .limit(1)
          .get();

      if (querySnapshot.docs.isNotEmpty) {
        final data = querySnapshot.docs.first.data();
        data['id'] = querySnapshot.docs.first.id;
        return data;
      }
      return null;
    } catch (e) {
      throw Exception('Failed to fetch driver profile: $e');
    }
  }

  /// Fetches truck assignment history for a driver (same collection as web).
  Future<List<Map<String, dynamic>>> getAssignmentHistory(
    String driverId,
  ) async {
    try {
      final querySnapshot = await _firestore
          .collection(_assignmentsCollection)
          .where('driverId', isEqualTo: driverId)
          .get();

      final List<Map<String, dynamic>> list = [];
      for (final doc in querySnapshot.docs) {
        final data = doc.data();
        final createdAt = data['createdAt'];
        final revokedAt = data['revokedAt'];

        // Fetch truck type from trucks collection
        String truckType = '';
        final truckId = data['truckId'] as String?;
        if (truckId != null && truckId.isNotEmpty) {
          try {
            final truckDoc = await _firestore
                .collection('trucks')
                .doc(truckId)
                .get();
            if (truckDoc.exists) {
              truckType = truckDoc.data()?['type'] as String? ?? '';
            }
          } catch (_) {}
        }

        list.add({
          'id': doc.id,
          'truckPlate': data['truckPlate'] ?? '',
          'truckModel': data['truckModel'] ?? '',
          'truckType': truckType,
          'status': data['status'] ?? 'active',
          'createdAt': createdAt is Timestamp ? createdAt.toDate() : createdAt,
          'revokedAt': revokedAt is Timestamp ? revokedAt.toDate() : revokedAt,
        });
      }
      list.sort((a, b) {
        final aAt = a['createdAt'] as DateTime?;
        final bAt = b['createdAt'] as DateTime?;
        if (aAt == null && bAt == null) return 0;
        if (aAt == null) return 1;
        if (bAt == null) return -1;
        return bAt.compareTo(aAt);
      });
      return list;
    } catch (e) {
      throw Exception('Failed to fetch assignment history: $e');
    }
  }
}
