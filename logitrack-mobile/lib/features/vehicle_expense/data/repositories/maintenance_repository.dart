import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:firebase_storage/firebase_storage.dart';
import '../../../home/data/services/image_compression_service.dart';
import '../models/vehicle_expense.dart';

class MaintenanceRepository {
  final _db = FirebaseFirestore.instance;

  /// อัปโหลดรูปใบเสร็จการซ่อม ขึ้น Storage
  Future<String> uploadMaintenancePhoto({
    required String taskId,
    required String photoType,
    required List<int> imageBytes,
  }) async {
    if (imageBytes.isEmpty) throw ArgumentError('imageBytes.isEmpty');
    
    final compressed = await compressImageForUpload(imageBytes);
    final path = 'maintenance/$taskId/$photoType.jpg';
    final ref = FirebaseStorage.instance.ref().child(path);
    await ref.putData(compressed, SettableMetadata(contentType: 'image/jpeg'));
    return await ref.getDownloadURL();
  }

  /// ดึงข้อมูลรายการซ่อมบำรุงที่ยังไม่จบของรถคันนี้
  Stream<List<Map<String, dynamic>>> streamActiveMaintenance(String truckId) {
    if (truckId.isEmpty) return Stream.value([]);
    
    return _db
        .collection('maintenance')
        .where('truckId', isEqualTo: truckId)
        // Web admin ใช้ snake_case `in_progress`; cloud trigger ใช้ `PM Booking`;
        // mobile check-in ตั้งเป็น `In-Progress`
        .where('status', whereIn: [
          'PM Booking',
          'Scheduled',
          'In-Progress',
          'in_progress',
        ])
        .snapshots()
        .map((snap) {
          return snap.docs
              .map((doc) => {'id': doc.id, ...doc.data()})
              .toList();
        });
  }

  /// รายการซ่อมที่แอดมินกด Completed — แสดงในประวัติค่าใช้จ่ายรถร่วมกับเติมน้ำมัน / อื่นๆ
  Future<List<VehicleExpense>> fetchCompletedMaintenanceAsExpenses({
    required String truckId,
    required String driverId,
    int limit = 24,
  }) async {
    if (truckId.isEmpty || driverId.isEmpty) return [];

    final snap = await _db
        .collection('maintenance')
        .where('truckId', isEqualTo: truckId)
        .where('status', isEqualTo: 'completed')
        .limit(limit)
        .get();

    final list = snap.docs
        .map(
          (doc) => _completedMaintenanceToVehicleExpense(
            doc.id,
            doc.data(),
            driverId,
          ),
        )
        .toList()
      ..sort((a, b) => b.date.compareTo(a.date));
    return list;
  }

  /// พขร. เช็คอินเข้าศูนย์ซ่อม
  Future<void> checkInMaintenance(String taskId) async {
    await _db.collection('maintenance').doc(taskId).update({
      'status': 'In-Progress',
      'checkInAt': FieldValue.serverTimestamp(),
      'updatedAt': FieldValue.serverTimestamp(),
    });
  }

  /// พขร. แจ้งซ่อมเสร็จสิ้น (รอแอดมินตรวจสอบปิดรอบ)
  Future<void> submitMaintenanceCompletion(
    String taskId, {
    required String invoiceUrl,
    required double invoiceAmount,
  }) async {
    await _db.collection('maintenance').doc(taskId).update({
      'status': 'In-Progress', // หรือ 'Submitted' เพื่อให้แอดมินเห็นว่าคนขับส่งงานแล้ว
      'driverSubmitted': true,
      'checkOutAt': FieldValue.serverTimestamp(),
      'invoiceUrl': invoiceUrl,
      'invoiceAmount': invoiceAmount,
      'updatedAt': FieldValue.serverTimestamp(),
    });
  }
}

double _maintenanceAmount(Map<String, dynamic> d) {
  final total = (d['totalCost'] as num?)?.toDouble();
  if (total != null && total > 0) return total;
  final inv = (d['invoiceAmount'] as num?)?.toDouble();
  if (inv != null && inv > 0) return inv;
  final labor = (d['costLabor'] as num?)?.toDouble() ?? 0;
  final parts = (d['costParts'] as num?)?.toDouble() ?? 0;
  final sum = labor + parts;
  return sum > 0 ? sum : 0;
}

DateTime _maintenanceCompletedDate(Map<String, dynamic> d) {
  final end = d['endDate'] as String?;
  if (end != null && end.isNotEmpty) {
    final parsed = DateTime.tryParse(end);
    if (parsed != null) return parsed;
    final parts = end.split('-');
    if (parts.length == 3) {
      final y = int.tryParse(parts[0]);
      final m = int.tryParse(parts[1]);
      final day = int.tryParse(parts[2]);
      if (y != null && m != null && day != null) {
        return DateTime(y, m, day);
      }
    }
  }
  final u = d['updatedAt'];
  if (u is Timestamp) return u.toDate();
  if (u is String) {
    final pu = DateTime.tryParse(u);
    if (pu != null) return pu;
  }
  final c = d['createdAt'];
  if (c is Timestamp) return c.toDate();
  if (c is String) {
    final pc = DateTime.tryParse(c);
    if (pc != null) return pc;
  }
  return DateTime.now();
}

String? _maintenanceReceiptUrl(Map<String, dynamic> d) {
  final inv = d['invoiceUrl'] as String?;
  if (inv != null && inv.isNotEmpty) return inv;
  for (final key in ['images', 'receipts']) {
    final list = d[key];
    if (list is List) {
      for (final e in list) {
        if (e is String && e.isNotEmpty) return e;
      }
    }
  }
  return null;
}

VehicleExpense _completedMaintenanceToVehicleExpense(
  String docId,
  Map<String, dynamic> d,
  String driverId,
) {
  final amount = _maintenanceAmount(d);
  final date = _maintenanceCompletedDate(d);
  final typeLabel = d['type'] as String? ?? '';
  final service = d['serviceType'] as String? ?? '';
  final desc = typeLabel.isNotEmpty && service.isNotEmpty
      ? '[$typeLabel] $service'
      : (service.isNotEmpty ? service : typeLabel);

  return VehicleExpense(
    id: 'm_$docId',
    driverId: driverId,
    type: VehicleExpenseType.other,
    date: date,
    amount: amount,
    category: OtherExpenseCategory.maintenance,
    description: desc.isNotEmpty ? desc : null,
    receiptPhotoUrl: _maintenanceReceiptUrl(d),
    status: 'APPROVED',
    createdAt: date,
    updatedAt: date,
  );
}
