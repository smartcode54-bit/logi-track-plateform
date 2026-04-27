import 'package:flutter/foundation.dart' show debugPrint, kIsWeb;
import 'package:intl/intl.dart' as intl;
import 'package:mobile_scanner/mobile_scanner.dart';

import '../utils/ocr_temp_file_io.dart'
    if (dart.library.html) '../utils/ocr_temp_file_stub.dart' as ocr_temp_file;

import 'cloud_vision_ocr_service.dart';
import 'ocr_digit_normalizer.dart';

enum OcrImageKind { auto, runsheetSpx, waybillZx, seal }

const String zxPartnerCode = 'JNT';

/// Result of OCR on a runsheet / Shopee screenshot (Vision API, key เดียวกับแผนที่).
class OcrScreenshotResult {
  final String? tripId;
  final String? sealCode;
  final String? secondarySealCode;
  final String? routeInfo;
  final String? origin;
  final String? destination;
  final String? distance;
  final String? parcelCount;
  final String? sealTime;
  final String? releaseTime;
  final String? totalWeight;
  final String? supplierCode;
  final String? sealSource;
  final String? ocrProfile;
  /// ช่องทาง / พาร์ทเนอร์จากป้าย เช่น JWT, TTP (หลัง LH- / FM-)
  final String? partnerCode;

  OcrScreenshotResult({
    this.tripId,
    this.sealCode,
    this.secondarySealCode,
    this.routeInfo,
    this.origin,
    this.destination,
    this.distance,
    this.parcelCount,
    this.sealTime,
    this.releaseTime,
    this.totalWeight,
    this.supplierCode,
    this.sealSource,
    this.ocrProfile,
    this.partnerCode,
  });
}

/// Runs OCR on raw image bytes (Vision API). อ่านไม่ได้ให้ใส่มือได้
Future<OcrScreenshotResult> runOcrOnImageBytes(
  List<int> imageBytes, {
  String? imagePath,
  OcrImageKind kind = OcrImageKind.auto,
}) async {
  String? barcodeTripId;
  String? barcodeSealCode;
  String? barcodeSecondarySealCode;

  try {
    String? pathToScan = imagePath;
    if ((pathToScan == null || pathToScan.isEmpty) &&
        imageBytes.isNotEmpty &&
        !kIsWeb) {
      pathToScan = await ocr_temp_file.createTempFileFromBytes(imageBytes);
    }
    if (pathToScan != null && pathToScan.isNotEmpty) {
      final controller = MobileScannerController();
      final capture = await controller.analyzeImage(pathToScan);
      final barcodes = capture?.barcodes ?? [];
      for (final b in barcodes) {
        final raw = b.rawValue?.trim();
        if (raw == null || raw.isEmpty) continue;
        final t = _extractTripIdFromBarcode(raw, kind: kind);
        if (t != null) barcodeTripId ??= t;
        final seals = _extractSealCodesFromBarcode(raw, kind: kind);
        if (seals.isNotEmpty) {
          barcodeSealCode ??= seals.first;
          if (seals.length > 1) {
            barcodeSecondarySealCode ??= seals[1];
          } else if (barcodeSecondarySealCode == null &&
              barcodeSealCode != seals.first) {
            barcodeSecondarySealCode = seals.first;
          }
        }
      }
      controller.dispose();
      if (imagePath == null && pathToScan.isNotEmpty) {
        await ocr_temp_file.deleteTempFile(pathToScan);
      }
      debugPrint(
        'QR Scan from Image -> TripID: $barcodeTripId, Seal: $barcodeSealCode, Seal2: $barcodeSecondarySealCode',
      );
    }
  } catch (e) {
    debugPrint('QR scan on image error: $e');
  }

  if (kind == OcrImageKind.seal &&
      (barcodeSealCode != null || barcodeSecondarySealCode != null)) {
    return OcrScreenshotResult(
      sealCode: barcodeSealCode,
      secondarySealCode: barcodeSecondarySealCode,
      sealSource: 'scanned',
      ocrProfile: 'seal',
    );
  }

  try {
    final fullText = await runCloudVisionOcrOnImageBytes(imageBytes);

    // Log ผลอ่านไว้ดู
    debugPrint(
      '=== OCR Full Text (Vision API) ===\n${fullText == null || fullText.isEmpty ? "(ว่าง)" : fullText}\n=====================',
    );

    if (fullText == null || fullText.isEmpty) {
      if (barcodeTripId != null ||
          barcodeSealCode != null ||
          barcodeSecondarySealCode != null) {
        final profile = _inferOcrProfile(
          text: '',
          kind: kind,
          tripId: barcodeTripId,
        );
        final partner = _resolvePartnerCode(
          profile: profile,
          text: null,
          extractedPartner: null,
        );
        return OcrScreenshotResult(
          tripId: barcodeTripId,
          sealCode: barcodeSealCode,
          secondarySealCode: barcodeSecondarySealCode,
          partnerCode: partner,
          sealSource: barcodeSealCode != null ? 'scanned' : null,
          ocrProfile: profile,
        );
      }
      return OcrScreenshotResult();
    }

    return parseOcrScreenshotFromRawText(
      fullText,
      kind: kind,
      barcodeTripId: barcodeTripId,
      barcodeSealCode: barcodeSealCode,
      barcodeSecondarySealCode: barcodeSecondarySealCode,
    );
  } catch (e, st) {
    debugPrint('OCR screenshot error: $e\n$st');
    return OcrScreenshotResult();
  }
}

