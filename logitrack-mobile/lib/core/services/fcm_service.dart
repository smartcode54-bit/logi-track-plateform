import 'package:firebase_core/firebase_core.dart';
import 'package:firebase_messaging/firebase_messaging.dart';
import 'package:cloud_firestore/cloud_firestore.dart';
import '../../features/home/data/repositories/driver_repository.dart';
import '../../firebase_options.dart';

/// Request FCM permission, get token, and save to driver document for push notifications (first-mile tasks).
Future<void> initFcmAndSaveToken(String? driverId) async {
  if (driverId == null || driverId.isEmpty) return;
  final messaging = FirebaseMessaging.instance;
  final permission = await messaging.requestPermission(
    alert: true,
    badge: true,
    sound: true,
  );
  if (permission != AuthorizationStatus.authorized &&
      permission != AuthorizationStatus.provisional) {
    return;
  }
  final token = await messaging.getToken();
  if (token != null) {
    await DriverRepository().updateFcmToken(driverId, token);
  }
}

/// Save/merge FCM token to users/{uid}/fcmTokens (for chat push notifications).
/// Call after login with Firebase Auth UID.
Future<void> saveFcmTokenToUser(String? uid) async {
  if (uid == null || uid.isEmpty) return;
  final messaging = FirebaseMessaging.instance;
  final permission = await messaging.requestPermission(
    alert: true,
    badge: true,
    sound: true,
  );
  if (permission != AuthorizationStatus.authorized &&
      permission != AuthorizationStatus.provisional) {
    return;
  }
  final token = await messaging.getToken();
  if (token == null) return;
  try {
    await FirebaseFirestore.instance.collection('users').doc(uid).set(
      {
        'fcmTokens': {'app': token},
        'updatedAt': FieldValue.serverTimestamp(),
      },
      SetOptions(merge: true),
    );
  } catch (e) {
    // ignore
  }
}

/// Call from main.dart to handle background/terminated messages.
@pragma('vm:entry-point')
Future<void> firebaseMessagingBackgroundHandler(RemoteMessage message) async {
  await Firebase.initializeApp(options: DefaultFirebaseOptions.currentPlatform);
}
