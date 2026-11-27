import 'dart:convert';

import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:logi_track_driver_app/core/api/api_client.dart';
import 'package:logi_track_driver_app/core/api/api_exception.dart';
import 'package:logi_track_driver_app/core/api/token_store.dart';
import 'package:logi_track_driver_app/go/features/auth/data/firebase_session_source.dart';
import 'package:logi_track_driver_app/go/features/auth/data/go_auth_repository.dart';

import 'api_client_test.dart' show FakeAdapter, MemoryStorage, json;

/// A scripted 3.x Firebase session. [session] is handed out until [end] is called, like FlutterFire after sign-out.
class FakeLegacy implements LegacyFirebaseSession {
  FakeLegacy({this.session, this.offline = false});
  LegacySession? session;
  final bool offline;
  int reads = 0;
  int ends = 0;

  @override
  Future<LegacySession?> read() async {
    reads++;
    if (offline) throw NetworkException('offline');
    return session;
  }

  @override
  Future<void> end() async {
    ends++;
    session = null;
  }
}

const _driverMe = {
  'data': {
    'id': 'u-1',
    'email': 'driver@gmail.com',
    'displayName': 'Driver',
    'driver': {'id': 'd-1', 'tenantId': 't-1', 'legacyDocId': 'L1'},
  }
};
const _staffMe = {
  'data': {'id': 'u-2', 'email': 'ops@x.test', 'displayName': 'Ops'}
};
const _tokens = {
  'data': {'accessToken': 'a1', 'refreshToken': 'r1', 'expiresIn': 900}
};

Map<String, Object> _err(String code, [Map<String, Object> details = const {}]) => {
      'error': {'code': code, 'message': 'x', 'details': details}
    };

