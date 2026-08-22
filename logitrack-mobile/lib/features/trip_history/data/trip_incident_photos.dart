import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:firebase_auth/firebase_auth.dart';
import 'package:http/http.dart' as http;

/// Firestore collection for incident reports.
/// ⚠️ camelCase singular — NOT `incident_reports` (that is the Storage path).
/// Mirrors `incident_report_repository.dart:6`.
const String incidentReportCollection = 'incidentReport';

/// One downloadable incident photo linked to a trip.
class IncidentPhoto {
  /// Download URL (Storage `incident_reports/**` is public-read).
  final String url;

  /// `map` | `situation1` | `situation2`.
  final String type;

  /// 0-based order of the incident report within the trip (by `createdAt`).
  /// Used to disambiguate file names when a trip has several reports.
  final int reportSeq;

  const IncidentPhoto({
    required this.url,
    required this.type,
    required this.reportSeq,
  });
}

/// Incident photos are stored as three nullable URL fields on the report doc,
/// not a `photos[]` array (`incident_report_repository.dart:81-96`). Order matters
/// for the download name.
const List<List<String>> _incidentPhotoFields = [
  ['map', 'mapPhotoUrl'],
  ['situation1', 'situation1PhotoUrl'],
  ['situation2', 'situation2PhotoUrl'],
];

/// Fetch the incident photos linked to [tripId]
/// (query `incidentReport where tripId == tripId`, ADR 0018 §Amendment / spec R11).
///
/// Returns `[]` when [tripId] is null/empty, when no incidents exist, or on error —
/// a trip with no incidents must render normally. Reports are ordered by `createdAt`.
Future<List<IncidentPhoto>> fetchIncidentPhotosForTrip(String? tripId) async {
  final id = tripId?.trim() ?? '';
  if (id.isEmpty) return [];

  // ⚠️ Query by driverId, NOT tripId. The `incidentReport` read rule is gated
  // per-document on `driverId == auth.uid` (firestore.rules:93-99). Firestore
  // rejects a query wholesale unless its constraints guarantee every match is
  // readable, so a `tripId`-only query is denied (→ empty, silently). Incidents
  // are always written with `driverId = auth uid` (incident_report_page.dart:146),
  // so filtering by uid satisfies the rule (single-field index); we narrow to the
  // trip in memory.
  final uid = FirebaseAuth.instance.currentUser?.uid;
  if (uid == null || uid.isEmpty) return [];

  try {
    final snap = await FirebaseFirestore.instance
        .collection(incidentReportCollection)
        .where('driverId', isEqualTo: uid)
        .get();

    // Narrow to this trip, then order reports by createdAt asc (in memory).
    final docs = snap.docs
        .where((d) => (d.data()['tripId'] as String?)?.trim() == id)
        .toList()
      ..sort((a, b) {
        final am =
            (a.data()['createdAt'] as Timestamp?)?.millisecondsSinceEpoch ?? 0;
        final bm =
            (b.data()['createdAt'] as Timestamp?)?.millisecondsSinceEpoch ?? 0;
        return am.compareTo(bm);
      });

    final out = <IncidentPhoto>[];
    for (var seq = 0; seq < docs.length; seq++) {
      final data = docs[seq].data();
      for (final field in _incidentPhotoFields) {
        final url = (data[field[1]] as String?)?.trim();
        if (url != null && url.isNotEmpty) {
          out.add(IncidentPhoto(url: url, type: field[0], reportSeq: seq));
        }
      }
    }
    if (out.isEmpty) return out;

    // Drop photos whose Storage object is actually gone (404/403) so a report
    // whose image files were deleted shows the green "no delay report" banner
    // instead of broken 404 thumbnails. A network error keeps the photo (so a
    // real report is never hidden by a temporary connectivity blip).
    final checked = await Future.wait(
      out.map((p) async => (await _incidentPhotoExists(p.url)) ? p : null),
    );
    return checked.whereType<IncidentPhoto>().toList();
  } catch (_) {
    return [];
  }
}

/// True unless the URL definitively resolves to a missing object (404/403).
/// Uses a 1-byte ranged GET so it is cheap; network errors return true (keep).
Future<bool> _incidentPhotoExists(String url) async {
  try {
    final resp = await http
        .get(Uri.parse(url), headers: const {'Range': 'bytes=0-0'})
        .timeout(const Duration(seconds: 8));
    return resp.statusCode != 404 && resp.statusCode != 403;
  } catch (_) {
    return true;
  }
}
