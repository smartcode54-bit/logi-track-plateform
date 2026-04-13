import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/material.dart';
import 'package:package_info_plus/package_info_plus.dart';
import 'package:pub_semver/pub_semver.dart';
import 'package:url_launcher/url_launcher.dart';

/// Reads [settings/mobile_app] and blocks the app if current version is below [minAllowedVersion].
class MobileAppVersionService {
  MobileAppVersionService._();
  static final MobileAppVersionService instance = MobileAppVersionService._();

  /// Returns true if the user may proceed (version OK or config missing / parse error — fail open).
  Future<bool> ensureAllowedToRun(BuildContext context) async {
    try {
      final doc = await FirebaseFirestore.instance
          .collection('settings')
          .doc('mobile_app')
          .get();
      if (!doc.exists) return true;
      final d = doc.data();
      if (d == null) return true;
      final minStr = d['minAllowedVersion'] as String?;
      final apkUrl = (d['apkDownloadUrl'] as String?)?.trim() ?? '';
      if (minStr == null || minStr.isEmpty) return true;

      Version minVer;
      try {
        minVer = Version.parse(minStr.trim());
      } catch (_) {
        return true;
      }

      final info = await PackageInfo.fromPlatform();
      Version current;
      try {
        current = Version.parse(info.version.trim());
      } catch (_) {
        return true;
      }

      if (current >= minVer) return true;

      if (!context.mounted) return false;

      await showDialog<void>(
        context: context,
        barrierDismissible: false,
        builder: (ctx) => PopScope(
          canPop: false,
          child: AlertDialog(
            title: Text('mobile_force_update_title'.tr()),
            content: Text('mobile_force_update_body'.tr()),
            actions: [
              if (apkUrl.isNotEmpty)
                TextButton(
                  onPressed: () async {
                    final uri = Uri.tryParse(apkUrl);
                    if (uri != null && await canLaunchUrl(uri)) {
                      await launchUrl(uri, mode: LaunchMode.externalApplication);
                    }
                  },
                  child: Text('mobile_force_update_download'.tr()),
                ),
            ],
          ),
        ),
      );
      return false;
    } catch (_) {
      return true;
    }
  }
}
