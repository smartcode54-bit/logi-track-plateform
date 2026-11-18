import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/material.dart';
import 'package:flutter_bloc/flutter_bloc.dart';

import '../../../../core/api/upload_client.dart';
import '../../../../features/home/presentation/pages/qr_scan_page.dart';
import '../../../app/go_badge.dart';
import '../../../shared/evidence_form_bloc.dart';
import '../../../shared/evidence_widgets.dart';
import '../../../shared/photo_capture.dart';
import '../../tasks/domain/driver_task.dart';
import '../../trips/data/go_trip_repository.dart';
import 'loading_bloc.dart';

/// The gotest loading of a single-stop job (spec mobile-go-core-loop R13). The route is the job's (read-only); the
/// trip number comes from the runsheet's QR/barcode or is typed (no text OCR in this slice, Q8). Pops the created trip.
class GoLoadingPage extends StatefulWidget {
  const GoLoadingPage({super.key, required this.task, required this.uploads, required this.trips});
  final DriverTask task;
  final UploadClient uploads;
  final GoTripRepository trips;

  @override
  State<GoLoadingPage> createState() => _GoLoadingPageState();
}

class _GoLoadingPageState extends State<GoLoadingPage> {
  final _tripNo = TextEditingController();
  final _seal = TextEditingController();
  final _parcels = TextEditingController();
  final _weight = TextEditingController();
  final _capture = PhotoCapture();

  @override
  void dispose() {
    for (final c in [_tripNo, _seal, _parcels, _weight]) {
      c.dispose();
    }
    super.dispose();
  }

  void _changed(BuildContext context) {
    context.read<EvidenceFormBloc>().add(EvidenceFieldsChanged({
      LoadingBloc.fieldTripNo: _tripNo.text.trim(),
      LoadingBloc.fieldSeal: _seal.text.trim(),
      LoadingBloc.fieldParcels: _parcels.text.trim(),
      LoadingBloc.fieldWeight: _weight.text.trim(),
    }));
  }

  Future<void> _scan(BuildContext context) async {
    final raw = await Navigator.of(context).push<String>(
      MaterialPageRoute(builder: (_) => QrScanPage(title: 'loading_phase_trip_id'.tr())),
    );
    if (raw == null || raw.trim().isEmpty || !context.mounted) return;
    _tripNo.text = raw.trim();
    _changed(context);
  }

  @override
  Widget build(BuildContext context) {
    final task = widget.task;
    return BlocProvider<EvidenceFormBloc>(
      create: (_) => LoadingBloc(uploads: widget.uploads, trips: widget.trips, task: task),
      child: Builder(builder: (context) {
        return BlocListener<EvidenceFormBloc, EvidenceState>(
          listenWhen: (a, b) => a.result == null && b.result != null,
          listener: (context, state) => Navigator.of(context).pop(state.result),
          child: Scaffold(
            appBar: goAppBar('loading_phase_title'.tr()),
            body: ListView(padding: const EdgeInsets.all(16), children: [
              Card(
                child: ListTile(
                  title: Text(task.taskNo),
                  subtitle: Text('${'loading_phase_origin'.tr()}: ${task.source}\n'
                      '${'checkin_destination'.tr()}: ${task.destination}'),
                  isThreeLine: true,
                ),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _tripNo,
                decoration: InputDecoration(
                  labelText: '${'loading_phase_trip_id'.tr()} *',
                  suffixIcon: IconButton(icon: const Icon(Icons.qr_code_scanner), onPressed: () => _scan(context)),
                ),
                onChanged: (_) => _changed(context),
              ),
              TextField(
                controller: _seal,
                decoration: InputDecoration(labelText: 'loading_phase_seal_code'.tr()),
                onChanged: (_) => _changed(context),
              ),
              Row(children: [
                Expanded(
                  child: TextField(
                    controller: _parcels,
                    keyboardType: TextInputType.number,
                    decoration: InputDecoration(labelText: 'go_parcel_count'.tr()),
                    onChanged: (_) => _changed(context),
                  ),
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: TextField(
                    controller: _weight,
                    keyboardType: const TextInputType.numberWithOptions(decimal: true),
                    decoration: InputDecoration(labelText: 'go_total_weight'.tr()),
                    onChanged: (_) => _changed(context),
                  ),
                ),
              ]),
              const SizedBox(height: 16),
              EvidenceSlots(capture: _capture),
              const SizedBox(height: 24),
              const EvidenceSubmitBar(labelKey: 'go_submit_loading', errorOverrides: {
                'duplicate_trip_id': 'loading_error_duplicate_trip_id',
                'duplicate_seal': 'loading_error_duplicate_seal',
                'task_not_checked_in': 'loading_error_not_checked_in',
                'task_has_open_trip': 'loading_error_open_trip',
                'multi_drop_not_supported': 'gotest_multi_drop_unavailable',
                'not_task_owner': 'checkin_error_not_task_owner',
              }),
            ]),
          ),
        );
      }),
    );
  }
}
