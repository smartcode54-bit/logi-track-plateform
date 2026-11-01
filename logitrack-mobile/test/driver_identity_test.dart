import 'package:flutter_test/flutter_test.dart';
import 'package:logi_track_driver_app/core/utils/driver_identity.dart';

void main() {
  group('driverIdentityKeys', () {
    test('returns the current Auth UID and every past one from authIdHistory', () {
      expect(
        driverIdentityKeys({
          'authId': 'uid_new',
          'authIdHistory': ['W47P8pGs', 'uid_old2'],
        }),
        ['uid_new', 'W47P8pGs', 'uid_old2'],
      );
    });

    test('includes the doc id first when given', () {
      expect(
        driverIdentityKeys({
          'authId': 'uid_new',
          'authIdHistory': ['uid_old'],
        }, docId: 'drv_1'),
        ['drv_1', 'uid_new', 'uid_old'],
      );
    });

    test('accepts the legacy authUid field and de-dupes repeated ids', () {
      expect(
        driverIdentityKeys({
          'authUid': 'uid_x',
          'authIdHistory': ['uid_x', 'uid_y'],
        }),
        ['uid_x', 'uid_y'],
      );
    });

    test('ignores blanks, a non-list history and a missing driver doc', () {
      expect(
        driverIdentityKeys({
          'authId': 'uid_1',
          'authIdHistory': ['', '   ', null, 'uid_2'],
        }),
        ['uid_1', 'uid_2'],
      );
      expect(driverIdentityKeys({'authId': 'uid_1', 'authIdHistory': 'oops'}), ['uid_1']);
      expect(driverIdentityKeys(null, docId: 'drv_9'), ['drv_9']);
      expect(driverIdentityKeys(null), isEmpty);
    });
  });
}
