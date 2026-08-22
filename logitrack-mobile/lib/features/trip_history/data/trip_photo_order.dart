import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:intl/intl.dart';

import '../../home/data/models/trip_record.dart';

/// Workflow ordering for a trip's [[Photo type]] — see ADR 0018 / the spec
/// `shared-docs/specs/mobile-download-trip-photos.md` §Design.
///
/// The stored `trip_records.photos[]` order is insertion/replace order
/// (`mergeTripPhotosReplacingTypes`), NOT workflow order, so we sort by this rank.
///
/// Groups (lower sorts first):
///   1000  loading   : runsheet, runsheet_extra_1..3, pre_close, closing, seal
///   2000  delivery  : pre_open, opening, empty_container, runsheet_received
///   3000  multi-stop: stop_{index}_{subtype} — by index, then subtype
///   4000  incident  : handled via [incidentPhotoRank] (different collection)
///   9000  unknown/legacy → last, stable
const List<String> _loadingOrder = [
  'runsheet',
  'runsheet_extra_1',
  'runsheet_extra_2',
  'runsheet_extra_3',
  'pre_close',
  'closing',
  'seal',
];

const List<String> _deliveryOrder = [
  'pre_open',
  'opening',
  'empty_container',
  'runsheet_received',
];

/// Sort rank for a trip photo `type`. Incident photos use [incidentPhotoRank].
int tripPhotoWorkflowRank(String type) {
  final t = type.trim();

  final li = _loadingOrder.indexOf(t);
  if (li >= 0) return 1000 + li;

  final di = _deliveryOrder.indexOf(t);
  if (di >= 0) return 2000 + di;

  // Multi-stop: stop_{index}_{subtype}
  final m = RegExp(r'^stop_(\d+)_(.+)$').firstMatch(t);
  if (m != null) {
    final idx = int.tryParse(m.group(1)!) ?? 0;
    final sub = m.group(2)!;
    final si = _deliveryOrder.indexOf(sub);
    return 3000 + idx * 10 + (si >= 0 ? si : 9);
  }

  return 9000; // unknown / legacy → last
}

/// Sort rank for an incident photo. [reportSeq] orders reports (by createdAt);
/// within a report: map → situation1 → situation2. Sits after all trip photos
/// (4000) but before unknown trip types (9000).
int incidentPhotoRank(int reportSeq, String type) {
  const sub = {'map': 0, 'situation1': 1, 'situation2': 2};
  return 4000 + reportSeq * 10 + (sub[type.trim()] ?? 9);
}

String _stampFromDate(DateTime d) => DateFormat('yyyyMMdd-HHmm').format(d);

/// Resolve the [[Assigned round]] stamp (`yyyyMMdd-HHmm`) used in downloaded file
/// names. Reads `task.date` + `task.time` via `trip.taskId` (one read); falls back
/// to `trip.createdAt` when there is no taskId / task / valid time (ADR 0018 §5).
/// Never throws — a missing stamp must not block the download.
Future<String> resolveAssignedRoundStamp(TripRecord trip) async {
  final taskId = trip.taskId?.trim() ?? '';
  if (taskId.isNotEmpty) {
    try {
      final doc = await FirebaseFirestore.instance
          .collection('tasks')
          .doc(taskId)
          .get();
      if (doc.exists) {
        final data = doc.data() ?? {};
        final rawDate = data['date'];
        DateTime? date;
        if (rawDate is Timestamp) {
          date = rawDate.toDate();
        } else if (rawDate is DateTime) {
          date = rawDate;
        }
        final time = (data['time'] as String?)?.trim() ?? '';
        if (date != null) {
          final parts = time.split(':');
          if (parts.length == 2) {
            final hh = int.tryParse(parts[0]);
            final mm = int.tryParse(parts[1]);
            if (hh != null && mm != null) {
              return _stampFromDate(
                DateTime(date.year, date.month, date.day, hh, mm),
              );
            }
          }
          // Have a date but no usable time → date at midnight.
          return _stampFromDate(
            DateTime(date.year, date.month, date.day),
          );
        }
      }
    } catch (_) {
      // fall through to createdAt
    }
  }

  final created = trip.createdAt;
  if (created != null) return _stampFromDate(created);
  return 'unknown';
}
