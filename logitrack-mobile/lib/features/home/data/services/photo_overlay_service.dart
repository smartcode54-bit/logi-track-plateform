import 'dart:convert';
import 'dart:ui' as ui;

import 'package:flutter/foundation.dart' show debugPrint;
import 'package:flutter/material.dart' show Colors;
import 'package:flutter/painting.dart';
import 'package:flutter/services.dart';
import 'package:flutter_compass/flutter_compass.dart';
import 'package:flutter_dotenv/flutter_dotenv.dart';
import 'package:geocoding/geocoding.dart';
import 'package:http/http.dart' as http;
import 'package:image/image.dart' as img;
import 'package:qr/qr.dart';

// ─── Timeouts ───────────────────────────────────────────────────────────────
const Duration _kGeocodeTimeout = Duration(seconds: 3);
const Duration _kCompassTimeout = Duration(seconds: 2);
const Duration _kStaticMapTimeout = Duration(seconds: 5);

// ─── Thai weekday / month names ──────────────────────────────────────────────
const _thaiWeekdays = ['จ.', 'อ.', 'พ.', 'พฤ.', 'ศ.', 'ส.', 'อา.'];
const _thaiMonths = [
  'ม.ค.', 'ก.พ.', 'มี.ค.', 'เม.ย.', 'พ.ค.', 'มิ.ย.',
  'ก.ค.', 'ส.ค.', 'ก.ย.', 'ต.ค.', 'พ.ย.', 'ธ.ค.',
];

String _formatThaiDateTime(DateTime dt) {
  final wd = _thaiWeekdays[dt.weekday - 1];
  final mo = _thaiMonths[dt.month - 1];
  final h = dt.hour.toString().padLeft(2, '0');
  final m = dt.minute.toString().padLeft(2, '0');
  return '$wd ${dt.day} $mo ${dt.year + 543}  $h:$m';
}

// ─── Reverse geocode via Google API ─────────────────────────────────────────
Future<String> _reverseGeocodeViaGoogleApi(double lat, double lng) async {
  final apiKey = dotenv.env['GOOGLE_MAPS_API_KEY']?.trim();
  if (apiKey == null || apiKey.isEmpty) return '';
  try {
    final uri = Uri.parse(
      'https://maps.googleapis.com/maps/api/geocode/json'
      '?latlng=$lat,$lng&key=$apiKey&language=th',
    );
    final resp = await http.get(uri).timeout(_kGeocodeTimeout);
    if (resp.statusCode != 200) return '';
    final data = jsonDecode(resp.body) as Map<String, dynamic>?;
    final results = data?['results'] as List<dynamic>?;
    if (results == null || results.isEmpty) return '';
    return (results.first as Map<String, dynamic>)['formatted_address'] as String? ?? '';
  } catch (e) {
    debugPrint('Google Geocoding fallback failed: $e');
    return '';
  }
}

// ─── Fetch Google Static Maps thumbnail ──────────────────────────────────────
Future<Uint8List?> _fetchStaticMap(double lat, double lng) async {
  final apiKey = (dotenv.env['GOOGLE_MAPS_STATIC_KEY'] ?? dotenv.env['GOOGLE_MAPS_API_KEY'])?.trim();
  if (apiKey == null || apiKey.isEmpty) return null;
  try {
    final uri = Uri.parse(
      'https://maps.googleapis.com/maps/api/staticmap'
      '?center=$lat,$lng&zoom=16&size=640x640&scale=2&maptype=roadmap'
      '&markers=color:red%7C$lat,$lng&key=$apiKey',
    );
    final resp = await http.get(uri).timeout(_kStaticMapTimeout);
    return resp.statusCode == 200 ? resp.bodyBytes : null;
  } catch (e) {
    debugPrint('Static map fetch failed: $e');
    return null;
  }
}

// ─── OverlayContext ───────────────────────────────────────────────────────────
class OverlayContext {
  final String address;
  final String cityName;
  final double? heading;
  final double? temperature;
  final Uint8List? staticMapBytes;

  OverlayContext({
    required this.address,
    this.cityName = '',
    this.heading,
    this.temperature,
    this.staticMapBytes,
  });
}

