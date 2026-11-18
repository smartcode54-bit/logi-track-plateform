import 'package:flutter/foundation.dart';

import '../../../core/api/api_client.dart';
import '../../../core/config/app_flavor.dart';

/// The gotest heartbeat (spec mobile-go-core-loop R9): `POST /v1/mobile/heartbeat` on the driver's Go token, at
/// most once per 45 s; flavor is the backend fleet (`dev`), the version `4.0.0-gotest.N` tells it apart. A failure is
/// only logged: the heartbeat must never interrupt the driver.
class GoHeartbeatService {
  GoHeartbeatService({required this.api, required this.installId, required this.appVersion, required this.buildNumber});

  final ApiClient api;
  final String installId;
  final String appVersion;
  final String buildNumber;

  static const _debounce = Duration(seconds: 45);
  DateTime? _last;

  Future<void> pulse() async {
    final last = _last;
    if (last != null && DateTime.now().difference(last) < _debounce) return;
    _last = DateTime.now();
    try {
      await api.postData('/v1/mobile/heartbeat', {
        'installId': installId,
        'platform': 'android',
        'appVersion': appVersion,
        'buildNumber': buildNumber,
        'flavor': AppFlavor.backendFleet,
      });
    } catch (e) {
      debugPrint('[heartbeat] $e');
    }
  }
}
