import 'dart:convert';
import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:firebase_storage/firebase_storage.dart';
import 'package:flutter/foundation.dart' show debugPrint;
import 'package:shared_preferences/shared_preferences.dart';
import '../../../home/data/services/image_compression_service.dart';
import '../models/vehicle_expense.dart';

const String vehicleExpensesCollection = 'vehicle_expenses';
const String _storageFolder = 'vehicle_expenses';
const String _pendingKey = 'logitrack_vehicle_expense_pending';

/// ใช้เมื่อบันทึกไม่ได้ (เน็ตตัด/แอปปิด) แต่เก็บไว้ในเครื่องแล้ว — UI ควรแสดง "บันทึกไว้ในเครื่อง จะส่งเมื่อมีสัญญาณ"
class VehicleExpenseOfflineSavedException implements Exception {
  VehicleExpenseOfflineSavedException([this.message]);
  final String? message;
}

/// อัปโหลดรูป vehicle expense ขึ้น Storage แล้วคืน URL
/// Path: vehicle_expenses/{docId}/{photoType}.jpg
Future<String> uploadVehicleExpensePhoto({
  required String docId,
  required String photoType,
  required List<int> imageBytes,
}) async {
  if (imageBytes.isEmpty) {
    throw ArgumentError('uploadVehicleExpensePhoto: imageBytes.isEmpty');
  }
  debugPrint(
    'VehicleExpense: uploading $photoType (${imageBytes.length} bytes) to Storage...',
  );
  final compressed = await compressImageForUpload(imageBytes);
  final path = '$_storageFolder/$docId/$photoType.jpg';
  final ref = FirebaseStorage.instance.ref().child(path);
  await ref.putData(compressed, SettableMetadata(contentType: 'image/jpeg'));
  final url = await ref.getDownloadURL();
  debugPrint('VehicleExpense: $photoType uploaded -> $url');
  return url;
}

/// บันทึกค่าใช้จ่ายรถ (เติมน้ำมัน / อื่นๆ) ลง Firestore
/// ถ้ามี [receiptPhoto] / [odometerPhoto] จะอัปโหลดขึ้น Storage แล้วบันทึก URL ลง doc
/// ถ้าเชื่อมต่อไม่ได้ จะเก็บลงคิวในเครื่องและ throw [VehicleExpenseOfflineSavedException]
Future<String?> saveVehicleExpense(
  VehicleExpense expense, {
  List<int>? receiptPhoto,
  List<int>? odometerPhoto,
}) async {
  final col = FirebaseFirestore.instance.collection(vehicleExpensesCollection);
  final data = expense.toFirestore();
  String? docId;

  try {
    if (expense.id != null &&
        expense.id!.isNotEmpty &&
        !expense.id!.startsWith('pending_')) {
      docId = expense.id;
      final note = expense.adminNote;
      if (note == null || note.trim().isEmpty) {
        data['adminNote'] = FieldValue.delete();
      }
      await col.doc(docId).set(data, SetOptions(merge: true));
    } else {
      final ref = await col.add(data);
      docId = ref.id;
    }
  } catch (e) {
    debugPrint('VehicleExpense: Firestore write failed: $e');
    await _addPending(expense);
    throw VehicleExpenseOfflineSavedException();
  }

  // อัปโหลดรูปขึ้น Storage แล้วอัปเดต doc (ถ้าบันทึก Firestore สำเร็จแล้ว)
  if (docId != null && (receiptPhoto != null || odometerPhoto != null)) {
    String? receiptUrl;
    String? odometerUrl;
    try {
      if (expense.type == VehicleExpenseType.fuel) {
        if (receiptPhoto != null && receiptPhoto.isNotEmpty) {
          receiptUrl = await uploadVehicleExpensePhoto(
            docId: docId,
            photoType: 'receipt',
            imageBytes: receiptPhoto,
          );
        }
        if (odometerPhoto != null && odometerPhoto.isNotEmpty) {
          odometerUrl = await uploadVehicleExpensePhoto(
            docId: docId,
            photoType: 'odometer',
            imageBytes: odometerPhoto,
          );
        }
      } else if (expense.type == VehicleExpenseType.other &&
          receiptPhoto != null &&
          receiptPhoto.isNotEmpty) {
        receiptUrl = await uploadVehicleExpensePhoto(
          docId: docId,
          photoType: 'receipt',
          imageBytes: receiptPhoto,
        );
      }
      if (receiptUrl != null || odometerUrl != null) {
        final update = <String, dynamic>{};
        if (receiptUrl != null) update['receiptPhotoUrl'] = receiptUrl;
        if (odometerUrl != null) update['odometerPhotoUrl'] = odometerUrl;
        update['updatedAt'] = Timestamp.fromDate(DateTime.now());
        await col.doc(docId).update(update);
        debugPrint('VehicleExpense: doc updated with photo URLs');
      }
    } catch (e) {
      debugPrint('VehicleExpense: Storage upload or update failed: $e');
      rethrow;
    }
  }

  return docId;
}