String? extractTripIdFromRawValue(
  String raw, {
  OcrImageKind kind = OcrImageKind.auto,
}) =>
    _extractTripIdFromBarcode(raw, kind: kind);

String? extractPrimarySealFromRawValue(
  String raw, {
  OcrImageKind kind = OcrImageKind.auto,
}) {
  final seals = _extractSealCodesFromBarcode(raw, kind: kind);
  return seals.isNotEmpty ? seals.first : null;
}

OcrScreenshotResult parseOcrScreenshotFromRawText(
  String fullText, {
  OcrImageKind kind = OcrImageKind.auto,
  String? barcodeTripId,
  String? barcodeSealCode,
  String? barcodeSecondarySealCode,
}) {
  if (fullText.trim().isEmpty) {
    final profile = _inferOcrProfile(
      text: '',
      kind: kind,
      tripId: barcodeTripId,
    );
    return OcrScreenshotResult(
      tripId: barcodeTripId,
      sealCode: barcodeSealCode,
      secondarySealCode: barcodeSecondarySealCode,
      partnerCode: _resolvePartnerCode(
        profile: profile,
        text: null,
        extractedPartner: null,
      ),
      sealSource: barcodeSealCode != null ? 'scanned' : null,
      ocrProfile: profile,
    );
  }

  final profile = _inferOcrProfile(
    text: fullText,
    kind: kind,
    tripId: barcodeTripId,
  );
  final textTripId = _extractTripIdByProfile(fullText, profile);
  final textSeal = _extractSealCodeByProfile(fullText, profile);
  final textSeal2 = _extractSecondarySealCodeByProfile(fullText, profile);
  final partner = _resolvePartnerCode(
    profile: profile,
    text: fullText,
    extractedPartner: _clean(_extractPartnerCode(fullText)),
  );
  final supplier = _clean(_extractSupplierCode(fullText));
  return OcrScreenshotResult(
    tripId: barcodeTripId ?? _clean(textTripId),
    sealCode: barcodeSealCode ?? _clean(textSeal),
    secondarySealCode: barcodeSecondarySealCode ?? _clean(textSeal2),
    routeInfo: _clean(_extractRouteInfo(fullText)),
    origin: _clean(_extractOrigin(fullText)),
    destination: _clean(_extractDestination(fullText)),
    distance: _clean(_extractDistance(fullText)),
    parcelCount: _clean(_extractParcelCount(fullText)),
    sealTime: _clean(_extractSealTime(fullText)),
    releaseTime: _clean(_extractReleaseTime(fullText)),
    totalWeight: _clean(_extractTotalWeight(fullText)),
    supplierCode: supplier,
    sealSource: barcodeSealCode != null ? 'scanned' : null,
    ocrProfile: profile,
    partnerCode: partner,
  );
}

