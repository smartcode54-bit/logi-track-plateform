import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:firebase_storage/firebase_storage.dart';
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
