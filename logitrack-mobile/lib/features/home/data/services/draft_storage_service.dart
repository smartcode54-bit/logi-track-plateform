import 'dart:convert';
import 'dart:typed_data';
import 'package:flutter/foundation.dart' show kIsWeb, debugPrint;
import 'package:path_provider/path_provider.dart';
import 'package:path/path.dart' as p;
import 'package:shared_preferences/shared_preferences.dart';

import 'draft_storage_io.dart'
    if (dart.library.html) 'draft_storage_io_stub.dart'
    as io;

/// ข้อมูล draft ฟอร์ม Loading (รับงาน) ที่ยังไม่ส่ง
class LoadingDraft {
  final String tripId;
  final String sealCode;
  final String origin;
  final String destination;
  final String distance;
  final String parcelCount;
  final String sealTime;
  final String totalWeight;
  final String? partnerCode;
  final String? jobType;
  final String? runsheetPath;
  final Map<String, String> stepPhotoPaths; // stepKey -> path

  const LoadingDraft({
    this.tripId = '',
    this.sealCode = '',
    this.origin = '',
    this.destination = '',
    this.distance = '',
    this.parcelCount = '',
    this.sealTime = '',
    this.totalWeight = '',
    this.partnerCode,
    this.jobType,
    this.runsheetPath,
    this.stepPhotoPaths = const {},
  });

  Map<String, dynamic> toJson() => {
    'tripId': tripId,
    'sealCode': sealCode,
    'origin': origin,
    'destination': destination,
    'distance': distance,
    'parcelCount': parcelCount,
    'sealTime': sealTime,
    'totalWeight': totalWeight,
    'partnerCode': partnerCode,
    'jobType': jobType,
    'runsheetPath': runsheetPath,
    'stepPhotoPaths': stepPhotoPaths,
  };

  static LoadingDraft fromJson(Map<String, dynamic> json) {
    final stepMap = json['stepPhotoPaths'];
    return LoadingDraft(
      tripId: json['tripId'] as String? ?? '',
      sealCode: json['sealCode'] as String? ?? '',
      origin: json['origin'] as String? ?? '',
      destination: json['destination'] as String? ?? '',
      distance: json['distance'] as String? ?? '',
      parcelCount: json['parcelCount'] as String? ?? '',
      sealTime: json['sealTime'] as String? ?? '',
      totalWeight: json['totalWeight'] as String? ?? '',
      partnerCode: json['partnerCode'] as String?,
      jobType: json['jobType'] as String?,
      runsheetPath: json['runsheetPath'] as String?,
      stepPhotoPaths: stepMap is Map
          ? Map<String, String>.from(
              stepMap.map((k, v) => MapEntry(k.toString(), v.toString())),
            )
          : {},
    );
  }
}

/// ข้อมูล draft การส่งงาน (Delivery) ที่ยังไม่ส่ง
class DeliveryDraft {
  final String tripId;
  final String? origin;
  final String? destination;
  final String? sealCode;
  final String? jobType;
  final String? taskId;
  final Map<String, String> photoPaths; // stepKey -> path

  const DeliveryDraft({
    required this.tripId,
    this.origin,
    this.destination,
    this.sealCode,
    this.jobType,
    this.taskId,
    this.photoPaths = const {},
  });

  Map<String, dynamic> toJson() => {
    'tripId': tripId,
    'origin': origin,
    'destination': destination,
    'sealCode': sealCode,
    'jobType': jobType,
    'taskId': taskId,
    'photoPaths': photoPaths,
  };

  static DeliveryDraft fromJson(Map<String, dynamic> json) {
    final photoMap = json['photoPaths'];
    return DeliveryDraft(
      tripId: json['tripId'] as String? ?? '',
      origin: json['origin'] as String?,
      destination: json['destination'] as String?,
      sealCode: json['sealCode'] as String?,
      jobType: json['jobType'] as String?,
      taskId: json['taskId'] as String?,
      photoPaths: photoMap is Map
          ? Map<String, String>.from(
              photoMap.map((k, v) => MapEntry(k.toString(), v.toString())),
            )
          : {},
    );
  }
}

const String _prefKeyLoadingDraft = 'logitrack_loading_draft';
const String _prefKeyDeliveryDraft = 'logitrack_delivery_draft';
const String _prefKeyActiveCheckInTaskId = 'logitrack_active_checkin_task_id';

