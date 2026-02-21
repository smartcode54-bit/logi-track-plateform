import 'dart:typed_data';
import 'package:flutter/foundation.dart' show debugPrint;
import 'package:flutter_image_compress/flutter_image_compress.dart';

/// Target max file size for upload (500KB)
const int kMaxUploadBytes = 500 * 1024;

/// Max width for resizing (keeps clarity for OCR and evidence)
const int kMaxWidth = 1024;

/// JPEG quality range 70–80%; we start at 75 and reduce if over 500KB
const int kDefaultQuality = 75;

/// Compress image bytes for upload: max width 1024px, JPEG 70–80%, under 500KB.
/// Input can be any format (PNG/JPEG from camera or overlay).
/// Returns JPEG bytes suitable for Firebase Storage.
Future<Uint8List> compressImageForUpload(List<int> imageBytes) async {
  if (imageBytes.isEmpty) return Uint8List(0);

  final input = Uint8List.fromList(imageBytes);
  int quality = kDefaultQuality;
  Uint8List? result;

  try {
    // Resize to max width 1024 (minHeight: 1 so scaling is driven by width)
    result = await FlutterImageCompress.compressWithList(
      input,
      minWidth: kMaxWidth,
      minHeight: 1,
      quality: quality,
      format: CompressFormat.jpeg,
    );
    if (result == null) return input;

    // If still over 500KB, reduce quality stepwise
    const qualitySteps = [65, 55, 45, 35];
    for (final q in qualitySteps) {
      if (result!.length <= kMaxUploadBytes) break;
      quality = q;
      result = await FlutterImageCompress.compressWithList(
        input,
        minWidth: kMaxWidth,
        minHeight: 1,
        quality: quality,
        format: CompressFormat.jpeg,
      );
      if (result == null) return input;
    }

    if (result != null && result!.length > kMaxUploadBytes) {
      debugPrint(
        'Image compression: still ${(result.length / 1024).toStringAsFixed(1)}KB after lowest quality',
      );
    }
    return result ?? input;
  } catch (e) {
    debugPrint('Image compression failed, using original: $e');
    return input;
  }
}
