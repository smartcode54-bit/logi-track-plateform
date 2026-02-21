import 'dart:typed_data';
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

/// บันทึกรับงาน (Loading Phase) ลง TripRecords
Future<void> submitLoadingPhaseRecord({
  required String tripId,
  required String jobType,
  String? sealCode,
  String? origin,
  String? destination,
  String? distance,
  int? parcelCount,
  String? sealTime,
  String? totalWeight,
  double? lat,
  double? lng,
  required Map<String, StampedPhotoInput> stepPhotos,
  TripOcrData? ocrData,
}) async {
  // Upload all photos in parallel to reduce save time on slow networks
  final photoFutures = stepPhotos.entries.map((entry) async {
    final type = entry.key;
    final photo = entry.value;
    final url = await uploadTripPhoto(
      tripId: tripId,
      photoType: type,
      imageBytes: photo.bytes,
    );
    return TripPhoto(
      url: url,
      type: type,
      geocoding: TripPhotoGeocoding(
        lat: photo.lat,
        lng: photo.lng,
        timestamp: photo.timestamp,
      ),
    );
  });
  final photos = await Future.wait(photoFutures);
  final record = TripRecord(
    id: tripId,
    status: 'loading',
    jobType: jobType,
    photos: photos,
    ocrData: ocrData,
    spxTripId: tripId,
    sealCode: sealCode,
    origin: origin,
    destination: destination,
    distance: distance,
    parcelCount: parcelCount,
    sealTime: sealTime,
    totalWeight: totalWeight,
    lat: lat,
    lng: lng,
    createdAt: DateTime.now(),
    updatedAt: DateTime.now(),
  );
  final data = record.toFirestore();
  final map = Map<String, dynamic>.from(data);
  if (map['createdAt'] is DateTime) {
    map['createdAt'] = Timestamp.fromDate(map['createdAt'] as DateTime);
  }
  if (map['updatedAt'] is DateTime) {
    map['updatedAt'] = Timestamp.fromDate(map['updatedAt'] as DateTime);
  }
  await FirebaseFirestore.instance
      .collection(tripRecordsCollection)
      .doc(tripId)
      .set(map, SetOptions(merge: true));
}

/// Input สำหรับรูปที่ถ่ายแล้ว (bytes + geocoding)
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
