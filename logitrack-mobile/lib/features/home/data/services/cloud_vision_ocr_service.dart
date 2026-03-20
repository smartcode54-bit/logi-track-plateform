import 'dart:convert';

import 'package:flutter/foundation.dart' show debugPrint;
import 'package:flutter_dotenv/flutter_dotenv.dart';
import 'package:http/http.dart' as http;

import 'ocr_image_preprocess.dart';

const _visionBase = 'https://vision.googleapis.com/v1/images:annotate';

/// Whether the active `.env` (per flavor) has a key for Cloud Vision OCR.
bool isCloudVisionApiKeyConfigured() {
  final k = dotenv.env['GOOGLE_CLOUD_VISION_API_KEY']?.trim() ??
      dotenv.env['GOOGLE_MAPS_API_KEY']?.trim();
  return k != null && k.isNotEmpty;
}

/// เรียก Google Cloud Vision API ด้วยรูป base64 คืน full text หรือ null
/// ใช้ GOOGLE_CLOUD_VISION_API_KEY ก่อน ไม่มีค่อยใช้ GOOGLE_MAPS_API_KEY
Future<String?> runCloudVisionOcrOnImageBytes(List<int> imageBytes) async {
  final apiKey = dotenv.env['GOOGLE_CLOUD_VISION_API_KEY']?.trim() ??
      dotenv.env['GOOGLE_MAPS_API_KEY']?.trim();
  if (apiKey == null || apiKey.isEmpty) {
    debugPrint(
      'Cloud Vision OCR: GOOGLE_CLOUD_VISION_API_KEY / GOOGLE_MAPS_API_KEY not set',
    );
    return null;
  }

  final processed = preprocessImageForOcr(imageBytes) ?? imageBytes;

  try {
    final docText = await _visionSingleFeature(processed, apiKey, 'DOCUMENT_TEXT_DETECTION');
    final textDetect = await _visionSingleFeature(processed, apiKey, 'TEXT_DETECTION');
    final merged = _mergeVisionTexts(docText, textDetect);
    if (merged != null && merged.isNotEmpty) return merged;

    // Last resort: raw image (some JPEGs decode poorly after preprocess)
    if (!identical(processed, imageBytes)) {
      final docRaw = await _visionSingleFeature(imageBytes, apiKey, 'DOCUMENT_TEXT_DETECTION');
      final textRaw = await _visionSingleFeature(imageBytes, apiKey, 'TEXT_DETECTION');
      return _mergeVisionTexts(docRaw, textRaw);
    }
    return null;
  } catch (e, st) {
    debugPrint('Cloud Vision OCR: $e\n$st');
    return null;
  }
}

String? _mergeVisionTexts(String? a, String? b) {
  final x = a?.trim();
  final y = b?.trim();
  if (x == null || x.isEmpty) return (y == null || y.isEmpty) ? null : y;
  if (y == null || y.isEmpty) return x;
  if (y.length > x.length * 1.5) return '$x\n\n$y';
  if (x.length > y.length * 1.5) return '$y\n\n$x';
  return '$x\n$y';
}

Future<String?> _visionSingleFeature(
  List<int> imageBytes,
  String apiKey,
  String featureType,
) async {
  try {
    final base64Image = base64Encode(imageBytes);
    final uri = Uri.parse('$_visionBase?key=$apiKey');
    final body = {
      'requests': [
        {
          'image': {'content': base64Image},
          'features': [
            {'type': featureType, 'maxResults': 1},
          ],
        },
      ],
    };

    final response = await http
        .post(
          uri,
          headers: {'Content-Type': 'application/json'},
          body: jsonEncode(body),
        )
        .timeout(const Duration(seconds: 45));

    if (response.statusCode != 200) {
      final bodyPreview = response.body.length > 300
          ? '${response.body.substring(0, 300)}...'
          : response.body;
      debugPrint('Cloud Vision OCR ($featureType): ${response.statusCode} $bodyPreview');
      return null;
    }

    final data = jsonDecode(response.body) as Map<String, dynamic>?;
    final responses = data?['responses'] as List<dynamic>?;
    if (responses == null || responses.isEmpty) return null;

    final first = responses.first as Map<String, dynamic>?;
    final err = first?['error'];
    if (err != null) {
      debugPrint('Cloud Vision OCR ($featureType): ${err['message']}');
      return null;
    }

    final fullTextAnnotation = first?['fullTextAnnotation'];
    if (fullTextAnnotation is Map<String, dynamic>) {
      final fullText = fullTextAnnotation['text'] as String?;
      if (fullText != null && fullText.isNotEmpty) return fullText;
    }

    final annotations = first?['textAnnotations'] as List<dynamic>?;
    if (annotations != null && annotations.isNotEmpty) {
      final firstAnnotation = annotations.first as Map<String, dynamic>?;
      final desc =
          firstAnnotation != null ? firstAnnotation['description'] as String? : null;
      if (desc != null && desc.isNotEmpty) return desc;
    }

    return null;
  } catch (e) {
    debugPrint('Cloud Vision OCR ($featureType) error: $e');
    return null;
  }
}