String? _clean(String? v) => (v != null && v.isNotEmpty) ? v : null;

String? _extractTripIdFromBarcode(
  String raw, {
  required OcrImageKind kind,
}) {
  final compact = _compactBarcodeText(raw);
  final lt = RegExp(r'LT[O0]?[A-Za-z0-9\-]{7,}', caseSensitive: false)
      .firstMatch(raw) ??
      RegExp(r'LT[O0]?[A-Za-z0-9\-]{7,}', caseSensitive: false)
          .firstMatch(compact);
  if (lt != null && kind != OcrImageKind.waybillZx) {
    var val = lt.group(0)!.toUpperCase();
    if (val.startsWith('LTO')) val = val.replaceFirst('LTO', 'LT0');
    return val;
  }
  final zx = RegExp(r'ZX[A-Za-z0-9\-]{6,}', caseSensitive: false)
      .firstMatch(raw) ??
      RegExp(r'ZX[A-Za-z0-9\-]{6,}', caseSensitive: false)
          .firstMatch(compact);
  if (zx != null && kind != OcrImageKind.runsheetSpx && kind != OcrImageKind.seal) {
    return zx.group(0)!.toUpperCase();
  }
  return null;
}

List<String> _extractSealCodesFromBarcode(
  String raw, {
  required OcrImageKind kind,
}) {
  final out = <String>[];
  final compact = _compactBarcodeText(raw);
  final spx = RegExp(r'(?:SPX|5PX)\s*[A-Za-z0-9\-]{5,}', caseSensitive: false)
      .firstMatch(raw) ??
      RegExp(r'(?:SPX|5PX)[A-Za-z0-9\-]{5,}', caseSensitive: false)
          .firstMatch(compact);
  if (spx != null && kind != OcrImageKind.waybillZx && kind != OcrImageKind.seal) {
    final c = _compactSealCode(spx.group(0));
    if (c != null) out.add(c);
  }
  final numericMatches = RegExp(r'\b\d{6,12}\b')
      .allMatches(raw)
      .map((m) => m.group(0)!)
      .toList();
  if (numericMatches.isNotEmpty &&
      (kind == OcrImageKind.seal || kind == OcrImageKind.waybillZx)) {
    out.addAll(numericMatches);
  }
  return out.toSet().toList();
}

String _compactBarcodeText(String raw) =>
    raw.toUpperCase().replaceAll(RegExp(r'[^A-Z0-9\-]'), '');

/// SPX + digits often have a space in runsheets / QR payloads; rules elsewhere expect no spaces.
String? _compactSealCode(String? v) {
  if (v == null || v.isEmpty) return null;
  final s = v.toUpperCase().replaceAll(RegExp(r'\s+'), '');
  return s.length >= 8 ? s : null; // SPX + at least 5 alnum
}

// ===== Field Extraction (based on actual SPX Express runsheet format) =====

String _inferOcrProfile({
  required String text,
  required OcrImageKind kind,
  String? tripId,
}) {
  if (kind == OcrImageKind.seal) return 'seal';
  if (kind == OcrImageKind.runsheetSpx) return 'spx_runsheet';
  if (kind == OcrImageKind.waybillZx) return 'zx_waybill';
  final trip = (tripId ?? '').toUpperCase();
  if (trip.startsWith('ZX')) return 'zx_waybill';
  final upper = text.toUpperCase();
  if (upper.contains('ซัพพลายเออร์') ||
      upper.contains('สถานีต้นทาง') ||
      upper.contains('สถานีปลายทาง') ||
      upper.contains('J&T') ||
      upper.contains('J&T EXPRESS') ||
      RegExp(r'ZX[A-Z0-9\-]{6,}').hasMatch(upper)) {
    return 'zx_waybill';
  }
  return 'spx_runsheet';
}

