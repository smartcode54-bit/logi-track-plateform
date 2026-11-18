import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/material.dart';
import 'package:package_info_plus/package_info_plus.dart';

import '../../core/api/api_client.dart';
import '../../core/api/token_store.dart';
import '../../core/api/upload_client.dart';
import '../../core/config/app_flavor.dart';
import '../../core/services/mobile_app_version_service.dart';
import '../../core/services/mobile_install_id_service.dart';
import '../core/services/go_heartbeat_service.dart';
import '../core/services/go_version_config_source.dart';
import '../features/auth/data/go_auth_repository.dart';
import '../features/check_in/data/go_truck_repository.dart';
import '../features/tasks/data/go_task_repository.dart';
import '../features/trips/data/go_trip_repository.dart';

/// The composition root of the gotest app (spec mobile-go-core-loop R1): one API client on
/// `--dart-define=API_BASE_URL`, the session store and the repositories. Nothing here touches Firebase.
class GoServices {
  GoServices._({
    required this.api,
    required this.tokens,
    required this.uploads,
    required this.auth,
    required this.tasks,
    required this.trips,
    required this.trucks,
    required this.heartbeat,
    required this.installId,
    required this.versionLabel,
  });

  final ApiClient api;
  final TokenStore tokens;
  final UploadClient uploads;
  final GoAuthRepository auth;
  final GoTaskRepository tasks;
  final GoTripRepository trips;
  final GoTruckRepository trucks;
  final GoHeartbeatService heartbeat;
  final String installId;
  final String versionLabel;

  /// [onSessionEnded] is called when the server ends the session (revoked, refresh refused).
  static Future<GoServices> create({
    required void Function() onSessionEnded,
    required String Function() languageCode,
  }) async {
    final info = await PackageInfo.fromPlatform();
    // Firebase Installations is not initialised in gotest: the id falls back to the stored random id.
    final installId = await getOrCreateMobileInstallId();
    final tokens = TokenStore();
    final api = ApiClient(
      baseUrl: AppFlavor.apiBaseUrl,
      tokens: tokens,
      headers: AppHeaders(
        appVersion: info.version,
        buildNumber: info.buildNumber,
        flavor: AppFlavor.name,
        installId: installId,
        languageCode: languageCode,
      ),
      onSessionEnded: onSessionEnded,
    );
    MobileAppVersionService.instance.remoteSource = goVersionConfigSource(api);
    return GoServices._(
      api: api,
      tokens: tokens,
      uploads: UploadClient(api),
      auth: GoAuthRepository(api: api, tokens: tokens, installId: installId, appVersion: info.version),
      tasks: GoTaskRepository(api),
      trips: GoTripRepository(api),
      trucks: GoTruckRepository(api),
      heartbeat: GoHeartbeatService(
          api: api, installId: installId, appVersion: info.version, buildNumber: info.buildNumber),
      installId: installId,
      versionLabel: 'v${info.version} (${info.buildNumber}) · ${'gotest_badge'.tr()}',
    );
  }

  /// The forced-update gate of ADR 0007 on the Go settings (R8).
  Future<bool> checkVersion(BuildContext context) => MobileAppVersionService.instance.ensureAllowedToRun(context);
}