Future<List<Map<String, dynamic>>> _getPendingList() async {
  try {
    final prefs = await SharedPreferences.getInstance();
    final raw = prefs.getString(_pendingKey);
    if (raw == null || raw.isEmpty) return [];
    final list = jsonDecode(raw) as List<dynamic>?;
    if (list == null) return [];
    return list.map((e) => Map<String, dynamic>.from(e as Map)).toList();
  } catch (_) {
    return [];
  }
}

Future<void> _savePendingList(List<Map<String, dynamic>> list) async {
  final prefs = await SharedPreferences.getInstance();
  await prefs.setString(_pendingKey, jsonEncode(list));
}

Future<void> _addPending(VehicleExpense expense) async {
  final list = await _getPendingList();
  list.add(expense.toJsonForLocal());
  await _savePendingList(list);
}

/// ดึงรายการที่ค้างในเครื่อง (ยังไม่ได้ส่ง Firestore)
Future<List<VehicleExpense>> getPendingVehicleExpenses(String driverId) async {
  final list = await _getPendingList();
  final result = <VehicleExpense>[];
  for (var i = 0; i < list.length; i++) {
    final map = list[i];
    if ((map['driverId'] as String? ?? '') != driverId) continue;
    try {
      result.add(VehicleExpense.fromMap(map, id: 'pending_$i'));
    } catch (_) {}
  }
  return result;
}

/// ส่งรายการค้างของ driver ขึ้น Firestore (เรียกเมื่อเปิดหน้าหรือมีเน็ตกลับ)
/// คืนจำนวนที่ส่งสำเร็จ
Future<int> syncPendingVehicleExpenses(String driverId) async {
  final list = await _getPendingList();
  if (list.isEmpty) return 0;
  var synced = 0;
  final toRemove = <int>[];
  for (var i = 0; i < list.length; i++) {
    final map = list[i];
    if ((map['driverId'] as String? ?? '') != driverId) continue;
    try {
      final expense = VehicleExpense.fromMap(map, id: null);
      await _saveToFirestoreOnly(expense);
      toRemove.add(i);
      synced++;
    } catch (_) {
      // ยังส่งไม่ได้ (เน็ตไม่มี) ข้ามรายการนี้
    }
  }
  if (toRemove.isNotEmpty) {
    toRemove.sort((a, b) => b.compareTo(a));
    for (final i in toRemove) {
      list.removeAt(i);
    }
    await _savePendingList(list);
  }
  return synced;
}

/// บันทึกลง Firestore อย่างเดียว (ใช้ใน sync — ไม่เพิ่ม pending ถ้าล้ม)
Future<String?> _saveToFirestoreOnly(VehicleExpense expense) async {
  final col = FirebaseFirestore.instance.collection(vehicleExpensesCollection);
  final data = expense.toFirestore();
  final ref = await col.add(data);
  return ref.id;
}

