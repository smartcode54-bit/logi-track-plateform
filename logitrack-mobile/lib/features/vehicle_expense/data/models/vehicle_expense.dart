import 'package:cloud_firestore/cloud_firestore.dart';

/// ประเภทค่าใช้จ่าย: เติมน้ำมัน | อื่นๆ (ปะยาง ซ่อม ค่าทางด่วน ฯลฯ)
enum VehicleExpenseType { fuel, other }

/// ประเภทย่อยสำหรับค่าใช้จ่ายอื่น (ไม่ใช่น้ำมัน)
enum OtherExpenseCategory { tireRepair, maintenance, toll, parking, other }

/// โมเดลบันทึกค่าใช้จ่ายรถ (เติมน้ำมัน หรือค่าอื่นๆ)
/// เก็บใน Firestore collection: vehicle_expenses
class VehicleExpense {
  final String? id;
  final String driverId;
  final VehicleExpenseType type;
  final DateTime date;
  final double amount; // บาท

  // เติมน้ำมัน
  final double? volumeLiters;
  final double? pricePerLiter;
  final int? odometer;

  /// เลขประจำตัวผู้เสียภาษีสถานี (TAX ID จากบิล / OCR)
  final String? stationTaxId;

  /// เลขที่ใบกำกับภาษี (TAX INV NO. จากบิล / OCR)
  final String? taxInvId;

  /// สถานที่เติม: "lat,lng" จาก getCurrentLocation (e.g. "13.7563,100.5018")
  final String? refillLocation;

  /// URL รูปบิลน้ำมัน (อัปโหลดไป Storage)
  final String? receiptPhotoUrl;

  /// URL รูปเลขไมล์ (อัปโหลดไป Storage)
  final String? odometerPhotoUrl;
  final String? note;

  // ค่าใช้จ่ายอื่น
  final OtherExpenseCategory? category;
  final String? description;

  // สถานะอนุมัติค่าใช้จ่ายแบบใหม่
  final String status; // 'PENDING' | 'APPROVED' | 'REJECTED'
  final String? adminNote;
  final String? truckId;
  final String? truckLicensePlate;

  final DateTime? createdAt;
  final DateTime? updatedAt;

  const VehicleExpense({
    this.id,
    required this.driverId,
    required this.type,
    required this.date,
    required this.amount,
    this.volumeLiters,
    this.pricePerLiter,
    this.odometer,
    this.stationTaxId,
    this.taxInvId,
    this.refillLocation,
    this.receiptPhotoUrl,
    this.odometerPhotoUrl,
    this.note,
    this.category,
    this.description,
    this.status = 'PENDING',
    this.adminNote,
    this.truckId,
    this.truckLicensePlate,
    this.createdAt,
    this.updatedAt,
  });

  bool get isFuel => type == VehicleExpenseType.fuel;
  bool get isOther => type == VehicleExpenseType.other;

  static DateTime? _parseDate(dynamic v) {
    if (v == null) return null;
    if (v is DateTime) return v;
    if (v is Timestamp) return v.toDate();
    if (v is String) return DateTime.tryParse(v);
    return null;
  }

  static OtherExpenseCategory? _parseCategory(String? s) {
    if (s == null || s.isEmpty) return null;
    switch (s) {
      case 'tire_repair':
        return OtherExpenseCategory.tireRepair;
      case 'maintenance':
        return OtherExpenseCategory.maintenance;
      case 'toll':
        return OtherExpenseCategory.toll;
      case 'parking':
        return OtherExpenseCategory.parking;
      case 'other':
      default:
        return OtherExpenseCategory.other;
    }
  }

  static String categoryToFirestore(OtherExpenseCategory c) {
    switch (c) {
      case OtherExpenseCategory.tireRepair:
        return 'tire_repair';
      case OtherExpenseCategory.maintenance:
        return 'maintenance';
      case OtherExpenseCategory.toll:
        return 'toll';
      case OtherExpenseCategory.parking:
        return 'parking';
      case OtherExpenseCategory.other:
        return 'other';
    }
  }

