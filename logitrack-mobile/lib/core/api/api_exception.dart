/// Errors of the Go API client (T55, spec mobile-go-core-loop R2).
///
/// [ApiException] is an answer of the server (the error envelope `{error:{code,message,details,requestId}}`);
/// [NetworkException] is no answer at all (offline, timeout, DNS). The two are kept apart on purpose: a network error
/// is never an auth error and never logs the driver out.
library;

/// An error answer of the Go API.
class ApiException implements Exception {
  ApiException({
    required this.status,
    required this.code,
    this.message = '',
    this.details = const {},
    this.requestId,
  });

  /// HTTP status (401, 409, 422, ...).
  final int status;

  /// `error.code` (`invalid_token`, `failed_precondition`, `duplicate_trip_id`, ...).
  final String code;
  final String message;
  final Map<String, dynamic> details;
  final String? requestId;

  /// `details.reason` when the server gave one (`queue_blocked`, `task_not_open`, `token_expired`, ...).
  String? get reason => details['reason'] as String?;

  /// The field violations of a 422 (`details.fields[{field, reason}]`), as `field reason` strings.
  List<String> get fieldViolations {
    final raw = details['fields'];
    if (raw is! List) return const [];
    return [
      for (final f in raw)
        if (f is Map) '${f['field'] ?? ''} ${f['reason'] ?? ''}'.trim(),
    ];
  }

  /// Whether a field violation names [field] (optionally with [reason]).
  bool hasField(String field, [String? reason]) {
    final raw = details['fields'];
    if (raw is! List) return false;
    return raw.any((f) => f is Map && f['field'] == field && (reason == null || f['reason'] == reason));
  }

  @override
  String toString() => 'ApiException($status $code${reason == null ? '' : ' $reason'})';
}

/// No answer from the server: offline, timeout, connection refused, TLS failure.
class NetworkException implements Exception {
  NetworkException(this.cause);
  final Object cause;

  @override
  String toString() => 'NetworkException($cause)';
}
