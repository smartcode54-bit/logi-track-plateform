import 'dart:typed_data';

import 'package:image_picker/image_picker.dart';

import '../../features/home/data/services/evidence_location_cache.dart';
import '../../features/home/data/services/image_compression_service.dart';
import '../../features/home/data/services/photo_overlay_service.dart';

/// One evidence photo ready to upload: compressed JPEG bytes and where it was taken.
class CapturedPhoto {
  CapturedPhoto({required this.bytes, required this.takenAt, this.lat, this.lng, this.address});
  final Uint8List bytes;
  final DateTime takenAt;
  final double? lat;
  final double? lng;
  final String? address;
}

/// The camera and the screenshot picker of the gotest pages (spec mobile-go-core-loop D2): the same overlay and
/// compression as the 3.x pages, without any Firebase call. A camera photo is stamped with the current position
/// (EvidenceLocationCache: a fix at most 60 s old, the overlay refreshed after 100 m); no position = no pin. A
/// customer-app screenshot is never overlaid (ADR 0019).
class PhotoCapture {
  PhotoCapture({ImagePicker? picker}) : _picker = picker ?? ImagePicker();

  final ImagePicker _picker;
  final EvidenceLocationCache _location = EvidenceLocationCache();

  Future<CapturedPhoto?> camera() async {
    final file = await _picker.pickImage(source: ImageSource.camera, imageQuality: 90);
    if (file == null) return null;
    final raw = await file.readAsBytes();
    final now = DateTime.now();
    final here = await _location.current();
    final pos = here.position;
    List<int> stamped = raw;
    if (pos != null) {
      stamped = await overlayGeocodingAndTimestamp(
        imageBytes: raw,
        lat: pos.latitude,
        lng: pos.longitude,
        timestamp: now,
        ctx: here.context,
      );
    }
    return CapturedPhoto(
      bytes: await compressImageForUpload(stamped),
      takenAt: now,
      lat: pos?.latitude,
      lng: pos?.longitude,
      address: here.context?.address,
    );
  }

  Future<CapturedPhoto?> screenshot() async {
    final file = await _picker.pickImage(source: ImageSource.gallery);
    if (file == null) return null;
    return CapturedPhoto(bytes: await compressImageForUpload(await file.readAsBytes()), takenAt: DateTime.now());
  }
}
