import 'dart:io';
import 'dart:typed_data';

Future<String> ensureDraftDir(String path) async {
  final dir = Directory(path);
  if (!await dir.exists()) await dir.create(recursive: true);
  return path;
}

Future<String?> writeDraftPhoto(String path, Uint8List bytes) async {
  try {
    final file = File(path);
    await file.writeAsBytes(bytes);
    return path;
  } catch (_) {
    return null;
  }
}

Future<Uint8List?> readDraftPhoto(String path) async {
  try {
    final file = File(path);
    if (!await file.exists()) return null;
    return await file.readAsBytes();
  } catch (_) {
    return null;
  }
}

Future<void> clearDraftFiles(String basePath, {bool loading = false, bool delivery = false}) async {
  try {
    final dir = Directory(basePath);
    if (!await dir.exists()) return;
    await for (final f in dir.list()) {
      if (f is File) {
        final name = f.path.split(RegExp(r'[/\\]')).last;
        if (loading && (name == 'loading_runsheet.jpg' || name.startsWith('loading_'))) {
          await f.delete();
        }
        if (delivery && name.startsWith('delivery_')) {
          await f.delete();
        }
      }
    }
  } catch (_) {}
}
