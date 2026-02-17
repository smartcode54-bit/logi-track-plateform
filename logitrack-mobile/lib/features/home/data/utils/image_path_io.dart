import 'dart:io';
import 'package:path_provider/path_provider.dart';
import 'package:image_picker/image_picker.dart';

/// บน Android/iOS: คืน path ของรูป (จาก xfile.path หรือเขียนลง temp แล้วคืน path)
Future<String?> getImagePathForDecode(XFile xfile) async {
  final path = xfile.path;
  // ignore: unnecessary_null_comparison — xfile.path nullable in API but analyzer may treat as non-null
  if (path != null && path.isNotEmpty) return path;
  final dir = await getTemporaryDirectory();
  final bytes = await xfile.readAsBytes();
  final file = File('${dir.path}/qr_scan_${DateTime.now().millisecondsSinceEpoch}.jpg');
  await file.writeAsBytes(bytes);
  return file.path;
}
