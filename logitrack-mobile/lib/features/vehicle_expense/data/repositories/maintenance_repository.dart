import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:firebase_storage/firebase_storage.dart';
import '../../../home/data/services/image_compression_service.dart';

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
        .where('status', whereIn: ['PM Booking', 'Scheduled', 'In-Progress'])
        .snapshots()
        .map((snap) {
          return snap.docs
              .map((doc) => {'id': doc.id, ...doc.data()})
              .toList();
        });
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
