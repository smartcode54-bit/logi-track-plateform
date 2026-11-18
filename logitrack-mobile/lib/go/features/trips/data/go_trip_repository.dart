import '../../../../core/api/api_client.dart';
import '../../../../core/api/api_exception.dart';
import '../domain/mobile_trip.dart';

/// The driver's trip writes and reads on the Go API (spec mobile-go-core-loop R13, R14, R23-R25). Every write takes
/// the submit's `Idempotency-Key` ([opId]); a retry passes the same one.
class GoTripRepository {
  GoTripRepository(this.api);
  final ApiClient api;

  /// `POST /v1/mobile/tasks/{id}/check-in` ✱.
  Future<void> checkIn({
    required String opId,
    required String taskId,
    required String truckId,
    required DateTime checkInAt,
    required String photoKey,
    required String appScreenshotKey,
    double? lat,
    double? lng,
    String? installId,
  }) async {
    await api.postData(
      '/v1/mobile/tasks/${Uri.encodeComponent(taskId)}/check-in',
      {
        'truckId': truckId,
        'checkInAt': checkInAt.toUtc().toIso8601String(),
        'photoKey': photoKey,
        'appScreenshotKey': appScreenshotKey,
        'lat': ?lat,
        'lng': ?lng,
        'installId': ?installId,
      },
      idempotencyKey: opId,
    );
  }

  /// `POST /v1/mobile/trips` ✱ (the loading of a single-stop job).
  Future<MobileTrip> createTrip({
    required String opId,
    required String taskId,
    required String tripNo,
    required List<TripPhotoRef> photos,
    String? sealCode,
    int? parcelCount,
    double? totalWeight,
    DateTime? sealTime,
    double? lat,
    double? lng,
  }) async {
    final d = await api.postData(
      '/v1/mobile/trips',
      {
        'tripNo': tripNo.trim(),
        'taskId': taskId,
        if (sealCode != null && sealCode.trim().isNotEmpty) 'sealCode': sealCode.trim(),
        'parcelCount': ?parcelCount,
        'totalWeight': ?totalWeight,
        if (sealTime != null) 'sealTime': sealTime.toUtc().toIso8601String(),
        'lat': ?lat,
        'lng': ?lng,
        'photos': photos.map((p) => p.toJson()).toList(),
      },
      idempotencyKey: opId,
    );
    if (d == null) throw ApiException(status: 0, code: 'trip_empty');
    return MobileTrip.fromJson(d);
  }

  /// `POST /v1/mobile/trips/{id}/deliver` ✱; [tripRef] is the trip's uuid or its number.
  Future<MobileTrip> deliver({
    required String opId,
    required String tripRef,
    required DateTime deliveredAt,
    required List<TripPhotoRef> photos,
    double? lat,
    double? lng,
  }) async {
    final d = await api.postData(
      '/v1/mobile/trips/${Uri.encodeComponent(tripRef)}/deliver',
      {
        'deliveredAt': deliveredAt.toUtc().toIso8601String(),
        'lat': ?lat,
        'lng': ?lng,
        'photos': photos.map((p) => p.toJson()).toList(),
      },
      idempotencyKey: opId,
    );
    if (d == null) throw ApiException(status: 0, code: 'trip_empty');
    return MobileTrip.fromJson(d);
  }

  /// `GET /v1/mobile/trips/pending-in-transit`: the driver's in-transit trip, or null.
  Future<MobileTrip?> pendingInTransit() async {
    final d = await api.getData('/v1/mobile/trips/pending-in-transit');
    return d == null ? null : MobileTrip.fromJson(d);
  }

  /// `GET /v1/mobile/trips/by-no/{tripNo}`; null when the server has no such trip for the driver (404).
  Future<MobileTrip?> byNumber(String tripNo) async {
    try {
      final d = await api.getData('/v1/mobile/trips/by-no/${Uri.encodeComponent(tripNo)}');
      return d == null ? null : MobileTrip.fromJson(d);
    } on ApiException catch (e) {
      if (e.status == 404) return null;
      rethrow;
    }
  }
}
