import 'package:url_launcher/url_launcher.dart';

import '../../../home/data/repositories/checkin_repository.dart';

/// เปิด Google Maps นำทางจาก **พิกัดปัจจุบัน** (GPS) ไปยังอู่/ปลายทาง
///
/// ถ้าไม่ได้รับตำแหน่ง (ปิด GPS / ปฏิเสธสิทธิ์) จะ fallback เป็นลิงก์ destination อย่างเดียว
/// ให้แอป Maps ใช้จุดเริ่มตามที่ผู้ใช้เลือก
Future<bool> openGoogleMapsDirectionsFromHere({
  required double destinationLat,
  required double destinationLng,
}) async {
  try {
    final pos = await getCurrentPosition();
    final uri = Uri.parse(
      'https://www.google.com/maps/dir/?api=1'
      '&origin=${pos.latitude},${pos.longitude}'
      '&destination=$destinationLat,$destinationLng'
      '&travelmode=driving',
    );
    return launchUrl(uri, mode: LaunchMode.externalApplication);
  } catch (_) {
    final uri = Uri.parse(
      'https://www.google.com/maps/dir/?api=1'
      '&destination=$destinationLat,$destinationLng'
      '&travelmode=driving',
    );
    return launchUrl(uri, mode: LaunchMode.externalApplication);
  }
}
