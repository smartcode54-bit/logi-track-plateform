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

  /// Must match `setGlobalOptions({ region })` in `logitrack-web/functions`.
  static const String deployedRegion = 'asia-southeast1';

  /// Use for [HttpsCallable] when not using [call].
  static FirebaseFunctions get regional =>
      FirebaseFunctions.instanceFor(region: deployedRegion);

  /// Functions are deployed in [deployedRegion]; use this instance to call them.
  FirebaseFunctions get _functions =>
      FirebaseFunctions.instanceFor(region: deployedRegion);

  /// Call a callable Cloud Function by name.
  ///
  /// [name] — exported function name (e.g. 'notifyTaskUpdate', 'setAdminClaims').
  /// [data] — optional map of arguments (must be JSON-serializable).
  /// Returns the [data] field of the response.
  Future<T?> call<T>(String name, {Map<String, dynamic>? data}) async {
    final callable = _functions.httpsCallable(name);
    final result = await (data != null
        ? callable.call<T>(data)
        : callable.call<T>());
    return result.data;
  }
}
