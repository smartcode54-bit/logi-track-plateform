import 'package:dio/dio.dart';

import 'api_client.dart';
import 'api_exception.dart';
import 'token_store.dart';

/// A rotated token pair from `POST /v1/auth/refresh`.
class RefreshedTokens {
  RefreshedTokens({required this.accessToken, required this.expiresIn, this.refreshToken});
  final String accessToken;
  final String? refreshToken;
  final int expiresIn;
}

/// Attaches the Go access token and keeps it fresh (spec mobile-go-core-loop R3, developer-spec §11.4).
///
/// It is a [QueuedInterceptor]: requests and errors pass it one at a time, so two requests that find the token
/// expired refresh it once (single flight) and the second one reuses the new token.
///
///   * proactive: a token missing or expiring within 60 s is refreshed before the request;
///   * reactive: `401 token_expired` (any reason, claims_changed included) refreshes and retries the request **once**;
///   * `401 session_revoked` / `invalid_token`, or a refresh refused with 401: the session is cleared and
///     [onSessionEnded] sends the driver to the login page;
///   * a network failure during a refresh is a [NetworkException]: the session stays, the driver retries later.
///     A network error never logs the driver out.
///
/// The auth routes themselves (`/v1/auth/*`) carry no bearer and are never retried here.
class AuthInterceptor extends QueuedInterceptor {
  AuthInterceptor({
    required this.tokens,
    required this.retryDio,
    required this.refresh,
    required this.onSessionEnded,
  });

  final TokenStore tokens;
  final Dio retryDio;
  final Future<RefreshedTokens> Function(String refreshToken) refresh;
  final void Function() onSessionEnded;

  static const _retriedKey = 'lt_retried';
  static const _proactiveMargin = Duration(seconds: 60);

  static bool _isAuthRoute(RequestOptions o) => o.path.startsWith('/v1/auth/');

  @override
  Future<void> onRequest(RequestOptions options, RequestInterceptorHandler handler) async {
    if (_isAuthRoute(options)) return handler.next(options);
    if (tokens.accessExpiringWithin(_proactiveMargin)) {
      try {
        await _refreshNow();
      } on ApiException catch (e) {
        return handler.reject(DioException(requestOptions: options, error: e, response: null));
      } on NetworkException catch (e) {
        return handler.reject(DioException(requestOptions: options, error: e, type: DioExceptionType.connectionError));
      }
    }
    final access = tokens.accessToken;
    if (access != null) options.headers['Authorization'] = 'Bearer $access';
    handler.next(options);
  }

  @override
  Future<void> onError(DioException err, ErrorInterceptorHandler handler) async {
    final res = err.response;
    if (res?.statusCode != 401 || _isAuthRoute(err.requestOptions)) return handler.next(err);
    final mapped = mapDioException(err);
    if (mapped is! ApiException) return handler.next(err);
    if (mapped.code != 'token_expired') {
      // session_revoked, invalid_token: this session is over.
      await _endSession();
      return handler.next(err);
    }
    if (err.requestOptions.extra[_retriedKey] == true) return handler.next(err);
    final sentWith = err.requestOptions.headers['Authorization'];
    try {
      // Another request may already have refreshed while this one waited in the queue.
      if (sentWith == null || sentWith == 'Bearer ${tokens.accessToken}') {
        await _refreshNow(force: true);
      }
      final retry = err.requestOptions
        ..extra[_retriedKey] = true
        ..headers['Authorization'] = 'Bearer ${tokens.accessToken}';
      final again = await retryDio.fetch<dynamic>(retry);
      return handler.resolve(again);
    } on ApiException catch (e) {
      return handler.reject(DioException(requestOptions: err.requestOptions, error: e, response: res));
    } on NetworkException catch (e) {
      return handler.reject(DioException(requestOptions: err.requestOptions, error: e, type: DioExceptionType.connectionError));
    } on DioException catch (e) {
      return handler.reject(e);
    }
  }

  /// Refreshes the access token. A refresh refused with 401 ends the session (and throws the [ApiException]); a
  /// network failure throws [NetworkException] and keeps the session.
  Future<void> _refreshNow({bool force = false}) async {
    if (!force && !tokens.accessExpiringWithin(_proactiveMargin)) return;
    final refreshToken = await tokens.readRefreshToken();
    if (refreshToken == null || refreshToken.isEmpty) {
      await _endSession();
      throw ApiException(status: 401, code: 'session_revoked');
    }
    try {
      final t = await refresh(refreshToken);
      await tokens.save(accessToken: t.accessToken, expiresIn: t.expiresIn, refreshToken: t.refreshToken);
    } on ApiException catch (e) {
      if (e.status == 401) await _endSession();
      rethrow;
    }
  }

  Future<void> _endSession() async {
    await tokens.clear();
    onSessionEnded();
  }
}
