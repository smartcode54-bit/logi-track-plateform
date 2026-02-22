import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:firebase_storage/firebase_storage.dart';
import '../models/trip_record.dart';
import '../services/image_compression_service.dart';

/// ชื่อ collection ตาม shared-docs (TripRecords - SSOT for Web Dashboard & Billing)
const String tripRecordsCollection = 'trip_records';

/// Path ใน Storage สำหรับรูปของ trip: trip_records/{tripId}/{photoType}.jpg (compressed JPEG)
String tripRecordPhotoPath(String tripId, String photoType) =>
    'trip_records/$tripId/$photoType.jpg';

/// Result of duplicate check for Trip ID and Seal Code.
class DuplicateCheckResult {
  final bool tripIdExists;
  final bool sealCodeExists;

  const DuplicateCheckResult({
    required this.tripIdExists,
    required this.sealCodeExists,
  });

  bool get hasDuplicate => tripIdExists || sealCodeExists;
}

/// Check if [tripId] or [sealCode] already exists in trip_records.
/// - tripId: document with id [tripId] exists.
/// - sealCode: another trip (different document) has the same sealCode; only checked if [sealCode] is not null/empty.
Future<DuplicateCheckResult> checkDuplicateTripIdAndSeal({
  required String tripId,
  String? sealCode,
}) async {
  final col = FirebaseFirestore.instance.collection(tripRecordsCollection);

  final tripDoc = await col.doc(tripId).get();
  final tripIdExists = tripDoc.exists;

  bool sealCodeExists = false;
  final seal = sealCode?.trim();
  if (seal != null && seal.isNotEmpty) {
    final sealQuery = await col
        .where('sealCode', isEqualTo: seal)
        .limit(2)
        .get();
    for (final doc in sealQuery.docs) {
      if (doc.id != tripId) {
        sealCodeExists = true;
        break;
      }
    }
  }

  return DuplicateCheckResult(
    tripIdExists: tripIdExists,
    sealCodeExists: sealCodeExists,
  );
}

/// ดึงประวัติเที่ยวของคนขับ (สำหรับหน้า ประวัติการรับและส่ง) เรียง createdAt ล่าสุดก่อน
/// ใช้แค่ where(driverId) แล้วเรียงใน memory เพื่อไม่ต้องใช้ composite index
Future<List<TripRecord>> getTripHistoryByDriver(String driverId) async {
  if (driverId.isEmpty) return [];
  try {
    final snapshot = await FirebaseFirestore.instance
        .collection(tripRecordsCollection)
        .where('driverId', isEqualTo: driverId)
        .get();
    final list = snapshot.docs
        .map((doc) => TripRecord.fromMap(
              doc.data(),
              id: doc.id,
            ))
        .toList();
    list.sort((a, b) {
      final at = a.createdAt ?? DateTime(0);
      final bt = b.createdAt ?? DateTime(0);
      return bt.compareTo(at);
    });
    return list.take(300).toList();
  } catch (e, st) {
    assert(() {
      // ignore: avoid_print
      print('getTripHistoryByDriver error: $e\n$st');
      return true;
    }());
    return [];
  }
}

/// เช็คสถานะเที่ยวใน DB: คืนค่า status (เช่น 'in_transit', 'delivered') หรือ null ถ้าไม่มีเอกสาร
/// ใช้ก่อนแสดง/เก็บงาน delivery ค้าง — ถ้าไม่มีหรือ status เป็น delivered แล้ว ไม่เก็บ/ไม่แสดง
Future<String?> getTripStatus(String tripId) async {
  if (tripId.trim().isEmpty) return null;
  try {
    final doc = await FirebaseFirestore.instance
        .collection(tripRecordsCollection)
        .doc(tripId)
        .get();
    if (!doc.exists) return null;
    return doc.data()?['status'] as String?;
  } catch (_) {
    return null;
  }
}

/// ดึงงานค้างที่ยังไม่ส่ง (status = in_transit) ของ Driver คนนี้ จาก Firestore
/// ใช้เมื่อเปิดแอปแล้วไม่มี summary ในเครื่อง — query ตาม [driverId]
/// คืนค่า map สำหรับสร้าง SavedTripSummary หรือ null (ถ้า driverId ว่างหรือไม่มีเที่ยวค้าง)
Future<Map<String, dynamic>?> getPendingInTransitTrip(String? driverId) async {
  if (driverId == null || driverId.isEmpty) return null;
  try {
    final snapshot = await FirebaseFirestore.instance
        .collection(tripRecordsCollection)
        .where('driverId', isEqualTo: driverId)
        .where('status', isEqualTo: 'in_transit')
        .limit(10)
        .get();
    if (snapshot.docs.isEmpty) return null;
    // เลือกเที่ยวล่าสุดตาม updatedAt (เรียงใน memory)
    final doc = snapshot.docs.reduce((a, b) {
      final aAt = a.data()['updatedAt'] as Timestamp?;
      final bAt = b.data()['updatedAt'] as Timestamp?;
      if (aAt == null) return b;
      if (bAt == null) return a;
      return bAt.compareTo(aAt) > 0 ? b : a;
    });
    final data = doc.data();
    return {
      'tripId': doc.id,
      'origin': data['origin'] as String?,
      'destination': data['destination'] as String?,
      'sealCode': data['sealCode'] as String?,
      'jobType': data['jobType'] as String?,
    };
  } catch (_) {
    return null;
  }
}

/// อัปโหลดรูป (bytes) ขึ้น Storage หลังบีบอัดเป็น JPEG max 1024px, quality 70–80%, เป้าหมาย <500KB
Future<String> uploadTripPhoto({
  required String tripId,
  required String photoType,
  required List<int> imageBytes,
}) async {
  final compressed = await compressImageForUpload(imageBytes);
  final ref = FirebaseStorage.instance.ref().child(
    tripRecordPhotoPath(tripId, photoType),
  );
  await ref.putData(
    compressed,
    SettableMetadata(contentType: 'image/jpeg'),
  );
  return ref.getDownloadURL();
}

/// Input สำหรับรูปที่ถ่ายแล้ว (bytes + geocoding) — ใช้ร่วมโดย Loading และ Delivery
class StampedPhotoInput {
  final List<int> bytes;
  final double lat;
  final double lng;
  final DateTime timestamp;

  StampedPhotoInput({
    required this.bytes,
    required this.lat,
    required this.lng,
    required this.timestamp,
  });
}