  factory VehicleExpense.fromMap(Map<String, dynamic> map, {String? id}) {
    final typeStr = map['type'] as String?;
    final type = typeStr == 'fuel'
        ? VehicleExpenseType.fuel
        : VehicleExpenseType.other;
    final dateValue = map['date'];
    DateTime date = DateTime.now();
    if (dateValue != null) {
      final d = _parseDate(dateValue);
      if (d != null) date = d;
    }
    final amount = (map['amount'] as num?)?.toDouble() ?? 0.0;
    return VehicleExpense(
      id: id ?? map['id'] as String?,
      driverId: (map['driverId'] as String?) ?? '',
      type: type,
      date: date,
      amount: amount,
      volumeLiters: (map['volumeLiters'] as num?)?.toDouble(),
      pricePerLiter: (map['pricePerLiter'] as num?)?.toDouble(),
      odometer: (map['odometer'] as num?)?.toInt(),
      stationTaxId:
          map['stationTaxId'] as String? ?? map['gasStation'] as String?,
      taxInvId: map['taxInvId'] as String?,
      refillLocation: map['refillLocation'] as String?,
      receiptPhotoUrl: map['receiptPhotoUrl'] as String?,
      odometerPhotoUrl: map['odometerPhotoUrl'] as String?,
      note: map['note'] as String?,
      category: _parseCategory(map['category'] as String?),
      description: map['description'] as String?,
      status: map['status'] as String? ?? 'PENDING',
      adminNote: map['adminNote'] as String?,
      truckId: map['truckId'] as String?,
      truckLicensePlate: map['truckLicensePlate'] as String?,
      createdAt: _parseDate(map['createdAt']),
      updatedAt: _parseDate(map['updatedAt']),
    );
  }

  Map<String, dynamic> toFirestore() {
    final now = DateTime.now();
    final map = <String, dynamic>{
      'driverId': driverId,
      'type': type == VehicleExpenseType.fuel ? 'fuel' : 'other',
      'date': Timestamp.fromDate(date),
      'amount': amount,
      'status': status,
      'createdAt': createdAt ?? now,
      'updatedAt': updatedAt ?? now,
    };
    if (adminNote != null && adminNote!.isNotEmpty) map['adminNote'] = adminNote;
    if (truckId != null && truckId!.isNotEmpty) map['truckId'] = truckId;
    if (truckLicensePlate != null && truckLicensePlate!.isNotEmpty) {
      map['truckLicensePlate'] = truckLicensePlate;
    }

    if (type == VehicleExpenseType.fuel) {
      if (volumeLiters != null) map['volumeLiters'] = volumeLiters;
      if (pricePerLiter != null) map['pricePerLiter'] = pricePerLiter;
      if (odometer != null) map['odometer'] = odometer;
      if (stationTaxId != null && stationTaxId!.isNotEmpty) {
        map['stationTaxId'] = stationTaxId;
      }
      if (taxInvId != null && taxInvId!.isNotEmpty) map['taxInvId'] = taxInvId;
      if (refillLocation != null && refillLocation!.isNotEmpty) {
        map['refillLocation'] = refillLocation;
      }
      if (receiptPhotoUrl != null && receiptPhotoUrl!.isNotEmpty) {
        map['receiptPhotoUrl'] = receiptPhotoUrl;
      }
      if (odometerPhotoUrl != null && odometerPhotoUrl!.isNotEmpty) {
        map['odometerPhotoUrl'] = odometerPhotoUrl;
      }
      if (note != null && note!.isNotEmpty) map['note'] = note;
    } else {
      if (category != null) map['category'] = categoryToFirestore(category!);
      if (refillLocation != null && refillLocation!.isNotEmpty) {
        map['refillLocation'] = refillLocation;
      }
      if (receiptPhotoUrl != null && receiptPhotoUrl!.isNotEmpty) {
        map['receiptPhotoUrl'] = receiptPhotoUrl;
      }
    }
    return map;
  }

  /// สำหรับเก็บค้าง (pending) ในเครื่อง — ใช้ date/createdAt/updatedAt เป็น ISO string เพื่อ serialize ลง SharedPreferences
  Map<String, dynamic> toJsonForLocal() {
    final now = DateTime.now();
    final d = createdAt ?? now;
    final u = updatedAt ?? now;
    final map = toFirestore()
      ..['date'] = date.toUtc().toIso8601String()
      ..['createdAt'] = d.toUtc().toIso8601String()
      ..['updatedAt'] = u.toUtc().toIso8601String();
    return map;
  }
}
