import 'package:flutter/material.dart';

/// ข้อมูลสรุปเที่ยวที่เพิ่งบันทึก (จาก Loading) เพื่อแสดงบนหน้า Delivery
class SavedTripSummary {
  final String tripId;
  final String? origin;
  final String? destination;
  final String? sealCode;
  final String? jobType;
  final String? taskId;
  final String? truckId;
  final String? truckLicensePlate;

  const SavedTripSummary({
    required this.tripId,
    this.origin,
    this.destination,
    this.sealCode,
    this.jobType,
    this.taskId,
    this.truckId,
    this.truckLicensePlate,
  });

  Map<String, dynamic> toJson() => {
    'tripId': tripId,
    'origin': origin,
    'destination': destination,
    'sealCode': sealCode,
    'jobType': jobType,
    'taskId': taskId,
    'truckId': truckId,
    'truckLicensePlate': truckLicensePlate,
  };

  static SavedTripSummary? fromJson(Map<String, dynamic>? map) {
    if (map == null) return null;
    final tripId = map['tripId'] as String?;
    if (tripId == null || tripId.isEmpty) return null;
    return SavedTripSummary(
      tripId: tripId,
      origin: map['origin'] as String?,
      destination: map['destination'] as String?,
      sealCode: map['sealCode'] as String?,
      jobType: map['jobType'] as String?,
      taskId: map['taskId'] as String?,
      truckId: map['truckId'] as String?,
      truckLicensePlate: map['truckLicensePlate'] as String?,
    );
  }
}

/// ให้ tab อย่าง LoadingPhasePage เรียกไปหน้า Delivery พร้อมส่ง summary ได้
/// และให้ Delivery เรียก onDeliveryCompleted เมื่อส่งงานเสร็จแล้ว (เพื่อปลดล็อก Pick up)
class MainLayoutScope extends InheritedWidget {
  const MainLayoutScope({
    super.key,
    required this.goToDeliveryTab,
    this.onDeliveryCompleted,
    this.onEmbeddedMultiDeliveryAbort,
    required super.child,
  });

  final void Function(SavedTripSummary? summary) goToDeliveryTab;

  /// เรียกเมื่อส่งงาน (Delivery) บันทึกเสร็จแล้ว เพื่อเคลียร์ active trip และปลดล็อกแท็บ Pick up
  final void Function()? onDeliveryCompleted;

  /// Multi-stop อยู่ในแท็บ [IndexedStack] — ถ้าโหลดงานแล้ว error อย่าใช้ Navigator.pop เพราะจะ pop ทั้ง [MainLayout] เป็นมืดว่าง
  final void Function(String message)? onEmbeddedMultiDeliveryAbort;

  static MainLayoutScope? of(BuildContext context) {
    return context.dependOnInheritedWidgetOfExactType<MainLayoutScope>();
  }

  static MainLayoutScope? maybeOf(BuildContext context) {
    return context.findAncestorWidgetOfExactType<MainLayoutScope>();
  }

  @override
  bool updateShouldNotify(MainLayoutScope old) =>
      goToDeliveryTab != old.goToDeliveryTab ||
      onDeliveryCompleted != old.onDeliveryCompleted ||
      onEmbeddedMultiDeliveryAbort != old.onEmbeddedMultiDeliveryAbort;
}
