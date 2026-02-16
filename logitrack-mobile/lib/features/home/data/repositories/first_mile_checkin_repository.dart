import 'dart:io';
import 'dart:typed_data';
import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:firebase_storage/firebase_storage.dart';
import 'package:geolocator/geolocator.dart';
import 'package:image/image.dart' as img;

/// Stamps geolocation and timestamp onto image bytes. Returns new PNG bytes.
Future<List<int>> stampImageWithLocationAndTime({
  required String imagePath,
  required double lat,
  required double lng,
  required DateTime timestamp,
}) async {
  final bytes = await File(imagePath).readAsBytes();
  final image = img.decodeImage(bytes);
  if (image == null) throw Exception('Could not decode image');

  final stamp = '${lat.toStringAsFixed(6)}, ${lng.toStringAsFixed(6)}\n${timestamp.toIso8601String()}';
  const lineHeight = 18;
  final numLines = stamp.split('\n').length;
  final textY = image.height - (lineHeight * numLines) - 8;
  if (textY > 0) {
    img.drawString(
      image,
      stamp,
      font: img.arial14,
      x: 8,
      y: textY,
      color: img.ColorRgba8(255, 255, 255, 255),
    );
  }
  return img.encodePng(image) ?? bytes;
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
Future<void> submitCheckIn({
  required String taskId,
  required String imagePath,
  required double lat,
  required double lng,
  required DateTime timestamp,
}) async {
  final stamped = await stampImageWithLocationAndTime(
    imagePath: imagePath,
    lat: lat,
    lng: lng,
    timestamp: timestamp,
  );
  final ref = FirebaseStorage.instance
      .ref()
      .child('first_mile_checkin')
      .child(taskId)
      .child('${timestamp.millisecondsSinceEpoch}.png');
  await ref.putData(
    Uint8List.fromList(stamped),
    SettableMetadata(contentType: 'image/png'),
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
