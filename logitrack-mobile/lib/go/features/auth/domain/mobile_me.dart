/// The signed-in driver as `GET /v1/mobile/me` answers it (T55).
class MobileMe {
  MobileMe({required this.userId, required this.email, required this.displayName, this.driver});

  final String userId;
  final String email;
  final String displayName;

  /// null for an account without a linked driver (it may not use the app: driver_profile_required).
  final MobileDriver? driver;

  factory MobileMe.fromJson(Map<String, dynamic> j) {
    final d = j['driver'];
    return MobileMe(
      userId: j['id'] as String? ?? '',
      email: j['email'] as String? ?? '',
      displayName: (j['displayName'] as String?)?.trim().isNotEmpty == true
          ? (j['displayName'] as String).trim()
          : (j['email'] as String? ?? ''),
      driver: d is Map<String, dynamic> ? MobileDriver.fromJson(d) : null,
    );
  }
}

class MobileDriver {
  MobileDriver({required this.id, required this.tenantId, required this.legacyDocId});
  final String id;
  final String tenantId;
  final String legacyDocId;

  factory MobileDriver.fromJson(Map<String, dynamic> j) => MobileDriver(
        id: j['id'] as String? ?? '',
        tenantId: j['tenantId'] as String? ?? '',
        legacyDocId: j['legacyDocId'] as String? ?? '',
      );
}
