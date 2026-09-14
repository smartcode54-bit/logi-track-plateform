import 'package:flutter_test/flutter_test.dart';
import 'package:logi_track_driver_app/features/home/data/repositories/task_repository.dart';

/// Unit tests for the driver check-in queue order (ADR 0027 / 0028).
///
/// The queue is ordered by the **actual pickup** date-time, not the plan date: the plan date is a
/// billing tag that can sit on a different day (or month) from the real work, and this list does not
/// just display — `isQueueEligibleForCheckIn` blocks a task while anything above it is unfinished,
/// so a wrong order locks a driver out of the job they are standing at.
Map<String, dynamic> task({
  required String id,
  DateTime? date,
  String? time,
  DateTime? actualPickupAt,
  int? runOrder,
  DateTime? createdAt,
  String status = 'Assigned',
}) {
  return {
    'id': id,
    'date': ?date,
    'time': ?time,
    'actualPickupAt': ?actualPickupAt,
    'runOrder': ?runOrder,
    'createdAt': ?createdAt,
    'status': status,
  };
}

List<String> orderedIds(List<Map<String, dynamic>> list) {
  sortTasksForDriverQueue(list);
  return list.map((t) => t['id'] as String).toList();
}

void main() {
  group('taskQueueInstant', () {
    test('uses the actual pickup date-time when the admin set one', () {
      final actual = DateTime(2026, 9, 2, 14, 30);
      final t = task(
        id: 'a',
        date: DateTime(2026, 8, 30),
        time: '14:30',
        actualPickupAt: actual,
      );
      expect(taskQueueInstant(t), actual);
    });

    test('falls back to the plan date at its stored time', () {
      final t = task(id: 'a', date: DateTime(2026, 9, 2), time: '08:05');
      expect(taskQueueInstant(t), DateTime(2026, 9, 2, 8, 5));
    });

    test('accepts a single-digit hour and treats an unparsable time as midnight', () {
      expect(
        taskQueueInstant(task(id: 'a', date: DateTime(2026, 9, 2), time: '8:05')),
        DateTime(2026, 9, 2, 8, 5),
      );
      expect(
        taskQueueInstant(task(id: 'b', date: DateTime(2026, 9, 2), time: '-')),
        DateTime(2026, 9, 2),
      );
      expect(
        taskQueueInstant(task(id: 'c', date: DateTime(2026, 9, 2))),
        DateTime(2026, 9, 2),
      );
    });

    test('is null when the task is not schedulable at all', () {
      expect(taskQueueInstant(task(id: 'a')), isNull);
    });
  });

  group('compareTasksForDriverQueue', () {
    test('orders by the actual pickup day, even against an earlier plan date', () {
      // The case this change exists for: "b" is planned for the 30th (its billing month) but is
      // actually picked up on 2 Sep, so it must run AFTER the job dispatched on 1 Sep.
      final ids = orderedIds([
        task(
          id: 'b',
          date: DateTime(2026, 8, 30),
          time: '09:00',
          actualPickupAt: DateTime(2026, 9, 2, 9, 0),
        ),
        task(
          id: 'a',
          date: DateTime(2026, 9, 1),
          time: '13:00',
          actualPickupAt: DateTime(2026, 9, 1, 13, 0),
        ),
      ]);
      expect(ids, ['a', 'b']);
    });

    test('orders by time within the same pickup day', () {
      final ids = orderedIds([
        task(id: 'late', actualPickupAt: DateTime(2026, 9, 1, 16, 0)),
        task(id: 'early', actualPickupAt: DateTime(2026, 9, 1, 6, 30)),
        task(id: 'mid', actualPickupAt: DateTime(2026, 9, 1, 11, 15)),
      ]);
      expect(ids, ['early', 'mid', 'late']);
    });

    test('keeps plan-date order for tasks with no actual pickup (legacy unchanged)', () {
      final ids = orderedIds([
        task(id: 'second', date: DateTime(2026, 9, 1), time: '15:00'),
        task(id: 'first', date: DateTime(2026, 9, 1), time: '07:00'),
        task(id: 'third', date: DateTime(2026, 9, 3), time: '06:00'),
      ]);
      expect(ids, ['first', 'second', 'third']);
    });

    test('interleaves scheduled and plan-only tasks by real chronology', () {
      final ids = orderedIds([
        task(id: 'dispatched', actualPickupAt: DateTime(2026, 9, 1, 14, 0)),
        task(id: 'planOnly', date: DateTime(2026, 9, 1), time: '08:00'),
      ]);
      expect(ids, ['planOnly', 'dispatched']);
    });

    test('sorts unschedulable tasks last', () {
      final ids = orderedIds([
        task(id: 'noDate'),
        task(id: 'dated', date: DateTime(2026, 9, 5), time: '09:00'),
      ]);
      expect(ids, ['dated', 'noDate']);
    });

    test('breaks a tie on runOrder, legacy no-runOrder first', () {
      final at = DateTime(2026, 9, 1, 9, 0);
      final ids = orderedIds([
        task(id: 'ro2', actualPickupAt: at, runOrder: 2),
        task(id: 'ro1', actualPickupAt: at, runOrder: 1),
        task(id: 'legacy', actualPickupAt: at),
      ]);
      expect(ids, ['legacy', 'ro1', 'ro2']);
    });

    test('falls back to createdAt when instant and runOrder tie', () {
      final at = DateTime(2026, 9, 1, 9, 0);
      final ids = orderedIds([
        task(id: 'newer', actualPickupAt: at, runOrder: 1, createdAt: DateTime(2026, 8, 31, 18)),
        task(id: 'older', actualPickupAt: at, runOrder: 1, createdAt: DateTime(2026, 8, 31, 9)),
      ]);
      expect(ids, ['older', 'newer']);
    });
  });
}
