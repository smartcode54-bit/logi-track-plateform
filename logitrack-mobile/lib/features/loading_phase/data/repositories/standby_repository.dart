import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:firebase_storage/firebase_storage.dart';
import '../../../home/data/services/image_compression_service.dart';

const String _standbyCollection = 'standby_records';

String _standbyPhotoPath(String recordId, String photoType) =>
    'standby_records/$recordId/$photoType.jpg';

Future<String> _uploadStandbyPhoto({
  required String recordId,
  required String photoType,
  required List<int> imageBytes,
}) async {
  final compressed = await compressImageForUpload(imageBytes);
  final ref = FirebaseStorage.instance
      .ref()
      .child(_standbyPhotoPath(recordId, photoType));
  await ref.putData(compressed, SettableMetadata(contentType: 'image/jpeg'));
  return ref.getDownloadURL();
}

/// บันทึก standby record เมื่อ driver เช็คอินแล้วไม่มีงานจัดส่ง
/// [worksheetImage] = ใบงานลูกค้า, [siteImage] = รูปถ่ายหน้างาน
/// ผลข้างเคียง: อัพเดต tasks/{taskId} → Completed, trip_records/{tripId} → standby
Future<void> submitStandbyRecord({
  required String driverId,
  required String? taskId,
  required String? tripId,
  required String startLocation,
  required String endLocation,
  required DateTime startedAt,
  String? note,
  List<int>? worksheetImage,
  List<int>? siteImage,
  double? lat,
  double? lng,
}) async {
  final db = FirebaseFirestore.instance;
  final col = db.collection(_standbyCollection);
  final ref = col.doc();
  final recordId = ref.id;
  final now = DateTime.now();

  String? worksheetUrl;
  String? siteUrl;

  if (worksheetImage != null && worksheetImage.isNotEmpty) {
    worksheetUrl = await _uploadStandbyPhoto(
      recordId: recordId,
      photoType: 'customer_worksheet',
      imageBytes: worksheetImage,
    );
  }
  if (siteImage != null && siteImage.isNotEmpty) {
    siteUrl = await _uploadStandbyPhoto(
      recordId: recordId,
      photoType: 'site_photo',
      imageBytes: siteImage,
    );
  }

  final durationMinutes = now.difference(startedAt).inMinutes;

  final photos = <Map<String, String>>[
    if (worksheetUrl != null)
      {'url': worksheetUrl, 'type': 'customer_worksheet'},
    if (siteUrl != null)
      {'url': siteUrl, 'type': 'site_photo'},
  ];

  final batch = db.batch();

  // Write standby_records doc
  batch.set(ref, {
    'driverId': driverId,
    'taskId': taskId,
    'tripId': tripId,
    'startLocation': startLocation,
    'endLocation': endLocation,
    'startedAt': Timestamp.fromDate(startedAt),
    'endedAt': Timestamp.fromDate(now),
    'durationMinutes': durationMinutes,
    'photos': photos,
    'note': (note?.trim().isEmpty ?? true) ? null : note?.trim(),
    'status': 'completed',
    'lat': lat,
    'lng': lng,
    'createdAt': Timestamp.fromDate(now),
    'updatedAt': Timestamp.fromDate(now),
  });

  // Mark task as Completed (same as normal delivery finish)
  if (taskId != null && taskId.isNotEmpty) {
    batch.update(db.collection('tasks').doc(taskId), {
      'status': 'Completed',
      'updatedAt': FieldValue.serverTimestamp(),
    });
  }

  // Mark trip_record as standby so Driver Monitor shows it
  if (tripId != null && tripId.isNotEmpty) {
    batch.update(db.collection('trip_records').doc(tripId), {
      'status': 'standby',
      'updatedAt': FieldValue.serverTimestamp(),
    });
  }

  await ref.set({
    'driverId': driverId,
    'taskId': taskId,
    'tripId': tripId,
    'startLocation': startLocation,
    'endLocation': endLocation,
    'startedAt': Timestamp.fromDate(startedAt),
    'endedAt': Timestamp.fromDate(now),
    'durationMinutes': durationMinutes,
    'photos': photos,
    'note': (note?.trim().isEmpty ?? true) ? null : note?.trim(),
    'status': 'completed',
    'lat': lat,
    'lng': lng,
    'createdAt': Timestamp.fromDate(now),
    'updatedAt': Timestamp.fromDate(now),
  });

  if (taskId != null && taskId.isNotEmpty) {
    try {
      await db.collection('tasks').doc(taskId).update({
        'status': 'Completed',
        'updatedAt': FieldValue.serverTimestamp(),
      });
    } catch (_) {
      // Non-critical: standby record already saved.
    }
  }

}
