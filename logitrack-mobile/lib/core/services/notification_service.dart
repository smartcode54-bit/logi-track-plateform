import 'package:flutter/material.dart';
import 'package:flutter_local_notifications/flutter_local_notifications.dart';

/// Channel id must match MainActivity.kt "chat" channel for custom sound.
const String kChatNotificationChannelId = 'chat';
const String kChatNotificationChannelName = 'Chat messages';

final FlutterLocalNotificationsPlugin flutterLocalNotificationsPlugin =
    FlutterLocalNotificationsPlugin();

/// Initialize local notifications with defaultIcon (mipmap/ic_launcher).
/// [navigatorKey] is used when user taps a notification to navigate to chat.
Future<void> initLocalNotifications(GlobalKey<NavigatorState>? navigatorKey) async {
  const AndroidInitializationSettings initSettingsAndroid =
      AndroidInitializationSettings('mipmap/ic_launcher');

  const InitializationSettings initSettings = InitializationSettings(
    android: initSettingsAndroid,
  );

  await flutterLocalNotificationsPlugin.initialize(
    initSettings,
    onDidReceiveNotificationResponse: (NotificationResponse response) {
      final String? payload = response.payload;
      if (payload == null || payload.isEmpty) return;
      navigatorKey?.currentState?.pushNamed('/chat-room', arguments: payload);
    },
  );
}

/// Show a chat notification (foreground). Uses channel "chat" so custom sound from MainActivity is used.
Future<void> showChatNotification({
  required String title,
  required String body,
  required String chatId,
  int id = 0,
}) async {
  const AndroidNotificationDetails androidDetails = AndroidNotificationDetails(
    kChatNotificationChannelId,
    kChatNotificationChannelName,
    channelDescription: 'Notifications for new messages from admin',
    importance: Importance.high,
    priority: Priority.high,
    playSound: true,
    enableVibration: true,
  );

  const NotificationDetails details = NotificationDetails(
    android: androidDetails,
  );

  await flutterLocalNotificationsPlugin.show(
    id,
    title,
    body,
    details,
    payload: chatId,
  );
}
