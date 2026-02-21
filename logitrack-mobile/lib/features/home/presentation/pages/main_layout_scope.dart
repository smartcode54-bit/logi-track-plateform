import 'package:flutter/material.dart';

/// ข้อมูลสรุปเที่ยวที่เพิ่งบันทึก (จาก Loading) เพื่อแสดงบนหน้า Delivery
class SavedTripSummary {
  final String tripId;
  final String? origin;
  final String? destination;
  final String? sealCode;
  final String? jobType;

  const SavedTripSummary({
    required this.tripId,
    this.origin,
    this.destination,
    this.sealCode,
    this.jobType,
  });
}

/// ให้ tab อย่าง LoadingPhasePage เรียกไปหน้า Delivery พร้อมส่ง summary ได้
/// และให้ Delivery เรียก onDeliveryCompleted เมื่อส่งงานเสร็จแล้ว (เพื่อปลดล็อก Pick up)
class MainLayoutScope extends InheritedWidget {
  const MainLayoutScope({
    super.key,
    required this.goToDeliveryTab,
    this.onDeliveryCompleted,
    required super.child,
  });

  final void Function(SavedTripSummary? summary) goToDeliveryTab;

  /// เรียกเมื่อส่งงาน (Delivery) บันทึกเสร็จแล้ว เพื่อเคลียร์ active trip และปลดล็อกแท็บ Pick up
  final void Function()? onDeliveryCompleted;

  static MainLayoutScope? of(BuildContext context) {
    return context.dependOnInheritedWidgetOfExactType<MainLayoutScope>();
  }

  @override
  bool updateShouldNotify(MainLayoutScope old) =>
      goToDeliveryTab != old.goToDeliveryTab ||
      onDeliveryCompleted != old.onDeliveryCompleted;
}