/// The first-launch session exchange of the gotest/4.x app (spec mobile-auth-exchange R11-R16, AC8).
void main() {
  late FakeAdapter server;
  late TokenStore tokens;
  Map<String, dynamic>? exchangeBody;

  GoAuthRepository repo(FakeLegacy legacy, Future<ResponseBody> Function(RequestOptions o) handler) {
    server = FakeAdapter((o) {
      if (o.path == '/v1/auth/exchange') {
        exchangeBody = Map<String, dynamic>.from(o.data is String ? jsonDecode(o.data) : o.data);
      }
      return handler(o);
    });
    final api = Dio()..httpClientAdapter = server;
    final plain = Dio()..httpClientAdapter = server;
    return GoAuthRepository(
      api: ApiClient(
        baseUrl: 'https://api.test',
        tokens: tokens,
        headers: AppHeaders(
            appVersion: '4.0.0-gotest.1', buildNumber: '1', flavor: 'gotest', installId: 'i-1', languageCode: () => 'th'),
        onSessionEnded: () {},
        dio: api,
        refreshDio: plain,
      ),
      tokens: tokens,
      installId: 'i-1',
      appVersion: '4.0.0-gotest.1',
      legacy: legacy,
    );
  }

  setUp(() {
    tokens = TokenStore(storage: MemoryStorage());
    exchangeBody = null;
  });

  const signedIn = LegacySession(idToken: 'fb-id-token', email: 'driver@gmail.com');

  test('200: exchanges the Firebase token, stores the Go session, ends Firebase, resolves the driver', () async {
    final legacy = FakeLegacy(session: signedIn);
    final auth = repo(legacy, (o) async => o.path == '/v1/auth/exchange' ? json(200, _tokens) : json(200, _driverMe));

    final me = await auth.resume();

    expect(me?.driver, isNotNull);
    expect(exchangeBody, {
      'idToken': 'fb-id-token',
      'platform': 'android',
      'installId': 'i-1',
      'appVersion': '4.0.0-gotest.1',
    });
    expect(await tokens.readRefreshToken(), 'r1');
    expect(legacy.ends, 1);
  });

  test('R14: the exchange runs once; the next launch resumes the Go session', () async {
    final legacy = FakeLegacy(session: signedIn);
    final auth = repo(legacy, (o) async => o.path == '/v1/auth/exchange' ? json(200, _tokens) : json(200, _driverMe));
    await auth.resume();
    final callsAfterFirst = server.calls.where((c) => c.contains('/v1/auth/exchange')).length;

    final me = await auth.resume();

    expect(me?.driver, isNotNull);
    expect(legacy.reads, 1, reason: 'a stored Go refresh token never reads the Firebase session (R16)');
    expect(server.calls.where((c) => c.contains('/v1/auth/exchange')).length, callsAfterFirst);
  });

  test('R16: with a Go refresh token the Firebase session is never read', () async {
    await tokens.save(accessToken: 'a0', expiresIn: 900, refreshToken: 'r0');
    final legacy = FakeLegacy(session: signedIn);
    final auth = repo(legacy, (o) async => json(200, _driverMe));

    await auth.resume();

    expect(legacy.reads, 0);
    expect(exchangeBody, isNull);
  });

  test('no Firebase user: login form, no request', () async {
    final legacy = FakeLegacy();
    final auth = repo(legacy, (o) async => json(500, {}));

    expect(await auth.resume(), isNull);
    expect(server.calls, isEmpty);
  });

  test('403 password_change_required: asks for a new password with the Firebase address, ends Firebase', () async {
    final legacy = FakeLegacy(session: signedIn);
    final auth = repo(legacy, (o) async => json(403, _err('password_change_required', {'passwordChangeTicket': 'tk'})));

    final err = await auth.resume().then<Object?>((_) => null, onError: (Object e) => e);

    expect(err, isA<PasswordChangeRequired>());
    expect((err as PasswordChangeRequired).ticket, 'tk');
    expect(err.email, 'driver@gmail.com');
    expect(legacy.ends, 1);
  });

  for (final c in [
    (401, 'invalid_token', 'login_notice_exchange_relogin'),
    (403, 'no_account', 'login_notice_exchange_relogin'),
    (403, 'account_disabled', 'login_error_account_disabled'),
  ]) {
    test('${c.$1} ${c.$2}: refused with ${c.$3}, Firebase ended, no Go session', () async {
      final legacy = FakeLegacy(session: signedIn);
      final auth = repo(legacy, (o) async => json(c.$1, _err(c.$2)));

      final err = await auth.resume().then<Object?>((_) => null, onError: (Object e) => e);

      expect(err, isA<ExchangeRefused>());
      expect((err as ExchangeRefused).messageKey, c.$3);
      expect(legacy.ends, 1);
      expect(await tokens.readRefreshToken(), isNull);
    });
  }

  test('404 (bridge mode not mobile yet): login form, Firebase session kept for a later launch', () async {
    final legacy = FakeLegacy(session: signedIn);
    final auth = repo(legacy, (o) async => json(404, _err('not_found')));

    expect(await auth.resume(), isNull);
    expect(legacy.ends, 0);
    expect(legacy.session, isNotNull);
  });

  for (final status in [503, 429]) {
    test('$status: treated as offline, Firebase session kept', () async {
      final legacy = FakeLegacy(session: signedIn);
      final auth = repo(legacy, (o) async => json(status, _err(status == 503 ? 'unavailable' : 'resource_exhausted')));

      await expectLater(auth.resume(), throwsA(isA<NetworkException>()));
      expect(legacy.ends, 0);
    });
  }

  test('offline while refreshing the Firebase token: NetworkException, nothing sent, session kept', () async {
    final legacy = FakeLegacy(session: signedIn, offline: true);
    final auth = repo(legacy, (o) async => json(500, {}));

    await expectLater(auth.resume(), throwsA(isA<NetworkException>()));
    expect(server.calls, isEmpty);
    expect(legacy.ends, 0);
  });

  test('a user without a driver: Firebase ended, Go session logged out, driver_profile_required', () async {
    final legacy = FakeLegacy(session: signedIn);
    final auth = repo(legacy, (o) async {
      if (o.path == '/v1/auth/exchange') return json(200, _tokens);
      if (o.path == '/v1/mobile/me') return json(200, _staffMe);
      return json(200, {'data': null});
    });

    final err = await auth.resume().then<Object?>((_) => null, onError: (Object e) => e);

    expect(err, isA<ApiException>());
    expect((err as ApiException).code, 'driver_profile_required');
    expect(legacy.ends, 1);
    expect(await tokens.readRefreshToken(), isNull);
  });
}
