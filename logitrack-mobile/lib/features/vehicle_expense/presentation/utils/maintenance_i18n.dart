import 'package:easy_localization/easy_localization.dart';

/// แปล [serviceType] จากฝั่ง admin (เช่น periodic_check) ให้เป็นข้อความที่อ่านได้
String trMaintenanceServiceType(String? serviceType) {
  if (serviceType == null || serviceType.trim().isEmpty) {
    return 'maintenance_service_default'.tr();
  }
  final raw = serviceType.trim();
  final key = 'maintenance_service_$raw';
  final t = key.tr();
  return t == key ? raw : t;
}

/// แปลสถานะเอกสาร maintenance ใน Firestore
String trMaintenanceStatus(String? status) {
  if (status == null || status.isEmpty) return '';
  final norm = status
      .replaceAll(' ', '_')
      .replaceAll('-', '_')
      .toLowerCase();
  final key = 'maintenance_status_$norm';
  final t = key.tr();
  return t == key ? status : t;
}
