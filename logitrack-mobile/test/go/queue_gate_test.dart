import 'package:flutter_test/flutter_test.dart';
import 'package:logi_track_driver_app/go/features/tasks/domain/driver_task.dart';
import 'package:logi_track_driver_app/go/features/tasks/domain/queue_gate.dart';

DriverTask task(String id, String status, {String? tripStatus}) => DriverTask.fromJson({
      'id': id,
      'taskNo': id,
      'taskType': 'first_mile',
      'status': status,
      'isMultiDelivery': false,
      if (tripStatus != null) 'trip': {'id': 't-$id', 'tripNo': 'ZX$id', 'status': tripStatus},
    });

/// The queue gate table of the spec (mobile-go-core-loop AC3, R11): the server enforces the same rule (AC19).
void main() {
  group('taskBlocksSuccessor', () {
    final cases = <String, (DriverTask, bool)>{
      'pending blocks': (task('a', 'pending'), true),
      'assigned blocks': (task('a', 'assigned'), true),
      'in transit blocks': (task('a', 'in_transit', tripStatus: 'in_transit'), true),
      'checked in without a trip blocks': (task('a', 'checked_in'), true),
      'checked in with an open trip blocks': (task('a', 'checked_in', tripStatus: 'in_transit'), true),
      'checked in with a delivered trip does not': (task('a', 'checked_in', tripStatus: 'delivered'), false),
      'completed does not': (task('a', 'completed'), false),
      'cancelled does not': (task('a', 'cancelled'), false),
      'unknown status blocks': (task('a', 'something_new'), true),
    };
    cases.forEach((name, c) => test(name, () => expect(taskBlocksSuccessor(c.$1), c.$2)));
  });

  test('the first task is always eligible; a later one waits for every blocker before it', () {
    final q = [task('1', 'assigned'), task('2', 'assigned')];
    expect(isQueueEligibleForCheckIn(q[0], q), isTrue);
    expect(isQueueEligibleForCheckIn(q[1], q), isFalse);
    final done = [task('1', 'checked_in', tripStatus: 'delivered'), task('2', 'assigned')];
    expect(isQueueEligibleForCheckIn(done[1], done), isTrue);
  });

  test('a task missing from the queue is not eligible', () {
    expect(isQueueEligibleForCheckIn(task('x', 'assigned'), [task('1', 'assigned')]), isFalse);
  });
}
