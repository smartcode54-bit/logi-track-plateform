// The ONE file under lib/go allowed to import Firebase (spec mobile-auth-exchange R15, E3): it reads the Firebase
// session APK 3.x left on the phone so the first 4.x launch can trade it for a Go session. test/go_fence_test.dart
// allowlists this path only.
import 'dart:async';

import 'package:firebase_auth/firebase_auth.dart';
import 'package:firebase_core/firebase_core.dart';
import 'package:flutter/foundation.dart' show debugPrint;

import '../../../../core/api/api_exception.dart';
import '../../../../firebase_options.dart';

/// The Firebase user still signed in from APK 3.x: the ID token Go verifies and the user's address.
class LegacySession {
  const LegacySession({required this.idToken, required this.email});
  final String idToken;
  final String email;
}

/// Where the exchange gets the 3.x session (spec mobile-auth-exchange R12); a seam so the repository tests run without
/// Firebase.
abstract class LegacyFirebaseSession {
  /// The still-signed-in Firebase user, or null when there is none. Throws [NetworkException] when the token needs a
  /// refresh and the phone is offline (the session is kept for the next launch).
  Future<LegacySession?> read();

  /// Signs out of Firebase and deletes the default app, so an accidental Firebase call afterwards fails at once
  /// (`[core/no-app]`, the runtime fence of spec mobile-go-core-loop D3). Never throws.
  Future<void> end();
}

/// [LegacyFirebaseSession] on FlutterFire. Firebase is initialised only when [read] is called, i.e. on a cold start
/// without a Go session (R16), under the default app name: FlutterFire stores the session per app name, so it must be
/// `[DEFAULT]`, as in APK 3.x.
class FlutterFireLegacySession implements LegacyFirebaseSession {
  FlutterFireLegacySession({this.restoreTimeout = const Duration(seconds: 5)});

  /// How long to wait for FlutterFire to restore the persisted user before treating the phone as signed out.
  final Duration restoreTimeout;

  FirebaseApp? _app;

  Future<FirebaseApp> _init() async {
    if (_app != null) return _app!;
    try {
      _app = await Firebase.initializeApp(options: DefaultFirebaseOptions.currentPlatform);
    } on FirebaseException catch (e) {
      // Android may already have the default app from google-services.json (as main.dart handles it).
      if (e.code != 'duplicate-app') rethrow;
      _app = Firebase.app();
    }
    return _app!;
  }

  @override
  Future<LegacySession?> read() async {
    final app = await _init();
    final auth = FirebaseAuth.instanceFor(app: app);
    User? user = auth.currentUser;
    if (user == null) {
      try {
        user = await auth.authStateChanges().first.timeout(restoreTimeout);
      } on TimeoutException {
        user = null;
      }
    }
    if (user == null) return null;
    try {
      final token = await user.getIdToken();
      if (token == null || token.isEmpty) return null;
      return LegacySession(idToken: token, email: user.email ?? '');
    } on FirebaseAuthException catch (e) {
      if (e.code == 'network-request-failed') throw NetworkException(e);
      // The Firebase session is no longer usable (user deleted or disabled in Firebase, token revoked): nothing to
      // exchange; the login form follows.
      debugPrint('[LegacyFirebaseSession] getIdToken failed: ${e.code}');
      return null;
    }
  }

  @override
  Future<void> end() async {
    final app = _app;
    if (app == null) return;
    try {
      await FirebaseAuth.instanceFor(app: app).signOut();
    } catch (e) {
      debugPrint('[LegacyFirebaseSession] signOut failed: $e');
    }
    try {
      await app.delete();
    } catch (e) {
      debugPrint('[LegacyFirebaseSession] delete failed: $e');
    }
    _app = null;
  }
}
