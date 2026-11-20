import 'dart:typed_data';

import 'package:dio/dio.dart';

import 'api_client.dart';
import 'api_exception.dart';

/// Photo uploads of the gotest app (spec mobile-go-core-loop R12-R15, N3): `POST /v1/mobile/uploads/presign` on the
/// API (bearer), then a `PUT` of the bytes straight to the returned URL on a separate Dio **without** the bearer.
/// The returned key is what the entity request references; the server commits it in the entity transaction.
///
/// A key already uploaded is never uploaded again (the bloc keeps it across retries). When the signed URL expired
/// before the PUT (403), the upload is signed again for the same key (`{key}`, the re-sign of a pending upload).
class UploadClient {
  UploadClient(this.api, {Dio? putDio})
      : _put = putDio ??
            Dio(BaseOptions(
              connectTimeout: const Duration(seconds: 15),
              sendTimeout: const Duration(seconds: 60),
              receiveTimeout: const Duration(seconds: 30),
            ));

  final ApiClient api;
  final Dio _put;

  /// Uploads [bytes] (JPEG) for [purpose] (`checkin_photo`, `checkin_app_screenshot`, `trip_photo`) of the entity
  /// [entityId] (the task for a check-in, the trip or the submit's op id for trip photos); [variant] is the trip
  /// photo type. Returns the object key.
  Future<String> uploadJpeg({
    required String purpose,
    required String entityId,
    required Uint8List bytes,
    String? variant,
  }) async {
    var signed = await _presign({
      'purpose': purpose,
      'entityId': entityId,
      'contentType': 'image/jpeg',
      'sizeBytes': bytes.length,
      'variant': ?variant,
    });
    try {
      await _send(signed, bytes);
    } on ApiException catch (e) {
      if (e.status != 403) rethrow;
      signed = await _presign({'key': signed.key, 'contentType': 'image/jpeg', 'sizeBytes': bytes.length});
      await _send(signed, bytes);
    }
    return signed.key;
  }

  Future<_Presigned> _presign(Map<String, dynamic> body) async {
    final d = await api.postData('/v1/mobile/uploads/presign', body);
    if (d == null) throw ApiException(status: 0, code: 'presign_empty');
    final headers = <String, String>{};
    final rawHeaders = d['headers'];
    if (rawHeaders is Map) {
      rawHeaders.forEach((k, v) => headers['$k'] = '$v');
    }
    return _Presigned(
      key: d['key'] as String,
      url: d['url'] as String,
      method: (d['method'] as String?) ?? 'PUT',
      headers: headers,
    );
  }

  Future<void> _send(_Presigned p, Uint8List bytes) async {
    try {
      await _put.request<dynamic>(
        p.url,
        data: Stream.fromIterable([bytes]),
        options: Options(
          method: p.method,
          headers: {...p.headers, Headers.contentLengthHeader: bytes.length},
          responseType: ResponseType.plain,
        ),
      );
    } on DioException catch (e) {
      final res = e.response;
      if (res == null) throw NetworkException(e.error ?? e.type);
      throw ApiException(status: res.statusCode ?? 0, code: 'upload_failed');
    }
  }
}

class _Presigned {
  _Presigned({required this.key, required this.url, required this.method, required this.headers});
  final String key;
  final String url;
  final String method;
  final Map<String, String> headers;
}
