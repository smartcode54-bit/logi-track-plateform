import 'package:google_sign_in/google_sign_in.dart';

/// A Google account the driver picked: the ID token Go verifies and the address it carries.
class GoogleIdentity {
  const GoogleIdentity({required this.idToken, required this.email});
  final String idToken;
  final String email;
}

/// Google sign-in failed on the phone, before any request to Go ([code] = the plugin's code, e.g. a missing
/// Android OAuth client or Play services); a cancel is not a failure (null from [GoogleIdentitySource.pick]).
class GoogleSignInFailed implements Exception {
  GoogleSignInFailed(this.code);
  final String code;

  @override
  String toString() => 'GoogleSignInFailed($code)';
}

/// Where the gotest app gets a Google ID token (developer-spec §11.4): the seam that keeps the plugin out of the
/// repository's unit tests.
abstract class GoogleIdentitySource {
  /// False when the build has no server client id: the Google button stays disabled.
  bool get available;

  /// Shows the account picker. Null when the driver cancels; throws [GoogleSignInFailed] otherwise.
  Future<GoogleIdentity?> pick();

  /// Forgets the picked account so the next sign-in asks again (logout). Never throws.
  Future<void> forget();
}

/// [GoogleIdentitySource] on google_sign_in 7: `initialize(serverClientId)` once per process, then `authenticate()`.
/// The token's `aud` is [serverClientId] (the dev project's OAuth web client) and its `azp` the gotest Android client,
/// which is how Go tells a driver-app token from a web (GIS) one; no nonce is sent (`POST /v1/auth/google`,
/// Appendix C §C.4.10).
class PluginGoogleIdentitySource implements GoogleIdentitySource {
  PluginGoogleIdentitySource(this.serverClientId);

  final String serverClientId;

  /// Memoized: the plugin counts itself initialised even when `initialize` throws, so a second call after a
  /// failure would fail with "init() has already been called" instead of the real cause.
  static Future<void>? _init;

  @override
  bool get available => serverClientId.isNotEmpty;

  Future<void> _ensureInitialized() =>
      _init ??= GoogleSignIn.instance.initialize(serverClientId: serverClientId);

  @override
  Future<GoogleIdentity?> pick() async {
    if (!available) throw GoogleSignInFailed('not_configured');
    try {
      await _ensureInitialized();
      final account = await GoogleSignIn.instance.authenticate();
      final idToken = account.authentication.idToken;
      if (idToken == null || idToken.isEmpty) throw GoogleSignInFailed('no_id_token');
      return GoogleIdentity(idToken: idToken, email: account.email);
    } on GoogleSignInException catch (e) {
      if (e.code == GoogleSignInExceptionCode.canceled) return null;
      throw GoogleSignInFailed(e.code.name);
    }
  }

  @override
  Future<void> forget() async {
    if (_init == null) return;
    try {
      await _init;
      await GoogleSignIn.instance.signOut();
    } catch (_) {
      // The local session ends anyway.
    }
  }
}
