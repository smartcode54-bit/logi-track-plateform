import 'dart:typed_data';

import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:firebase_storage/firebase_storage.dart';
import '../../../home/data/services/image_compression_service.dart';

/// Leave request types matching backend schema (Sick and Business only).
const leaveTypes = ['SICK', 'BUSINESS'];

/// Storage path for leave evidence: leave_evidence/{userId}/{timestamp}_{index}.jpg
String _leaveEvidencePath(String userId, int index, int timestampMs) {
  return 'leave_evidence/$userId/${timestampMs}_$index.jpg';
}

/// Upload one evidence image; returns download URL.
Future<String> uploadLeaveEvidence({
  required String userId,
  required int index,
  required int timestampMs,
  required Uint8List imageBytes,
}) async {
  if (imageBytes.isEmpty) throw ArgumentError('imageBytes must not be empty');
  final compressed = await compressImageForUpload(imageBytes);
  final ref = FirebaseStorage.instance.ref().child(_leaveEvidencePath(userId, index, timestampMs));
  await ref.putData(compressed, SettableMetadata(contentType: 'image/jpeg'));
  return ref.getDownloadURL();
}

class LeaveRequestRepository {
  final FirebaseFirestore _firestore = FirebaseFirestore.instance;

  /// Stream leave requests for a driver, newest first.
  Stream<QuerySnapshot<Map<String, dynamic>>> watchLeaveRequestsForDriver(
    String driverId,
  ) {
    if (driverId.isEmpty) {
      return const Stream.empty();
    }
    return _firestore
        .collection('leave_requests')
        .where('driverId', isEqualTo: driverId)
        .orderBy('createdAt', descending: true)
        .snapshots();
  }

  /// Create a new leave request. Returns the document id.
  /// [attachmentUrls] optional list of download URLs (e.g. from uploadLeaveEvidence).
  Future<String> createLeaveRequest({
    required String driverId,
    String? driverName,
    required String type,
    required DateTime startDate,
    required DateTime endDate,
    required String reason,
    List<String>? attachmentUrls,
  }) async {
    if (driverId.isEmpty) {
      throw ArgumentError('driverId is required');
    }
    final now = FieldValue.serverTimestamp();
    final doc = await _firestore.collection('leave_requests').add({
      'driverId': driverId,
      if (driverName != null && driverName.isNotEmpty) 'driverName': driverName,
      'type': type,
      'status': 'PENDING',
      'startDate': Timestamp.fromDate(startDate),
      'endDate': Timestamp.fromDate(endDate),
      'reason': reason,
      if (attachmentUrls != null && attachmentUrls.isNotEmpty) 'attachments': attachmentUrls,
      'createdAt': now,
      'updatedAt': now,
    });
    return doc.id;
  }
}