String? _extractTripIdByProfile(String text, String profile) {
  if (profile == 'zx_waybill') {
    final zx = RegExp(r'ZX[A-Za-z0-9\-]{6,}', caseSensitive: false)
        .firstMatch(text);
    return zx?.group(0)?.toUpperCase();
  }
  return _extractTripId(text);
}

String? _extractSealCodeByProfile(String text, String profile) {
  if (profile == 'zx_waybill') {
    final seals = RegExp(r'\b\d{6,12}\b')
        .allMatches(text)
        .map((m) => m.group(0)!)
        .toList();
    return seals.isNotEmpty ? seals.first : null;
  }
  return _extractSealCode(text);
}

String? _extractSecondarySealCodeByProfile(String text, String profile) {
  if (profile != 'zx_waybill') return null;
  final seals = RegExp(r'\b\d{6,12}\b')
      .allMatches(text)
      .map((m) => m.group(0)!)
      .toList();
  return seals.length > 1 ? seals[1] : null;
}

String? _resolvePartnerCode({
  required String profile,
  required String? text,
  required String? extractedPartner,
}) {
  if (profile == 'zx_waybill') return zxPartnerCode;
  return extractedPartner ?? _clean(_extractPartnerCode(text ?? ''));
}

/// Trip ID: "เลขทริป : LT0Q2E2467U61" or "LT102P24DZIX1" etc.
String? _extractTripId(String text) {
  // รองรับ LT0, LT1, LTO, LTQ... ฯลฯ (สอดคล้องกับ QR regex)
  final match = RegExp(r'LT[O0]?[A-Za-z0-9\-]{7,}').firstMatch(text);
  if (match != null) {
    var raw = match.group(0)!;
    if (raw.startsWith('LTO')) {
      raw = raw.replaceFirst('LTO', 'LT0');
    }
    return raw;
  }
  return null;
}

/// Seal Code from SPX runsheet, e.g. `เลข Seal Code: SPX2723354` (also `SPX 2723354`).
String? _extractSealCode(String text) {
  // Label line; เลข is optional — OCR often mangles Thai prefix.
  var match = RegExp(
    r'(?:เลข\s*)?Seal\s*Code\s*[:\s]+\s*((?:SPX|5PX)\s*[A-Za-z0-9\-]{5,})',
    caseSensitive: false,
  ).firstMatch(text);
  if (match != null) {
    var g = match.group(1)!;
    g = g.replaceFirst(RegExp(r'^5PX', caseSensitive: false), 'SPX');
    return _compactSealCode(g);
  }
  match = RegExp(r'(?:SPX|5PX)\s*[A-Za-z0-9\-]{5,}', caseSensitive: false)
      .firstMatch(text);
  if (match != null) {
    var g = match.group(0)!;
    g = g.replaceFirst(RegExp(r'^5PX', caseSensitive: false), 'SPX');
    return _compactSealCode(g);
  }
  match = RegExp(r'(?:SPX|5PX)\d{5,}', caseSensitive: false).firstMatch(text);
  if (match != null) {
    var g = match.group(0)!;
    g = g.replaceFirst(RegExp(r'^5PX', caseSensitive: false), 'SPX');
    return _compactSealCode(g);
  }
  return null;
}

/// Partner slot from runsheet: `LH-TTP`, `FM-XXX` on driver line (ชื่อคนขับ) preferred.
String? _extractPartnerCode(String text) {
  if (text.trim().isEmpty) return null;
  final pattern = RegExp(
    r'(?:LH|FM)\s*-\s*([A-Z0-9]{2,})',
    caseSensitive: false,
  );

  bool validCode(String code) {
    final u = code.toUpperCase();
    if (u == 'SUB') return false;
    return u.isNotEmpty;
  }

  final lines = text.split(RegExp(r'\r?\n'));
  for (final line in lines) {
    if (line.contains('ชื่อคนขับ') ||
        line.toUpperCase().contains('DRIVER')) {
      final m = pattern.firstMatch(line.toUpperCase());
      if (m != null) {
        final code = m.group(1)!;
        if (validCode(code)) return code.toUpperCase();
      }
    }
  }

  final fullUpper = text.toUpperCase();
  final m = pattern.firstMatch(fullUpper);
  if (m != null) {
    final code = m.group(1)!;
    if (validCode(code)) return code.toUpperCase();
  }
  return null;
}

