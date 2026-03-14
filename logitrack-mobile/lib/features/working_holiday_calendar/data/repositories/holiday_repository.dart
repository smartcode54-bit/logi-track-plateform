import 'package:cloud_firestore/cloud_firestore.dart';

/// Repository for reading company holidays (read-only for drivers).
/// Firestore rules: allow read if request.auth != null.
class HolidayRepository {
  final FirebaseFirestore _firestore = FirebaseFirestore.instance;

  /// Stream holidays for a given year, ordered by date.
  Stream<QuerySnapshot<Map<String, dynamic>>> watchHolidaysForYear(int year) {
    final start = DateTime(year, 1, 1);
    final end = DateTime(year, 12, 31, 23, 59, 59, 999);
    return _firestore
        .collection('holidays')
        .where('date', isGreaterThanOrEqualTo: Timestamp.fromDate(start))
        .where('date', isLessThanOrEqualTo: Timestamp.fromDate(end))
        .orderBy('date', descending: false)
        .snapshots();
  }
}
