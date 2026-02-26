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

  HubDoc({
    this.id,
    required this.sourceId,
    required this.sourceNameEn,
    required this.sourceNameTh,
    this.latitude,
    this.longitude,
    this.stationType = stationTypeHub,
  });

  factory HubDoc.fromFirestore(Map<String, dynamic> data, [String? docId]) {
    final lat = data['latitude'];
    final lng = data['longitude'];
    return HubDoc(
      id: docId,
      sourceId: (data['source_id'] ?? '').toString(),
      sourceNameEn: (data['source_name_en'] ?? '').toString(),
      sourceNameTh: (data['source_name_th'] ?? data['source_name_en'] ?? '')
          .toString(),
      latitude: lat is num ? lat.toDouble() : null,
      longitude: lng is num ? lng.toDouble() : null,
      stationType:
          (data['station_type'] ?? stationTypeHub).toString().toUpperCase() ==
              stationTypeSoc
          ? stationTypeSoc
          : stationTypeHub,
    );
  }

  bool get hasCoordinates =>
      latitude != null &&
      longitude != null &&
      latitude!.isFinite &&
      longitude!.isFinite;
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
