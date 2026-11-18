import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:logi_track_driver_app/core/api/api_client.dart';
import 'package:logi_track_driver_app/core/api/api_exception.dart';
import 'package:logi_track_driver_app/core/api/token_store.dart';

/// A fake server for dio: answers by path, counts calls, can fail with no answer.
class FakeAdapter implements HttpClientAdapter {
  FakeAdapter(this.handler);
  final Future<ResponseBody> Function(RequestOptions o) handler;
  final calls = <String>[];

  @override
  Future<ResponseBody> fetch(RequestOptions o, Stream<Uint8List>? body, Future<void>? cancel) {
    calls.add('${o.method} ${o.path} ${o.headers['Authorization'] ?? ''}');
    return handler(o);
  }

  @override
  void close({bool force = false}) {}
}

ResponseBody json(int status, Object body) => ResponseBody.fromString(jsonEncode(body), status,
    headers: {
      Headers.contentTypeHeader: [Headers.jsonContentType],
    });

/// An in-memory secure storage (the platform channel is not there in unit tests).
class MemoryStorage extends FlutterSecureStorage {
  MemoryStorage() : super();
  final Map<String, String> m = {};

  @override
  Future<void> write({required String key, required String? value, AppleOptions? iOptions, AndroidOptions? aOptions,
      LinuxOptions? lOptions, WebOptions? webOptions, AppleOptions? mOptions, WindowsOptions? wOptions}) async {
    if (value == null) {
      m.remove(key);
    } else {
      m[key] = value;
    }
  }

  @override
  Future<String?> read({required String key, AppleOptions? iOptions, AndroidOptions? aOptions, LinuxOptions? lOptions,
          WebOptions? webOptions, AppleOptions? mOptions, WindowsOptions? wOptions}) async =>
      m[key];

  @override
  Future<void> delete({required String key, AppleOptions? iOptions, AndroidOptions? aOptions, LinuxOptions? lOptions,
      WebOptions? webOptions, AppleOptions? mOptions, WindowsOptions? wOptions}) async {
    m.remove(key);
  }
}

/// The Go API client (spec mobile-go-core-loop F1, R2, R3, N3).
void main() {
  late FakeAdapter server;
  late TokenStore tokens;
  late int ended;
  late int refreshes;

  ApiClient client(Future<ResponseBody> Function(RequestOptions o) handler) {
    server = FakeAdapter(handler);
    final api = Dio();
    final plain = Dio();
    api.httpClientAdapter = server;
    plain.httpClientAdapter = server;
    return ApiClient(
      baseUrl: 'https://api.test',
      tokens: tokens,
      headers: AppHeaders(
          appVersion: '4.0.0-gotest.1', buildNumber: '1', flavor: 'gotest', installId: 'i-1', languageCode: () => 'th'),
      onSessionEnded: () => ended++,
      dio: api,
      refreshDio: plain,
    );
  }

  setUp(() async {
    tokens = TokenStore(storage: MemoryStorage());
    ended = 0;
    refreshes = 0;
  });

  Future<ResponseBody> refreshOk() async {
    refreshes++;
    return json(200, {
      'data': {'accessToken': 'new$refreshes', 'refreshToken': 'r$refreshes', 'expiresIn': 900}
    });
  }

  test('sends the app headers and maps the error envelope', () async {
    await tokens.save(accessToken: 'a', expiresIn: 900, refreshToken: 'r');
    late RequestOptions seen;
    final api = client((o) async {
      seen = o;
      return json(409, {
        'error': {'code': 'failed_precondition', 'message': 'x', 'details': {'reason': 'queue_blocked'}}
      });
    });
    final err = await api.getData('/v1/mobile/tasks').then<Object?>((_) => null, onError: (Object e) => e);
    expect(err, isA<ApiException>());
    expect((err as ApiException).reason, 'queue_blocked');
    expect(seen.headers['X-App-Flavor'], 'gotest');
    expect(seen.headers['X-Install-Id'], 'i-1');
    expect(seen.headers['Accept-Language'], 'th');
    expect(seen.headers['Authorization'], 'Bearer a');
    expect(seen.headers['X-Request-Id'], isNotEmpty);
  });

  test('token_expired refreshes once for two concurrent requests and retries each once', () async {
    await tokens.save(accessToken: 'old', expiresIn: 900, refreshToken: 'r0');
    final api = client((o) async {
      if (o.path == '/v1/auth/refresh') return refreshOk();
      if (o.headers['Authorization'] == 'Bearer old') {
        return json(401, {
          'error': {'code': 'token_expired', 'details': {'reason': 'expired'}}
        });
      }
      return json(200, {'data': <String, dynamic>{'ok': true}});
    });
    final results = await Future.wait([api.getData('/v1/mobile/tasks'), api.getData('/v1/mobile/me')]);
    expect(results.every((r) => r?['ok'] == true), isTrue);
    expect(refreshes, 1, reason: 'single-flight refresh');
    expect(ended, 0);
  });

  test('a refresh refused with 401 ends the session', () async {
    await tokens.save(accessToken: 'old', expiresIn: 900, refreshToken: 'r0');
    final api = client((o) async {
      if (o.path == '/v1/auth/refresh') {
        return json(401, {
          'error': {'code': 'session_revoked'}
        });
      }
      return json(401, {
        'error': {'code': 'token_expired', 'details': {'reason': 'expired'}}
      });
    });
    await expectLater(api.getData('/v1/mobile/tasks'), throwsA(isA<ApiException>()));
    expect(ended, 1);
    expect(await tokens.readRefreshToken(), isNull);
  });

  test('a network failure never logs out', () async {
    await tokens.save(accessToken: 'old', expiresIn: 0, refreshToken: 'r0'); // expired: the refresh runs first
    final api = client((o) async => throw DioException.connectionError(
        requestOptions: o, reason: 'offline'));
    await expectLater(api.getData('/v1/mobile/tasks'), throwsA(isA<NetworkException>()));
    expect(ended, 0);
    expect(await tokens.readRefreshToken(), 'r0');
  });

  test('session_revoked on a request ends the session', () async {
    await tokens.save(accessToken: 'a', expiresIn: 900, refreshToken: 'r0');
    final api = client((o) async => json(401, {
          'error': {'code': 'session_revoked'}
        }));
    await expectLater(api.getData('/v1/mobile/tasks'), throwsA(isA<ApiException>()));
    expect(ended, 1);
  });

  test('a write carries its Idempotency-Key', () async {
    await tokens.save(accessToken: 'a', expiresIn: 900, refreshToken: 'r0');
    late RequestOptions seen;
    final api = client((o) async {
      seen = o;
      return json(201, {'data': <String, dynamic>{}});
    });
    await api.postData('/v1/mobile/trips', {'tripNo': 'X'}, idempotencyKey: 'op-1');
    expect(seen.headers['Idempotency-Key'], 'op-1');
  });
}
