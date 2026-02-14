import 'package:cloud_firestore/cloud_firestore.dart';

class DriverRepository {
  final FirebaseFirestore _firestore = FirebaseFirestore.instance;

  Future<Map<String, dynamic>?> getCurrentDriver(String authId) async {
    try {
      final querySnapshot = await _firestore
          .collection('drivers')
          .where('authId', isEqualTo: authId)
          .limit(1)
          .get();

      if (querySnapshot.docs.isNotEmpty) {
        return querySnapshot.docs.first.data();
      }
      return null;
    } catch (e) {
      throw Exception('Failed to fetch driver data: $e');
    }
  }
}
