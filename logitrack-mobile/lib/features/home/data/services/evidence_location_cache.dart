import 'package:flutter/foundation.dart' show debugPrint;
import 'package:geolocator/geolocator.dart';

import '../repositories/checkin_repository.dart';
import 'photo_overlay_service.dart';

/// GPS fix + overlay context (address, compass, static-map pin) for stamping evidence photos.
///
/// The pages used to cache the first fix forever: the loading page and single-stop delivery page
/// live in MainLayout's IndexedStack and never cleared it, and the multi-drop page read GPS once in
/// initState. Every later photo — the next stop, even the next trip — carried the old pin.
///
/// This cache reuses a fix only for [maxAge] (a burst of photos at one stop costs one GPS read)
/// and re-fetches the overlay context only when the fix moved more than [reuseRadiusMeters]
/// (geocode + static map are the slow, billed part). A failed read returns no position rather
/// than an expired one — a missing pin is honest, a wrong pin is not.
class EvidenceLocationCache {
  EvidenceLocationCache({
    this.maxAge = const Duration(seconds: 60),
    this.reuseRadiusMeters = 100,
  });

  final Duration maxAge;
  final double reuseRadiusMeters;

  Position? _position;
  DateTime? _fixedAt;
  OverlayContext? _context;
  Position? _contextPosition;

  /// Fix used by the most recent [current] call (null if none or it failed).
  Position? get lastPosition => _position;

  Future<({Position? position, OverlayContext? context})> current() async {
    final fixedAt = _fixedAt;
    if (_position == null ||
        fixedAt == null ||
        DateTime.now().difference(fixedAt) > maxAge) {
      try {
        _position = await getCurrentPosition();
        _fixedAt = DateTime.now();
      } catch (e) {
        debugPrint('[EvidenceLocationCache] GPS read failed: $e');
        reset();
        return (position: null, context: null);
      }
    }

    final pos = _position!;
    final ctxPos = _contextPosition;
    final contextStale = _context == null ||
        ctxPos == null ||
        Geolocator.distanceBetween(
              ctxPos.latitude,
              ctxPos.longitude,
              pos.latitude,
              pos.longitude,
            ) >
            reuseRadiusMeters;
    if (contextStale) {
      try {
        _context = await fetchOverlayContext(pos.latitude, pos.longitude);
        _contextPosition = pos;
      } catch (e) {
        debugPrint('[EvidenceLocationCache] overlay context failed: $e');
        _context = null;
        _contextPosition = null;
      }
    }
    return (position: pos, context: _context);
  }

  void reset() {
    _position = null;
    _fixedAt = null;
    _context = null;
    _contextPosition = null;
  }
}
