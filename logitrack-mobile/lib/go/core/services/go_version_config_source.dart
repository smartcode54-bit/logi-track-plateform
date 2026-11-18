import '../../../core/api/api_client.dart';
import '../../../core/config/app_flavor.dart';
import '../../../core/services/mobile_app_version_service.dart';

/// The floor of the gotest app (spec mobile-go-core-loop R8): `GET /v1/mobile/settings?flavor=dev` (anonymous) in
/// place of Firestore `settings/mobile_app`; MobileAppVersionService keeps its cache and fail-open rules (ADR 0007).
VersionConfigSource goVersionConfigSource(ApiClient api) {
  return () async {
    final d = await api.getPublicData('/v1/mobile/settings', query: {'flavor': AppFlavor.backendFleet});
    final min = (d?['minAllowedVersion'] as String?)?.trim() ?? '';
    if (min.isEmpty) return null;
    return VersionConfig(
      minAllowedVersion: min,
      apkDownloadUrl: (d?['apkDownloadUrl'] as String?)?.trim() ?? '',
    );
  };
}
