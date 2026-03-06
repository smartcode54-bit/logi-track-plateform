import 'package:cloud_firestore/cloud_firestore.dart';

/// Collection Hub→SOC: ระยะทาง/เวลาเดินทาง (Google Distance Matrix). Doc ID = hubId_socId.
const String hubSocDistancesCollection = 'hub_soc_distances';

/// Collection SOC→Hub: ระยะทาง/เวลาเดินทาง (แยกจาก hub_soc_distances). Doc ID = socId_hubId.
const String socHubDistancesCollection = 'soc_hub_distances';

/// SOC keys ตรงกับ web (firstMileTaskSchema SOC_KEYS). ใช้ normalize doc id ใน soc_hub_distances.
const List<String> _socKeys = ['SOCE', 'SOCN', 'SOCW'];

/// แปลง source_id ของ SOC เป็น key มาตรฐาน SOCE/SOCN/SOCW (ให้ตรงกับ doc id ใน soc_hub_distances).
String normalizeSocIdToKey(String sourceId) {
  final u = (sourceId ?? '').trim().toUpperCase();
  for (final key in _socKeys) {
    final k = key.toUpperCase();
    if (u == k || u.startsWith('$k ') || u.startsWith('$k(')) return key;
  }
  return sourceId;
}

/// Document ID = ต้นทาง_ปลายทาง (origin_destination).
/// Hub→SOC: hubId_socId ใน hub_soc_distances | SOC→Hub: socId_hubId ใน soc_hub_distances (socId ต้องเป็น key เช่น SOCE).
String hubSocDistanceDocId(
  String originSourceId,
  String destinationSourceId, {
  bool originIsSoc = false,
}) {
  final origin = originIsSoc
      ? normalizeSocIdToKey(originSourceId)
      : originSourceId;
  return '${origin}_$destinationSourceId';
}

/// ผลลัพธ์จาก hub_soc_distances (distanceKm, durationMinutes ใช้แสดงใน Preview และบันทึก trip_records).
class HubSocDistanceResult {
  final double distanceKm;
  final double durationMinutes;

  const HubSocDistanceResult({
    required this.distanceKm,
    required this.durationMinutes,
  });
}

/// ดึงระยะทางและเวลาเดินทางตามต้นทาง-ปลายทางที่ driver เลือก.
/// Hub→SOC: อ่านจาก hub_soc_distances (doc id = hubId_socId).
/// SOC→Hub: อ่านจาก soc_hub_distances (doc id = socId_hubId).
/// คืน null ถ้าไม่มี doc (เช่น เลือก HUB–HUB หรือยังไม่ได้คำนวณใน Admin).
Future<HubSocDistanceResult?> fetchHubSocDistance({
  required String originSourceId,
  required String destinationSourceId,
  required String originStationType,
  required String destinationStationType,
}) async {
  if (originSourceId == destinationSourceId) return null;
  final isSocToHub =
      originStationType.toUpperCase() == 'SOC' &&
      destinationStationType.toUpperCase() == 'HUB';
  final docId = hubSocDistanceDocId(
    originSourceId,
    destinationSourceId,
    originIsSoc: isSocToHub,
  );
  final collectionName = isSocToHub
      ? socHubDistancesCollection
      : hubSocDistancesCollection;
  final ref = FirebaseFirestore.instance.collection(collectionName).doc(docId);
  final snap = await ref.get();
  if (!snap.exists) return null;
  final data = snap.data();
  if (data == null) return null;
  final distanceKm = data['distanceKm'];
  final durationMinutes = data['durationMinutes'];
  if (distanceKm is! num || durationMinutes is! num) return null;
  return HubSocDistanceResult(
    distanceKm: distanceKm.toDouble(),
    durationMinutes: durationMinutes.toDouble(),
  );
}
