import 'dart:typed_data';

import 'package:image/image.dart' as img;

/// Grayscale + contrast + downscale so Cloud Vision gets sharper edges on phone photos.
List<int>? preprocessImageForOcr(List<int> imageBytes) {
  try {
    final decoded = img.decodeImage(Uint8List.fromList(imageBytes));
    if (decoded == null) return null;
    var i = decoded;
    const maxSide = 1800;
    if (i.width > maxSide || i.height > maxSide) {
      if (i.width >= i.height) {
        i = img.copyResize(i, width: maxSide);
      } else {
        i = img.copyResize(i, height: maxSide);
      }
    }
    i = img.grayscale(i);
    i = img.adjustColor(i, contrast: 1.25, brightness: 1.03);
    return img.encodeJpg(i, quality: 90);
  } catch (_) {
    return null;
  }
}
