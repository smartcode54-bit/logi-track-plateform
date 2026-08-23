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

  /// For UI debug: ว่าเอกสาร Trip ID มีอยู่จริงไหม (ก่อนเช็ค same-driver)
  final bool docExists;
  /// For UI debug: driverId ในเอกสารเดิม (ถ้ามี)
  final String? existingDriverId;
  /// For UI debug: คนขับปัจจุบันที่ส่งเข้ามา
  final String? currentDriverId;
  /// For UI debug: ว่าผ่านเพราะ same-driver ไหม
  final bool sameDriverAllowed;

  const DuplicateCheckResult({
    required this.tripIdExists,
    required this.sealCodeExists,
    this.docExists = false,
    this.existingDriverId,
    this.currentDriverId,
    this.sameDriverAllowed = false,
  });

  bool get hasDuplicate => tripIdExists || sealCodeExists;
}

/// Check if [tripId] or [sealCode] already exists in trip_records.
/// - tripId: document with id [tripId] exists → block always, regardless of driver.
/// - sealCode: another trip (different document) has the same sealCode; only checked if [sealCode] is not null/empty.
Future<DuplicateCheckResult> checkDuplicateTripIdAndSeal({
  required String tripId,
  String? sealCode,
  String? currentDriverId,
}) async {
  final col = FirebaseFirestore.instance.collection(tripRecordsCollection);

  final tripDoc = await col.doc(tripId).get();
  final docExists = tripDoc.exists;
  final existingDriverId = tripDoc.data()?['driverId'] as String?;
  // Block duplicate trip ID unconditionally — same driver is not an exception.
  final bool tripIdExists = docExists;
  const bool sameDriverAllowed = false;

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
    docExists: docExists,
    existingDriverId: existingDriverId,
    currentDriverId: currentDriverId,
    sameDriverAllowed: sameDriverAllowed,
  );
}

/// ดึงประวัติเที่ยวของคนขับ (สำหรับหน้า ประวัติการรับและส่ง) เรียง createdAt ล่าสุดก่อน
/// ใช้แค่ where(driverId) แล้วเรียงใน memory เพื่อไม่ต้องใช้ composite index
Future<List<TripRecord>> getTripHistoryByDriver(String driverId) async {
  if (driverId.isEmpty) return [];
  try {
    final query = FirebaseFirestore.instance
        .collection(tripRecordsCollection)
        .where('driverId', isEqualTo: driverId);
    // Prefer fresh server data so admin edits/deletions reflect on refresh —
    // the default offline cache would otherwise keep showing deleted trips (and
    // their now-404 photo URLs). Fall back to cache when the server is
    // unreachable so a driver in low signal still sees their history.
    QuerySnapshot<Map<String, dynamic>> snapshot;
    try {
      snapshot = await query.get(const GetOptions(source: Source.server));
    } catch (_) {
      snapshot = await query.get();
    }
    final list = snapshot.docs
        .map((doc) => TripRecord.fromMap(doc.data(), id: doc.id))
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

/// ผลการตรวจเที่ยวค้างจาก server เพื่อตัดสินใจว่าจะเคลียร์งานค้างในเครื่องหรือไม่
/// - [delivered] / [cancelled] / [missing] → เคลียร์ได้ (เที่ยวปิด/ยกเลิก/ถูกลบจากเครื่องอื่น เช่น แอดมินบนเว็บ)
/// - [active] → ยังเป็นงานค้างจริง (in_transit) อย่าเคลียร์
/// - [unknown] → ออฟไลน์/อ่าน server ไม่ได้ อย่าเพิ่งเคลียร์ (กันลบงานค้างผิด)
enum PendingTripCheck { delivered, cancelled, missing, active, unknown }

/// true เมื่อสถานะนี้ถือว่า "ปิดแล้ว" — เคลียร์งานค้างในเครื่องได้
bool isClearablePendingTrip(PendingTripCheck c) =>
    c == PendingTripCheck.delivered ||
    c == PendingTripCheck.cancelled ||
    c == PendingTripCheck.missing;

/// ตรวจสถานะเที่ยวค้างจาก **server โดยตรง** (ข้าม cache) — สำคัญเมื่อเที่ยวถูกปิด/แก้ไขจากเครื่องอื่น
/// (เช่น แอดมินปิด/ยกเลิกงานบนเว็บ) แล้ว cache ในมือถือยังค้างเป็น in_transit ทำให้รับงานรอบใหม่ไม่ได้
Future<PendingTripCheck> checkPendingTripOnServer(String tripId) async {
  if (tripId.trim().isEmpty) return PendingTripCheck.missing;
  try {
    final doc = await FirebaseFirestore.instance
        .collection(tripRecordsCollection)
        .doc(tripId)
        .get(const GetOptions(source: Source.server));
    if (!doc.exists) return PendingTripCheck.missing;
    final status = (doc.data()?['status'] as String?)?.toLowerCase();
    if (status == 'delivered') return PendingTripCheck.delivered;
    if (status == 'cancelled' || status == 'canceled') {
      return PendingTripCheck.cancelled;
    }
    return PendingTripCheck.active;
  } catch (_) {
    return PendingTripCheck.unknown;
  }
}

/// คืน set ของ taskId ที่เที่ยวถูกส่งแล้ว (status = delivered) ของคนขับนี้
/// ใช้ในหน้า Check-in เพื่อไม่บล็อกการเพิ่มงานใหม่เมื่อ task ยังเป็น "Checked in" แต่เที่ยวส่งแล้ว
/// Query แค่ driverId แล้วกรอง status ใน memory เพื่อไม่ต้องใช้ composite index
Future<Set<String>> getDeliveredTaskIdsForDriver(String driverId) async {
  if (driverId.isEmpty) return {};
  try {
    final snapshot = await FirebaseFirestore.instance
        .collection(tripRecordsCollection)
        .where('driverId', isEqualTo: driverId)
        .get();
    final ids = <String>{};
    for (final doc in snapshot.docs) {
      final data = doc.data();
      if (data['status'] != 'delivered') continue;
      final taskId = data['taskId'] as String?;
      if (taskId != null && taskId.isNotEmpty) ids.add(taskId);
    }
    return ids;
  } catch (_) {
    return {};
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
      'taskId': data['taskId'] as String?,
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
  await ref.putData(compressed, SettableMetadata(contentType: 'image/jpeg'));
  return ref.getDownloadURL();
}

/// Un-overlaid customer-app screenshot photo types (ADR 0019). These are captured from the
/// customer's app (camera or gallery), NOT stamped with GPS/time overlay, so they must NOT carry
/// geocoding metadata (stamping the current location on an image captured elsewhere = false
/// provenance). Covers the flat types and the per-stop `stop_{index}_arrived`.
bool isScreenshotPhotoType(String type) {
  final t = type.trim();
  if (t == 'checkin_app' || t == 'truck_release' || t == 'arrived') return true;
  return RegExp(r'^stop_\d+_arrived$').hasMatch(t);
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
