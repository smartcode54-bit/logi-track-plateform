import 'package:easy_localization/easy_localization.dart';

import '../../core/api/api_exception.dart';

/// The localized text of a Go API failure (spec mobile-go-core-loop N1): chosen from `error.code` / `details.reason`
/// only, never the server's raw message. [overrides] maps a code or reason to a screen-specific key.
String apiErrorText(Object error, {Map<String, String> overrides = const {}}) {
  if (error is NetworkException) return 'api_error_network'.tr();
  if (error is! ApiException) return 'api_error_unknown'.tr(namedArgs: {'code': '$error'});
  final reason = error.reason;
  // details (tripNo, status, ...) fill the message's {placeholders}.
  final args = {for (final e in error.details.entries) if (e.value is! Map && e.value is! List) e.key: '${e.value}'};
  if (reason != null && overrides.containsKey(reason)) return overrides[reason]!.tr(namedArgs: args);
  if (overrides.containsKey(error.code)) return overrides[error.code]!.tr(namedArgs: args);
  if (error.code == 'locked') {
    final secs = (error.details['retryAfterSeconds'] as num?)?.toInt() ?? 60;
    return 'login_error_locked'.tr(namedArgs: {'minutes': '${(secs + 59) ~/ 60}'});
  }
  if (error.status == 429) {
    final s = error.details['retryAfterSeconds'] ?? '?';
    return 'api_error_rate_limited'.tr(namedArgs: {'seconds': '$s'});
  }
  if (error.status >= 500) return 'api_error_server'.tr();
  if (error.status == 422 && error.fieldViolations.isNotEmpty) {
    return 'api_error_validation'.tr(namedArgs: {'fields': error.fieldViolations.join(', ')});
  }
  return 'api_error_unknown'.tr(namedArgs: {'code': reason ?? error.code});
}
