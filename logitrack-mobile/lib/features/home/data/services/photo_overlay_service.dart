import 'dart:typed_data';
import 'package:image/image.dart' as img;

/// Overlays geocoding (lat/lng) and timestamp onto image bytes.
/// Used for in-app camera photos in Loading Phase and Check-in so evidence is stamped on the file.
/// Returns new PNG bytes. [imageBytes] works on all platforms (including web).
Future<List<int>> overlayGeocodingAndTimestamp({
  required List<int> imageBytes,
  required double lat,
  required double lng,
  required DateTime timestamp,
  String? address,
}) async {
  final image = img.decodeImage(Uint8List.fromList(imageBytes));
  if (image == null) throw Exception('Could not decode image');

  final coordLine = '${lat.toStringAsFixed(6)}, ${lng.toStringAsFixed(6)}';
  final timeLine = timestamp.toIso8601String();
  final stamp = address != null && address.isNotEmpty
      ? '$coordLine\n$address\n$timeLine'
      : '$coordLine\n$timeLine';

  const lineHeight = 18;
  const padding = 8;
  final numLines = stamp.split('\n').length;
  final stampHeight = (lineHeight * numLines) + padding * 2;
  final textY = image.height - stampHeight + padding;
  final textX = padding;

  if (textY > 0 && textX < image.width) {
    final barTop = textY - 2;
    final barBottom = image.height;
    img.fillRect(
      image,
      x1: 0,
      y1: barTop,
      x2: image.width,
      y2: barBottom,
      color: img.ColorRgba8(0, 0, 0, 180),
    );
    img.drawString(
      image,
      stamp,
      font: img.arial14,
      x: textX,
      y: textY,
      color: img.ColorRgba8(255, 255, 255, 255),
    );
  }
  return img.encodePng(image) ?? imageBytes;
}
