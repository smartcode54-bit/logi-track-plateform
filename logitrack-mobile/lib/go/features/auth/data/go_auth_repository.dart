import '../../../../core/api/api_client.dart';
import '../../../../core/api/api_exception.dart';
import '../../../../core/api/token_store.dart';
import '../domain/mobile_me.dart';

/// A login that needs a new password first (`403 password_change_required`, R79).
class PasswordChangeRequired implements Exception {
  PasswordChangeRequired(this.ticket);
  final String ticket;
}

/// The session against the Go API (spec mobile-go-core-loop R3-R7): login, the silent start, the forced password
/// change, `GET /v1/mobile/me` and logout.
class GoAuthRepository {
  GoAuthRepository({required this.api, required this.tokens, required this.installId, required this.appVersion});

  final ApiClient api;
  final TokenStore tokens;
  final String installId;
  final String appVersion;

  /// `POST /v1/auth/login` then `GET /v1/mobile/me`. Throws [PasswordChangeRequired] for a temporary password,
  /// [ApiException] / [NetworkException] otherwise; an account without a driver is logged out again and answered as
  /// `403 driver_profile_required`.
  Future<MobileMe> login(String email, String password) async {
    try {
      final d = await api.postData('/v1/auth/login', {
        'email': email.trim(),
        'password': password,
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
      if (e.code == 'password_change_required' && ticket is String) throw PasswordChangeRequired(ticket);
      rethrow;
    }
    return _driverOrLogout();
  }

  /// The silent start of a cold launch (R4): with a stored refresh token the interceptor refreshes on the first call.
  /// Returns null when there is no session to resume; a network failure throws [NetworkException] (the token stays).
  Future<MobileMe?> resume() async {
    final refresh = await tokens.readRefreshToken();
    if (refresh == null || refresh.isEmpty) return null;
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
