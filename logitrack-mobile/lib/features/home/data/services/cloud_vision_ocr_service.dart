import 'dart:convert';

import 'package:flutter/foundation.dart' show debugPrint;
import 'package:flutter_dotenv/flutter_dotenv.dart';
import 'package:http/http.dart' as http;

const _visionBase = 'https://vision.googleapis.com/v1/images:annotate';

/// เรียก Google Cloud Vision API ด้วยรูป base64 คืน full text หรือ null
/// ใช้ GOOGLE_CLOUD_VISION_API_KEY ก่อน ไม่มีค่อยใช้ GOOGLE_MAPS_API_KEY
Future<String?> runCloudVisionOcrOnImageBytes(List<int> imageBytes) async {
  final apiKey = dotenv.env['GOOGLE_CLOUD_VISION_API_KEY']?.trim() ??
      dotenv.env['GOOGLE_MAPS_API_KEY']?.trim();
  if (apiKey == null || apiKey.isEmpty) {
    debugPrint('Cloud Vision OCR: GOOGLE_CLOUD_VISION_API_KEY / GOOGLE_MAPS_API_KEY not set');
    return null;
  }

  try {
    final base64Image = base64Encode(imageBytes);
    final uri = Uri.parse('$_visionBase?key=$apiKey');
    // DOCUMENT_TEXT_DETECTION เหมาะกับบิล/ใบเสร็จ ( dense text ) มากกว่า TEXT_DETECTION
    final body = {
      'requests': [
        {
          'image': {'content': base64Image},
          'features': [
            {'type': 'DOCUMENT_TEXT_DETECTION', 'maxResults': 1},
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
        .timeout(const Duration(seconds: 30));

    if (response.statusCode != 200) {
      final bodyPreview = response.body.length > 300 ? '${response.body.substring(0, 300)}...' : response.body;
      debugPrint('Cloud Vision OCR: API error ${response.statusCode} $bodyPreview');
      return null;
    }

    final data = jsonDecode(response.body) as Map<String, dynamic>?;
    final responses = data?['responses'] as List<dynamic>?;
    if (responses == null || responses.isEmpty) return null;

    final first = responses.first as Map<String, dynamic>?;
    final err = first?['error'];
    if (err != null) {
      debugPrint('Cloud Vision OCR: ${err['message']}');
      return null;
    }

    // fullTextAnnotation.text = ข้อความทั้งบล็อก (DOCUMENT_TEXT_DETECTION คืนค่าตรงนี้)
    final fullTextAnnotation = first?['fullTextAnnotation'];
    if (fullTextAnnotation is Map<String, dynamic>) {
      final fullText = fullTextAnnotation['text'] as String?;
      if (fullText != null && fullText.isNotEmpty) return fullText;
    }

    // fallback: textAnnotations[0].description (ข้อความรวมทั้งรูป)
    final annotations = first?['textAnnotations'] as List<dynamic>?;
    if (annotations != null && annotations.isNotEmpty) {
      final firstAnnotation = annotations.first as Map<String, dynamic>?;
      final desc = firstAnnotation != null ? firstAnnotation['description'] as String? : null;
      if (desc != null && desc.isNotEmpty) return desc;
    }

    debugPrint('Cloud Vision OCR: 200 OK but no text in response. Keys: ${first?.keys.toList()}');
    return null;
  } catch (e, st) {
    debugPrint('Cloud Vision OCR: $e\n$st');
    return null;
  }
}
