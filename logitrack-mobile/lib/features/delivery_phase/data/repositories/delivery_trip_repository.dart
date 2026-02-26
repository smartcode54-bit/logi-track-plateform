import 'package:cloud_firestore/cloud_firestore.dart';
import '../../../home/data/models/trip_record.dart';
import '../../../home/data/repositories/trip_records_repository.dart';

/// บันทึกส่งงาน (Delivery Phase) อัปโหลดรูปส่งงาน แล้วอัปเดตสถานะเป็น delivered พร้อม DeliveredTimestamp + lat,lng
Future<void> submitDeliveryPhaseRecord({
  required String tripId,
  String? taskId,
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

  final ref = FirebaseFirestore.instance
      .collection(tripRecordsCollection)
      .doc(tripId);

  // ใช้ set(merge: true) แทน update() เพื่อให้สถานะ delivered ถูกเขียนเสมอ (รวมกรณี LH ที่ doc อาจยังไม่ถูก merge จากที่อื่น)
  final snap = await ref.get();
  final List<Map<String, dynamic>> mergedPhotos = [];
  if (snap.exists && snap.data() != null) {
    final existing = snap.data()!['photos'];
    if (existing is List) {
      for (final e in existing) {
        if (e is Map<String, dynamic>) mergedPhotos.add(e);
      }
    }
  }
  mergedPhotos.addAll(newPhotoMaps);

  await ref.set({
    'status': 'delivered',
    'deliveredTimestamp': Timestamp.fromDate(now),
    'deliveredLat': deliveredLat,
    'deliveredLng': deliveredLng,
    'updatedAt': Timestamp.fromDate(now),
    'photos': mergedPhotos,
  }, SetOptions(merge: true));

  if (taskId != null && taskId.isNotEmpty) {
    try {
      await FirebaseFirestore.instance.collection('tasks').doc(taskId).update({
        'status': 'Completed',
        'updatedAt': FieldValue.serverTimestamp(),
      });
    } catch (_) {
      // Ignored if document doesn't exist or isn't a first mile task.
    }
  }
}
