import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/material.dart';
import 'package:flutter_bloc/flutter_bloc.dart';

import '../../../../core/api/upload_client.dart';
import '../../../app/go_badge.dart';
import '../../../shared/evidence_form_bloc.dart';
import '../../../shared/evidence_widgets.dart';
import '../../../shared/photo_capture.dart';
import '../../trips/data/go_trip_repository.dart';
import '../../trips/domain/mobile_trip.dart';
import 'delivery_bloc.dart';

/// The gotest delivery of a single-stop trip (spec mobile-go-core-loop R14). Pops the delivered trip.
class GoDeliveryPage extends StatelessWidget {
  const GoDeliveryPage({super.key, required this.trip, required this.uploads, required this.trips});
  final MobileTrip trip;
  final UploadClient uploads;
  final GoTripRepository trips;

  @override
  Widget build(BuildContext context) {
    return BlocProvider<EvidenceFormBloc>(
      create: (_) => DeliveryBloc(uploads: uploads, trips: trips, trip: trip),
      child: BlocListener<EvidenceFormBloc, EvidenceState>(
        listenWhen: (a, b) => a.result == null && b.result != null,
        listener: (context, state) => Navigator.of(context).pop(state.result),
        child: Scaffold(
          appBar: goAppBar('go_delivery_title'.tr()),
          body: ListView(padding: const EdgeInsets.all(16), children: [
            Card(
              child: ListTile(
                title: Text(trip.tripNo),
                subtitle: Text('${'loading_phase_origin'.tr()}: ${trip.origin ?? '-'}\n'
                    '${'checkin_destination'.tr()}: ${trip.destination ?? '-'}'),
                isThreeLine: true,
              ),
            ),
            const SizedBox(height: 16),
            EvidenceSlots(capture: PhotoCapture()),
            const SizedBox(height: 24),
            const EvidenceSubmitBar(labelKey: 'go_submit_delivery', errorOverrides: {
              'trip_not_open': 'delivery_error_trip_not_open',
              'multi_drop_not_supported': 'gotest_multi_drop_unavailable',
              'not_task_owner': 'checkin_error_not_task_owner',
            }),
          ]),
        ),
      ),
    );
  }
}