/// Key ที่ MainLayout ใช้เก็บ pending delivery summary — expose ไว้ให้ clearAllUserData ใช้ได้
const String prefKeyPendingDeliverySummary = 'logitrack_pending_delivery_summary';

const String _draftDirName = 'logitrack_draft';

/// เก็บ/โหลดงานค้าง (draft) เมื่อแอปถูกปิดหรือหลุด เพื่อให้กลับมาโหลดต่อได้
class DraftStorageService {
  DraftStorageService._();
  static final DraftStorageService _instance = DraftStorageService._();
  static DraftStorageService get instance => _instance;

  SharedPreferences? _prefs;
  String? _draftDirPath;

  Future<SharedPreferences> get _preferences async {
    _prefs ??= await SharedPreferences.getInstance();
    return _prefs!;
  }

  /// โฟลเดอร์เก็บไฟล์ draft (รูป) — ไม่ support บน Web
  Future<String?> get _draftDirectory async {
    if (kIsWeb) return null;
    if (_draftDirPath != null) return _draftDirPath;
    try {
      final dir = await getApplicationDocumentsDirectory();
      final draftDir = p.join(dir.path, _draftDirName);
      final folder = await _ensureDir(draftDir);
      _draftDirPath = folder;
      return _draftDirPath;
    } catch (e) {
      debugPrint('DraftStorage: get draft dir failed $e');
      return null;
    }
  }

  Future<String> _ensureDir(String path) async {
    return io.ensureDraftDir(path);
  }

  Future<String?> _writePhotoBytes(String filename, Uint8List bytes) async {
    final base = await _draftDirectory;
    if (base == null) return null;
    return io.writeDraftPhoto(p.join(base, filename), bytes);
  }

  Future<Uint8List?> _readPhotoBytes(String path) async {
    if (kIsWeb) return null;
    return io.readDraftPhoto(path);
  }

  Future<void> _clearLoadingDraftFiles() async {
    final base = await _draftDirectory;
    if (base != null) await io.clearDraftFiles(base, loading: true);
  }

  Future<void> _clearDeliveryDraftFiles() async {
    final base = await _draftDirectory;
    if (base != null) await io.clearDraftFiles(base, delivery: true);
  }

  // ---------- Loading draft ----------

  /// บันทึก draft ฟอร์ม Loading + path ของรูป (รูปเขียนลง disk แยก)
  Future<void> saveLoadingDraft({
    required String tripId,
    required String sealCode,
    required String origin,
    required String destination,
    required String distance,
    required String parcelCount,
    required String sealTime,
    required String totalWeight,
    String? partnerCode,
    String? jobType,
    Uint8List? runsheetPhoto,
    Map<String, Uint8List>? stepPhotos,
  }) async {
    final prefs = await _preferences;
    final base = await _draftDirectory;
    if (base == null &&
        (runsheetPhoto != null || (stepPhotos?.isNotEmpty == true))) {
      return;
    }

    String? runsheetPath;
    if (runsheetPhoto != null && runsheetPhoto.isNotEmpty) {
      runsheetPath = await _writePhotoBytes(
        'loading_runsheet.jpg',
        runsheetPhoto,
      );
    }

    final stepPhotoPaths = <String, String>{};
    if (stepPhotos != null && base != null) {
      for (final e in stepPhotos.entries) {
        if (e.value.isNotEmpty) {
          final path = await _writePhotoBytes('loading_${e.key}.jpg', e.value);
          if (path != null) stepPhotoPaths[e.key] = path;
        }
      }
    }

    final draft = LoadingDraft(
      tripId: tripId,
      sealCode: sealCode,
      origin: origin,
      destination: destination,
      distance: distance,
      parcelCount: parcelCount,
      sealTime: sealTime,
      totalWeight: totalWeight,
      partnerCode: partnerCode,
      jobType: jobType,
      runsheetPath: runsheetPath,
      stepPhotoPaths: stepPhotoPaths,
    );
    await prefs.setString(_prefKeyLoadingDraft, jsonEncode(draft.toJson()));
  }

  /// โหลด draft Loading (metadata เท่านั้น; รูปโหลดจาก path แยก)
  Future<LoadingDraft?> loadLoadingDraft() async {
    final prefs = await _preferences;
    final raw = prefs.getString(_prefKeyLoadingDraft);
    if (raw == null || raw.isEmpty) return null;
    try {
      final map = jsonDecode(raw) as Map<String, dynamic>?;
      if (map == null) return null;
      return LoadingDraft.fromJson(map);
    } catch (_) {
      return null;
    }
  }

