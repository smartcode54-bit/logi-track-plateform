import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:firebase_auth/firebase_auth.dart';
import 'package:flutter/foundation.dart';
import 'package:package_info_plus/package_info_plus.dart';

import 'mobile_install_id_service.dart';

/// Hybrid heartbeat: login, resume, job actions — debounced to limit Firestore writes.
class MobileClientHeartbeatService {
  MobileClientHeartbeatService._();
  static final MobileClientHeartbeatService instance = MobileClientHeartbeatService._();

  DateTime? _lastPulseAt;
  static const Duration _debounce = Duration(seconds: 45);

  bool _withinDebounce() {
    final last = _lastPulseAt;
    if (last == null) return false;
    return DateTime.now().difference(last) < _debounce;
  }

  /// Call after significant job writes (check-in, delivery complete, etc.).
  Future<void> onJobAction(String driverId) => pulse(driverId);

  /// Call when app returns to foreground (if [driverId] known).
  Future<void> onResumed(String? driverId) async {
    if (driverId == null || driverId.isEmpty) return;
    await pulse(driverId);
  }

  /// Call after driver profile resolved post-login.
  Future<void> onLoginResolved(String driverId) => pulse(driverId);

  Future<void> pulse(String driverId) async {
    if (driverId.isEmpty) return;
    final uid = FirebaseAuth.instance.currentUser?.uid;
    if (uid == null) return;
    if (_withinDebounce()) return;

    try {
      final driverRef =
          FirebaseFirestore.instance.collection('drivers').doc(driverId);
      final driverSnap = await driverRef.get();
      if (!driverSnap.exists) return;
      final data = driverSnap.data()!;
      if ((data['authId'] as String?) != uid) return;

      final subcontractorRaw = data['subcontractorId'];
      final partnerId = subcontractorRaw == null
          ? ''
          : subcontractorRaw.toString().trim();

      final first = (data['firstName'] as String?)?.trim() ?? '';
      final last = (data['lastName'] as String?)?.trim() ?? '';
      var driverName = '$first $last'.trim();
      if (driverName.isEmpty) {
        driverName = (data['mobile'] as String?)?.trim() ?? driverId;
      }

      final info = await PackageInfo.fromPlatform();
      final installId = await getOrCreateMobileInstallId();
      const flavor = String.fromEnvironment('FLAVOR', defaultValue: 'dev');

      final platform = defaultTargetPlatform == TargetPlatform.iOS
          ? 'ios'
          : defaultTargetPlatform == TargetPlatform.android
              ? 'android'
              : 'other';

      await driverRef.collection('mobile_installations').doc(installId).set({
        'driverId': driverId,
        'driverName': driverName,
        'partnerId': partnerId,
        'platform': platform,
        'appVersion': info.version,
        'buildNumber': info.buildNumber,
        'flavor': flavor,
        'lastSeenAt': FieldValue.serverTimestamp(),
      }, SetOptions(merge: true));

      _lastPulseAt = DateTime.now();
    } catch (e, st) {
      debugPrint('MobileClientHeartbeatService.pulse: $e\n$st');
    }
  }
}
