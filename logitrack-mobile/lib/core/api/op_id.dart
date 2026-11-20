import 'package:uuid/uuid.dart';

const _uuid = Uuid();

/// A new operation id: UUIDv7, used as the `Idempotency-Key` of one submit (spec mobile-go-core-loop R15). Made once
/// when the driver presses submit and reused for every retry of that same submit.
String newOpId() => _uuid.v7();

/// A request id (UUIDv4) for `X-Request-Id`, one per HTTP request.
String newRequestId() => _uuid.v4();
