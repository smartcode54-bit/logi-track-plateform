import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:flutter/foundation.dart' show debugPrint;

import '../../../../core/services/mobile_client_heartbeat_service.dart';
import 'package:intl/intl.dart' as intl;
import '../../../home/data/models/trip_record.dart';
import '../../../home/data/repositories/trip_records_repository.dart';

/// บันทึกรับงาน (Loading Phase) ลง TripRecords
/// [distanceFromHubSocKm] และ [durationMinutes] จาก hub_soc_distances (ถ้ามี) ใช้สำหรับ STD/STA และระยะทางใน record
Future<void> submitLoadingPhaseRecord({
  required String tripId,
  required String jobType,
  String? driverId,
  String? taskId,
  String? sealCode,
  String? partnerCode,
  String? origin,
  String? destination,
  String? distance,
  double? distanceFromHubSocKm,
  double? durationMinutes,
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
  final now = DateTime.now();
  // STD = CreateAt (เวลาบันทึก)
  final std = now;
  // STA = เวลา Seal รถ + durationMinutes (จาก hub_soc_distances)
  DateTime? sta;
  if (sealTime != null &&
      sealTime.trim().isNotEmpty &&
      durationMinutes != null) {
    try {
      final seal = intl.DateFormat(
        'dd-MM-yyyy HH:mm:ss',
      ).parse(sealTime.trim());
      sta = seal.add(Duration(minutes: durationMinutes.round()));
    } catch (_) {}
  }
  // ระยะทาง: จาก hub_soc_distances ก่อน ไม่มีถึงใช้จาก form
  final distanceValue = distanceFromHubSocKm != null
      ? (distanceFromHubSocKm == distanceFromHubSocKm.roundToDouble()
            ? '${distanceFromHubSocKm.round()}'
            : distanceFromHubSocKm.toStringAsFixed(2))
      : distance;
  final record = TripRecord(
    id: tripId,
    status: 'in_transit', // รับงานแล้ว กำลังนำส่ง (ไม่ใช่ loading)
    jobType: jobType,
    photos: photos,
    ocrData: ocrData,
    spxTripId: tripId,
    taskId: taskId,
    sealCode: sealCode,
    partnerCode: partnerCode,
    driverId: driverId,
    origin: origin,
    destination: destination,
    distance: distanceValue,
    parcelCount: parcelCount,
    sealTime: sealTime,
    totalWeight: totalWeight,
    lat: lat,
    lng: lng,
    std: std,
    sta: sta,
    ata: null, // จะอัปเดตเมื่อถึง Hub/SOC ด้วย Geofencing
    durationMinutes: durationMinutes,
    createdAt: now,
    updatedAt: now,
  );
  final data = record.toFirestore();
  final map = Map<String, dynamic>.from(data);
  void toTimestamp(String key) {
    if (map[key] is DateTime) {
      map[key] = Timestamp.fromDate(map[key] as DateTime);
    }
  }

  toTimestamp('createdAt');
  toTimestamp('updatedAt');
  toTimestamp('std');
  toTimestamp('sta');
  toTimestamp('ata');
  if (map['deliveredTimestamp'] is DateTime) {
    map['deliveredTimestamp'] = Timestamp.fromDate(
      map['deliveredTimestamp'] as DateTime,
    );
  }
  await FirebaseFirestore.instance
      .collection(tripRecordsCollection)
      .doc(tripId)
      .set(map, SetOptions(merge: true));

  if (taskId != null && taskId.isNotEmpty) {
    final updateData = <String, dynamic>{};
    if (origin != null && origin.isNotEmpty) {
      updateData['sourceHub'] = origin;
    }
    if (destination != null && destination.isNotEmpty) {
      updateData['destination'] = destination;
    }
    if (updateData.isNotEmpty) {
      try {
        await FirebaseFirestore.instance
            .collection('tasks')
            .doc(taskId)
            .update(updateData);
      } catch (e) {
        // Task metadata update is supplementary — trip record is already saved.
        // Log for investigation but do not surface as a user-facing error.
        debugPrint('[LoadingPhase] tasks.update($taskId) failed (non-critical): $e');
      }
    }
  }

  if (driverId != null && driverId.isNotEmpty) {
    await MobileClientHeartbeatService.instance.onJobAction(driverId);
  }
}
