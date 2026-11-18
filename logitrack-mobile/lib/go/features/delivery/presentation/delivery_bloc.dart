import '../../../shared/evidence_form_bloc.dart';
import '../../trips/data/go_trip_repository.dart';
import '../../trips/domain/mobile_trip.dart';

/// The delivery of a single-stop trip (spec mobile-go-core-loop R14, R15, R24): the arrival screenshot (`arrived`,
/// no overlay) and the unloading photos, owned by the trip; then `POST /v1/mobile/trips/{id}/deliver`.
class DeliveryBloc extends EvidenceFormBloc {
  DeliveryBloc({required super.uploads, required this.trips, required this.trip})
      : super(slots: const [
          EvidenceSlot('arrived', labelKey: 'go_photo_arrived', screenshot: true),
          EvidenceSlot('pre_open', labelKey: 'go_photo_pre_open'),
          EvidenceSlot('opening', labelKey: 'go_photo_opening'),
          EvidenceSlot('empty_container', labelKey: 'go_photo_empty_container'),
          EvidenceSlot('runsheet_received', labelKey: 'go_photo_runsheet_received'),
        ]);

  final GoTripRepository trips;
  final MobileTrip trip;

  @override
  String purposeOf(EvidenceSlot slot) => 'trip_photo';

  @override
  String entityIdFor(String opId) => trip.id;

  @override
  Future<Object> submit(String opId, Map<String, String> keys, EvidenceState state) {
    final last = state.photos['empty_container'] ?? state.photos['opening'];
    return trips.deliver(
      opId: opId,
      tripRef: trip.id,
      deliveredAt: last?.takenAt ?? DateTime.now(),
      lat: last?.lat,
      lng: last?.lng,
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
