import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart';

import 'api_exception.dart';
import 'auth_interceptor.dart';
import 'op_id.dart';
import 'token_store.dart';

/// The app facts every request carries (spec mobile-go-core-loop R2, developer-spec §11.2).
class AppHeaders {
  AppHeaders({
    required this.appVersion,
    required this.buildNumber,
    required this.flavor,
    required this.installId,
    required this.languageCode,
    this.platform = 'android',
  });

  final String appVersion;
  final String buildNumber;
  final String flavor;
  final String installId;
  final String platform;

  /// `en` or `th`, read on every request so a language switch applies at once.
  final String Function() languageCode;

  Map<String, String> toMap() => {
        'X-App-Version': appVersion,
        'X-App-Build': buildNumber,
        'X-App-Flavor': flavor,
        'X-Install-Id': installId,
        'X-Platform': platform,
        'Accept-Language': languageCode(),
        'X-Request-Id': newRequestId(),
      };
}

/// A page of a list route: `{data: [...], nextCursor}`.
class ApiPage {
  ApiPage(this.items, this.nextCursor);
  final List<Map<String, dynamic>> items;
  final String? nextCursor;
}

/// The one Go API client of the gotest app (T55). JSON in and out, camelCase; every failure is an [ApiException]
/// (the server answered) or a [NetworkException] (it did not). The access token is attached and refreshed by
/// [AuthInterceptor]; a write passes its `Idempotency-Key`.
class ApiClient {
  ApiClient({
    required String baseUrl,
    required this.tokens,
    required this.headers,
    required void Function() onSessionEnded,
    Dio? dio,
    Dio? refreshDio,
  }) : dio = dio ?? Dio() {
    final options = BaseOptions(
      baseUrl: baseUrl,
      connectTimeout: const Duration(seconds: 10),
      receiveTimeout: const Duration(seconds: 30),
      sendTimeout: const Duration(seconds: 30),
      contentType: Headers.jsonContentType,
      responseType: ResponseType.json,
    );
    this.dio.options = options;
    final refresher = refreshDio ?? Dio(options);
    _plain = refresher;
    this.dio.interceptors.add(InterceptorsWrapper(onRequest: (o, h) {
      o.headers.addAll(headers.toMap());
      h.next(o);
    }));
    this.dio.interceptors.add(AuthInterceptor(
          tokens: tokens,
          // A plain Dio: a retry from inside onError must not pass this QueuedInterceptor again (its error queue is
          // busy with the current error, so a failing retry would wait on itself).
          retryDio: refresher,
          refresh: (refreshToken) => _refresh(refresher, refreshToken),
          onSessionEnded: onSessionEnded,
        ));
    if (kDebugMode) {
      this.dio.interceptors.add(LogInterceptor(
            requestHeader: false,
            responseHeader: false,
            requestBody: false,
            responseBody: false,
            logPrint: (o) => debugPrint('[api] $o'),
          ));
    }
  }

  final Dio dio;
  final TokenStore tokens;
  final AppHeaders headers;

  /// A Dio without the auth interceptor: the refresh, the retries and the anonymous routes.
  late final Dio _plain;

  /// `GET path` of an anonymous route (the version gate, `GET /v1/mobile/settings`) → the `data` object. No bearer
  /// and no refresh: it works before login and never ends a session.
  Future<Map<String, dynamic>?> getPublicData(String path, {Map<String, dynamic>? query}) async {
    final body = await _send(() => _plain.get<dynamic>(path,
        queryParameters: query, options: Options(headers: headers.toMap())));
    final data = body is Map ? body['data'] : null;
    return data is Map<String, dynamic> ? data : null;
  }

  /// `GET path` → the `data` object.
  Future<Map<String, dynamic>?> getData(String path, {Map<String, dynamic>? query}) async {
    final body = await _send(() => dio.get<dynamic>(path, queryParameters: query));
    final data = body is Map ? body['data'] : null;
    return data is Map<String, dynamic> ? data : null;
  }

  /// `GET path` → a page.
  Future<ApiPage> getPage(String path, {Map<String, dynamic>? query}) async {
    final body = await _send(() => dio.get<dynamic>(path, queryParameters: query));
    final raw = body is Map ? body['data'] : null;
    final items = <Map<String, dynamic>>[
      if (raw is List)
        for (final it in raw)
          if (it is Map<String, dynamic>) it,
    ];
    final next = body is Map ? body['nextCursor'] as String? : null;
    return ApiPage(items, (next == null || next.isEmpty) ? null : next);
  }

  /// `POST path` with [body] → the `data` object. [idempotencyKey] marks a replayable write (✱ routes).
  Future<Map<String, dynamic>?> postData(String path, Object? body, {String? idempotencyKey}) async {
    final res = await _send(() => dio.post<dynamic>(
          path,
          data: body,
          options: Options(headers: {'Idempotency-Key': ?idempotencyKey}),
        ));
    final data = res is Map ? res['data'] : null;
    return data is Map<String, dynamic> ? data : null;
  }

  Future<dynamic> _send(Future<Response<dynamic>> Function() call) async {
    try {
      final res = await call();
      return res.data;
    } on DioException catch (e) {
      throw mapDioException(e);
    }
  }

  static Future<RefreshedTokens> _refresh(Dio refresher, String refreshToken) async {
    try {
      final res = await refresher.post<dynamic>('/v1/auth/refresh', data: {'refreshToken': refreshToken});
      final data = (res.data as Map)['data'] as Map<String, dynamic>;
      return RefreshedTokens(
        accessToken: data['accessToken'] as String,
        refreshToken: data['refreshToken'] as String?,
        expiresIn: (data['expiresIn'] as num).toInt(),
      );
    } on DioException catch (e) {
      throw mapDioException(e);
    }
  }
}

/// Maps a dio failure to [ApiException] (an error envelope, any status) or [NetworkException] (no answer).
Object mapDioException(DioException e) {
  final err = e.error;
  if (err is ApiException || err is NetworkException) return err!;
  final res = e.response;
  if (res == null) return NetworkException(e.error ?? e.type);
  final body = res.data;
  final env = body is Map ? body['error'] : null;
  if (env is Map) {
    final details = env['details'];
    return ApiException(
      status: res.statusCode ?? 0,
      code: env['code'] as String? ?? 'unknown',
      message: env['message'] as String? ?? '',
      details: details is Map<String, dynamic> ? details : const {},
      requestId: env['requestId'] as String?,
    );
  }
  return ApiException(status: res.statusCode ?? 0, code: 'http_${res.statusCode ?? 0}');
}