// ─── fetchOverlayContext ──────────────────────────────────────────────────────
Future<OverlayContext> fetchOverlayContext(double lat, double lng) async {
  String address = '';
  String cityName = '';
  double? heading;

  // Native reverse geocode first
  try {
    final placemarks = await placemarkFromCoordinates(lat, lng)
        .timeout(_kGeocodeTimeout);
    if (placemarks.isNotEmpty) {
      final p = placemarks.first;
      cityName = p.locality ?? p.administrativeArea ?? '';
      final parts = <String>[
        if (p.subThoroughfare?.isNotEmpty == true) p.subThoroughfare!,
        if (p.thoroughfare?.isNotEmpty == true) p.thoroughfare!,
        if (p.subLocality?.isNotEmpty == true) p.subLocality!,
        if (p.locality?.isNotEmpty == true) p.locality!,
        if (p.subAdministrativeArea?.isNotEmpty == true) p.subAdministrativeArea!,
        if (p.administrativeArea?.isNotEmpty == true) p.administrativeArea!,
        if (p.postalCode?.isNotEmpty == true) p.postalCode!,
        if (p.country?.isNotEmpty == true) p.country!,
      ];
      address = parts.join(' ');
    }
  } catch (e) {
    debugPrint('Native geocode failed: $e');
  }

  // Fallback to Google API
  final compact = address.trim().replaceAll(RegExp(r'\s+'), '');
  if (address.isEmpty || RegExp(r'^\d{4,6}$').hasMatch(compact)) {
    final ga = await _reverseGeocodeViaGoogleApi(lat, lng);
    if (ga.isNotEmpty) {
      address = ga;
      // Extract city from Google formatted address (last before postal/country)
      if (cityName.isEmpty) {
        final parts = address.split(' ');
        cityName = parts.length > 2 ? parts[parts.length - 2] : address;
      }
    }
  }

  // Compass
  try {
    final event = await FlutterCompass.events?.first.timeout(_kCompassTimeout);
    heading = event?.heading;
  } catch (_) {}

  // Static map (parallel-safe — network call, not UI)
  final staticMapBytes = await _fetchStaticMap(lat, lng);

  return OverlayContext(
    address: address,
    cityName: cityName,
    heading: heading,
    staticMapBytes: staticMapBytes,
  );
}

// ─── Public entry point (keeps same signature as before) ─────────────────────
Future<List<int>> overlayGeocodingAndTimestamp({
  required List<int> imageBytes,
  required double lat,
  required double lng,
  required DateTime timestamp,
  String? address,
  OverlayContext? ctx,
  double? temperature,
}) async {
  try {
    return await _buildOverlay(
      imageBytes: Uint8List.fromList(imageBytes),
      lat: lat,
      lng: lng,
      timestamp: timestamp,
      address: address ?? ctx?.address ?? '',
      cityName: ctx?.cityName ?? '',
      staticMapBytes: ctx?.staticMapBytes,
    );
  } catch (e) {
    debugPrint('Overlay failed, returning original: $e');
    return imageBytes;
  }
}

