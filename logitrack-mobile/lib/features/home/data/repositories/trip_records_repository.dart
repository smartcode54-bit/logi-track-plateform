import 'dart:typed_data';
import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:firebase_storage/firebase_storage.dart';
import '../models/trip_record.dart';

/// ชื่อ collection ตาม shared-docs (TripRecords - SSOT for Web Dashboard & Billing)
const String tripRecordsCollection = 'trip_records';

/// Path ใน Storage สำหรับรูปของ trip: trip_records/{tripId}/{photoType}.png
String tripRecordPhotoPath(String tripId, String photoType) =>
    'trip_records/$tripId/$photoType.png';

/// อัปโหลดรูป (bytes) ขึ้น Storage แล้วคืน URL
Future<String> uploadTripPhoto({
  required String tripId,
  required String photoType,
  required List<int> imageBytes,
}) async {
  final ref = FirebaseStorage.instance
      .ref()
      .child(tripRecordPhotoPath(tripId, photoType));
  await ref.putData(
    Uint8List.fromList(imageBytes),
    SettableMetadata(contentType: 'image/png'),
  );
  return ref.getDownloadURL();
}

/// บันทึกรับงาน (Loading Phase) ลง TripRecords
/// [tripId] = Trip ID จาก Shop Express (spxTripId) ใช้เป็น document id
/// [stepPhotos] = map key เป็น pre_close | closing | seal | runsheet, value เป็น StampedPhoto (bytes + lat, lng, timestamp)
Future<void> submitLoadingPhaseRecord({
  required String tripId,
  required String jobType,
  String? sealCode,
  String? origin,
  String? destination,
  required Map<String, StampedPhotoInput> stepPhotos,
  TripOcrData? ocrData,
}) async {
  final photos = <TripPhoto>[];
  for (final entry in stepPhotos.entries) {
    final type = entry.key;
    final photo = entry.value;
    final url = await uploadTripPhoto(
      tripId: tripId,
      photoType: type,
      imageBytes: photo.bytes,
    );
    photos.add(TripPhoto(
      url: url,
      type: type,
      geocoding: TripPhotoGeocoding(
        lat: photo.lat,
        lng: photo.lng,
        timestamp: photo.timestamp,
      ),
    ));
  }
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
    createdAt: DateTime.now(),
    updatedAt: DateTime.now(),
  );
  final data = record.toFirestore();
  // Convert DateTime to Timestamp for Firestore
  final map = Map<String, dynamic>.from(data);
  if (map['createdAt'] is DateTime) map['createdAt'] = Timestamp.fromDate(map['createdAt'] as DateTime);
  if (map['updatedAt'] is DateTime) map['updatedAt'] = Timestamp.fromDate(map['updatedAt'] as DateTime);
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
