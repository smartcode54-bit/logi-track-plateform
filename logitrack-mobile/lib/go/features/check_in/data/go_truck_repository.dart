import '../../../../core/api/api_client.dart';
import '../../../../features/home/data/repositories/trucks_repository.dart' show SelectableTruck;

/// The driver's company trucks for the check-in picker (`GET /v1/mobile/trucks`, spec mobile-go-core-loop R12, R20):
/// only active trucks with a vehicle class come back, so every one offered can price the trip.
class GoTruckRepository {
  GoTruckRepository(this.api);
  final ApiClient api;

  Future<List<SelectableTruck>> companyTrucks() async {
    final out = <SelectableTruck>[];
    String? cursor;
    do {
      final page = await api.getPage('/v1/mobile/trucks', query: {'cursor': ?cursor});
      for (final t in page.items) {
        out.add(SelectableTruck(
          id: t['id'] as String? ?? '',
          licensePlate: (t['licensePlate'] as String? ?? '').trim(),
          docType: (t['typeRaw'] as String? ?? '').trim(),
          model: t['model'] as String?,
        ));
      }
      cursor = page.nextCursor;
    } while (cursor != null && out.length < 1000);
    return out;
  }
}
