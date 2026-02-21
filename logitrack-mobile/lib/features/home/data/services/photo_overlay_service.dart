import 'dart:typed_data';
import 'package:flutter/foundation.dart' show compute, debugPrint;
import 'package:geocoding/geocoding.dart';
import 'package:flutter_compass/flutter_compass.dart';
import 'package:image/image.dart' as img;

/// Compass direction (short) from bearing degrees
String _compassDirection(double heading) {
  const directions = ['N', 'NE', 'E', 'SE', 'S', 'SW', 'W', 'NW'];
  final index = ((heading + 22.5) % 360 / 45).floor();
  return directions[index % 8];
}

/// Month abbreviations (Latin for bitmap font)
const _monthAbbr = [
  'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
  'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
];
const _weekdayShort = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];

/// Timeouts for overlay context (ลดจาก 5s/3s เพื่อไม่ให้ step overlay หน่วงเกินไป)
const Duration _kGeocodeTimeout = Duration(seconds: 3);
const Duration _kCompassTimeout = Duration(seconds: 2);

/// Pre-fetch address and compass once (call before stamping multiple photos)
Future<OverlayContext> fetchOverlayContext(double lat, double lng) async {
  String address = '';
  double? heading;

  // Reverse geocode (จุดช้า: network call)
  try {
    final placemarks = await placemarkFromCoordinates(
      lat,
      lng,
    ).timeout(_kGeocodeTimeout);
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

  // Compass (จุดช้า: sensor; timeout สั้นเพื่อไม่หน่วง)
  try {
    final event = await FlutterCompass.events?.first.timeout(
      _kCompassTimeout,
    );
    heading = event?.heading;
  } catch (e) {
    debugPrint('Compass failed: $e');
  }

  return OverlayContext(address: address, heading: heading, temperature: null);
}

/// Cached overlay data (address, compass heading, optional temperature)
class OverlayContext {
  final String address;
  final double? heading;
  final double? temperature;
  OverlayContext({required this.address, this.heading, this.temperature});
}

/// Stamp image with evidence overlay (date, time, location, coords, compass, optional temp).
/// Uses pre-fetched [OverlayContext] so geocode/compass calls happen only once.
///
/// จุดที่เคยทำให้ช้า: (1) compress บน main thread ก่อน compute → ย้ายไปทำใน isolate
/// (2) geocode timeout 5s / compass 3s → ลดเหลือ 3s/2s
/// (3) แต่ละรูปเรียก getCurrentPosition + fetchOverlayContext → ใช้ [ctx] แบบ reuse ได้
Future<List<int>> overlayGeocodingAndTimestamp({
  required List<int> imageBytes,
  required double lat,
  required double lng,
  required DateTime timestamp,
  String? address,
  OverlayContext? ctx,
  double? temperature,
}) async {
  // ทำทั้งหมดใน isolate: decode, resize (ถ้า >1024), วาด overlay, encode JPEG — ไม่ block UI
  try {
    return await compute(
      _processOverlay,
      _OverlayParams(
        imageBytes: imageBytes,
        lat: lat,
        lng: lng,
        timestamp: timestamp,
        address: address ?? ctx?.address ?? '',
        heading: ctx?.heading,
        temperature: temperature ?? ctx?.temperature,
      ),
    );
  } catch (e) {
    debugPrint('Overlay failed, returning original: $e');
    return imageBytes;
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
  final double? temperature;

  _OverlayParams({
    required this.imageBytes,
    required this.lat,
    required this.lng,
    required this.timestamp,
    required this.address,
    this.heading,
    this.temperature,
  });
}

/// Runs in isolate — decode, resize to max 1024px, draw overlay, encode JPEG (ไม่ block UI).
/// Template: left = big day, month year, weekday, time (AM/PM); right = address, coords, temp, compass.
List<int> _processOverlay(_OverlayParams p) {
  var decoded = img.decodeImage(Uint8List.fromList(p.imageBytes));
  if (decoded == null) return p.imageBytes;
  if (decoded.width > 1024) {
    decoded = img.copyResize(decoded, width: 1024);
  }

  final ts = p.timestamp;
  final dayNum = ts.day.toString();
  final monthYearStr = '${_monthAbbr[ts.month - 1]} ${ts.year}';
  final weekday = _weekdayShort[ts.weekday - 1];
  final hour12 = ts.hour % 12;
  final hourStr = (hour12 == 0 ? 12 : hour12).toString();
  final minuteStr = ts.minute.toString().padLeft(2, '0');
  final amPm = ts.hour < 12 ? 'AM' : 'PM';
  final timeLine = '$hourStr:$minuteStr $amPm';

  final coordLine =
      'Lat: ${p.lat.toStringAsFixed(6)}, Lng: ${p.lng.toStringAsFixed(6)}';
  final compassLine = p.heading != null
      ? '${p.heading!.toStringAsFixed(0)} ${_compassDirection(p.heading!)}'
      : null;
  final tempLine = p.temperature != null
      ? '${p.temperature!.toStringAsFixed(2)}°'
      : null;

  // 2.5x larger for readability (arial14→24, arial24→48)
  final fontSmall = img.arial24;
  final fontLarge = img.arial48;

  // Scaled ~2.5x from original (line heights and panel for larger fonts)
  const leftPanelWidth = 200;
  const paddingH = 18;
  const paddingV = 20;
  const lineHeight14 = 72;  // was 36
  const lineHeight24 = 96;  // was 44
  const lineHeightBig = 110; // day number, was 72

  final addrLines = _wrapText(
    p.address,
    decoded.width - leftPanelWidth - paddingH * 4,
  );
  var rightLineCount = addrLines.length + 1; // address + coords
  if (compassLine != null) rightLineCount += 1;
  if (tempLine != null) rightLineCount += 1;

  final leftHeight = lineHeightBig + lineHeight14 * 3 + paddingV * 2;
  final rightHeight = lineHeight14 * rightLineCount + paddingV * 2;
  final overlayHeight = leftHeight > rightHeight ? leftHeight : rightHeight;

  final overlayTop = decoded.height - overlayHeight;
  if (overlayTop <= 0) return img.encodeJpg(decoded, quality: 75);

  // Terracotta/reddish-brown background (like template)
  final overlayColor = img.ColorRgba8(180, 95, 75, 245);
  final leftPanelColor = img.ColorRgba8(160, 85, 65, 250);
  img.fillRect(
    decoded,
    x1: 0,
    y1: overlayTop,
    x2: decoded.width,
    y2: decoded.height,
    color: overlayColor,
  );
  img.fillRect(
    decoded,
    x1: 0,
    y1: overlayTop,
    x2: leftPanelWidth,
    y2: decoded.height,
    color: leftPanelColor,
  );

  var y = overlayTop + paddingV;
  img.drawString(
    decoded,
    dayNum,
    font: fontLarge,
    x: paddingH,
    y: y,
    color: img.ColorRgba8(255, 255, 255, 255),
  );
  y += lineHeightBig;
  img.drawString(
    decoded,
    monthYearStr,
    font: fontSmall,
    x: paddingH,
    y: y,
    color: img.ColorRgba8(255, 255, 255, 255),
  );
  y += lineHeight14;
  img.drawString(
    decoded,
    weekday,
    font: fontSmall,
    x: paddingH,
    y: y,
    color: img.ColorRgba8(255, 255, 255, 255),
  );
  y += lineHeight14;
  img.drawString(
    decoded,
    timeLine,
    font: fontLarge,
    x: paddingH,
    y: y,
    color: img.ColorRgba8(255, 255, 255, 255),
  );

  final rightX = leftPanelWidth + paddingH;
  var ry = overlayTop + paddingV;
  for (final line in addrLines) {
    img.drawString(
      decoded,
      line,
      font: fontSmall,
      x: rightX,
      y: ry,
      color: img.ColorRgba8(255, 255, 255, 255),
    );
    ry += lineHeight14;
  }
  img.drawString(
    decoded,
    coordLine,
    font: fontSmall,
    x: rightX,
    y: ry,
    color: img.ColorRgba8(255, 255, 255, 255),
  );
  ry += lineHeight14;
  if (tempLine != null) {
    img.drawString(
      decoded,
      tempLine,
      font: fontSmall,
      x: rightX,
      y: ry,
      color: img.ColorRgba8(255, 220, 200, 255),
    );
    ry += lineHeight14;
  }
  if (compassLine != null) {
    img.drawString(
      decoded,
      compassLine,
      font: fontSmall,
      x: rightX,
      y: ry,
      color: img.ColorRgba8(255, 200, 150, 255),
    );
  }

  return img.encodeJpg(decoded, quality: 75);
}

/// Simple text wrapping (charWidth tuned for arial24)
List<String> _wrapText(String text, int maxWidth) {
  if (text.isEmpty) return [''];
  const charWidth = 14; // ~2.5x from 8 for arial24
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