/// Route info (fallback)
String? _extractRouteInfo(String text) {
  final match = RegExp(
    r'(?:LH\s*trip\s*route|[Rr]oute)\s*[:\s]*([^\n]+)',
  ).firstMatch(text);
  return match?.group(1)?.trim();
}

String? _extractSupplierCode(String text) {
  final match = RegExp(
    r'(?:ซัพพลายเออร์|supplier)\s*[:\s]+([A-Za-z0-9\-_]{2,})',
    caseSensitive: false,
  ).firstMatch(text);
  return match?.group(1)?.trim().toUpperCase();
}

/// Origin: "สถานีเริ่มต้น: ALANG-A - วังทองหลาง"
String? _extractOrigin(String text) {
  var match = RegExp(
    r'(?:สถานีเริ่มต้น|สถาน[ีi]เร[ิi]มต้น|สถาน[ีi]เร[ิi]ม|[Oo]rigin\s*station|[Ff]rom\s*station|[Ff]rom|[Oo]rigin|ต้นทาง|Start)\s*[:\s]+(.+)',
    caseSensitive: false,
  ).firstMatch(text);
  var line = match?.group(1)?.trim().split('\n').first.trim();
  if (line != null && line.isNotEmpty) return line;
  match = RegExp(
    r'(?:Station|STA)\s*(?:start|from)?\s*[:\s]+([^\n]+)',
    caseSensitive: false,
  ).firstMatch(text);
  return match?.group(1)?.trim().split('\n').first.trim();
}

/// Destination: "สถานีถัดไป: SOCE" or "ปลายทาง TO: SOCE"
String? _extractDestination(String text) {
  var match = RegExp(
    r'(?:สถานีปลายทาง|สถานีถัดไป|ปลายทาง\s*(?:TO)?|[Nn]ext\s*station|[Tt]o\s*station|[Dd]estination|[Tt]o\s*[:])\s*[:\s]*(.+)',
    caseSensitive: false,
  ).firstMatch(text);
  var line = match?.group(1)?.trim().split('\n').first.trim();
  if (line != null && line.isNotEmpty) return line;
  match = RegExp(
    r'\b(SOCE|SOCN|SOCW)\b',
    caseSensitive: false,
  ).firstMatch(text);
  return match?.group(1)?.toUpperCase();
}

String? _extractReleaseTime(String text) {
  var match = RegExp(
    r'(?:วันที่ปล่อยรถ|release\s*(?:date|time)?)\s*[:\s]+(\d{4}[/\-.]\d{2}[/\-.]\d{2}\s+\d{1,2}:\d{2}(?::\d{2})?)',
    caseSensitive: false,
  ).firstMatch(text);
  if (match != null) {
    final f = _parseOcrDateTimeToAppFormat(match.group(1)!);
    if (f != null) return f;
  }
  match = RegExp(
    r'(?:วันที่ปล่อยรถ|release\s*(?:date|time)?)\s*[:\s]+(\d{2}[/\-.]\d{2}[/\-.]\d{4}\s+\d{1,2}:\d{2}(?::\d{2})?)',
    caseSensitive: false,
  ).firstMatch(text);
  if (match != null) {
    final f = _parseOcrDateTimeToAppFormat(match.group(1)!);
    if (f != null) return f;
  }
  return null;
}

