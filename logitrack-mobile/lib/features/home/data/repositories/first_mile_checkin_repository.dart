import 'dart:typed_data';
import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:firebase_storage/firebase_storage.dart';
import 'package:geolocator/geolocator.dart';
import '../services/image_compression_service.dart';
import '../services/photo_overlay_service.dart';

/// Stamps geolocation and timestamp onto image bytes. Returns new PNG bytes.
/// Delegates to [overlayGeocodingAndTimestamp] for Loading Phase / Check-in consistency.
Future<List<int>> stampImageWithLocationAndTime({
  required List<int> imageBytes,
  required double lat,
  required double lng,
  required DateTime timestamp,
}) async {
  return overlayGeocodingAndTimestamp(
    imageBytes: imageBytes,
    lat: lat,
    lng: lng,
    timestamp: timestamp,
  );
}

/// Get current position. Throws if permission denied or unavailable.
Future<Position> getCurrentPosition() async {
  final enabled = await Geolocator.isLocationServiceEnabled();
  if (!enabled) throw Exception('Location service is disabled');
  final permission = await Geolocator.checkPermission();
  if (permission == LocationPermission.denied) {
    final requested = await Geolocator.requestPermission();
    if (requested == LocationPermission.denied || requested == LocationPermission.deniedForever) {
      throw Exception('Location permission denied');
    }
  }
  return Geolocator.getCurrentPosition(desiredAccuracy: LocationAccuracy.high);
}

/// Upload stamped image to Storage and update first_mile_tasks with check-in data.
/// [imageBytes] — use XFile.readAsBytes() so it works on web and mobile.
Future<void> submitCheckIn({
  required String taskId,
  required List<int> imageBytes,
  required double lat,
  required double lng,
  required DateTime timestamp,
}) async {
  final stamped = await stampImageWithLocationAndTime(
    imageBytes: imageBytes,
    lat: lat,
    lng: lng,
    timestamp: timestamp,
  );
  final compressed = await compressImageForUpload(stamped);
  final ref = FirebaseStorage.instance
      .ref()
      .child('first_mile_checkin')
      .child(taskId)
      .child('${timestamp.millisecondsSinceEpoch}.jpg');
  await ref.putData(
    compressed,
    SettableMetadata(contentType: 'image/jpeg'),
  );
  final photoUrl = await ref.getDownloadURL();

  await FirebaseFirestore.instance.collection('first_mile_tasks').doc(taskId).update({
    'status': 'Checked in',
    'checkInAt': Timestamp.fromDate(timestamp),
    'checkInPhotoUrl': photoUrl,
    'checkInLat': lat,
    'checkInLng': lng,
    'updatedAt': Timestamp.fromDate(DateTime.now()),
  });
}
