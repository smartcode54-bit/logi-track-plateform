/// Every identifier a driver can be referenced by in denormalized data: the Firestore doc id
/// (when [docId] is given), the current Auth UID (`authId`, or legacy `authUid`), and any past
/// Auth UIDs kept in `authIdHistory`.
///
/// `vehicle_expenses.driverId` (and `trip_records` likewise) store whichever Auth UID was current
/// when the doc was written. An email / Google re-link moves the driver to a NEW uid, so a query
/// on the current uid alone misses everything saved before it. Querying each of these keys finds
/// them again. Mirror of logitrack-web/lib/driverName.ts `driverIdentityKeys` — keep both in sync.
///
/// Callers that only know the signed-in uid use `{uid, ...driverIdentityKeys(driverDoc)}`.
List<String> driverIdentityKeys(Map<String, dynamic>? driver, {String? docId}) {
  final keys = <String>[];
  void push(Object? v) {
    final s = (v ?? '').toString().trim();
    if (s.isNotEmpty && !keys.contains(s)) keys.add(s);
  }

  if (docId != null) push(docId);
  if (driver != null) {
    push(driver['authId']);
    push(driver['authUid']);
    final hist = driver['authIdHistory'];
    if (hist is List) hist.forEach(push);
  }
  return keys;
}
