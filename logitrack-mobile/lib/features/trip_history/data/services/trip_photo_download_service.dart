import 'package:gal/gal.dart';
import 'package:http/http.dart' as http;

import '../../../home/data/models/trip_record.dart';
import '../trip_incident_photos.dart';
import '../trip_photo_order.dart';

/// Gallery album that every downloaded trip photo lands in (ADR 0018 §4).
const String logiTrackAlbum = 'LogiTrack';

/// One ordered, downloadable photo of a trip — a trip evidence photo or an
/// incident photo, already sorted by workflow rank.
class TripPhotoItem {
  final String url;
  final String type;
  final bool isIncident;

  /// 0-based incident-report order (only meaningful when [isIncident]).
  final int reportSeq;

  /// Precomputed workflow sort rank (see trip_photo_order.dart).
  final int sortKey;

  const TripPhotoItem({
    required this.url,
    required this.type,
    required this.isIncident,
    required this.reportSeq,
    required this.sortKey,
  });
}

/// Result of a bulk "download all" — best-effort (ADR 0018 §6).
class TripPhotoDownloadResult {
  final int saved;
  final int total;
  final List<String> failures;

  const TripPhotoDownloadResult({
    required this.saved,
    required this.total,
    required this.failures,
  });

  bool get savedNone => total > 0 && saved == 0;
}

/// Thrown when the user declines photo-library access so the UI can show the
/// permission-denied path (open settings) instead of a generic error.
class PhotoPermissionDeniedException implements Exception {
  const PhotoPermissionDeniedException();
}

/// Re-request add-only photo access (used by the "open settings" retry).
/// Returns true if access is granted. Never throws.
Future<bool> requestPhotoAccess() async {
  try {
    return await Gal.requestAccess(toAlbum: true);
  } catch (_) {
    return false;
  }
}

/// Build the ordered list of a trip's downloadable photos: trip photos
/// (`TripRecord.photos`) + incident photos (`incidentReport` by `tripId`),
/// sorted by workflow rank. Used by the viewer to render and by the download.
Future<List<TripPhotoItem>> loadOrderedTripPhotos(TripRecord trip) async {
  final items = <TripPhotoItem>[];

  for (final p in trip.photos) {
    if (p.url.trim().isEmpty) continue;
    items.add(TripPhotoItem(
      url: p.url,
      type: p.type,
      isIncident: false,
      reportSeq: 0,
      sortKey: tripPhotoWorkflowRank(p.type),
    ));
  }

  final incidents = await fetchIncidentPhotosForTrip(trip.id);
  for (final ip in incidents) {
    items.add(TripPhotoItem(
      url: ip.url,
      type: ip.type,
      isIncident: true,
      reportSeq: ip.reportSeq,
      sortKey: incidentPhotoRank(ip.reportSeq, ip.type),
    ));
  }

  items.sort((a, b) => a.sortKey.compareTo(b.sortKey));
  return items;
}

String _safeTripId(TripRecord trip) {
  final raw = (trip.id ?? trip.spxTripId ?? 'trip').trim();
  final cleaned = raw.replaceAll(RegExp(r'[^A-Za-z0-9_-]'), '');
  return cleaned.isEmpty ? 'trip' : cleaned;
}

/// File name for a saved photo (ADR 0018 §4). [ordinal] is the 1-based position
/// in the sorted sequence, zero-padded to 2 digits so gallery name-sort matches
/// workflow order.
String _fileNameFor(TripPhotoItem item, String tripId, String roundStamp, int ordinal) {
  final nn = ordinal.toString().padLeft(2, '0');
  if (item.isIncident) {
    return 'LogiTrack_${tripId}_${roundStamp}_$nn-incident${item.reportSeq + 1}-${item.type}';
  }
  return 'LogiTrack_${tripId}_${roundStamp}_$nn-${item.type}';
}

/// Save every photo in [items] to the LogiTrack gallery album. Best-effort: one
/// failure does not abort the rest; returns saved/total + failed file names.
///
/// Requests add-only photo access first (ADR 0018 §9); throws
/// [PhotoPermissionDeniedException] if the user declines.
Future<TripPhotoDownloadResult> saveTripPhotosToGallery(
  TripRecord trip,
  List<TripPhotoItem> items,
) async {
  if (items.isEmpty) {
    return const TripPhotoDownloadResult(saved: 0, total: 0, failures: []);
  }

  final hasAccess = await Gal.hasAccess(toAlbum: true);
  if (!hasAccess) {
    final granted = await Gal.requestAccess(toAlbum: true);
    if (!granted) throw const PhotoPermissionDeniedException();
  }

  final tripId = _safeTripId(trip);
  final roundStamp = await resolveAssignedRoundStamp(trip);

  var saved = 0;
  final failures = <String>[];

  for (var i = 0; i < items.length; i++) {
    final item = items[i];
    final name = _fileNameFor(item, tripId, roundStamp, i + 1);
    try {
      final resp = await http
          .get(Uri.parse(item.url))
          .timeout(const Duration(seconds: 30));
      if (resp.statusCode != 200 || resp.bodyBytes.isEmpty) {
        failures.add(name);
        continue;
      }
      await Gal.putImageBytes(resp.bodyBytes, album: logiTrackAlbum, name: name);
      saved++;
    } catch (_) {
      failures.add(name);
    }
  }

  return TripPhotoDownloadResult(
    saved: saved,
    total: items.length,
    failures: failures,
  );
}