/// Distance: "ระยะทางจากสถานีเริ่มต้นถึงสถานีปลายทาง 53.000 KM"
String? _extractDistance(String text) {
  var match = RegExp(
    '(?:ระยะทาง[^\\n]*?|[Dd]istance[:\\s]*)($kDigitOrConfusable+(?:\\.$kDigitOrConfusable+)?)\\s*(?:ระ[ท]าง\\s*)?(?:km|KM|กม\\.?)',
    caseSensitive: false,
  ).firstMatch(text);
  if (match != null) return '${normalizeOcrDigits(match.group(1))} km';

  match = RegExp(
    '($kDigitOrConfusable+(?:\\.$kDigitOrConfusable+)?)\\s*KM',
    caseSensitive: false,
  ).firstMatch(text);
  if (match != null) return '${normalizeOcrDigits(match.group(1))} km';

  return null;
}

/// Parcel count: same-line `Q'ty of Parcel 547`, or table layout with value in a box on the next line.
String? _extractParcelCount(String text) {
  // Common SPX table: "Qty of Parcel" then newline, then "618" alone in a cell.
  var match = RegExp(
    r"(?:Q'?\s*(?:ty|uantity)|\bQty\b)\s+of\s+[Pp]arcel[s]?\s*\r?\n\s*(\d{2,5})\b",
    caseSensitive: false,
  ).firstMatch(text);
  var raw = match?.group(1);
  if (raw != null) return normalizeOcrDigits(raw);

  // Same block but digits separated by spaces/table chars (not only newline).
  match = RegExp(
    r"(?:Q'?\s*(?:ty|uantity)|\bQty\b)\s+of\s+[Pp]arcel[s]?[^\d]{0,220}?(\d{2,5})\b",
    caseSensitive: false,
  ).firstMatch(text);
  raw = match?.group(1);
  if (raw != null) return normalizeOcrDigits(raw);

  match = RegExp(
    r'จำนวน\s*(?:พัสดุ)?[^\d]{0,160}?(\d{2,5})\b',
    caseSensitive: false,
  ).firstMatch(text);
  raw = match?.group(1);
  if (raw != null) return normalizeOcrDigits(raw);

  match = RegExp(
    '''(?:Q['']?(?:ty|uantity)\\s*(?:of\\s*)?[Pp]arcel[s]?|[Pp]arcel\\s*[Cc]ount|[Pp]arcel\\s*\\(|[Pp]cs|[Pp]ieces|ชิ้น)\\s*[:\\s]+($kDigitOrConfusable+)''',
    caseSensitive: false,
  ).firstMatch(text);
  raw = match?.group(1);
  if (raw != null) return normalizeOcrDigits(raw);
  match = RegExp(
    r'(?:Parcel|พัสดุ)\s*[:\s#|]+($kDigitOrConfusable{2,6})\b',
    caseSensitive: false,
  ).firstMatch(text);
  raw = match?.group(1);
  return raw != null ? normalizeOcrDigits(raw) : null;
}

/// Seal time for Loading form: must parse as `dd-MM-yyyy HH:mm:ss` (not HH:mm only).
String? _extractSealTime(String text) {
  var match = RegExp(
    r'(?:เวลา\s*[Ss]eal\s*(?:รถ)?|[Ss]eal\s*[Tt]ime)\s*[:\s]+(\d{4}[/\-.]\d{2}[/\-.]\d{2}\s+[\d]{1,2}:\d{2}:\d{2})',
    caseSensitive: false,
  ).firstMatch(text);
  if (match != null) {
    final f = _parseOcrDateTimeToAppFormat(match.group(1)!);
    if (f != null) return f;
  }
  match = RegExp(
    r'(?:เวลา\s*[Ss]eal|[Ss]eal\s*[Tt]ime)\s*[:\s]+(\d{2}[/\-.]\d{2}[/\-.]\d{4}\s+[\d]{1,2}:\d{2}:\d{2})',
    caseSensitive: false,
  ).firstMatch(text);
  if (match != null) {
    final f = _parseOcrDateTimeToAppFormat(match.group(1)!);
    if (f != null) return f;
  }
  // Time only → combine with today's date (form + STA need full datetime)
  match = RegExp(
    r'(?:เวลา\s*[Ss]eal\s*(?:รถ)?|[Ss]eal\s*[Tt]ime)\s*[:\s]+(?:\d{4}[/\-.]\d{2}[/\-.]\d{2}\s+)?([\d]{1,2}[:\.][\d]{2}(?::[\d]{2})?)',
    caseSensitive: false,
  ).firstMatch(text);
  if (match != null) {
    final t = _normalizeTimeWithSeconds(match.group(1)!);
    final now = DateTime.now();
    return intl.DateFormat('dd-MM-yyyy HH:mm:ss').format(
      DateTime(now.year, now.month, now.day, t.$1, t.$2, t.$3),
    );
  }
  match = RegExp(
    r'(?:เวลา\s*[Ss]eal|เวลาซีล|[Ss]eal)\s*[:\s]*([\d]{1,2}[:\.]?[\d]{2})',
    caseSensitive: false,
  ).firstMatch(text);
  if (match != null) {
    final t = _normalizeTimeWithSeconds(match.group(1)!);
    final now = DateTime.now();
    return intl.DateFormat('dd-MM-yyyy HH:mm:ss').format(
      DateTime(now.year, now.month, now.day, t.$1, t.$2, t.$3),
    );
  }
  return null;
}

