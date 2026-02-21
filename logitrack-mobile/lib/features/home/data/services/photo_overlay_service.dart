import 'dart:typed_data';
import 'package:flutter/foundation.dart' show compute, debugPrint;
import 'package:geocoding/geocoding.dart';
import 'package:flutter_compass/flutter_compass.dart';
import 'package:flutter_image_compress/flutter_image_compress.dart';
import 'package:image/image.dart' as img;

/// Compass direction in Thai from bearing degrees
String _compassDirectionThai(double heading) {
  const directions = ['N', 'NE', 'E', 'SE', 'S', 'SW', 'W', 'NW'];
  final index = ((heading + 22.5) % 360 / 45).floor();
  return directions[index % 8];
}

/// Pre-fetch address and compass once (call before stamping multiple photos)
Future<OverlayContext> fetchOverlayContext(double lat, double lng) async {
  String address = '';
  double? heading;

  // Reverse geocode (with timeout)
  try {
    final placemarks = await placemarkFromCoordinates(
      lat,
      lng,
    ).timeout(const Duration(seconds: 5));
    if (placemarks.isNotEmpty) {
      final p = placemarks.first;
      final parts = <String>[];
      if (p.subLocality != null && p.subLocality!.isNotEmpty) {
        parts.add(p.subLocality!);
      }
      if (p.locality != null && p.locality!.isNotEmpty) {
        parts.add(p.locality!);
      }
      if (p.subAdministrativeArea != null &&
          p.subAdministrativeArea!.isNotEmpty) {
        parts.add(p.subAdministrativeArea!);
      }
      if (p.administrativeArea != null && p.administrativeArea!.isNotEmpty) {
        parts.add(p.administrativeArea!);
      }
      if (p.postalCode != null && p.postalCode!.isNotEmpty) {
        parts.add(p.postalCode!);
      }
      address = parts.join(' ');
    }
  } catch (e) {
    debugPrint('Reverse geocode failed: $e');
  }

  // Compass (with timeout)
  try {
    final event = await FlutterCompass.events?.first.timeout(
      const Duration(seconds: 3),
    );
    heading = event?.heading;
  } catch (e) {
    debugPrint('Compass failed: $e');
  }

  return OverlayContext(address: address, heading: heading);
}

/// Cached overlay data (address + compass heading)
class OverlayContext {
  final String address;
  final double? heading;
  OverlayContext({required this.address, this.heading});
}

/// Stamp image with rich overlay. Uses pre-fetched [OverlayContext] so
/// geocod/compass calls happen only once.
Future<List<int>> overlayGeocodingAndTimestamp({
  required List<int> imageBytes,
  required double lat,
  required double lng,
  required DateTime timestamp,
  String? address,
  OverlayContext? ctx,
}) async {
  // 1. Resize to max width 1024px, JPEG quality 75% (70–80% for OCR/evidence clarity)
  List<int> compressedBytes = imageBytes;
  try {
    final result = await FlutterImageCompress.compressWithList(
      Uint8List.fromList(imageBytes),
      minWidth: 1024,
      minHeight: 1, // scale by width so output width ≤ 1024
      quality: 75,
      format: CompressFormat.jpeg,
    );
    compressedBytes = result.toList();
  } catch (e) {
    debugPrint('Compression failed, using original bytes: $e');
  }

  // 2. Run heavy image processing in isolate to avoid blocking UI
  try {
    return await compute(
      _processOverlay,
      _OverlayParams(
        imageBytes: compressedBytes,
        lat: lat,
        lng: lng,
        timestamp: timestamp,
        address: address ?? ctx?.address ?? '',
        heading: ctx?.heading,
      ),
    );
  } catch (e) {
    debugPrint('Overlay failed, returning original: $e');
    return compressedBytes;
  }
}

/// Parameters for isolate processing
class _OverlayParams {
  final List<int> imageBytes;
  final double lat;
  final double lng;
  final DateTime timestamp;
  final String address;
  final double? heading;

  _OverlayParams({
    required this.imageBytes,
    required this.lat,
    required this.lng,
    required this.timestamp,
    required this.address,
    this.heading,
  });
}

