import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:shared_preferences/shared_preferences.dart';

const _keyLastReadBroadcastMs = 'logitrack_last_read_broadcast_ms';

/// Saves the timestamp when the driver last opened the broadcast list (so we can show "new" badge).
Future<void> saveLastReadBroadcast(Timestamp? sentAt) async {
  final prefs = await SharedPreferences.getInstance();
  final ms = sentAt != null ? sentAt.millisecondsSinceEpoch : DateTime.now().millisecondsSinceEpoch;
  await prefs.setInt(_keyLastReadBroadcastMs, ms);
}

/// Returns the last-read timestamp in milliseconds since epoch, or null if never read.
Future<int?> getLastReadBroadcastMs() async {
  final prefs = await SharedPreferences.getInstance();
  return prefs.getInt(_keyLastReadBroadcastMs);
}
