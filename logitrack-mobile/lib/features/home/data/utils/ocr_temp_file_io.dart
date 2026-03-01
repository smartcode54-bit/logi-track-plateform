import 'dart:io';
import 'dart:typed_data';

import 'package:path_provider/path_provider.dart';

/// สร้างไฟล์ชั่วคราวจาก bytes แล้วคืน path (สำหรับสแกน QR จาก bytes)
Future<String?> createTempFileFromBytes(List<int> imageBytes) async {
  try {
    final dir = await getTemporaryDirectory();
    final file = File(
      '${dir.path}/ocr_runsheet_${DateTime.now().millisecondsSinceEpoch}.jpg',
    );
    await file.writeAsBytes(
      imageBytes is Uint8List ? imageBytes : Uint8List.fromList(imageBytes),
    );
    return file.path;
  } catch (_) {
    return null;
  }
}

/// ลบไฟล์ชั่วคราวหลังสแกนเสร็จ
Future<void> deleteTempFile(String path) async {
  try {
    await File(path).delete();
  } catch (_) {}
}