/// Returns (hour, minute, second)
(int, int, int) _normalizeTimeWithSeconds(String raw) {
  final cleaned = raw.replaceAll('.', ':');
  final parts = cleaned.split(':');
  if (parts.length >= 2) {
    final h = int.tryParse(parts[0].trim()) ?? 0;
    final m = int.tryParse(parts[1].trim()) ?? 0;
    final s = parts.length >= 3 ? (int.tryParse(parts[2].trim()) ?? 0) : 0;
    return (
      h.clamp(0, 23).toInt(),
      m.clamp(0, 59).toInt(),
      s.clamp(0, 59).toInt(),
    );
  }
  if (!cleaned.contains(':') && cleaned.length == 4) {
    final h = int.tryParse(cleaned.substring(0, 2)) ?? 0;
    final m = int.tryParse(cleaned.substring(2)) ?? 0;
    return (h.clamp(0, 23).toInt(), m.clamp(0, 59).toInt(), 0);
  }
  return (0, 0, 0);
}

String? _parseOcrDateTimeToAppFormat(String raw) {
  try {
    final s = raw.trim().replaceAll('.', ':').replaceAll('-', '/');
    final space = s.indexOf(' ');
    if (space < 0) return null;
    final datePart = s.substring(0, space).trim();
    final timePart = s.substring(space + 1).trim();
    final dp = datePart.split('/');
    if (dp.length != 3) return null;
    int y, mo, d;
    if (dp[0].length == 4) {
      y = int.parse(dp[0]);
      mo = int.parse(dp[1]);
      d = int.parse(dp[2]);
    } else {
      d = int.parse(dp[0]);
      mo = int.parse(dp[1]);
      y = int.parse(dp[2]);
    }
    final tp = timePart.split(':');
    final h = int.parse(tp[0]);
    final m = int.parse(tp[1]);
    final sec = tp.length >= 3 ? int.parse(tp[2]) : 0;
    final dt = DateTime(y, mo, d, h, m, sec);
    return intl.DateFormat('dd-MM-yyyy HH:mm:ss').format(dt);
  } catch (_) {
    return null;
  }
}

/// Total weight: "Total weight(kg) 608.985"
String? _extractTotalWeight(String text) {
  var match = RegExp(
    '(?:[Tt]otal\\s*[Ww]eight\\s*(?:\\(kg\\))?|น้ำหนัก(?:รวม)?)\\s*[:\\s]*($kDigitOrConfusable+(?:\\.$kDigitOrConfusable+)?)',
    caseSensitive: false,
  ).firstMatch(text);
  if (match != null) return '${normalizeOcrDigits(match.group(1))} kg';
  match = RegExp(
    '(?:kg|KG)\\s*[:\\s]*($kDigitOrConfusable+(?:\\.$kDigitOrConfusable+)?)',
    caseSensitive: false,
  ).firstMatch(text);
  if (match != null) return '${normalizeOcrDigits(match.group(1))} kg';
  return null;
}
