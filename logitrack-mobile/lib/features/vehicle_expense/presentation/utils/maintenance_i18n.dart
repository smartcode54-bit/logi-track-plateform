import 'package:easy_localization/easy_localization.dart';

/// สถานที่นัด / อู่ — Firestore ใช้ `provider` (ฝั่ง web admin); ของเก่าอาจเป็น `locationName`
String? rawMaintenanceLocation(Map<String, dynamic> task) {
  final p = task['provider'];
  if (p is String && p.trim().isNotEmpty) return p.trim();
  final l = task['locationName'];
  if (l is String && l.trim().isNotEmpty) return l.trim();
  return null;
}

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

/// พิกัดอู่จากแอดมิน (providerLat / providerLng) สำหรับเปิดแผนที่นำทาง
({double lat, double lng})? maintenanceProviderCoords(
  Map<String, dynamic> task,
) {
  final la = task['providerLat'];
  final ln = task['providerLng'];
  if (la is! num || ln is! num) return null;
  final lat = la.toDouble();
  final lng = ln.toDouble();
  if (lat < -90 || lat > 90 || lng < -180 || lng > 180) return null;
  return (lat: lat, lng: lng);
}
