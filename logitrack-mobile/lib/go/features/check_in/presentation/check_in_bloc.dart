import '../../../shared/evidence_form_bloc.dart';
import '../../tasks/domain/driver_task.dart';
import '../../trips/data/go_trip_repository.dart';

/// The check-in of one task (spec mobile-go-core-loop R12, R15, R22): the camera photo (overlay) and the mandatory
/// customer-app screenshot (ADR 0019, no overlay), both owned by the task, and the truck of this job; then
/// `POST /v1/mobile/tasks/{id}/check-in` with the submit's Idempotency-Key.
class CheckInBloc extends EvidenceFormBloc {
  CheckInBloc({required super.uploads, required this.trips, required this.task, required this.installId})
      : super(slots: const [
          EvidenceSlot('checkin_photo', labelKey: 'checkin_take_photo'),
          EvidenceSlot('checkin_app', labelKey: 'checkin_app_screenshot_title', screenshot: true),
        ]);

  final GoTripRepository trips;
  final DriverTask task;
  final String installId;

  static const fieldTruckId = 'truckId';

  @override
  String purposeOf(EvidenceSlot slot) => slot.id == 'checkin_app' ? 'checkin_app_screenshot' : 'checkin_photo';

  @override
  String entityIdFor(String opId) => task.id;

  @override
  String? validateFields(Map<String, Object?> fields) =>
      (fields[fieldTruckId] as String?)?.isNotEmpty == true ? null : 'truck_required';

  @override
  Future<Object> submit(String opId, Map<String, String> keys, EvidenceState state) async {
    final photo = state.photos['checkin_photo']!;
    await trips.checkIn(
      opId: opId,
      taskId: task.id,
      truckId: state.fields[fieldTruckId] as String,
      checkInAt: photo.takenAt,
      photoKey: keys['checkin_photo']!,
      appScreenshotKey: keys['checkin_app']!,
      lat: photo.lat,
      lng: photo.lng,
      installId: installId,
    );
    return true;
  }
}
