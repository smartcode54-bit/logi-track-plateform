import 'package:flutter/foundation.dart' show debugPrint;

import 'cloud_vision_ocr_service.dart';

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
Future<OcrScreenshotResult> runOcrOnImageBytes(List<int> imageBytes) async {
  try {
    final fullText = await runCloudVisionOcrOnImageBytes(imageBytes);

    // Log ผลอ่านไว้ดู
    debugPrint(
      '=== OCR Full Text (Vision API) ===\n${fullText == null || fullText.isEmpty ? "(ว่าง)" : fullText}\n=====================',
    );

    if (fullText == null || fullText.isEmpty) return OcrScreenshotResult();

    return OcrScreenshotResult(
      tripId: _clean(_extractTripId(fullText)),
      sealCode: _clean(_extractSealCode(fullText)),
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

// ===== Field Extraction (based on actual SPX Express runsheet format) =====

/// Trip ID: "เลขทริป : LT0Q2E2467U61"
String? _extractTripId(String text) {
  final match = RegExp(r'LT[A-Za-z0-9\-]{8,}').firstMatch(text);
  return match?.group(0);
}

/// Seal Code: "เลข Seal Code: SPX3784238"
String? _extractSealCode(String text) {
  final match = RegExp(r'SPX[A-Za-z0-9\-]{5,}').firstMatch(text);
  return match?.group(0);
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
  final match = RegExp(
    r'(?:สถานีเริ่มต้น|สถาน[ีi]เร[ิi]ม|[Oo]rigin|[Ff]rom|ต้นทาง)\s*[:\s]+(.+)',
    caseSensitive: false,
  ).firstMatch(text);
  return match?.group(1)?.trim().split('\n').first.trim();
}

/// Destination: "สถานีถัดไป: SOCE" or "ปลายทาง TO: SOCE"
String? _extractDestination(String text) {
  final match = RegExp(
    r'(?:สถานีถัดไป|ปลายทาง\s*(?:TO)?|[Dd]estination)\s*[:\s]+(.+)',
    caseSensitive: false,
  ).firstMatch(text);
  return match?.group(1)?.trim().split('\n').first.trim();
}

/// Distance: "ระยะทางจากสถานีเริ่มต้นถึงสถานีปลายทาง 53.000 KM"
String? _extractDistance(String text) {
  var match = RegExp(
    r'(?:ระยะทาง[^\n]*?|[Dd]istance[:\s]*)([\d]+(?:\.[\d]+)?)\s*(?:ระ[ท]าง\s*)?(?:km|KM|กม\.?)',
    caseSensitive: false,
  ).firstMatch(text);
  if (match != null) return '${match.group(1)} km';

  match = RegExp(
    r'([\d]+(?:\.[\d]+)?)\s*KM',
    caseSensitive: false,
  ).firstMatch(text);
  if (match != null) return '${match.group(1)} km';

  return null;
}

/// Parcel count: "Q'ty of Parcel 547"
String? _extractParcelCount(String text) {
  final match = RegExp(
    r'''(?:Q['']?(?:ty|uantity)\s*(?:of\s*)?[Pp]arcel[s]?|จำนวน(?:พัสดุ)?|[Pp]arcel\s*[Cc]ount|[Pp]cs|ชิ้น)\s*[:\s]*([\d]+)''',
    caseSensitive: false,
  ).firstMatch(text);
  return match?.group(1);
}

/// Seal time: "เวลา Seal รถ : 2026/02/14 19:03:59" → "19:03"
String? _extractSealTime(String text) {
  var match = RegExp(
    r'(?:เวลา\s*[Ss]eal\s*(?:รถ)?|[Ss]eal\s*[Tt]ime)\s*[:\s]+(?:\d{4}[/\-]\d{2}[/\-]\d{2}\s+)?([\d]{1,2}[:\.][\d]{2})',
    caseSensitive: false,
  ).firstMatch(text);
  if (match != null) return _normalizeTime(match.group(1)!);

  match = RegExp(
    r'(?:เวลา\s*[Ss]eal|เวลาซีล|[Ss]eal)\s*[:\s]*([\d]{1,2}[:\.]?[\d]{2})',
    caseSensitive: false,
  ).firstMatch(text);
  if (match != null) return _normalizeTime(match.group(1)!);

  return null;
}

/// Total weight: "Total weight(kg) 608.985"
String? _extractTotalWeight(String text) {
  final match = RegExp(
    r'(?:[Tt]otal\s*[Ww]eight\s*(?:\(kg\))?|น้ำหนัก(?:รวม)?)\s*[:\s]*([\d]+(?:\.[\d]+)?)',
    caseSensitive: false,
  ).firstMatch(text);
  if (match != null) return '${match.group(1)} kg';
  return null;
}

/// Normalize time strings like "1430" or "14.30" to "14:30"
String _normalizeTime(String raw) {
  final cleaned = raw.replaceAll('.', ':');
  if (!cleaned.contains(':') && cleaned.length == 4) {
    return '${cleaned.substring(0, 2)}:${cleaned.substring(2)}';
  }
  final parts = cleaned.split(':');
  if (parts.length >= 2) {
    return '${parts[0].padLeft(2, '0')}:${parts[1]}';
  }
  return cleaned;
}
