import '../../../shared/evidence_form_bloc.dart';
import '../../tasks/domain/driver_task.dart';
import '../../trips/data/go_trip_repository.dart';
import '../../trips/domain/mobile_trip.dart';

/// The loading of a single-stop job (spec mobile-go-core-loop R13, R15, R23): the trip number (scanned or typed),
/// seal, parcels, weight and the loading photos (`truck_release` is the customer-app screenshot, no overlay); then
/// `POST /v1/mobile/trips`. The photos are uploaded under the submit's op id (the trip has no id yet, D9).
class LoadingBloc extends EvidenceFormBloc {
  LoadingBloc({required super.uploads, required this.trips, required this.task})
      : super(slots: const [
          EvidenceSlot('pre_close', labelKey: 'go_photo_pre_close'),
          EvidenceSlot('closing', labelKey: 'go_photo_closing'),
          EvidenceSlot('seal', labelKey: 'go_photo_seal'),
          EvidenceSlot('truck_release', labelKey: 'go_photo_truck_release', screenshot: true),
          EvidenceSlot('runsheet', labelKey: 'go_photo_runsheet'),
          EvidenceSlot('runsheet_extra_1', labelKey: 'go_photo_runsheet_extra', required: false),
          EvidenceSlot('runsheet_extra_2', labelKey: 'go_photo_runsheet_extra', required: false),
          EvidenceSlot('runsheet_extra_3', labelKey: 'go_photo_runsheet_extra', required: false),
        ]);

  final GoTripRepository trips;
  final DriverTask task;

  static const fieldTripNo = 'tripNo';
  static const fieldSeal = 'sealCode';
  static const fieldParcels = 'parcelCount';
  static const fieldWeight = 'totalWeight';

  @override
  String purposeOf(EvidenceSlot slot) => 'trip_photo';

  @override
  String entityIdFor(String opId) => opId;

  @override
  String? validateFields(Map<String, Object?> fields) =>
      (fields[fieldTripNo] as String?)?.trim().isNotEmpty == true ? null : 'loading_phase_trip_id';

  @override
  Future<Object> submit(String opId, Map<String, String> keys, EvidenceState state) {
    final f = state.fields;
    final seal = state.photos['seal'];
    return trips.createTrip(
      opId: opId,
      taskId: task.id,
      tripNo: f[fieldTripNo] as String,
      sealCode: f[fieldSeal] as String?,
      parcelCount: int.tryParse('${f[fieldParcels] ?? ''}'),
      totalWeight: double.tryParse('${f[fieldWeight] ?? ''}'),
      sealTime: seal?.takenAt,
      lat: seal?.lat,
      lng: seal?.lng,
      photos: [
        for (final s in slots)
          if (keys.containsKey(s.id))
            TripPhotoRef(
              type: s.id,
              key: keys[s.id]!,
              lat: state.photos[s.id]?.lat,
              lng: state.photos[s.id]?.lng,
              address: state.photos[s.id]?.address,
              geocodedAt: state.photos[s.id]?.lat == null ? null : state.photos[s.id]?.takenAt,
            ),
      ],
    );
  }
}
