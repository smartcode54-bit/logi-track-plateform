import 'dart:io' show File, Platform;
import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:google_mlkit_text_recognition/google_mlkit_text_recognition.dart';
import 'package:path_provider/path_provider.dart';

/// Result of OCR on a Shopee screenshot: tripId (LTQ...), sealCode (SPX...), and optional route info.
class OcrScreenshotResult {
  final String? tripId;
  final String? sealCode;
  final String? routeInfo; // (Fallback / old)
  final String? origin;
  final String? destination;

  OcrScreenshotResult({
    this.tripId,
    this.sealCode,
    this.routeInfo,
    this.origin,
    this.destination,
  });
}

/// Picks an image from gallery (screenshot), runs OCR, and extracts tripId (LTQ...) and sealCode (SPX...).
/// Does NOT overlay geocoding/timestamp (screenshots are for OCR only).
/// On web or unsupported platforms, returns empty result and does not run native OCR.
Future<OcrScreenshotResult> pickScreenshotAndRunOcr({
  required Future<dynamic> Function() pickImageFromGallery,
}) async {
  if (kIsWeb) return OcrScreenshotResult();
  if (!Platform.isAndroid && !Platform.isIOS) return OcrScreenshotResult();

  final xfile = await pickImageFromGallery();
  if (xfile == null) return OcrScreenshotResult();

  final bytes = await (xfile as dynamic).readAsBytes();
  if (bytes == null || bytes is! List<int>) return OcrScreenshotResult();

  return runOcrOnImageBytes(bytes);
}

/// Runs OCR on raw image bytes and extracts LTQ (tripId) and SPX (sealCode) patterns.
Future<OcrScreenshotResult> runOcrOnImageBytes(List<int> imageBytes) async {
  if (kIsWeb) return OcrScreenshotResult();
  if (!Platform.isAndroid && !Platform.isIOS) return OcrScreenshotResult();

  try {
    final dir = await getTemporaryDirectory();
    final file = File(
      '${dir.path}/ocr_screenshot_${DateTime.now().millisecondsSinceEpoch}.jpg',
    );
    await file.writeAsBytes(imageBytes);

    final inputImage = InputImage.fromFilePath(file.path);
    final textRecognizer = TextRecognizer(script: TextRecognitionScript.latin);
    final recognizedText = await textRecognizer.processImage(inputImage);
    await textRecognizer.close();

    try {
      await file.delete();
    } catch (_) {}

    final fullText = recognizedText.text;
    final tripId = _extractTripId(fullText);
    final sealCode = _extractSealCode(fullText);
    final routeInfo = _extractRouteInfo(fullText);
    final origin = _extractOrigin(fullText);
    final destination = _extractDestination(fullText);

    return OcrScreenshotResult(
      tripId: tripId?.isNotEmpty == true ? tripId : null,
      sealCode: sealCode?.isNotEmpty == true ? sealCode : null,
      routeInfo: routeInfo?.isNotEmpty == true ? routeInfo : null,
      origin: origin?.isNotEmpty == true ? origin : null,
      destination: destination?.isNotEmpty == true ? destination : null,
    );
  } catch (_) {
    return OcrScreenshotResult();
  }
}

String? _extractTripId(String text) {
  // Support either LTQ... or LT0Q... (Trip IDs)
  final match = RegExp(r'LT[A-Za-z0-9\-]+').firstMatch(text);
  return match?.group(0);
}

String? _extractSealCode(String text) {
  final match = RegExp(r'SPX[A-Za-z0-9\-]+').firstMatch(text);
  return match?.group(0);
}

String? _extractRouteInfo(String text) {
  final routeMatch = RegExp(r'[Rr]oute\s*[:\s]*([^\n]+)').firstMatch(text);
  if (routeMatch != null) return routeMatch.group(1)?.trim();
  return null;
}

String? _extractOrigin(String text) {
  // MLKit latin sometimes reads 'สถานีเริ่มต้น' as 'aanūñalu' or similar.
  // We match either the Thai, or the common garbled result 'aanūñalu' followed by a colon.
  final match = RegExp(
    r'(?:สถานีเริ่มต้น|aan[ūu]ñalu|STA|aan[ūu]ri)[:\s]*([a-zA-Z0-9\-]+(?:[\s\-]+[a-zA-Z0-9]+)*)',
    caseSensitive: false,
  ).firstMatch(text);
  return match?.group(1)?.trim();
}

String? _extractDestination(String text) {
  // Regex to match "สถานีถัดไป:" or its common garbled equivalent.
  // "ปลายทาง" is often caught as "Uanu1v" or "UanE1v" depending on the scan.
  // Let's also look for 'SOCE' or standard patterns if needed, but we'll try to just match following the colon if we can.
  // The screenshot shows 'ปลายทาง' dropping down, but the label next to SOCE is 'สถานีถัดไป:'.
  // MLKit might read it as 'anürialJ:'
  final match = RegExp(
    r'(?:สถานีถัดไป|anürialJ|Uanu1v|ปลายทางTO|ปลายทาง|STD)[:\s]*([a-zA-Z0-9\-]+(?:[\s\-]+[a-zA-Z0-9]+)*)',
    caseSensitive: false,
  ).firstMatch(text);
  return match?.group(1)?.trim();
}
