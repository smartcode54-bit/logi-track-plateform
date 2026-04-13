import 'dart:math';

import 'package:firebase_app_installations/firebase_app_installations.dart';
import 'package:shared_preferences/shared_preferences.dart';

const _prefKeyInstallId = 'logitrack_mobile_install_id';

/// Stable ID per app install (Firebase Installations with SharedPreferences fallback).
Future<String> getOrCreateMobileInstallId() async {
  final prefs = await SharedPreferences.getInstance();
  final existing = prefs.getString(_prefKeyInstallId);
  if (existing != null && existing.isNotEmpty) {
    return existing;
  }
  String? id;
  try {
    id = await FirebaseInstallations.instance.getId();
  } catch (_) {
    id = null;
  }
  id ??= '${DateTime.now().millisecondsSinceEpoch}-${Random().nextInt(1 << 30)}';
  await prefs.setString(_prefKeyInstallId, id);
  return id;
}