// ─── Main overlay builder (runs on Flutter main thread for dart:ui + TextPainter) ──
Future<List<int>> _buildOverlay({
  required Uint8List imageBytes,
  required double lat,
  required double lng,
  required DateTime timestamp,
  required String address,
  required String cityName,
  Uint8List? staticMapBytes,
}) async {
  // 1. Decode + resize photo (max 1080px)
  final codec = await ui.instantiateImageCodec(imageBytes, targetWidth: 1080);
  final photoFrame = await codec.getNextFrame();
  final photo = photoFrame.image;
  final W = photo.width.toDouble();
  final H = photo.height.toDouble();

  // 2. Decode mini-map
  ui.Image? mapThumb;
  if (staticMapBytes != null) {
    try {
      final mc = await ui.instantiateImageCodec(staticMapBytes);
      mapThumb = (await mc.getNextFrame()).image;
    } catch (_) {}
  }

  // 3. Load LogiTrack logo asset
  ui.Image? logo;
  try {
    final data = await rootBundle.load('assets/app_icon.jpg');
    final lc = await ui.instantiateImageCodec(
      data.buffer.asUint8List(),
      targetWidth: 80,
      targetHeight: 80,
    );
    logo = (await lc.getNextFrame()).image;
  } catch (_) {}

  // 4. Layout: 20% map | 60% text | 20% branding+QR
  final scale = W / 1080.0;
  final pad = 10.0 * scale;

  final mapPanelW = W * 0.20;
  final rightPanelW = W * 0.20;
  final textPanelW = W * 0.60;
  final textX = mapPanelW + pad;
  final textMaxW = textPanelW - pad * 2;

  // ── Styles (measure before deciding overlayH) ──
  final city = cityName.isNotEmpty ? cityName : 'ประเทศไทย';
  final cityStyle = _ts(52 * scale, Colors.white, weight: FontWeight.bold);
  final addrStyle = _ts(34 * scale, const Color(0xFFD1D5DB));
  final metaStyle = _ts(30 * scale, const Color(0xFF9CA3AF));
  final coordStr = 'Lat ${lat.toStringAsFixed(6)}°  Long ${lng.toStringAsFixed(6)}°';
  final dateStr = _formatThaiDateTime(timestamp);

  final cityH  = _textHeight('$city  🇹🇭', cityStyle, textMaxW);
  final addrH  = address.isNotEmpty
      ? _textHeight(address, addrStyle, textMaxW, maxLines: 2) + 3 * scale : 0.0;
  final coordH = _textHeight(coordStr, metaStyle, textMaxW) + 3 * scale;
  final dateH  = _textHeight(dateStr, metaStyle, textMaxW);
  final contentH = cityH + addrH + coordH + dateH + pad * 1.5;

  // overlayH driven by content; min = mapPanelW so map is at least square
  final overlayH = contentH.clamp(mapPanelW, H * 0.40);
  final overlayTop = H - overlayH;

  // Map fills FULL left panel height (edge-to-edge)
  final mapRect = Rect.fromLTWH(0, overlayTop, mapPanelW, overlayH);

  // Right panel: branding row (top) + QR (below)
  final brandingH = overlayH * 0.28;
  final brandingX = W - rightPanelW;
  final brandingY = overlayTop;
  final brandingW = rightPanelW;

  final qrAvail = overlayH - brandingH - pad;
  final qrSize  = qrAvail.clamp(40.0, rightPanelW - pad);
  final qrX = W - rightPanelW + (rightPanelW - qrSize) / 2;
  final qrY = overlayTop + brandingH + (qrAvail - qrSize) / 2;
  final qrRect = Rect.fromLTWH(qrX, qrY, qrSize, qrSize);

  // 5. Draw
  final recorder = ui.PictureRecorder();
  final canvas = Canvas(recorder);

  // Photo
  canvas.drawImage(photo, Offset.zero, Paint());

  // Overlay bar — 50% transparent dark
  canvas.drawRect(
    Rect.fromLTWH(0, overlayTop, W, overlayH),
    Paint()..color = const Color(0x80111827),
  );

  // Mini-map (edge-to-edge left panel, no rounding)
  if (mapThumb != null) {
    canvas.save();
    canvas.clipRect(mapRect);
    canvas.drawImageRect(
      mapThumb,
      Rect.fromLTWH(0, 0, mapThumb.width.toDouble(), mapThumb.height.toDouble()),
      mapRect,
      Paint(),
    );
    canvas.restore();
    // Right border separator
    canvas.drawLine(
      Offset(mapPanelW, overlayTop),
      Offset(mapPanelW, H),
      Paint()..color = const Color(0x33FFFFFF)..strokeWidth = 1 * scale,
    );
  } else {
    // Placeholder with pin icon centered
    canvas.drawRect(mapRect, Paint()..color = const Color(0xFF1A2535));
    _drawText(
      canvas, '📍',
      _ts(28 * scale, const Color(0xFF6B7280)),
      mapRect.center - Offset(14 * scale, 14 * scale),
    );
  }

  // Text block — vertically centered in overlay (safe clamp)
  final tyStart = overlayTop + (overlayH - contentH) / 2;
  final tyLow  = overlayTop + pad;
  final tyHigh = (H - contentH - pad).clamp(tyLow, H);
  var ty = tyStart.clamp(tyLow, tyHigh);

  _drawText(canvas, '$city  🇹🇭', cityStyle, Offset(textX, ty), maxW: textMaxW);
  ty += cityH + 3 * scale;

  if (address.isNotEmpty) {
    _drawText(canvas, address, addrStyle, Offset(textX, ty), maxW: textMaxW, maxLines: 2);
    ty += addrH;
  }

  _drawText(canvas, coordStr, metaStyle, Offset(textX, ty), maxW: textMaxW);
  ty += coordH;

  _drawText(canvas, dateStr, metaStyle, Offset(textX, ty), maxW: textMaxW);

  // ─── Branding — single row: [Logo] [LogiTrack Pro] ───
  canvas.drawRect(
    Rect.fromLTWH(brandingX, brandingY, brandingW, brandingH),
    Paint()..color = const Color(0xE61D4ED8),
  );
  canvas.drawLine(
    Offset(brandingX, brandingY + brandingH),
    Offset(W, brandingY + brandingH),
    Paint()..color = const Color(0x44FFFFFF)..strokeWidth = 1 * scale,
  );

  // Logo icon — square, vertically centered in branding row
  final iconSize = (brandingH * 0.75).clamp(16.0, 48.0);
  final iconY = brandingY + (brandingH - iconSize) / 2;
  final iconX = brandingX + pad * 0.6;

  if (logo != null) {
    canvas.save();
    canvas.clipRRect(RRect.fromRectAndRadius(
      Rect.fromLTWH(iconX, iconY, iconSize, iconSize),
      Radius.circular(iconSize * 0.15),
    ));
    canvas.drawImageRect(
      logo,
      Rect.fromLTWH(0, 0, logo.width.toDouble(), logo.height.toDouble()),
      Rect.fromLTWH(iconX, iconY, iconSize, iconSize),
      Paint(),
    );
    canvas.restore();
  }

  // "LogiTrack Pro" — single line, font sized to fill remaining width
  final textAfterIcon = iconX + iconSize + pad * 0.5;
  final textSpace = W - textAfterIcon - pad * 0.5;
  // ~0.55 width-per-em ratio for bold latin; "LogiTrack Pro" = 13 chars
  final brandFontSize = (textSpace / (13 * 0.58)).clamp(8.0, 18.0);
  _drawText(
    canvas, 'LogiTrack Pro',
    _ts(brandFontSize, Colors.white, weight: FontWeight.bold),
    Offset(textAfterIcon, brandingY + (brandingH - brandFontSize * 1.3) / 2),
    maxW: textSpace,
    maxLines: 1,
  );

  // ─── QR Code ───
  _drawQr(canvas, qrRect, 'https://www.google.com/maps?q=$lat,$lng', scale);

  // 6. Encode final image
  final picture = recorder.endRecording();
  final result = await picture.toImage(W.toInt(), H.toInt());
  final rgbaData = await result.toByteData(format: ui.ImageByteFormat.rawRgba);
  if (rgbaData == null) return imageBytes;

  // Re-encode as JPEG (85% quality) using image package
  final imgObj = img.Image.fromBytes(
    width: W.toInt(),
    height: H.toInt(),
    bytes: rgbaData.buffer,
    numChannels: 4,
    order: img.ChannelOrder.rgba,
  );
  return img.encodeJpg(imgObj, quality: 85);
}

