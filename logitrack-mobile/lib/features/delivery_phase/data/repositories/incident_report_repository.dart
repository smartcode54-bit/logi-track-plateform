import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:firebase_storage/firebase_storage.dart';
import '../../../home/data/services/image_compression_service.dart';

/// Firestore collection for incident reports (delivery delay / problem reports).
const String incidentReportCollection = 'incidentReport';

/// Storage path prefix: incident_reports/{reportId}/{photoType}.jpg
String incidentReportPhotoPath(String reportId, String photoType) =>
    'incident_reports/$reportId/$photoType.jpg';

/// Upload one incident photo to Storage (compressed JPEG).
Future<String> uploadIncidentPhoto({
  required String reportId,
  required String photoType,
  required List<int> imageBytes,
}) async {
  final compressed = await compressImageForUpload(imageBytes);
  final ref = FirebaseStorage.instance.ref().child(
    incidentReportPhotoPath(reportId, photoType),
  );
  await ref.putData(
    compressed,
    SettableMetadata(contentType: 'image/jpeg'),
  );
  return ref.getDownloadURL();
}

/// เหตุที่ทำให้การจัดส่งล่าช้า — ใช้ translation key (incident_cause_*) แสดงใน UI กับ en/th
const List<String> incidentDelayCauseOptionKeys = [
  'incident_cause_accident',
  'incident_cause_engine',
  'incident_cause_tire',
  'incident_cause_traffic',
  'incident_cause_weather',
  'incident_cause_checkpoint',
];

/// Submit incident report: upload only the photos provided (at least 1 required).
/// [delayCause] = เหตุที่ทำให้การจัดส่งล่าช้า (จาก combobox).
Future<void> submitIncidentReport({
  required String driverId,
  required String? tripId,
  String? delayCause,
  List<int>? mapImage,
  List<int>? situation1Image,
  List<int>? situation2Image,
  double? lat,
  double? lng,
}) async {
  final col = FirebaseFirestore.instance.collection(incidentReportCollection);
  final reportRef = col.doc();

  final reportId = reportRef.id;
  final now = DateTime.now();

  String? mapUrl;
  String? situation1Url;
  String? situation2Url;
  if (mapImage != null && mapImage.isNotEmpty) {
    mapUrl = await uploadIncidentPhoto(
      reportId: reportId,
      photoType: 'map',
      imageBytes: mapImage,
    );
  }
  if (situation1Image != null && situation1Image.isNotEmpty) {
    situation1Url = await uploadIncidentPhoto(
      reportId: reportId,
      photoType: 'situation1',
      imageBytes: situation1Image,
    );
  }
  if (situation2Image != null && situation2Image.isNotEmpty) {
    situation2Url = await uploadIncidentPhoto(
      reportId: reportId,
      photoType: 'situation2',
      imageBytes: situation2Image,
    );
  }

  await reportRef.set({
    'driverId': driverId,
    'tripId': tripId ?? null,
    'delayCause': delayCause?.trim().isEmpty == true ? null : delayCause?.trim(),
    'mapPhotoUrl': mapUrl,
    'situation1PhotoUrl': situation1Url,
    'situation2PhotoUrl': situation2Url,
    'lat': lat,
    'lng': lng,
    'createdAt': Timestamp.fromDate(now),
    'updatedAt': Timestamp.fromDate(now),
  });
}
