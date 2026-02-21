import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:firebase_auth/firebase_auth.dart';
import 'package:google_sign_in/google_sign_in.dart';

class AuthRepository {
  final FirebaseAuth _auth = FirebaseAuth.instance;
  final FirebaseFirestore _firestore = FirebaseFirestore.instance;

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
          await _auth.signOut();
          throw Exception(
            'Access Denied: You must be a registered driver to use this app.',
          );
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

  Future<void> signOut() async {
    await _auth.signOut();
  }

  Stream<User?> get authStateChanges => _auth.authStateChanges();

  User? get currentUser => _auth.currentUser;
}
