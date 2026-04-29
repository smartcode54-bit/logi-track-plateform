import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:geolocator/geolocator.dart';

/// Firestore collection for Hub/SOC sources (matches web admin hubs).
const String hubsCollection = 'hubs';

/// Station type from Firestore: HUB = pickup point (First Mile), SOC = sort center (Line Haul).
const String stationTypeHub = 'HUB';
const String stationTypeSoc = 'SOC';

/// Job type values aligned with shared-docs trip-record schema.
const String jobTypeFirstMile = 'first_mile';
const String jobTypeLineHaul = 'line_haul';

/// Default radius in meters to consider driver "at" a hub or SOC.
const double defaultGeofenceRadiusMeters = 500.0;

/// Hub/SOC document as stored in Firestore (matches web hubSchema).
class HubDoc {
  final String? id;
  final String sourceId;
  final String sourceNameEn;
  final String sourceNameTh;
  final double? latitude;
  final double? longitude;
  final String stationType; // HUB | SOC
  final String? linkedCustomerId;
  final String? customerLinkKind; // customer | partner

  HubDoc({
    this.id,
    required this.sourceId,
    required this.sourceNameEn,
    required this.sourceNameTh,
    this.latitude,
    this.longitude,
    this.stationType = stationTypeHub,
    this.linkedCustomerId,
    this.customerLinkKind,
  });

  factory HubDoc.fromFirestore(Map<String, dynamic> data, [String? docId]) {
    final lat = data['latitude'];
    final lng = data['longitude'];
    final sourceId = (data['source_id'] ??
            data['sourceId'] ??
            data['hubId'] ??
            data['hubCode'] ??
            '')
        .toString();
    final nameEn = (data['source_name_en'] ?? data['sourceNameEn'] ?? '')
        .toString();
    final nameTh = (data['source_name_th'] ??
            data['sourceNameTh'] ??
            data['source_name_en'] ??
            data['sourceNameEn'] ??
            '')
        .toString();
    final rawType = (data['station_type'] ?? data['stationType'] ?? stationTypeHub)
        .toString()
        .trim();
    final linkedCustomerId = (data['linkedCustomerId'] ?? '').toString().trim();
    final customerLinkKind = (data['customerLinkKind'] ?? '').toString().trim();
    return HubDoc(
      id: docId,
      sourceId: sourceId,
      sourceNameEn: nameEn,
      sourceNameTh: nameTh.isNotEmpty ? nameTh : nameEn,
      latitude: lat is num ? lat.toDouble() : null,
      longitude: lng is num ? lng.toDouble() : null,
      stationType: rawType.toUpperCase() == stationTypeSoc
          ? stationTypeSoc
          : stationTypeHub,
      linkedCustomerId: linkedCustomerId.isEmpty ? null : linkedCustomerId,
      customerLinkKind: customerLinkKind.isEmpty ? null : customerLinkKind,
    );
  }

  bool get hasCoordinates =>
      latitude != null &&
      longitude != null &&
      latitude!.isFinite &&
      longitude!.isFinite;
}

/// ตรงกับ web `hubSourceIdHasSpxSuffix` — สถานีลงท้าย SPX → รหัสลูกค้า SPX บนงาน.
bool hubSourceIdHasSpxSuffix(String sourceIdOrHubCode) {
  final s = sourceIdOrHubCode.trim().toUpperCase();
  if (s.isEmpty) return false;
  if (s == 'SPX') return true;
  return s.endsWith('-SPX') || s.endsWith('_SPX') || s.endsWith('.SPX');
}

/// ฟิลด์เหมือน Admin ใส่บน `tasks` จากคู่ hub ที่คนขับเลือก (manual check-in).
///
/// ชื่อจาก `customers` ไม่ดึงบนมือถือได้ (rules) — `*LinkedCustomerName` ว่าง;
/// ให้เห็น partner/customer จาก `*CustomerLinkKind` + รหัส SPX (เมื่อมี suffix) แทน.
Map<String, dynamic> taskFieldsFromHubDocsForManualCreate({
  required HubDoc? originHub,
  required HubDoc? destHub,
}) {
  final out = <String, dynamic>{};
  void source(HubDoc? h) {
    if (h == null) return;
    final id = h.linkedCustomerId?.trim();
    if (id == null || id.isEmpty) return;
    out['sourceHubLinkedCustomerId'] = id;
    final k = h.customerLinkKind?.trim();
    out['sourceHubCustomerLinkKind'] =
        k != null && k.isNotEmpty ? k : 'customer';
    if (hubSourceIdHasSpxSuffix(h.sourceId)) {
      out['sourceHubLinkedCustomerCode'] = 'SPX';
    }
    out['sourceHubLinkedCustomerName'] = '';
  }

  void dest(HubDoc? h) {
    if (h == null) return;
    final id = h.linkedCustomerId?.trim();
    if (id == null || id.isEmpty) return;
    out['destinationLinkedCustomerId'] = id;
    final k = h.customerLinkKind?.trim();
    out['destinationCustomerLinkKind'] =
        k != null && k.isNotEmpty ? k : 'customer';
    if (hubSourceIdHasSpxSuffix(h.sourceId)) {
      out['destinationLinkedCustomerCode'] = 'SPX';
    }
    out['destinationLinkedCustomerName'] = '';
  }

  source(originHub);
  dest(destHub);
  return out;
}

