import 'package:cloud_functions/cloud_functions.dart';

/// Central place to call Firebase Cloud Functions.
///
/// Functions are deployed to region [asia-southeast1] (see logitrack-web/functions).
/// They run on Google's servers — the app does NOT "install" functions on the device;
/// it sends an HTTP request and receives the result.
///
/// Usage:
///   final result = await CloudFunctionsService.instance.call('functionName', data: {'key': 'value'});
class CloudFunctionsService {
  CloudFunctionsService._();

  static final CloudFunctionsService instance = CloudFunctionsService._();

  /// Functions are deployed in asia-southeast1; use this instance to call them.
  FirebaseFunctions get _functions =>
      FirebaseFunctions.instanceFor(region: 'asia-southeast1');

  /// Call a callable Cloud Function by name.
  ///
  /// [name] — exported function name (e.g. 'notifyFirstMileTaskUpdate', 'setAdminClaims').
  /// [data] — optional map of arguments (must be JSON-serializable).
  /// Returns the [data] field of the response.
  Future<T?> call<T>(String name, {Map<String, dynamic>? data}) async {
    final callable = _functions.httpsCallable<T>(name);
    final result = data != null ? await callable.call(data) : await callable.call();
    return result.data;
  }
}
