import 'dart:convert';

import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:logi_track_driver_app/core/api/api_client.dart';
import 'package:logi_track_driver_app/core/api/api_exception.dart';
import 'package:logi_track_driver_app/core/api/token_store.dart';
import 'package:logi_track_driver_app/go/features/auth/data/go_auth_repository.dart';
import 'package:logi_track_driver_app/go/features/auth/data/google_identity_source.dart';

import 'api_client_test.dart' show FakeAdapter, MemoryStorage, json;

/// A scripted Google account picker.
class FakeGoogle implements GoogleIdentitySource {
  FakeGoogle({this.identity, this.failure, this.available = true});
  final GoogleIdentity? identity;
  final GoogleSignInFailed? failure;
  @override
  final bool available;
  int picks = 0;
  int forgets = 0;

  @override
  Future<GoogleIdentity?> pick() async {
    picks++;
    if (failure != null) throw failure!;
    return identity;
  }

  @override
  Future<void> forget() async => forgets++;
}

const _me = {
  'data': {
    'id': 'u-1',
    'email': 'driver@gmail.com',
    'displayName': 'Driver',
    'driver': {'id': 'd-1', 'tenantId': 't-1', 'legacyDocId': 'L1'},
  }
};

const _tokens = {
  'data': {'accessToken': 'a1', 'refreshToken': 'r1', 'expiresIn': 900}
};

/// Google sign-in of the gotest app (developer-spec §11.4, Appendix C §C.4.10).
void main() {
  late FakeAdapter server;
  late TokenStore tokens;
  final bodies = <String, Map<String, dynamic>>{};

  GoAuthRepository repo(FakeGoogle google, Future<ResponseBody> Function(RequestOptions o) handler) {
    server = FakeAdapter((o) {
      if (o.data != null) bodies[o.path] = Map<String, dynamic>.from(o.data is String ? jsonDecode(o.data) : o.data);
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
      google: google,
    );
  }

  setUp(() {
    tokens = TokenStore(storage: MemoryStorage());
    bodies.clear();
  });

  test('posts the ID token without a nonce, stores the session and resolves the driver', () async {
    final google = FakeGoogle(identity: const GoogleIdentity(idToken: 'id-tok', email: 'driver@gmail.com'));
    final auth = repo(google, (o) async => o.path == '/v1/auth/google' ? json(200, _tokens) : json(200, _me));

    final me = await auth.loginWithGoogle();

    expect(me?.driver, isNotNull);
    expect(bodies['/v1/auth/google'], {
      'idToken': 'id-tok',
      'platform': 'android',
      'installId': 'i-1',
      'appVersion': '4.0.0-gotest.1',
    });
    expect(await tokens.readRefreshToken(), 'r1');
    expect(google.forgets, 0);
  });

  test('a cancelled picker makes no request and returns null', () async {
    final google = FakeGoogle();
    final auth = repo(google, (o) async => json(500, {}));

    expect(await auth.loginWithGoogle(), isNull);
    expect(server.calls, isEmpty);
  });

  test('403 no_account is surfaced and the picked account is forgotten', () async {
    final google = FakeGoogle(identity: const GoogleIdentity(idToken: 'id-tok', email: 'other@gmail.com'));
    final auth = repo(
        google,
        (o) async => json(403, {
              'error': {'code': 'no_account', 'message': 'x', 'details': {}}
            }));

    final err = await auth.loginWithGoogle().then<Object?>((_) => null, onError: (Object e) => e);

    expect(err, isA<ApiException>());
    expect((err as ApiException).code, 'no_account');
    expect(google.forgets, 1);
    expect(await tokens.readRefreshToken(), isNull);
  });

  test('a temporary password asks for a new one, carrying the Google address', () async {
    final google = FakeGoogle(identity: const GoogleIdentity(idToken: 'id-tok', email: 'driver@gmail.com'));
    final auth = repo(
        google,
        (o) async => json(403, {
              'error': {
                'code': 'password_change_required',
                'message': 'x',
                'details': {'passwordChangeTicket': 'tk'}
              }
            }));

    final err = await auth.loginWithGoogle().then<Object?>((_) => null, onError: (Object e) => e);

    expect(err, isA<PasswordChangeRequired>());
    expect((err as PasswordChangeRequired).ticket, 'tk');
    expect(err.email, 'driver@gmail.com');
  });

  test('a phone-side failure is thrown before any request', () async {
    final google = FakeGoogle(failure: GoogleSignInFailed('providerConfigurationError'));
    final auth = repo(google, (o) async => json(500, {}));

    await expectLater(auth.loginWithGoogle(), throwsA(isA<GoogleSignInFailed>()));
    expect(server.calls, isEmpty);
  });

  test('a build without a client id has Google off', () async {
    final auth = repo(FakeGoogle(available: false), (o) async => json(500, {}));

    expect(auth.googleAvailable, isFalse);
    await expectLater(auth.loginWithGoogle(), throwsA(isA<GoogleSignInFailed>()));
  });
}