/// Fetches all hubs (and SOCs) with coordinates from Firestore.
Future<List<HubDoc>> fetchHubsWithCoordinates() async {
  final snap = await FirebaseFirestore.instance
      .collection(hubsCollection)
      .get();

  final list = <HubDoc>[];
  for (final doc in snap.docs) {
    final hub = HubDoc.fromFirestore(doc.data(), doc.id);
    if (hub.hasCoordinates) list.add(hub);
  }
  return list;
}

/// Fetches all hubs (and SOCs) from Firestore, regardless of whether they have coordinates.
Future<List<HubDoc>> fetchAllHubs() async {
  final snap = await FirebaseFirestore.instance
      .collection(hubsCollection)
      .get();

  final list = <HubDoc>[];
  for (final doc in snap.docs) {
    list.add(HubDoc.fromFirestore(doc.data(), doc.id));
  }
  return list;
}

/// Driver สร้าง Hub/SOC ใหม่ด้วย รหัส + ชื่อ (ไม่ต้องมีพิกัด) เพื่อใช้ใน Manual Check In / Loading Phase ได้ทันที
/// ตรวจสอบรหัสซ้ำก่อนบันทึก
Future<HubDoc> addHubByDriver({
  required String sourceId,
  required String sourceName,
  required String stationType,
}) async {
  final id = sourceId.trim();
  final name = sourceName.trim();
  if (id.isEmpty) throw ArgumentError('รหัสต้องไม่ว่าง');
  if (name.isEmpty) throw ArgumentError('ชื่อต้องไม่ว่าง');

  // เช็ครหัสซ้ำ
  final existing = await FirebaseFirestore.instance
      .collection(hubsCollection)
      .where('source_id', isEqualTo: id)
      .limit(1)
      .get();
  if (existing.docs.isNotEmpty) {
    throw StateError('HUB_DUPLICATE_CODE');
  }

  final st = stationType.toUpperCase() == stationTypeSoc ? stationTypeSoc : stationTypeHub;
  final data = {
    'source_id': id,
    'source_name_en': name,
    'source_name_th': name,
    'station_type': st,
    'createdByDriver': true,
    'createdAt': FieldValue.serverTimestamp(),
  };

  final docRef = await FirebaseFirestore.instance.collection(hubsCollection).add(data);
  return HubDoc(
    id: docRef.id,
    sourceId: id,
    sourceNameEn: name,
    sourceNameTh: name,
    stationType: st,
  );
}

/// Detects job type from current GPS: if nearest station within [radiusMeters] is a HUB → first_mile, if SOC → line_haul.
/// Returns null if no station within radius.
Future<String?> detectJobTypeFromPosition({
  required double lat,
  required double lng,
  double radiusMeters = defaultGeofenceRadiusMeters,
}) async {
  final hubs = await fetchHubsWithCoordinates();
  if (hubs.isEmpty) return null;

  String? nearestType;
  double nearestDistance = double.infinity;

  for (final hub in hubs) {
    final d = Geolocator.distanceBetween(
      lat,
      lng,
      hub.latitude!,
      hub.longitude!,
    );
    if (d < nearestDistance && d <= radiusMeters) {
      nearestDistance = d;
      nearestType = hub.stationType;
    }
  }

  if (nearestType == null) return null;
  return nearestType == stationTypeHub ? jobTypeFirstMile : jobTypeLineHaul;
}

/// หา Hub/SOC จากข้อความปลายทางที่บันทึกใน trip (ชื่อ EN/TH หรือรหัส source_id ตามที่ใช้ใน Loading)
HubDoc? findHubByDestinationLabel(List<HubDoc> hubs, String? destinationLabel) {
  if (destinationLabel == null) return null;
  final t = destinationLabel.trim();
  if (t.isEmpty) return null;
  for (final h in hubs) {
    if (h.sourceNameEn == t || h.sourceNameTh == t || h.sourceId == t) {
      return h;
    }
  }
  final tl = t.toLowerCase();
  for (final h in hubs) {
    if (h.sourceNameEn.toLowerCase() == tl ||
        h.sourceNameTh.toLowerCase() == tl) {
      return h;
    }
  }
  return null;
}
