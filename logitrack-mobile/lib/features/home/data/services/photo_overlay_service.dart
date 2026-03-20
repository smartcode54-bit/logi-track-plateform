import 'dart:convert';
import 'dart:typed_data';
import 'package:flutter/foundation.dart' show compute, debugPrint;
import 'package:flutter_dotenv/flutter_dotenv.dart';
import 'package:flutter_compass/flutter_compass.dart';
import 'package:geocoding/geocoding.dart';
import 'package:http/http.dart' as http;
import 'package:image/image.dart' as img;

/// Compass direction (short) from bearing degrees
String _compassDirection(double heading) {
  const directions = ['N', 'NE', 'E', 'SE', 'S', 'SW', 'W', 'NW'];
  final index = ((heading + 22.5) % 360 / 45).floor();
  return directions[index % 8];
}

/// Month abbreviations (Latin for bitmap font)
const _monthAbbr = [
  'Jan',
  'Feb',
  'Mar',
  'Apr',
  'May',
  'Jun',
  'Jul',
  'Aug',
  'Sep',
  'Oct',
  'Nov',
  'Dec',
];
const _weekdayShort = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];

/// Timeouts for overlay context (ลดจาก 5s/3s เพื่อไม่ให้ step overlay หน่วงเกินไป)
const Duration _kGeocodeTimeout = Duration(seconds: 3);
const Duration _kCompassTimeout = Duration(seconds: 2);

/// Fallback: Google Geocoding API เมื่อ placemarkFromCoordinates ไม่ทำงาน (เช่น prod release).
/// โปรดใส่ GOOGLE_MAPS_API_KEY ใน .env.prod ด้วย เพื่อให้ overlay แสดงที่อ่านได้ (ไม่ใช่แค่ Lat/Lng).
Future<String> _reverseGeocodeViaGoogleApi(double lat, double lng) async {
  final apiKey = dotenv.env['GOOGLE_MAPS_API_KEY']?.trim();
  if (apiKey == null || apiKey.isEmpty) {
    debugPrint(
      'Photo overlay: GOOGLE_MAPS_API_KEY ไม่มีใน .env — ใส่ใน .env.prod เพื่อให้ prod แสดงที่อยู่บนรูป',
    );
    return '';
  }

  final uri = Uri.parse(
    'https://maps.googleapis.com/maps/api/geocode/json'
    '?latlng=$lat,$lng'
    '&key=$apiKey'
    '&language=th',
  );
  try {
    final resp = await http.get(uri).timeout(_kGeocodeTimeout);
    if (resp.statusCode != 200) return '';

    final data = jsonDecode(resp.body) as Map<String, dynamic>?;
    final results = data?['results'] as List<dynamic>?;
    if (results == null || results.isEmpty) return '';

    final first = results.first as Map<String, dynamic>;
    final formatted = first['formatted_address'] as String?;
    if (formatted == null || formatted.isEmpty) return '';

    // คืนค่าที่อยู่เต็มจาก formatted_address Directy เพื่อให้แสดงผลแบบเต็ม (เหมือนเครื่อง test หรือตามที่ google คืนมา)
    return formatted;

  } catch (e) {
    debugPrint('Google Geocoding API fallback failed: $e');
    return '';
  }
}

