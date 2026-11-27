import '../../../../core/api/api_client.dart';
import '../../../../core/api/api_exception.dart';
import '../../../../core/api/token_store.dart';
import '../domain/mobile_me.dart';
import 'firebase_session_source.dart';
import 'google_identity_source.dart';

/// The 3.x Firebase session could not be carried into a Go session, and retrying is pointless (spec
/// mobile-auth-exchange R13): the login page shows [messageKey].
class ExchangeRefused implements Exception {
  ExchangeRefused(this.messageKey);
  final String messageKey;

  @override
  String toString() => 'ExchangeRefused($messageKey)';
}

/// A login that needs a new password first (`403 password_change_required`, R79). [email] is set when the sign-in
/// was Google's (the form had no address): the change-password step logs in again with it.
class PasswordChangeRequired implements Exception {
  PasswordChangeRequired(this.ticket, {this.email});
  final String ticket;
  final String? email;
}

/// The session against the Go API (spec mobile-go-core-loop R3-R7): login (password or Google), the silent start,
/// the forced password change, `GET /v1/mobile/me` and logout.
class GoAuthRepository {
  GoAuthRepository({
    required this.api,
    required this.tokens,
    required this.installId,
    required this.appVersion,
    this.google,
    this.legacy,
  });

  final ApiClient api;
  final TokenStore tokens;
  final String installId;
  final String appVersion;

  /// Null or not [GoogleIdentitySource.available] = Google sign-in is off in this build.
  final GoogleIdentitySource? google;

  bool get googleAvailable => google?.available ?? false;

  /// The Firebase session of APK 3.x, traded once for a Go session (spec mobile-auth-exchange); null = no exchange.
  final LegacyFirebaseSession? legacy;

  /// `POST /v1/auth/login` then `GET /v1/mobile/me`. Throws [PasswordChangeRequired] for a temporary password,
  /// [ApiException] / [NetworkException] otherwise; an account without a driver is logged out again and answered as
  /// `403 driver_profile_required`.
  Future<MobileMe> login(String email, String password) async {
    await _openSession('/v1/auth/login', {'email': email.trim(), 'password': password});
    return _driverOrLogout();
  }

  /// Google sign-in (developer-spec §11.4, Appendix C §C.4.10): the account picker, then
  /// `POST /v1/auth/google {idToken, platform, installId, appVersion}` without a nonce, then the same path as [login].
  /// Null when the driver cancels the picker (no request is made). Go answers `403 no_account` when no active user is
  /// linked to that Google account and none has its (Gmail or Workspace) address: there is no self-signup. Throws
  /// [GoogleSignInFailed] when the phone could not get a token.
  Future<MobileMe?> loginWithGoogle() async {
    final source = google;
    if (source == null || !source.available) throw GoogleSignInFailed('not_configured');
    final picked = await source.pick();
    if (picked == null) return null;
    try {
      await _openSession('/v1/auth/google', {'idToken': picked.idToken}, email: picked.email);
    } catch (_) {
      // Let the next attempt offer the account picker again (another account may be the linked one).
      await source.forget();
      rethrow;
    }
    return _driverOrLogout();
  }

  /// `POST /v1/auth/exchange {idToken, platform, installId, appVersion}` with the 3.x Firebase ID token (R11-R14).
  /// Outcomes (table R13): success, a temporary password and the refusals end the Firebase session, so the exchange
  /// runs once; a closed route (404, the server is not in bridge mode `mobile` yet) and an unreachable or failing
  /// server keep it for a later launch.
  Future<MobileMe?> _exchangeLegacy() async {
    final source = legacy;
    if (source == null) return null;
    final session = await source.read(); // NetworkException: offline, the Firebase session stays
    if (session == null) {
      await source.end();
      return null;
    }
    try {
      await _openSession('/v1/auth/exchange', {'idToken': session.idToken}, email: session.email);
    } on PasswordChangeRequired {
      await source.end();
      rethrow;
    } on ApiException catch (e) {
      if (e.status == 404) return null; // not enabled on this server yet: login form, exchange again later
      if (e.status == 429 || e.status >= 500) throw NetworkException(e); // retry later, like offline
      await source.end();
      throw ExchangeRefused(
          e.code == 'account_disabled' ? 'login_error_account_disabled' : 'login_notice_exchange_relogin');
    }
    await source.end();
    return _driverOrLogout();
  }

  /// Posts a sign-in [body] (plus platform, install id and app version) and stores the tokens of the answer.
  Future<void> _openSession(String path, Map<String, Object?> body, {String? email}) async {
    try {
      final d = await api.postData(path, {
        ...body,
        'platform': 'android',
        'installId': installId,
        'appVersion': appVersion,
      });
      if (d == null) throw ApiException(status: 0, code: 'login_empty');
      await tokens.save(
        accessToken: d['accessToken'] as String,
        expiresIn: (d['expiresIn'] as num).toInt(),
        refreshToken: d['refreshToken'] as String?,
      );
    } on ApiException catch (e) {
      final ticket = e.details['passwordChangeTicket'];
      if (e.code == 'password_change_required' && ticket is String) {
        throw PasswordChangeRequired(ticket, email: email);
      }
      rethrow;
    }
  }

  /// The silent start of a cold launch (R4): with a stored refresh token the interceptor refreshes on the first call.
  /// Without one, the Firebase session APK 3.x left on the phone is exchanged for a Go session (spec
  /// mobile-auth-exchange R11). Returns null when there is no session to resume; a network failure throws
  /// [NetworkException] (the token, or the Firebase session, stays).
  Future<MobileMe?> resume() async {
    final refresh = await tokens.readRefreshToken();
    if (refresh == null || refresh.isEmpty) return _exchangeLegacy();
    try {
      return await _driverOrLogout();
    } on ApiException catch (e) {
      if (e.status == 401) return null;
      rethrow;
    }
  }

  /// `POST /v1/auth/password/change {passwordChangeTicket, newPassword}`, then a login with the new password (R5).
  Future<MobileMe> changePasswordAndLogin(String email, String ticket, String newPassword) async {
    await api.postData('/v1/auth/password/change', {'passwordChangeTicket': ticket, 'newPassword': newPassword});
    return login(email, newPassword);
  }

  Future<MobileMe> me() async {
    final d = await api.getData('/v1/mobile/me');
    if (d == null) throw ApiException(status: 0, code: 'me_empty');
    return MobileMe.fromJson(d);
  }

  /// `POST /v1/auth/logout {refreshToken, installId}`; the local session is cleared whatever the answer (R6).
  Future<void> logout() async {
    final refresh = await tokens.readRefreshToken();
    try {
      if (refresh != null) await api.postData('/v1/auth/logout', {'refreshToken': refresh, 'installId': installId});
    } catch (_) {
      // Offline or already revoked: the local session ends anyway.
    } finally {
      await tokens.clear();
      await google?.forget();
    }
  }

  Future<MobileMe> _driverOrLogout() async {
    final me = await this.me();
    if (me.driver == null) {
      await logout();
      throw ApiException(status: 403, code: 'driver_profile_required');
    }
    return me;
  }
}
