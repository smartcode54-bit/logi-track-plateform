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
///
/// [customerId] คือลูกค้าที่จะวางบิล — **ต้องเก็บลงตัว record เอง** (ADR 0008 §1) ไม่ใช่ไปหา
/// ผ่าน task ตอนคำนวณราคา เพราะ task ถูกแก้/ยกเลิก/ลบทีหลังได้ และ standby ที่ไม่มี task
/// ก็เป็นเหตุการณ์ที่เกิดจริงและต้องเก็บเงินได้ ถ้า resolve ไม่ได้ให้บันทึกแล้วติดธงไว้
/// (`customerResolved: false`) — ห้ามบล็อกคนขับ (ADR 0008 §2) เพราะรูปหลักฐานสำคัญกว่า
/// และคนขับที่หน้างานแก้ master data ไม่ได้อยู่แล้ว
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
  String? truckId,
  String? truckLicensePlate,
  String? customerId,
  /// ที่มาของ [customerId] — 'task' | 'origin_hub' | 'manual'
  String? customerResolvedFrom,
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

  final resolvedCustomerId = customerId?.trim();
  final hasCustomer = resolvedCustomerId != null && resolvedCustomerId.isNotEmpty;

  final docData = {
    'driverId': driverId,
    'taskId': taskId,
    'tripId': tripId,
    // ADR 0008 §1-2 — ราคาถูกคิดจากฟิลด์นี้ ไม่ต้องพึ่ง task; ธง customerResolved บอกฝั่งเว็บว่า
    // record ไหนต้องให้แอดมินเลือกลูกค้าให้ (ห้ามเดา/ห้ามใส่ลูกค้า default — ADR 0008 §7)
    if (hasCustomer) 'customerId': resolvedCustomerId,
    'customerResolved': hasCustomer,
    if (hasCustomer && customerResolvedFrom != null)
      'customerResolvedFrom': customerResolvedFrom,
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
    if (truckId != null) 'truckId': truckId,
    if (truckLicensePlate != null) 'truckLicensePlate': truckLicensePlate,
    'createdAt': Timestamp.fromDate(now),
    'updatedAt': Timestamp.fromDate(now),
  };

  batch.set(ref, docData);

  if (taskId != null && taskId.isNotEmpty) {
    batch.update(db.collection('tasks').doc(taskId), {
      'status': 'Completed',
      'updatedAt': FieldValue.serverTimestamp(),
    });
  }

  if (tripId != null && tripId.isNotEmpty) {
    batch.update(db.collection('trip_records').doc(tripId), {
      'status': 'standby',
      'updatedAt': FieldValue.serverTimestamp(),
    });
  }

  await batch.commit();
}