/// Pre-fetch address and compass once (call before stamping multiple photos)
Future<OverlayContext> fetchOverlayContext(double lat, double lng) async {
  String address = '';
  double? heading;

  // Reverse geocode (จุดช้า: network call) — ใช้ native ก่อน, fallback Google API เมื่อ prod ไม่แสดง
  try {
    final placemarks = await placemarkFromCoordinates(
      lat,
      lng,
    ).timeout(_kGeocodeTimeout);
    if (placemarks.isNotEmpty) {
      final p = placemarks.first;
      final parts = <String>[];
      if (p.subThoroughfare != null && p.subThoroughfare!.isNotEmpty) {
        parts.add(p.subThoroughfare!);
      }
      if (p.thoroughfare != null && p.thoroughfare!.isNotEmpty) {
        parts.add(p.thoroughfare!);
      }

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
    debugPrint('Reverse geocode (native) failed: $e');
  }

  // Fallback: Google Geocoding API เมื่อ native ไม่ได้ที่อยู่ "จริง ๆ"
  // - address ว่างเลย
  // - หรือได้แค่เลขรหัสไปรษณีย์ล้วน ๆ (เช่น "13170") ซึ่งอ่านยากเกินไป
  final compact = address.trim().replaceAll(RegExp(r'\s+'), '');
  final looksLikeJustPostal = RegExp(r'^\d{4,6}$').hasMatch(compact);
  if (address.isEmpty || looksLikeJustPostal) {
    final googleAddress = await _reverseGeocodeViaGoogleApi(lat, lng);
    if (googleAddress.isNotEmpty) {
      // ถ้า native ให้แค่รหัสไปรษณีย์ ให้แสดง "13170 จังหวัด/อำเภอ/ฯลฯ"
      address = looksLikeJustPostal
          ? '${address.trim()} $googleAddress'.trim()
          : googleAddress;
    }
  }

  // Compass (จุดช้า: sensor; timeout สั้นเพื่อไม่หน่วง)
  try {
    final event = await FlutterCompass.events?.first.timeout(_kCompassTimeout);
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
  // ปรับขนาดรูปก่อนวาด overlay: เปลี่ยน 1024 เป็นค่าอื่น (px) ถ้าอยากให้ overlay ใหญ่/เล็กตามความละเอียดรูป
  if (decoded.width > 1024) {
    decoded = img.copyResize(decoded, width: 1024);
  }

  final ts = p.timestamp;
  final dayNum = ts.day.toString();
  final monthYearStr = '${_monthAbbr[ts.month - 1]} ${ts.year}';
  final weekday = _weekdayShort[ts.weekday - 1];
  final hourStr = ts.hour.toString().padLeft(2, '0');
  final minuteStr = ts.minute.toString().padLeft(2, '0');
  final secondStr = ts.second.toString().padLeft(2, '0');
  final timeLine = '$hourStr:$minuteStr:$secondStr';

  final coordLine =
      'Lat: ${p.lat.toStringAsFixed(6)}, Lng: ${p.lng.toStringAsFixed(6)}';
  final compassPart = p.heading != null
      ? '${p.heading!.toStringAsFixed(0)} ${_compassDirection(p.heading!)}'
      : null;
  final tempPart = p.temperature != null
      ? '${p.temperature!.toStringAsFixed(2)}°'
      : null;
  // อุณหภูมิ + เข็มทิศ บรรทัดเดียวกัน (เหมือนในรูปอ้างอิง)
  final tempCompassLine = [?tempPart, ?compassPart].join('   ');

  // ─── ปรับขนาดตัวอักษร (package image มี arial14, arial24, arial48)
  final fontSmall = img.arial24;
  final fontLarge = img.arial48;
  final fontRight =
      img.arial24; // ที่อยู่ + ทิศ (ลดขนาด; package มีแค่ 14/24/48)

  // ─── ปรับขนาด overlay (แก้ค่าตรงนี้แล้วรันใหม่จะเห็นผล)
  // • leftPanelWidth = ความกว้างแผงซ้าย (วัน/เวลา). ลด 10% จาก 1/3 → 0.3 * width
  final leftPanelWidth = ((decoded.width / 3) * 0.9).round().clamp(180, 342);
  // • paddingH / paddingV = ช่องว่างขอบซ้าย–ขวา / บน–ล่าง ของข้อความ (px)
  const paddingH = 18;
  const paddingV = 10; // 0.5x (เดิม 20)
  // • lineHeightSmall = ความสูง 1 บรรทัดของข้อความเล็ก (เดือน, ที่อยู่, พิกัด ฯลฯ). ยิ่งมาก overlay สูงขึ้น
  const lineHeightSmall = 36; // 0.5x (เดิม 72)
  // • lineHeightBig = ความสูงของบรรทัด “วันที่” กับ “เวลา”. อยากให้เลขวัน/เวลาใหญ่เด่น → เพิ่มค่า
  const lineHeightBig = 55; // 0.5x (เดิม 110)
  const lineHeightRight = 33; // ความสูง 1 บรรทัดฝั่งขวา (36 * 0.9 ≈ ลด 10%)

  final addrLines = _wrapText(
    p.address,
    decoded.width - leftPanelWidth - paddingH * 3,
    14, // charWidth สำหรับ arial24 (ที่อยู่ + ทิศ ลดขนาด)
  );
  var rightLineCount = addrLines.length + 1; // ที่อยู่ + พิกัด
  if (tempCompassLine.isNotEmpty)
    rightLineCount += 1; // อุณหภูมิ+เข็มทิศบรรทัดเดียว

  final leftHeight = lineHeightBig + lineHeightSmall * 3 + paddingV * 2;
  final rightHeight = lineHeightRight * rightLineCount + paddingV * 2;
  final overlayHeight = leftHeight > rightHeight ? leftHeight : rightHeight;

  final overlayTop = decoded.height - overlayHeight;
  if (overlayTop <= 0) return img.encodeJpg(decoded, quality: 75);

  // พื้นหลังเทาอ่อน ตามรูปอ้างอิง (ซ้ายเข้มกว่านิดหนึ่ง)
  final overlayColor = img.ColorRgba8(235, 235, 235, 250);
  final leftPanelColor = img.ColorRgba8(220, 220, 220, 252);
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

  // สีตามรูป: ข้อความหลักแดง, วันในสัปดาห์+เวลาเทาเข้ม
  final colorRed = img.ColorRgba8(180, 50, 50, 255);
  final colorDarkGrey = img.ColorRgba8(80, 80, 80, 255);

  var y = overlayTop + paddingV;
  img.drawString(
    decoded,
    dayNum,
    font: fontLarge,
    x: paddingH,
    y: y,
    color: colorRed,
  );
  y += lineHeightBig;
  img.drawString(
    decoded,
    monthYearStr,
    font: fontSmall,
    x: paddingH,
    y: y,
    color: colorRed,
  );
  y += lineHeightSmall;
  img.drawString(
    decoded,
    weekday,
    font: fontSmall,
    x: paddingH,
    y: y,
    color: colorDarkGrey,
  );
  y += lineHeightSmall;
  img.drawString(
    decoded,
    timeLine,
    font: fontLarge,
    x: paddingH,
    y: y,
    color: colorDarkGrey,
  );

  final rightX = leftPanelWidth + paddingH;
  var ry = overlayTop + paddingV;
  for (final line in addrLines) {
    img.drawString(
      decoded,
      line,
      font: fontRight,
      x: rightX,
      y: ry,
      color: colorRed,
    );
    ry += lineHeightRight;
  }
  img.drawString(
    decoded,
    coordLine,
    font: fontRight,
    x: rightX,
    y: ry,
    color: colorRed,
  );
  ry += lineHeightRight;
  if (tempCompassLine.isNotEmpty) {
    img.drawString(
      decoded,
      tempCompassLine,
      font: fontRight,
      x: rightX,
      y: ry,
      color: colorRed,
    );
  }

  return img.encodeJpg(decoded, quality: 75);
}

/// Simple text wrapping สำหรับที่อยู่หลายบรรทัด
/// [charWidth] ประมาณความกว้างต่อตัวอักษร (px): arial24≈14, arial48≈21
List<String> _wrapText(String text, int maxWidth, [int charWidth = 14]) {
  if (text.isEmpty) return [''];
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
