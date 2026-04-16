import 'dart:convert';

import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:flutter/foundation.dart';
import 'package:firebase_auth/firebase_auth.dart';
import 'package:flutter_dotenv/flutter_dotenv.dart';
import 'package:geolocator/geolocator.dart';
import 'package:google_sign_in/google_sign_in.dart';
import 'package:http/http.dart' as http;

import '../../../../core/services/cloud_functions_service.dart';
import '../../../home/data/services/draft_storage_service.dart';

class AuthRepository {
  final FirebaseAuth _auth = FirebaseAuth.instance;
  final FirebaseFirestore _firestore = FirebaseFirestore.instance;
  static bool _googleInitialized = false;

  /// Keeps `users/{uid}.lastLogin` in sync for admin Security Center (Firestore is not updated on every Auth sign-in server-side).
  Future<void> _touchLastLogin(User user) async {
    try {
      final data = <String, dynamic>{
        'uid': user.uid,
        'email': user.email ?? '',
        'displayName': user.displayName ?? '',
        'lastLogin': DateTime.now().toUtc().toIso8601String(),
      };
      try {
        var perm = await Geolocator.checkPermission();
        if (perm == LocationPermission.denied) {
          perm = await Geolocator.requestPermission();
        }
        if (perm == LocationPermission.denied ||
            perm == LocationPermission.deniedForever) {
          // skip geo
        } else {
          final pos = await Geolocator.getCurrentPosition();
          data['lastLoginLat'] = pos.latitude;
          data['lastLoginLng'] = pos.longitude;
          data['lastLoginGeoSource'] = 'gps';
        }
      } catch (_) {}
      if (data['lastLoginLat'] == null || data['lastLoginLng'] == null) {
        try {
          final r = await http
              .get(Uri.parse('https://ipwho.is/json/'))
              .timeout(const Duration(seconds: 6));
          if (r.statusCode == 200) {
            final j = jsonDecode(r.body) as Map<String, dynamic>?;
            if (j != null && j['success'] != false) {
              final lat = (j['latitude'] as num?)?.toDouble();
              final lng = (j['longitude'] as num?)?.toDouble();
              if (lat != null && lng != null) {
                data['lastLoginLat'] = lat;
                data['lastLoginLng'] = lng;
                data['lastLoginGeoSource'] = 'ip';
              }
            }
          }
        } catch (_) {}
      }
      await _firestore.collection('users').doc(user.uid).set(
            data,
            SetOptions(merge: true),
          );
    } catch (e, st) {
      debugPrint('[AuthRepository] _touchLastLogin: $e\n$st');
    }
  }

  Future<User?> signInWithEmail(String email, String password) async {
    try {
      final UserCredential userCredential = await _auth
          .signInWithEmailAndPassword(email: email, password: password);

      final User? user = userCredential.user;

      if (user != null) {
        // Verify user is a driver
        final idTokenResult = await user.getIdTokenResult(true);
        final role = idTokenResult.claims?['role'];

        if (role != 'driver') {
          // Fallback: check if user is in drivers collection (claims may not have propagated)
          final driverQuery = await _firestore
              .collection('drivers')
              .where('authId', isEqualTo: user.uid)
              .limit(1)
              .get();

          if (driverQuery.docs.isEmpty) {
            await _auth.signOut();
            throw Exception(
              'Access Denied: You must be a registered driver to use this app.',
            );
          }
          // Set driver claims for users in drivers collection (mobile app = drivers)
          await CloudFunctionsService.instance.call('setDriverClaims');
        }

        return user;
      }
      return null;
    } on FirebaseAuthException catch (e) {
      throw Exception(e.message ?? 'Authentication failed');
    } catch (e) {
      throw Exception(e.toString());
    }
  }

  Future<User?> signInWithGoogle() async {
    try {
      final GoogleSignInAccount googleUser;
      try {
        // Initialize GoogleSignIn with the Web Client ID from env (only once)
        if (!_googleInitialized) {
          final serverClientId = dotenv.env['FIREBASE_WEB_CLIENT_ID'];
          await GoogleSignIn.instance.initialize(
            serverClientId: serverClientId,
          );
          _googleInitialized = true;
        }
        googleUser = await GoogleSignIn.instance.authenticate();
      } catch (e) {
        // If the user cancelled, we return null so the UI doesn't show a scary red error.
        // Otherwise, throw so we can see the real issue (like missing SHA-1 hash).
        if (e.toString().contains('sign_in_canceled')) {
          return null;
        }
        // Print and throw for debugging
        print('Google Sign-In Error: $e');
        throw Exception('Google Sign-In Error: $e');
      }

      final GoogleSignInAuthentication googleAuth = googleUser.authentication;
      final AuthCredential credential = GoogleAuthProvider.credential(
        accessToken: null, // accessToken is no longer provided directly in v7.x
        idToken: googleAuth.idToken,
      );

      final UserCredential userCredential = await _auth.signInWithCredential(
        credential,
      );
      final User? user = userCredential.user;

      if (user != null) {
        // Verify user is a driver
        final idTokenResult = await user.getIdTokenResult(true);
        final role = idTokenResult.claims?['role'];

        if (role != 'driver') {
          // Check if they are in the drivers collection in case claims haven't propagated
          final driverQuery = await _firestore
              .collection('drivers')
              .where('authId', isEqualTo: user.uid)
              .limit(1)
              .get();

          if (driverQuery.docs.isEmpty) {
            await GoogleSignIn.instance.signOut();
            await _auth.signOut();
            throw Exception(
              'Access Denied: You must be a registered driver to use this app.',
            );
          }
          // Set driver claims for users in drivers collection (mobile app = drivers)
          await CloudFunctionsService.instance.call('setDriverClaims');
        }

        await _touchLastLogin(user);
        return user;
      }
      return null;
    } on FirebaseAuthException catch (e) {
      throw Exception(e.message ?? 'Authentication failed');
    } catch (e) {
      throw Exception(e.toString());
    }
  }

  Future<void> signOut() async {
    // Clear all user-scoped local data before signing out so the next
    // user who logs in on this device does not see stale data.
    try {
      await DraftStorageService.instance.clearAllUserData();
    } catch (e) {
      debugPrint('[AuthRepository] signOut clearAllUserData error: $e');
    }
    await _auth.signOut();
  }

  Stream<User?> get authStateChanges => _auth.authStateChanges();

  User? get currentUser => _auth.currentUser;
}