/// ตรวจว่า driver นี้เคยบันทึก TAX INV ID นี้แล้วหรือยัง (เติมน้ำมันเท่านั้น)
/// ใช้ก่อน save เพื่อกันบันทึกซ้ำ
/// หมายเหตุ: Firestore ต้องมี composite index: driverId (ASC), taxInvId (ASC), type (ASC)
///
/// [pastDriverIds] = Auth UID เก่าของคนขับ (`authIdHistory`) — บิลที่บันทึกก่อนเปลี่ยนเมล/ผูก Google ใหม่
/// อยู่ใต้ uid เก่า ถ้าไม่เช็คด้วยจะส่งบิลเดิมซ้ำได้. [excludeDocId] = รายการที่กำลังแก้ (ไม่นับตัวเอง)
Future<bool> existsTaxInvIdForDriver(
  String driverId,
  String taxInvId, {
  Iterable<String> pastDriverIds = const [],
  String? excludeDocId,
}) async {
  if (driverId.isEmpty || taxInvId.trim().isEmpty) return false;
  final tid = taxInvId.trim();
  final ids = <String>{driverId, ...pastDriverIds.where((id) => id.isNotEmpty)};
  for (final id in ids) {
    try {
      final snapshot = await FirebaseFirestore.instance
          .collection(vehicleExpensesCollection)
          .where('driverId', isEqualTo: id)
          .where('taxInvId', isEqualTo: tid)
          .where('type', isEqualTo: 'fuel')
          .limit(2)
          .get();
      if (snapshot.docs.any((d) => d.id != excludeDocId)) return true;
    } catch (_) {
      // เช็คไม่ได้ (เน็ต / rules ยังไม่ deploy สำหรับ uid เก่า) — ไม่บล็อกการบันทึก
    }
  }
  return false;
}

/// ดึงรายการค่าใช้จ่ายรถของคนขับ เรียงวันที่ล่าสุดก่อน
/// หมายเหตุ: Firestore ต้องมี composite index: collection vehicle_expenses, fields driverId (ASC), date (DESC)
/// ถ้า index ยังไม่มี ให้กดลิงก์ในข้อความ error เพื่อสร้าง index ในคอนโซล Firebase
///
/// [pastDriverIds] = Auth UID เก่าของคนขับ (`authIdHistory`, ดู `driverIdentityKeys`). รายการที่บันทึก
/// ก่อนเปลี่ยนเมล/ผูก Google ใหม่เก็บ driverId เป็น uid เก่า — query ทีละ uid (equality) เพื่อใช้ index เดิม
/// และให้ rules ตรวจได้ทีละค่า. uid เก่าที่ query ไม่ผ่าน (เช่น rules ยังไม่ deploy) ข้ามไป ไม่ทำให้ทั้งรายการพัง
Future<List<VehicleExpense>> getVehicleExpensesByDriver(
  String driverId, {
  int limit = 50,
  VehicleExpenseType? type,
  Iterable<String> pastDriverIds = const [],
}) async {
  if (driverId.isEmpty) return [];
  Future<List<VehicleExpense>> byDriverId(String id) async {
    Query<Map<String, dynamic>> query = FirebaseFirestore.instance
        .collection(vehicleExpensesCollection)
        .where('driverId', isEqualTo: id);
    if (type != null) {
      query = query.where(
        'type',
        isEqualTo: type == VehicleExpenseType.fuel ? 'fuel' : 'other',
      );
    }
    final snapshot = await query
        .orderBy('date', descending: true)
        .limit(limit)
        .get();
    return snapshot.docs
        .map((doc) => VehicleExpense.fromMap(doc.data(), id: doc.id))
        .toList();
  }

  Future<List<VehicleExpense>> pastOrEmpty(String id) async {
    try {
      return await byDriverId(id);
    } catch (e) {
      debugPrint('VehicleExpense: history query for past uid $id failed: $e');
      return const [];
    }
  }

  final past = pastDriverIds
      .where((id) => id.isNotEmpty && id != driverId)
      .toSet();
  // The current uid's query still throws on failure (as before) — only past uids are best-effort.
  final results = await Future.wait([
    byDriverId(driverId),
    ...past.map(pastOrEmpty),
  ]);
  if (past.isEmpty) return results.first;
  final seen = <String?>{};
  final merged = [
    for (final list in results)
      for (final e in list)
        if (seen.add(e.id)) e,
  ];
  merged.sort((a, b) => b.date.compareTo(a.date));
  return merged.take(limit).toList();
}
