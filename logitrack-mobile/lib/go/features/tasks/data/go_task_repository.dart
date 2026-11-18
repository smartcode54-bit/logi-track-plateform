import '../../../../core/api/api_client.dart';
import '../domain/driver_task.dart';

/// The driver's queue from the Go API (spec mobile-go-core-loop R10, R19).
class GoTaskRepository {
  GoTaskRepository(this.api);
  final ApiClient api;

  /// `GET /v1/mobile/tasks`: the open queue in the server's order (ADR 0028), all of it on one page.
  Future<List<DriverTask>> openQueue() async {
    final page = await api.getPage('/v1/mobile/tasks');
    return page.items.map(DriverTask.fromJson).toList();
  }
}
