import 'dart:typed_data';

Future<String> ensureDraftDir(String path) async => path;

Future<String?> writeDraftPhoto(String path, Uint8List bytes) async => null;

Future<Uint8List?> readDraftPhoto(String path) async => null;

Future<void> clearDraftFiles(String basePath, {bool loading = false, bool delivery = false}) async {}
