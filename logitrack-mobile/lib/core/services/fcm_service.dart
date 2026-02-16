import 'package:firebase_core/firebase_core.dart';
import 'package:firebase_messaging/firebase_messaging.dart';
import '../../features/home/data/repositories/driver_repository.dart';
import '../../firebase_options.dart';

/// Request FCM permission, get token, and save to driver document for push notifications.
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

/// Call from main.dart to handle background/terminated messages.
@pragma('vm:entry-point')
Future<void> firebaseMessagingBackgroundHandler(RemoteMessage message) async {
  await Firebase.initializeApp(options: DefaultFirebaseOptions.currentPlatform);
}
