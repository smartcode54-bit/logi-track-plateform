import 'package:cloud_firestore/cloud_firestore.dart';
import '../../../home/data/models/trip_record.dart';
import '../../../home/data/repositories/trip_records_repository.dart';

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
