import 'package:cloud_firestore/cloud_firestore.dart';

import '../../../../core/services/cloud_functions_service.dart';
import '../../../../core/services/mobile_client_heartbeat_service.dart';
import '../../../home/data/models/trip_record.dart';
import '../../../home/data/repositories/driver_repository.dart';
import '../../../home/data/repositories/trip_records_repository.dart';

/// The job is over, so the driver is no longer responsible for its truck — drop the pointer and
/// let the fuel/expense forms fall back to their home truck. Best-effort: an uncleared activeTruck
/// only means the next expense defaults to the truck they last drove.
Future<void> _releaseActiveTruck(String? driverId) async {
  if (driverId == null || driverId.isEmpty) return;
  try {
    await DriverRepository().clearActiveTruck(driverId);
  } catch (_) {}
}

/// Merge Firestore photo maps: drop existing entries whose [type] is in [replacedTypes], append [newPhotoMaps].
List<Map<String, dynamic>> mergeTripPhotosReplacingTypes({
  required List<Map<String, dynamic>> existing,
  required Set<String> replacedTypes,
  required List<Map<String, dynamic>> newPhotoMaps,
}) {
  final kept = <Map<String, dynamic>>[];
  for (final e in existing) {
    final t = e['type']?.toString();
    if (t == null || !replacedTypes.contains(t)) {
      kept.add(e);
    }
  }
  return [...kept, ...newPhotoMaps];
}

