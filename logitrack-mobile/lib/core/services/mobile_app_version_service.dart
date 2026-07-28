import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/material.dart';
import 'package:package_info_plus/package_info_plus.dart';
import 'package:pub_semver/pub_semver.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:url_launcher/url_launcher.dart';

/// Reads [settings/mobile_app] and blocks the app if current version is below [minAllowedVersion].
///
/// **Failing open on a network error is deliberate and must stay.** Drivers work in places with no
/// signal; a Firestore read that times out must never stop someone mid-delivery. What the cache below
/// closes is a different hole: a driver who is *already known* to be below the floor could turn on
/// airplane mode, force-stop the app and reopen it — the read failed, the blanket catch let them in,
/// and the forced update was defeated by turning off mobile data.
///
/// So: every successful read is cached, and a failed read falls back to that cache. The cache can only
/// ever hold a floor this device actually saw, so a driver who has never been online since the floor
/// was raised is never blocked by it, and a transient error cannot newly lock anyone out. Past
/// [_cacheTtl] the cache is ignored — a device offline for a month should be sorted out at the depot,
/// not bricked in a truck.
///
/// See shared-docs/adr/0007-mobile-forced-update-pipeline.md.
class MobileAppVersionService {
  MobileAppVersionService._();
  static final MobileAppVersionService instance = MobileAppVersionService._();

  static const _minVersionKey = 'logitrack_min_allowed_version';
  static const _apkUrlKey = 'logitrack_apk_download_url';
  static const _cachedAtKey = 'logitrack_version_config_cached_at';
  static const _cacheTtl = Duration(days: 30);

  /// Returns true if the user may proceed (version OK, or no usable config — fail open).
  Future<bool> ensureAllowedToRun(BuildContext context) async {
    _VersionConfig? config;
    try {
      config = await _fetchRemote();
      // Writes the cache on success, and clears it when the floor has been removed — otherwise
      // lowering or deleting minAllowedVersion would never un-block a device holding an old cache.
      await _writeCache(config);
    } catch (_) {
      config = await _readCache();
    }

    if (config == null) return true;

    final Version minVer;
    try {
      minVer = Version.parse(config.minAllowedVersion.trim());
    } catch (_) {
      return true;
    }

    final Version current;
    try {
      final info = await PackageInfo.fromPlatform();
      current = Version.parse(info.version.trim());
    } catch (_) {
      return true;
    }

    if (current >= minVer) return true;
    if (!context.mounted) return false;

    await _showBlockingDialog(context, config.apkDownloadUrl);
    return false;
  }

  /// Reads the live config. Throws on network / permission failure so the caller can fall back to
  /// cache; returns null when the doc exists but configures no floor.
  Future<_VersionConfig?> _fetchRemote() async {
    final doc = await FirebaseFirestore.instance
        .collection('settings')
        .doc('mobile_app')
        .get();
    if (!doc.exists) return null;

    final data = doc.data();
    if (data == null) return null;

    final minStr = (data['minAllowedVersion'] as String?)?.trim() ?? '';
    if (minStr.isEmpty) return null;

    return _VersionConfig(
      minAllowedVersion: minStr,
      apkDownloadUrl: (data['apkDownloadUrl'] as String?)?.trim() ?? '',
    );
  }

  Future<void> _writeCache(_VersionConfig? config) async {
    try {
      final prefs = await SharedPreferences.getInstance();
      if (config == null) {
        await prefs.remove(_minVersionKey);
        await prefs.remove(_apkUrlKey);
        await prefs.remove(_cachedAtKey);
        return;
      }
      await prefs.setString(_minVersionKey, config.minAllowedVersion);
      await prefs.setString(_apkUrlKey, config.apkDownloadUrl);
      await prefs.setInt(_cachedAtKey, DateTime.now().millisecondsSinceEpoch);
    } catch (_) {
      // Caching is best-effort; failing to persist must not affect this run.
    }
  }

  /// The last floor this device saw, or null when there is none or it has gone stale.
  Future<_VersionConfig?> _readCache() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      final minStr = prefs.getString(_minVersionKey)?.trim() ?? '';
      if (minStr.isEmpty) return null;

      final cachedAtMs = prefs.getInt(_cachedAtKey);
      if (cachedAtMs == null) return null;
      final age = DateTime.now().difference(
        DateTime.fromMillisecondsSinceEpoch(cachedAtMs),
      );
      if (age > _cacheTtl || age.isNegative) return null;

      return _VersionConfig(
        minAllowedVersion: minStr,
        apkDownloadUrl: prefs.getString(_apkUrlKey)?.trim() ?? '',
      );
    } catch (_) {
      return null;
    }
  }

  /// Undismissable by design — the only way forward is installing the new APK.
  Future<void> _showBlockingDialog(BuildContext context, String apkUrl) {
    return showDialog<void>(
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
  }
}

class _VersionConfig {
  const _VersionConfig({
    required this.minAllowedVersion,
    required this.apkDownloadUrl,
  });

  final String minAllowedVersion;
  final String apkDownloadUrl;
}
