import 'package:flutter/foundation.dart' show debugPrint, kIsWeb;
import 'package:intl/intl.dart' as intl;
import 'package:mobile_scanner/mobile_scanner.dart';

import '../utils/ocr_temp_file_io.dart'
    if (dart.library.html) '../utils/ocr_temp_file_stub.dart' as ocr_temp_file;

import 'cloud_vision_ocr_service.dart';
import 'ocr_digit_normalizer.dart';

/// Result of OCR on a runsheet / Shopee screenshot (Vision API, key เดียวกับแผนที่).
class OcrScreenshotResult {
  final String? tripId;
  final String? sealCode;
  final String? routeInfo;
  final String? origin;
  final String? destination;
  final String? distance;
  final String? parcelCount;
  final String? sealTime;
  final String? totalWeight;

  OcrScreenshotResult({
    this.tripId,
    this.sealCode,
    this.routeInfo,
    this.origin,
    this.destination,
    this.distance,
    this.parcelCount,
    this.sealTime,
    this.totalWeight,
  });
}

/// Runs OCR on raw image bytes (Vision API). อ่านไม่ได้ให้ใส่มือได้
Future<OcrScreenshotResult> runOcrOnImageBytes(
  List<int> imageBytes, {
  String? imagePath,
}) async {
  String? qrTripId;
  String? qrSealCode;

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
        final lt = RegExp(r'LT[O0]?[A-Za-z0-9\-]{7,}').firstMatch(raw);
        if (lt != null) {
          var val = lt.group(0)!;
          if (val.startsWith('LTO')) val = val.replaceFirst('LTO', 'LT0');
          qrTripId ??= val;
        }
        final spx = RegExp(r'SPX\s*[A-Za-z0-9\-]{5,}', caseSensitive: false)
            .firstMatch(raw);
        if (spx != null) {
          final c = _compactSealCode(spx.group(0));
          if (c != null) qrSealCode ??= c;
        }
        final spx5 = RegExp(r'5PX\s*[A-Za-z0-9\-]{5,}', caseSensitive: false)
            .firstMatch(raw);
        if (spx5 != null) {
          final fixed = spx5.group(0)!.replaceFirst(RegExp(r'^5PX', caseSensitive: false), 'SPX');
          final c = _compactSealCode(fixed);
          if (c != null) qrSealCode ??= c;
        }
      }
      controller.dispose();
      if (imagePath == null && pathToScan.isNotEmpty) {
        await ocr_temp_file.deleteTempFile(pathToScan);
      }
      debugPrint('QR Scan from Image -> TripID: $qrTripId, Seal: $qrSealCode');
    }
  } catch (e) {
    debugPrint('QR scan on image error: $e');
  }

  try {
    final fullText = await runCloudVisionOcrOnImageBytes(imageBytes);

    // Log ผลอ่านไว้ดู
    debugPrint(
      '=== OCR Full Text (Vision API) ===\n${fullText == null || fullText.isEmpty ? "(ว่าง)" : fullText}\n=====================',
    );

    if (fullText == null || fullText.isEmpty) {
      if (qrTripId != null || qrSealCode != null) {
        return OcrScreenshotResult(tripId: qrTripId, sealCode: qrSealCode);
      }
      return OcrScreenshotResult();
    }

    return OcrScreenshotResult(
      tripId: qrTripId ?? _clean(_extractTripId(fullText)),
      sealCode: qrSealCode ?? _clean(_extractSealCode(fullText)),
      routeInfo: _clean(_extractRouteInfo(fullText)),
      origin: _clean(_extractOrigin(fullText)),
      destination: _clean(_extractDestination(fullText)),
      distance: _clean(_extractDistance(fullText)),
      parcelCount: _clean(_extractParcelCount(fullText)),
      sealTime: _clean(_extractSealTime(fullText)),
      totalWeight: _clean(_extractTotalWeight(fullText)),
    );
  } catch (e, st) {
    debugPrint('OCR screenshot error: $e\n$st');
    return OcrScreenshotResult();
  }
}

String? _clean(String? v) => (v != null && v.isNotEmpty) ? v : null;

/// SPX + digits often have a space in runsheets / QR payloads; rules elsewhere expect no spaces.
String? _compactSealCode(String? v) {
  if (v == null || v.isEmpty) return null;
  final s = v.toUpperCase().replaceAll(RegExp(r'\s+'), '');
  return s.length >= 8 ? s : null; // SPX + at least 5 alnum
}

// ===== Field Extraction (based on actual SPX Express runsheet format) =====

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

/// Route info (fallback)
String? _extractRouteInfo(String text) {
  final match = RegExp(
    r'(?:LH\s*trip\s*route|[Rr]oute)\s*[:\s]*([^\n]+)',
  ).firstMatch(text);
  return match?.group(1)?.trim();
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
    r'(?:สถานีถัดไป|ปลายทาง\s*(?:TO)?|[Nn]ext\s*station|[Tt]o\s*station|[Dd]estination|[Tt]o\s*[:])\s*[:\s]*(.+)',
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
    r'(?:Q['']?\s*(?:ty|uantity)|\bQty\b)\s+of\s+[Pp]arcel[s]?\s*\r?\n\s*(\d{2,5})\b',
    caseSensitive: false,
  ).firstMatch(text);
  var raw = match?.group(1);
  if (raw != null) return normalizeOcrDigits(raw);

  // Same block but digits separated by spaces/table chars (not only newline).
  match = RegExp(
    r'(?:Q['']?\s*(?:ty|uantity)|\bQty\b)\s+of\s+[Pp]arcel[s]?[^\d]{0,220}?(\d{2,5})\b',
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