List<Map<String, dynamic>> _photosListFromSnapshotData(
  Map<String, dynamic>? data,
) {
  if (data == null) return [];
  final existing = data['photos'];
  if (existing is! List) return [];
  final out = <Map<String, dynamic>>[];
  for (final e in existing) {
    if (e is Map<String, dynamic>) out.add(e);
  }
  return out;
}

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
    // Screenshot types (e.g. arrived) carry no geocoding — they are not overlaid captures (ADR 0019).
    return TripPhoto(
      url: url,
      type: type,
      geocoding: isScreenshotPhotoType(type)
          ? null
          : TripPhotoGeocoding(
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
  final existingFlat = _photosListFromSnapshotData(snap.data());
  final replacedTypes = deliveryPhotos.keys.toSet();
  final mergedPhotos = mergeTripPhotosReplacingTypes(
    existing: existingFlat,
    replacedTypes: replacedTypes,
    newPhotoMaps: newPhotoMaps,
  );

  final Map<String, dynamic> updateData = {
    'status': 'delivered',
    'deliveredTimestamp': Timestamp.fromDate(now),
    'deliveredLat': deliveredLat,
    'deliveredLng': deliveredLng,
    'updatedAt': Timestamp.fromDate(now),
    'photos': mergedPhotos,
  };

  await ref.set(updateData, SetOptions(merge: true));

  // Compute billing snapshot via callable (best-effort; failure does not block delivery)
  try {
    await CloudFunctionsService.instance.call(
      'computeTripBillingSnapshot',
      data: {'tripId': tripId},
    );
  } catch (_) {
    // Billing snapshot will be computed later by Driver Monitor backfill if this fails
  }

  // Notify the customer's/partner's LINE group of job completion (best-effort; no-op if the
  // linked entity has no lineGroupId configured; failure never blocks delivery)
  try {
    await CloudFunctionsService.instance.call(
      'sendCustomerLineNotification',
      data: {'tripId': tripId, 'event': 'delivered'},
    );
  } catch (_) {}

  if (taskId != null && taskId.isNotEmpty) {
    try {
      await FirebaseFirestore.instance.collection('tasks').doc(taskId).update({
        'status': 'Completed',
        'updatedAt': FieldValue.serverTimestamp(),
      });
    } catch (_) {
      // Ignored if document doesn't exist or isn't a first mile task.
    }
    try {
      final t = await FirebaseFirestore.instance.collection('tasks').doc(taskId).get();
      final did = t.data()?['driverId'] as String?;
      if (did != null && did.isNotEmpty) {
        await MobileClientHeartbeatService.instance.onJobAction(did);
      }
      await _releaseActiveTruck(did);
    } catch (_) {}
  }
}

/// Submit delivery progress for a single stop in a multi-delivery trip.
/// Direct Firestore write — avoids Cloud Function auth/App Check issues.
/// Returns true if ALL stops are now delivered (trip is complete).
Future<bool> submitDeliveryStopRecord({
  required String tripId,
  required String? taskId,
  required int stopIndex,
  required String destination,
  required List<Map<String, dynamic>> photos,
  required double deliveredLat,
  required double deliveredLng,
}) async {
  final now = DateTime.now();
  final ref = FirebaseFirestore.instance
      .collection(tripRecordsCollection)
      .doc(tripId);

  final snap = await ref.get();
  final data = snap.data() ?? {};

  final currentProgress = <Map<String, dynamic>>[];
  if (data['deliveryStopsProgress'] is List) {
    for (final e in (data['deliveryStopsProgress'] as List)) {
      if (e is Map<String, dynamic>) currentProgress.add(e);
    }
  }

  // Idempotent: skip if already delivered
  final alreadyDone = currentProgress.any(
    (s) => s['index'] == stopIndex && s['status'] == 'delivered',
  );
  if (alreadyDone) return false;

  final stopEntry = <String, dynamic>{
    'index': stopIndex,
    'destination': destination.trim().toUpperCase(),
    'status': 'delivered',
    'deliveredAt': Timestamp.fromDate(now),
    'deliveredLat': deliveredLat,
    'deliveredLng': deliveredLng,
    'photos': photos,
  };

  final updatedProgress = [
    ...currentProgress.where((s) => s['index'] != stopIndex),
    stopEntry,
  ];

  // Merge photos into trip record's main photos array
  final existingPhotos = _photosListFromSnapshotData(data);
  final newTypes = photos
      .map((p) => p['type']?.toString())
      .whereType<String>()
      .toSet();
  final mergedPhotos = mergeTripPhotosReplacingTypes(
    existing: existingPhotos,
    replacedTypes: newTypes,
    newPhotoMaps: photos,
  );

  await ref.set({
    'deliveryStopsProgress': updatedProgress,
    'photos': mergedPhotos,
    'updatedAt': Timestamp.fromDate(now),
  }, SetOptions(merge: true));

  // Check if all stops are now delivered → complete trip + task
  bool allDelivered = false;
  if (taskId != null && taskId.isNotEmpty) {
    try {
      final taskSnap = await FirebaseFirestore.instance
          .collection('tasks')
          .doc(taskId)
          .get();
      if (taskSnap.exists) {
        final taskData = taskSnap.data()!;
        final totalStops = (taskData['deliveryStops'] as List?)?.length ?? 1;
        final deliveredCount =
            updatedProgress.where((s) => s['status'] == 'delivered').length;

        if (deliveredCount >= totalStops) {
          allDelivered = true;

          await FirebaseFirestore.instance
              .collection('tasks')
              .doc(taskId)
              .update({
            'status': 'Completed',
            'updatedAt': FieldValue.serverTimestamp(),
          });

          await ref.update({
            'status': 'delivered',
            'deliveredTimestamp': Timestamp.fromDate(now),
          });

          try {
            await CloudFunctionsService.instance.call(
              'computeTripBillingSnapshot',
              data: {'tripId': tripId},
            );
          } catch (_) {}

          // Notify the customer's/partner's LINE group of job completion (best-effort)
          try {
            await CloudFunctionsService.instance.call(
              'sendCustomerLineNotification',
              data: {'tripId': tripId, 'event': 'delivered'},
            );
          } catch (_) {}

          try {
            final driverId = taskData['driverId'] as String?;
            if (driverId != null && driverId.isNotEmpty) {
              await MobileClientHeartbeatService.instance
                  .onJobAction(driverId);
            }
            await _releaseActiveTruck(taskData['driverId'] as String?);
          } catch (_) {}
        }
      }
    } catch (_) {}
  }

  return allDelivered;
}

/// Re-submit delivery photos after admin rejected
Future<void> resubmitDeliveryPhotos({
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
    // Screenshot types (e.g. arrived) carry no geocoding — they are not overlaid captures (ADR 0019).
    return TripPhoto(
      url: url,
      type: type,
      geocoding: isScreenshotPhotoType(type)
          ? null
          : TripPhotoGeocoding(
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

  final snap = await ref.get();
  final existingFlat = _photosListFromSnapshotData(snap.data());
  final replacedTypes = deliveryPhotos.keys.toSet();
  final mergedPhotos = mergeTripPhotosReplacingTypes(
    existing: existingFlat,
    replacedTypes: replacedTypes,
    newPhotoMaps: newPhotoMaps,
  );

  await ref.update({
    'photos': mergedPhotos,
    'reviewStatus': 'pending_review',
    'resubmittedAt': Timestamp.fromDate(now),
    'deliveredLat': deliveredLat,
    'deliveredLng': deliveredLng,
    'updatedAt': Timestamp.fromDate(now),
  });
}
