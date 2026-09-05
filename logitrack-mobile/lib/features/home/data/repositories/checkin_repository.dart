import 'dart:typed_data';
import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:flutter/foundation.dart';

import '../../../../core/services/cloud_functions_service.dart';
import '../../../../core/services/mobile_client_heartbeat_service.dart';
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

/// ถ่ายรูปหลักฐาน: ใส่ stamp overlay (วันเวลา สถานที่ พิกัด เข็มทิศ) แล้ว compress
/// [skipOverlay] ถ้า true จะ compress อย่างเดียว (ไม่ stamp) — ใช้เมื่อต้องการความเร็ว
/// [position] และ [overlayContext] ถ้าส่งมา จะข้าม getCurrentPosition + fetchOverlayContext (ใช้ cache ตอนถ่ายหลายรูป)
Future<Uint8List> stampOverlayAndCompressForEvidence(
  List<int> imageBytes, {
  bool skipOverlay = false,
  Position? position,
  OverlayContext? overlayContext,
}) async {
  if (skipOverlay) {
    return compressImageForUpload(imageBytes);
  }
  try {
    final pos = position ?? await getCurrentPosition();
    final timestamp = DateTime.now();
    final ctx =
        overlayContext ??
        await fetchOverlayContext(pos.latitude, pos.longitude);
    final stamped = await overlayGeocodingAndTimestamp(
      imageBytes: imageBytes,
      lat: pos.latitude,
      lng: pos.longitude,
      timestamp: timestamp,
      ctx: ctx,
    );
    return compressImageForUpload(stamped);
  } catch (_) {
    return compressImageForUpload(imageBytes);
  }
}

/// Get current position. Throws if permission denied or unavailable.
Future<Position> getCurrentPosition() async {
  final enabled = await Geolocator.isLocationServiceEnabled();
  if (!enabled) throw Exception('Location service is disabled');
  final permission = await Geolocator.checkPermission();
  if (permission == LocationPermission.denied) {
    final requested = await Geolocator.requestPermission();
    if (requested == LocationPermission.denied ||
        requested == LocationPermission.deniedForever) {
      throw Exception('Location permission denied');
    }
  }
  return Geolocator.getCurrentPosition(
    locationSettings: const LocationSettings(accuracy: LocationAccuracy.high),
  );
}

/// Upload stamped image to Storage and update tasks with check-in data.
/// รูป Check-in ใส่ stamp overlay เต็ม (วันเวลา สถานที่ พิกัด เข็มทิศ) เพื่อหลักฐาน
/// [imageBytes] — use XFile.readAsBytes() so it works on web and mobile.
Future<void> submitCheckIn({
  required String taskId,
  required List<int> imageBytes,
  required double lat,
  required double lng,
  required DateTime timestamp,
  // Un-overlaid customer-app screenshot ("arrived / entered stand"), ADR 0019. Compressed only —
  // never overlaid (it was captured in the customer app, not by the camera here).
  List<int>? appScreenshotBytes,
  List<String>? helperDriverIds, // Auth UIDs of helpers selected by the main driver
}) async {
  final ctx = await fetchOverlayContext(lat, lng);
  final stamped = await overlayGeocodingAndTimestamp(
    imageBytes: imageBytes,
    lat: lat,
    lng: lng,
    timestamp: timestamp,
    ctx: ctx,
  );
  final compressed = await compressImageForUpload(stamped);
  final ref = FirebaseStorage.instance
      .ref()
      .child('checkin')
      .child(taskId)
      .child('${timestamp.millisecondsSinceEpoch}.jpg');
  await ref.putData(compressed, SettableMetadata(contentType: 'image/jpeg'));
  final photoUrl = await ref.getDownloadURL();

  String? appScreenshotUrl;
  if (appScreenshotBytes != null && appScreenshotBytes.isNotEmpty) {
    final shotCompressed = await compressImageForUpload(appScreenshotBytes);
    final shotRef = FirebaseStorage.instance
        .ref()
        .child('checkin')
        .child(taskId)
        .child('app_screenshot_${timestamp.millisecondsSinceEpoch}.jpg');
    await shotRef.putData(
      shotCompressed,
      SettableMetadata(contentType: 'image/jpeg'),
    );
    appScreenshotUrl = await shotRef.getDownloadURL();
  }

  await FirebaseFirestore.instance.collection('tasks').doc(taskId).update({
    'status': 'Checked in',
    'checkInAt': Timestamp.fromDate(timestamp),
    'checkInPhotoUrl': photoUrl,
    if (appScreenshotUrl != null) 'checkInAppScreenshotUrl': appScreenshotUrl,
    'checkInLat': lat,
    'checkInLng': lng,
    if (helperDriverIds != null) 'helperDriverIds': helperDriverIds,
    'updatedAt': Timestamp.fromDate(DateTime.now()),
  });

  // Notify the customer's/partner's LINE group of the check-in (best-effort; no-op if the linked
  // entity has no lineGroupId configured; failure never blocks check-in). Log the callable's skip
  // reason / any error so a silent non-delivery is diagnosable instead of invisible.
  try {
    final res = await CloudFunctionsService.instance.call(
      'sendCustomerLineNotification',
      data: {'taskId': taskId, 'event': 'checkin'},
    );
    if (res is Map && res['skipped'] == true) {
      debugPrint('[line] checkin notification skipped: ${res['reason']}');
    }
  } catch (e) {
    debugPrint('[line] checkin notification failed: $e');
  }

  try {
    final t = await FirebaseFirestore.instance.collection('tasks').doc(taskId).get();
    final did = t.data()?['driverId'] as String?;
    if (did != null && did.isNotEmpty) {
      await MobileClientHeartbeatService.instance.onJobAction(did);
    }
  } catch (_) {}
}
