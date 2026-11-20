import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/material.dart';
import 'package:flutter_bloc/flutter_bloc.dart';

import '../../../../components/truck_picker_field.dart';
import '../../../../core/api/upload_client.dart';
import '../../../../features/home/data/repositories/trucks_repository.dart' show SelectableTruck;
import '../../../app/go_badge.dart';
import '../../../shared/evidence_form_bloc.dart';
import '../../../shared/evidence_widgets.dart';
import '../../../shared/photo_capture.dart';
import '../../tasks/domain/driver_task.dart';
import '../../trips/data/go_trip_repository.dart';
import '../data/go_truck_repository.dart';
import 'check_in_bloc.dart';

/// The gotest check-in (spec mobile-go-core-loop R12): the job, the truck (default: the job's), the camera photo
/// and the customer-app screenshot. Pops `true` when checked in.
class GoCheckInPage extends StatelessWidget {
  const GoCheckInPage({
    super.key,
    required this.task,
    required this.uploads,
    required this.trips,
    required this.trucks,
    required this.installId,
  });

  final DriverTask task;
  final UploadClient uploads;
  final GoTripRepository trips;
  final GoTruckRepository trucks;
  final String installId;

  @override
  Widget build(BuildContext context) {
    return BlocProvider<EvidenceFormBloc>(
      create: (_) => CheckInBloc(uploads: uploads, trips: trips, task: task, installId: installId),
      child: BlocListener<EvidenceFormBloc, EvidenceState>(
        listenWhen: (a, b) => a.result == null && b.result != null,
        listener: (context, _) => Navigator.of(context).pop(true),
        child: Scaffold(
          appBar: goAppBar('check_in_confirm_job'.tr()),
          body: ListView(padding: const EdgeInsets.all(16), children: [
            _JobCard(task: task),
            const SizedBox(height: 16),
            _TruckPicker(task: task, trucks: trucks),
            const SizedBox(height: 16),
            EvidenceSlots(capture: PhotoCapture()),
            const SizedBox(height: 24),
            const EvidenceSubmitBar(labelKey: 'checkin_save', errorOverrides: {
              'queue_blocked': 'task_blocked_by_queue',
              'not_task_owner': 'checkin_error_not_task_owner',
              'task_not_open': 'checkin_error_task_not_open',
              'unknown_vehicle_class': 'truck_error_unknown_class',
            }),
          ]),
        ),
      ),
    );
  }
}

class _JobCard extends StatelessWidget {
  const _JobCard({required this.task});
  final DriverTask task;

  @override
  Widget build(BuildContext context) {
    return Card(
      child: ListTile(
        title: Text(task.taskNo.isEmpty ? task.id.substring(0, 8) : task.taskNo),
        subtitle: Text('${'checkin_origin'.tr()}: ${task.source}\n${'checkin_destination'.tr()}: ${task.destination}'),
        isThreeLine: true,
      ),
    );
  }
}

/// The truck of this job from the driver's company fleet; preselects the job's truck.
class _TruckPicker extends StatefulWidget {
  const _TruckPicker({required this.task, required this.trucks});
  final DriverTask task;
  final GoTruckRepository trucks;

  @override
  State<_TruckPicker> createState() => _TruckPickerState();
}

class _TruckPickerState extends State<_TruckPicker> {
  List<SelectableTruck> _list = const [];
  SelectableTruck? _selected;
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final list = await widget.trucks.companyTrucks();
      if (!mounted) return;
      final pre = list.where((t) => t.id == widget.task.truckId).firstOrNull;
      setState(() {
        _list = list;
        _loading = false;
      });
      if (pre != null) _pick(pre);
    } catch (_) {
      if (mounted) setState(() => _loading = false);
    }
  }

  void _pick(SelectableTruck t) {
    setState(() => _selected = t);
    context.read<EvidenceFormBloc>().add(EvidenceFieldsChanged({CheckInBloc.fieldTruckId: t.id}));
  }

  @override
  Widget build(BuildContext context) {
    return TruckPickerField(trucks: _list, selected: _selected, loading: _loading, onChanged: _pick);
  }
}