/// Runs in isolate — pure image processing, no Flutter/platform calls
List<int> _processOverlay(_OverlayParams p) {
  final decoded = img.decodeImage(Uint8List.fromList(p.imageBytes));
  if (decoded == null) return p.imageBytes;

  // ===== Build text content =====
  final ts = p.timestamp;
  final day = ts.day.toString().padLeft(2, '0');
  final mon = ts.month.toString().padLeft(2, '0');
  final year = ts.year;
  final dateLine = '$year-$mon-$day';

  final hour = ts.hour.toString().padLeft(2, '0');
  final minute = ts.minute.toString().padLeft(2, '0');
  final sec = ts.second.toString().padLeft(2, '0');
  final timeLine = '$hour:$minute:$sec';

  final coordLine =
      'Lat: ${p.lat.toStringAsFixed(6)}, Lng: ${p.lng.toStringAsFixed(6)}';

  final compassLine = p.heading != null
      ? '${p.heading!.toStringAsFixed(0)} ${_compassDirectionThai(p.heading!)}'
      : null;

  // ===== Layout calculations =====
  final fontSmall = img.arial14;
  final fontLarge = img.arial24;

  const leftPanelWidth = 90;
  const paddingH = 8;
  const paddingV = 6;
  const lineHeight14 = 18;
  const lineHeight24 = 28;

  // Calculate lines for right panel
  final addrLines = _wrapText(
    p.address,
    decoded.width - leftPanelWidth - paddingH * 3,
  );
  final rightLineCount = addrLines.length + 1 + (compassLine != null ? 1 : 0);

  final leftHeight = lineHeight24 + lineHeight14 + paddingV * 2;
  final rightHeight = lineHeight14 * rightLineCount + paddingV * 2;
  final overlayHeight = leftHeight > rightHeight ? leftHeight : rightHeight;

  final overlayTop = decoded.height - overlayHeight;
  if (overlayTop <= 0) return img.encodePng(decoded);

  // ===== Draw overlay =====

  // Dark background
  img.fillRect(
    decoded,
    x1: 0,
    y1: overlayTop,
    x2: decoded.width,
    y2: decoded.height,
    color: img.ColorRgba8(0, 0, 0, 200),
  );

  // Left panel (teal)
  img.fillRect(
    decoded,
    x1: 0,
    y1: overlayTop,
    x2: leftPanelWidth,
    y2: decoded.height,
    color: img.ColorRgba8(0, 100, 80, 230),
  );

  // Date (large)
  var y = overlayTop + paddingV;
  img.drawString(
    decoded,
    dateLine,
    font: fontSmall,
    x: paddingH,
    y: y,
    color: img.ColorRgba8(255, 255, 255, 255),
  );
  y += lineHeight14;

  // Time
  img.drawString(
    decoded,
    timeLine,
    font: fontLarge,
    x: paddingH,
    y: y,
    color: img.ColorRgba8(255, 255, 255, 255),
  );

  // ===== Right panel =====
  final rightX = leftPanelWidth + paddingH;
  var ry = overlayTop + paddingV;

  // Address (green text)
  for (final line in addrLines) {
    img.drawString(
      decoded,
      line,
      font: fontSmall,
      x: rightX,
      y: ry,
      color: img.ColorRgba8(0, 220, 100, 255),
    );
    ry += lineHeight14;
  }

  // Coordinates (white)
  img.drawString(
    decoded,
    coordLine,
    font: fontSmall,
    x: rightX,
    y: ry,
    color: img.ColorRgba8(255, 255, 255, 255),
  );
  ry += lineHeight14;

  // Compass (yellow)
  if (compassLine != null) {
    img.drawString(
      decoded,
      compassLine,
      font: fontSmall,
      x: rightX,
      y: ry,
      color: img.ColorRgba8(255, 200, 50, 255),
    );
  }

  return img.encodePng(decoded);
}

/// Simple text wrapping
List<String> _wrapText(String text, int maxWidth) {
  if (text.isEmpty) return [''];
  const charWidth = 8;
  final maxChars = maxWidth ~/ charWidth;
  if (maxChars <= 0) return [text];

  final words = text.split(' ');
  final lines = <String>[];
  var current = '';
  for (final word in words) {
    if (current.isEmpty) {
      current = word;
    } else if ((current.length + 1 + word.length) <= maxChars) {
      current += ' $word';
    } else {
      lines.add(current);
      current = word;
    }
  }
  if (current.isNotEmpty) lines.add(current);
  return lines.isEmpty ? [''] : lines;
}