// ─── TextStyle shorthand ──────────────────────────────────────────────────────
TextStyle _ts(double size, Color color, {FontWeight weight = FontWeight.normal}) =>
    TextStyle(fontSize: size, color: color, fontWeight: weight, height: 1.3);

// ─── Draw text with TextPainter ───────────────────────────────────────────────
void _drawText(
  Canvas canvas,
  String text,
  TextStyle style,
  Offset offset, {
  double? maxW,
  int? maxLines,
}) {
  final tp = TextPainter(
    text: TextSpan(text: text, style: style),
    textDirection: TextDirection.ltr,
    maxLines: maxLines,
    ellipsis: maxLines != null ? '…' : null,
  )..layout(maxWidth: maxW ?? double.infinity);
  tp.paint(canvas, offset);
}

double _textHeight(String text, TextStyle style, double maxW, {int? maxLines}) {
  final tp = TextPainter(
    text: TextSpan(text: text, style: style),
    textDirection: TextDirection.ltr,
    maxLines: maxLines,
    ellipsis: maxLines != null ? '…' : null,
  )..layout(maxWidth: maxW);
  return tp.height;
}

// ─── Draw QR code ────────────────────────────────────────────────────────────
void _drawQr(Canvas canvas, Rect rect, String data, double scale) {
  try {
    final qrCode = QrCode.fromData(
      data: data,
      errorCorrectLevel: QrErrorCorrectLevel.M,
    );
    final qrImage = QrImage(qrCode);
    final modules = qrImage.moduleCount;
    final moduleSize = rect.width / modules;

    // White background
    canvas.drawRRect(
      RRect.fromRectAndRadius(rect, Radius.circular(3 * scale)),
      Paint()..color = Colors.white,
    );

    final black = Paint()..color = Colors.black;
    for (var r = 0; r < modules; r++) {
      for (var c = 0; c < modules; c++) {
        if (qrImage.isDark(r, c)) {
          canvas.drawRect(
            Rect.fromLTWH(
              rect.left + c * moduleSize,
              rect.top + r * moduleSize,
              moduleSize,
              moduleSize,
            ),
            black,
          );
        }
      }
    }
  } catch (e) {
    debugPrint('QR draw failed: $e');
  }
}
