import 'package:cloud_firestore/cloud_firestore.dart';
import '../../../home/data/models/trip_record.dart';
import '../../../home/data/repositories/trip_records_repository.dart';

/// บันทึกส่งงาน (Delivery Phase) อัปโหลดรูปส่งงาน แล้วอัปเดตสถานะเป็น delivered พร้อม DeliveredTimestamp + lat,lng
Future<void> submitDeliveryPhaseRecord({
  required String tripId,
  required Map<String, StampedPhotoInput> deliveryPhotos,
  required double deliveredLat,
  required double deliveredLng,
}) async {
  final now = DateTime.now();
  final photoFutures = deliveryPhotos.entries.map((entry) async {
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
  final newPhotos = await Future.wait(photoFutures);
  final newPhotoMaps = newPhotos.map((p) => p.toMap()).toList();

  await FirebaseFirestore.instance
      .collection(tripRecordsCollection)
      .doc(tripId)
      .update({
    'status': 'delivered',
    'deliveredTimestamp': Timestamp.fromDate(now),
    'deliveredLat': deliveredLat,
    'deliveredLng': deliveredLng,
    'updatedAt': Timestamp.fromDate(now),
    'photos': FieldValue.arrayUnion(newPhotoMaps),
  });
}