  /// โหลดรูปจาก draft (runsheet หรือ step ตาม path ที่เก็บไว้)
  Future<Uint8List?> loadLoadingDraftPhoto(String path) async {
    return _readPhotoBytes(path);
  }

  Future<void> clearLoadingDraft() async {
    final prefs = await _preferences;
    await prefs.remove(_prefKeyLoadingDraft);
    await _clearLoadingDraftFiles();
  }

  // ---------- Active Check In Task Id ----------

  Future<void> saveActiveCheckInTaskId(String taskId) async {
    final prefs = await _preferences;
    await prefs.setString(_prefKeyActiveCheckInTaskId, taskId);
  }

  Future<String?> loadActiveCheckInTaskId() async {
    final prefs = await _preferences;
    return prefs.getString(_prefKeyActiveCheckInTaskId);
  }

  Future<void> clearActiveCheckInTaskId() async {
    final prefs = await _preferences;
    await prefs.remove(_prefKeyActiveCheckInTaskId);
  }

  // ---------- Delivery draft ----------

  Future<void> saveDeliveryDraft({
    required String tripId,
    String? origin,
    String? destination,
    String? sealCode,
    String? jobType,
    String? taskId,
    required Map<String, Uint8List> photos,
  }) async {
    final prefs = await _preferences;
    final base = await _draftDirectory;
    if (base == null && photos.isNotEmpty) return;

    final photoPaths = <String, String>{};
    if (photos.isNotEmpty && base != null) {
      for (final e in photos.entries) {
        if (e.value.isNotEmpty) {
          final path = await _writePhotoBytes('delivery_${e.key}.jpg', e.value);
          if (path != null) photoPaths[e.key] = path;
        }
      }
    }

    final draft = DeliveryDraft(
      tripId: tripId,
      origin: origin,
      destination: destination,
      sealCode: sealCode,
      jobType: jobType,
      taskId: taskId,
      photoPaths: photoPaths,
    );
    await prefs.setString(_prefKeyDeliveryDraft, jsonEncode(draft.toJson()));
  }

  Future<DeliveryDraft?> loadDeliveryDraft() async {
    final prefs = await _preferences;
    final raw = prefs.getString(_prefKeyDeliveryDraft);
    if (raw == null || raw.isEmpty) return null;
    try {
      final map = jsonDecode(raw) as Map<String, dynamic>?;
      if (map == null) return null;
      return DeliveryDraft.fromJson(map);
    } catch (_) {
      return null;
    }
  }

  Future<Uint8List?> loadDeliveryDraftPhoto(String path) async {
    return _readPhotoBytes(path);
  }

  Future<void> clearDeliveryDraft() async {
    final prefs = await _preferences;
    await prefs.remove(_prefKeyDeliveryDraft);
    await _clearDeliveryDraftFiles();
  }

  // ---------- Clear all user-scoped data on logout ----------

  /// ล้างข้อมูลทั้งหมดที่ผูกกับ user คนนี้ เรียกตอน signOut เพื่อไม่ให้ user ถัดไปเห็นข้อมูลคนเก่า
  ///
  /// หมายเหตุ: [prefKeyPendingDeliverySummary] ไม่ถูก clear ที่นี่ —
  /// ข้อมูลนั้นถูก tag ด้วย uid ของเจ้าของไว้แล้ว ตรวจสอบใน MainLayout._loadPendingDeliverySummary
  /// ถ้า uid ไม่ตรงกับ user ปัจจุบัน จะถูกทิ้งอัตโนมัติ ซึ่งทำให้ user คนเดิม login กลับมาแล้วยังเห็นงานค้างได้
  Future<void> clearAllUserData() async {
    final prefs = await _preferences;
    await Future.wait([
      prefs.remove(_prefKeyLoadingDraft),
      prefs.remove(_prefKeyDeliveryDraft),
      prefs.remove(_prefKeyActiveCheckInTaskId),
    ]);
    await _clearLoadingDraftFiles();
    await _clearDeliveryDraftFiles();
    debugPrint('[DraftStorageService] clearAllUserData: done');
  }
}
