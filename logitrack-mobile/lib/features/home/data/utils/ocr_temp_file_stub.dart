
/// Stub สำหรับ Web: ไม่สร้าง temp file (dart:io ไม่มีบน Web)
Future<String?> createTempFileFromBytes(List<int> imageBytes) async => null;

Future<void> deleteTempFile(String path) async {}
