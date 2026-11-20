import 'package:flutter_secure_storage/flutter_secure_storage.dart';

/// Where the Go session lives (spec mobile-go-core-loop R3, N3): the access token only in memory, the refresh token
/// only in the platform's secure storage (Android Keystore). Tokens are never logged.
class TokenStore {
  TokenStore({FlutterSecureStorage? storage}) : _storage = storage ?? const FlutterSecureStorage();

  static const _refreshKey = 'lt_refresh_token';

  final FlutterSecureStorage _storage;
  String? _access;
  DateTime? _accessExpiresAt;

  String? get accessToken => _access;

  /// Whether the access token is missing or expires within [margin] (the proactive refresh of §11.4).
  bool accessExpiringWithin(Duration margin) {
    final exp = _accessExpiresAt;
    return _access == null || exp == null || exp.difference(DateTime.now()) < margin;
  }

  /// Stores a token pair from login or refresh. [expiresIn] is the access token's lifetime in seconds.
  Future<void> save({required String accessToken, required int expiresIn, String? refreshToken}) async {
    _access = accessToken;
    _accessExpiresAt = DateTime.now().add(Duration(seconds: expiresIn));
    if (refreshToken != null && refreshToken.isNotEmpty) {
      await _storage.write(key: _refreshKey, value: refreshToken);
    }
  }

  Future<String?> readRefreshToken() => _storage.read(key: _refreshKey);

  /// Forgets the session (logout, revoked or expired session).
  Future<void> clear() async {
    _access = null;
    _accessExpiresAt = null;
    await _storage.delete(key: _refreshKey);
  }
}
