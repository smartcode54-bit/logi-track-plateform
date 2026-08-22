import 'package:flutter_test/flutter_test.dart';
import 'package:logi_track_driver_app/features/trip_history/data/trip_photo_order.dart';

/// Unit tests for the workflow ordering behind ADR 0018 / spec
/// `mobile-download-trip-photos` — the sort that makes the bulk download come
/// out in loading → delivery → multi-stop → incident → unknown order.
void main() {
  group('tripPhotoWorkflowRank — loading group', () {
    test('runsheet family then camera steps in workflow order', () {
      final seq = [
        'runsheet',
        'runsheet_extra_1',
        'runsheet_extra_2',
        'runsheet_extra_3',
        'pre_close',
        'closing',
        'seal',
      ];
      for (var i = 0; i < seq.length - 1; i++) {
        expect(
          tripPhotoWorkflowRank(seq[i]) < tripPhotoWorkflowRank(seq[i + 1]),
          isTrue,
          reason: '${seq[i]} should rank before ${seq[i + 1]}',
        );
      }
    });
  });

  group('tripPhotoWorkflowRank — single delivery group', () {
    test('delivery steps in workflow order, all after loading', () {
      final delivery = ['pre_open', 'opening', 'empty_container', 'runsheet_received'];
      for (var i = 0; i < delivery.length - 1; i++) {
        expect(
          tripPhotoWorkflowRank(delivery[i]) < tripPhotoWorkflowRank(delivery[i + 1]),
          isTrue,
        );
      }
      expect(
        tripPhotoWorkflowRank('seal') < tripPhotoWorkflowRank('pre_open'),
        isTrue,
        reason: 'loading must come before delivery',
      );
    });
  });

  group('tripPhotoWorkflowRank — multi-stop group', () {
    test('after single delivery, ordered by stop index then subtype', () {
      expect(
        tripPhotoWorkflowRank('runsheet_received') <
            tripPhotoWorkflowRank('stop_0_pre_open'),
        isTrue,
        reason: 'single delivery must come before multi-stop',
      );
      expect(
        tripPhotoWorkflowRank('stop_0_pre_open') <
            tripPhotoWorkflowRank('stop_1_pre_open'),
        isTrue,
        reason: 'lower stop index first',
      );
      expect(
        tripPhotoWorkflowRank('stop_0_pre_open') <
            tripPhotoWorkflowRank('stop_0_opening'),
        isTrue,
        reason: 'within a stop, keep delivery subtype order',
      );
    });

    test('realistic max stop index stays below the incident group (< 4000)', () {
      expect(tripPhotoWorkflowRank('stop_20_runsheet_received') < 4000, isTrue);
    });
  });

  group('tripPhotoWorkflowRank — unknown/legacy', () {
    test('unknown type sorts last (after every known trip type)', () {
      expect(tripPhotoWorkflowRank('totally_unknown'), 9000);
      expect(
        tripPhotoWorkflowRank('seal') < tripPhotoWorkflowRank('totally_unknown'),
        isTrue,
      );
      expect(
        tripPhotoWorkflowRank('stop_0_pre_open') <
            tripPhotoWorkflowRank('totally_unknown'),
        isTrue,
      );
    });
  });

  group('incidentPhotoRank', () {
    test('sits after trip photos but before unknown types', () {
      expect(
        tripPhotoWorkflowRank('stop_20_runsheet_received') <
            incidentPhotoRank(0, 'map'),
        isTrue,
        reason: 'incident comes after multi-stop',
      );
      expect(
        incidentPhotoRank(5, 'situation2') < tripPhotoWorkflowRank('unknown'),
        isTrue,
        reason: 'incident comes before unknown/legacy',
      );
    });

    test('ordered by report sequence, then map → situation1 → situation2', () {
      expect(incidentPhotoRank(0, 'map') < incidentPhotoRank(0, 'situation1'), isTrue);
      expect(
        incidentPhotoRank(0, 'situation1') < incidentPhotoRank(0, 'situation2'),
        isTrue,
      );
      expect(
        incidentPhotoRank(0, 'situation2') < incidentPhotoRank(1, 'map'),
        isTrue,
        reason: 'a later report always sorts after an earlier one',
      );
    });
  });

  group('end-to-end ordering', () {
    test('a scrambled mixed set sorts into workflow order', () {
      // (rankKey, label) — deliberately shuffled input.
      final input = <MapEntry<int, String>>[
        MapEntry(incidentPhotoRank(0, 'map'), 'incident0_map'),
        MapEntry(tripPhotoWorkflowRank('seal'), 'seal'),
        MapEntry(tripPhotoWorkflowRank('stop_1_pre_open'), 'stop1_pre_open'),
        MapEntry(tripPhotoWorkflowRank('runsheet'), 'runsheet'),
        MapEntry(tripPhotoWorkflowRank('pre_open'), 'pre_open'),
        MapEntry(tripPhotoWorkflowRank('stop_0_pre_open'), 'stop0_pre_open'),
        MapEntry(tripPhotoWorkflowRank('mystery'), 'mystery'),
        MapEntry(incidentPhotoRank(0, 'situation1'), 'incident0_sit1'),
      ]..sort((a, b) => a.key.compareTo(b.key));

      expect(input.map((e) => e.value).toList(), [
        'runsheet',
        'seal',
        'pre_open',
        'stop0_pre_open',
        'stop1_pre_open',
        'incident0_map',
        'incident0_sit1',
        'mystery',
      ]);
    });
  });
}
