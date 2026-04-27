import 'package:url_launcher/url_launcher.dart';

/// เปิดแอปแผนที่ (Google Maps) โหมดขับรถ จากตำแหน่งปัจจุบัน → ปลายทาง
///
/// ถ้ามีพิกัดปลายทางใช้พิกัด; ถ้าไม่มีใช้ชื่อสถานที่ให้ Google ค้นหา
/// อ้างอิง: https://developers.google.com/maps/documentation/urls/get-started#directions-action
Future<bool> openGoogleMapsDrivingDirections({
  double? originLat,
  double? originLng,
  double? destLat,
  double? destLng,
  String? destinationPlaceName,
}) async {
  final name = destinationPlaceName?.trim();
  final q = <String, String>{
    'api': '1',
    'travelmode': 'driving',
  };
  if (originLat != null &&
      originLng != null &&
      originLat.isFinite &&
      originLng.isFinite) {
    q['origin'] =
        '${originLat.toStringAsFixed(6)},${originLng.toStringAsFixed(6)}';
  }
  if (destLat != null &&
      destLng != null &&
      destLat.isFinite &&
      destLng.isFinite) {
    q['destination'] =
        '${destLat.toStringAsFixed(6)},${destLng.toStringAsFixed(6)}';
  } else if (name != null && name.isNotEmpty) {
    q['destination'] = name;
  } else {
    return false;
  }

  final uri = Uri.https('www.google.com', '/maps/dir/', q);
  try {
    return await launchUrl(uri, mode: LaunchMode.externalApplication);
  } catch (_) {
    return false;
  }
}
