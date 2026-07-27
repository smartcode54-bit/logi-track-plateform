import 'dart:typed_data';
import 'package:easy_localization/easy_localization.dart';
import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:firebase_auth/firebase_auth.dart';

import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/material.dart';
import 'package:image_picker/image_picker.dart';
import 'package:url_launcher/url_launcher.dart';
import '../../data/repositories/task_repository.dart';
import '../../data/repositories/checkin_repository.dart';
import '../../data/repositories/trip_records_repository.dart';
import '../../data/repositories/driver_repository.dart';
import '../../data/repositories/trucks_repository.dart';
import '../../data/services/draft_storage_service.dart';
import '../../data/repositories/hubs_repository.dart';
import '../../../../components/truck_picker_field.dart';
import '../../../../core/presentation/widgets/searchable_hub_picker.dart';

enum TaskFilter { all, fm, lh }

class CheckInPage extends StatefulWidget {
  final String driverId;

  const CheckInPage({super.key, required this.driverId});

  @override
  State<CheckInPage> createState() => _CheckInPageState();
}

class _CheckInPageState extends State<CheckInPage> {
  TaskFilter _filter = TaskFilter.all;

  /// Hub name cache: sourceId → display name (Thai preferred)
  Map<String, String> _hubNameMap = {};

  /// Driver name cache: authId → display name — used to show helper names on task cards
  Map<String, String> _driverNameMap = {};

  /// Task IDs ที่เที่ยวส่งแล้ว (จาก trip_records status=delivered) — ใช้ไม่ให้บล็อกการเพิ่มงานใหม่
  Set<String> _deliveredTaskIds = {};

  /// เก็บ tasks ล่าสุดจาก stream สำหรับใช้ตอนกด + หลัง refresh delivered
  List<Map<String, dynamic>> _latestTasks = [];

  @override
  void initState() {
    super.initState();
    _loadHubNames();
    _loadDriverNames();
    _loadDeliveredTaskIds();
  }

  Future<void> _loadDriverNames() async {
    try {
      final snap = await FirebaseFirestore.instance.collection('drivers').get();
      final map = <String, String>{};
      for (final d in snap.docs) {
        final data = d.data();
        final authId = data['authId'] as String?;
        if (authId == null || authId.isEmpty) continue;
        final name = '${data['firstName'] ?? ''} ${data['lastName'] ?? ''}'.trim();
        map[authId] = name.isNotEmpty
            ? name
            : (data['fullNameTh'] as String? ?? authId);
      }
      if (mounted) setState(() => _driverNameMap = map);
    } catch (_) {}
  }

  Future<void> _loadDeliveredTaskIds() async {
    if (widget.driverId.isEmpty) return;
    try {
      // trip_records.driverId stores Auth UID (set from FirebaseAuth.instance.currentUser?.uid
      // in loading_phase_page). Use Auth UID so delivered trips are correctly identified and
      // old "Checked in" tasks do not block subsequent check-ins.
      final authUid = FirebaseAuth.instance.currentUser?.uid ?? widget.driverId;
      final ids = await getDeliveredTaskIdsForDriver(authUid);
      if (mounted) setState(() => _deliveredTaskIds = ids);
    } catch (_) {}
  }

  Future<void> _loadHubNames() async {
    try {
      final hubs = await fetchAllHubs();
      final map = <String, String>{};
      for (final h in hubs) {
        map[h.sourceId] = h.sourceNameTh.isNotEmpty
            ? h.sourceNameTh
            : h.sourceNameEn;
      }
      if (mounted) setState(() => _hubNameMap = map);
    } catch (_) {}
  }

  String _resolveHubName(String sourceId) {
    if (sourceId.isEmpty) return '-';
    return _hubNameMap[sourceId] ?? sourceId;
  }

  Widget _infoRow(String label, String value) => Padding(
        padding: const EdgeInsets.only(top: 2),
        child: Text.rich(
          TextSpan(
            style: const TextStyle(fontSize: 12),
            children: [
              TextSpan(
                text: '$label : ',
                style: const TextStyle(fontWeight: FontWeight.w600),
              ),
              TextSpan(text: value),
            ],
          ),
        ),
      );

  String _formatTaskLinkMeta(
    Map<String, dynamic> task,
    String side,
  ) {
    final name = (task['${side}LinkedCustomerName'] as String? ?? '').trim();
    final code = (task['${side}LinkedCustomerCode'] as String? ?? '').trim();
    final kind = (task['${side}CustomerLinkKind'] as String? ?? '').trim();
    if (name.isEmpty && code.isEmpty) return '';
    final kindLabel = kind == 'partner'
        ? 'checkin_partner_label'.tr()
        : 'checkin_customer_label'.tr();
    final value = code.isNotEmpty && name.isNotEmpty ? '$code - $name' : (name.isNotEmpty ? name : code);
    return '${side == 'sourceHub' ? 'checkin_origin'.tr() : 'checkin_destination'.tr()} $kindLabel: $value';
  }

  static bool _isHistory(Map<String, dynamic> t) {
    final status = t['status'] ?? '';
    return status == 'Checked in' ||
        status == 'Completed' ||
        status == 'Cancelled' ||
        status == 'Delivered';
  }

  /// เลือกประเภทงานที่ต้องการเพิ่ม: FM (เรียกเสริม/วน) หรือ LH
  Future<String?> _showAddTaskTypePicker(BuildContext context) async {
    return showModalBottomSheet<String>(
      context: context,
      builder: (ctx) => SafeArea(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Text(
                'manual_checkin_add_task'.tr(),
                style: Theme.of(context).textTheme.titleMedium,
              ),
              const SizedBox(height: 16),
              ListTile(
                leading: const Icon(Icons.local_shipping),
                title: Text('manual_checkin_add_fm'.tr()),
                subtitle: Text('manual_checkin_add_fm_subtitle'.tr()),
                onTap: () => Navigator.pop(ctx, 'FIRST_MILE'),
              ),
              ListTile(
                leading: const Icon(Icons.swap_horiz),
                title: Text('manual_checkin_add_lh'.tr()),
                subtitle: Text('manual_checkin_add_lh_subtitle'.tr()),
                onTap: () => Navigator.pop(ctx, 'LINE_HAUL'),
              ),
              const SizedBox(height: 8),
              TextButton(
                onPressed: () => Navigator.pop(ctx),
                child: Text('manual_checkin_cancel'.tr()),
              ),
            ],
          ),
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    return StreamBuilder<List<Map<String, dynamic>>>(
      stream: streamTasksForDriver(widget.driverId),
      builder: (context, snap) {
        final tasks = snap.data ?? [];
        if (tasks.isNotEmpty) _latestTasks = tasks;

        return Scaffold(
          appBar: AppBar(
            title: Text('my_tasks'.tr()),
            actions: [
              IconButton(
                icon: const Icon(Icons.add),
                onPressed: () async {
                  await _loadDeliveredTaskIds();
                  if (!mounted) return;
                  final stillOngoing = _latestTasks.any((t) {
                    final st = t['status'] as String? ?? '';
                    final taskDocId = t['id'] as String? ?? '';
                    final altTaskId =
                        t['taskId'] as String? ??
                        t['LineHaulTaskId'] as String? ??
                        t['FirstMileTaskId'] as String? ??
                        '';
                    final isDeliveredByTrip =
                        _deliveredTaskIds.contains(taskDocId) ||
                        (altTaskId.isNotEmpty &&
                            _deliveredTaskIds.contains(altTaskId));
                    if (st == 'Pending' ||
                        st == 'Assigned' ||
                        st == 'Completed' ||
                        st == 'Cancelled' ||
                        st == 'Delivered') {
                      return false;
                    }
                    if (st == 'Checked in' && isDeliveredByTrip) return false;
                    return true;
                  });
                  if (stillOngoing) {
                    ScaffoldMessenger.of(context).showSnackBar(
                      SnackBar(
                        content: Text('please_finish_ongoing_task_first'.tr()),
                      ),
                    );
                    return;
                  }
                  // เลือกประเภทงาน: FM (เรียกเสริม/วน) หรือ LH
                  final taskType = await _showAddTaskTypePicker(context);
                  if (taskType == null || !context.mounted) return;
                  final result = await Navigator.push<bool>(
                    context,
                    MaterialPageRoute(
                      builder: (_) => ManualCheckInPage(
                        driverId: widget.driverId,
                        taskType: taskType,
                      ),
                    ),
                  );
                  if (result == true && context.mounted) {
                    Navigator.pop(context, true);
                  }
                },
              ),
            ],
          ),
          body: () {
            if (snap.connectionState == ConnectionState.waiting &&
                !snap.hasData) {
              return const Center(
                child: Padding(
                  padding: EdgeInsets.all(24.0),
                  child: CircularProgressIndicator(),
                ),
              );
            }
            if (snap.hasError) {
              return Padding(
                padding: const EdgeInsets.all(24.0),
                child: Column(
                  mainAxisAlignment: MainAxisAlignment.center,
                  children: [
                    Icon(Icons.error_outline, size: 48, color: Colors.red[700]),
                    const SizedBox(height: 16),
                    Text(
                      'Error: ${snap.error}',
                      textAlign: TextAlign.center,
                      style: TextStyle(color: Colors.grey[700], fontSize: 12),
                    ),
                    const SizedBox(height: 8),
                    Text(
                      'If you see "index", create the Firestore index from the link in the error.',
                      textAlign: TextAlign.center,
                      style: TextStyle(color: Colors.grey[600], fontSize: 11),
                    ),
                  ],
                ),
              );
            }

            final filteredTasks = tasks.where((t) {
              final taskId =
                  t['taskId'] as String? ??
                  t['FirstMileTaskId'] as String? ??
                  '';
              final taskType =
                  t['taskType'] as String? ??
                  (taskId.startsWith('LH') ? 'LINE_HAUL' : 'FIRST_MILE');

              if (_filter == TaskFilter.fm) {
                return taskType == 'FIRST_MILE';
              } else if (_filter == TaskFilter.lh) {
                return taskType == 'LINE_HAUL';
              }
              return true;
            }).toList();

            final newTasks = filteredTasks
                .where((t) => !_isHistory(t))
                .toList();
            final historyTasks = filteredTasks.where(_isHistory).toList();

            if (filteredTasks.isEmpty && tasks.isNotEmpty) {
              return Padding(
                padding: const EdgeInsets.all(24.0),
                child: Center(
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      // Filter selection
                      SegmentedButton<TaskFilter>(
                        segments: const [
                          ButtonSegment<TaskFilter>(
                            value: TaskFilter.all,
                            label: Text('All'),
                          ),
                          ButtonSegment<TaskFilter>(
                            value: TaskFilter.fm,
                            label: Text('FM'),
                          ),
                          ButtonSegment<TaskFilter>(
                            value: TaskFilter.lh,
                            label: Text('LH'),
                          ),
                        ],
                        selected: <TaskFilter>{_filter},
                        onSelectionChanged: (Set<TaskFilter> newSelection) {
                          setState(() {
                            _filter = newSelection.first;
                          });
                        },
                      ),
                      const SizedBox(height: 32),
                      Icon(
                        Icons.filter_alt_off_outlined,
                        size: 64,
                        color: Colors.grey[400],
                      ),
                      const SizedBox(height: 16),
                      Text(
                        'No tasks match filter',
                        style: Theme.of(context).textTheme.titleMedium,
                      ),
                    ],
                  ),
                ),
              );
            }

            if (tasks.isEmpty) {
              return Padding(
                padding: const EdgeInsets.all(24.0),
                child: Center(
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Icon(
                        Icons.assignment_outlined,
                        size: 64,
                        color: Colors.grey[400],
                      ),
                      const SizedBox(height: 16),
                      Text(
                        'no_active_tasks'.tr(),
                        style: Theme.of(context).textTheme.titleMedium,
                      ),
                      const SizedBox(height: 8),
                      Text(
                        'No tasks assigned yet.',
                        style: TextStyle(color: Colors.grey[600]),
                      ),
                    ],
                  ),
                ),
              );
            }

            return ListView(
              padding: const EdgeInsets.all(16),
              children: [
                // --- Filter ---
                if (tasks.isNotEmpty)
                  Padding(
                    padding: const EdgeInsets.only(bottom: 24),
                    child: Align(
                      alignment: Alignment.center,
                      child: SegmentedButton<TaskFilter>(
                        segments: const [
                          ButtonSegment<TaskFilter>(
                            value: TaskFilter.all,
                            label: Padding(
                              padding: EdgeInsets.symmetric(horizontal: 12),
                              child: Text('All'),
                            ),
                          ),
                          ButtonSegment<TaskFilter>(
                            value: TaskFilter.fm,
                            label: Padding(
                              padding: EdgeInsets.symmetric(horizontal: 12),
                              child: Text('FM'),
                            ),
                          ),
                          ButtonSegment<TaskFilter>(
                            value: TaskFilter.lh,
                            label: Padding(
                              padding: EdgeInsets.symmetric(horizontal: 12),
                              child: Text('LH'),
                            ),
                          ),
                        ],
                        selected: <TaskFilter>{_filter},
                        onSelectionChanged: (Set<TaskFilter> newSelection) {
                          setState(() {
                            _filter = newSelection.first;
                          });
                        },
                      ),
                    ),
                  ),
                // --- New task ---
                Padding(
                  padding: const EdgeInsets.only(bottom: 8),
                  child: Text(
                    'check_in_new_task'.tr(),
                    style: Theme.of(context).textTheme.titleMedium?.copyWith(
                      fontWeight: FontWeight.w600,
                      color: Theme.of(context).colorScheme.primary,
                    ),
                  ),
                ),
                if (newTasks.isEmpty)
                  Padding(
                    padding: const EdgeInsets.only(bottom: 24),
                    child: Card(
                      child: Padding(
                        padding: const EdgeInsets.all(16),
                        child: Row(
                          children: [
                            Icon(
                              Icons.check_circle_outline,
                              color: Colors.grey[400],
                              size: 32,
                            ),
                            const SizedBox(width: 12),
                            Text(
                              'no_active_tasks'.tr(),
                              style: TextStyle(color: Colors.grey[600]),
                            ),
                          ],
                        ),
                      ),
                    ),
                  )
                else
                  ...newTasks.map(
                    (t) => _buildTaskCard(
                      context,
                      t,
                      canCheckIn: true,
                      sortedAllTasks: tasks,
                      deliveredTaskIds: _deliveredTaskIds,
                    ),
                  ),
                const SizedBox(height: 16),
                // --- History ---
                Padding(
                  padding: const EdgeInsets.only(bottom: 8),
                  child: Text(
                    'check_in_history'.tr(),
                    style: Theme.of(context).textTheme.titleMedium?.copyWith(
                      fontWeight: FontWeight.w600,
                      color: Theme.of(context).colorScheme.secondary,
                    ),
                  ),
                ),
                if (historyTasks.isEmpty)
                  Padding(
                    padding: const EdgeInsets.only(bottom: 24),
                    child: Card(
                      child: Padding(
                        padding: const EdgeInsets.all(16),
                        child: Row(
                          children: [
                            Icon(
                              Icons.history,
                              color: Colors.grey[400],
                              size: 32,
                            ),
                            const SizedBox(width: 12),
                            Text(
                              'check_in_no_history'.tr(),
                              style: TextStyle(color: Colors.grey[600]),
                            ),
                          ],
                        ),
                      ),
                    ),
                  )
                else
                  ...historyTasks.map(
                    (t) => _buildTaskCard(
                      context,
                      t,
                      canCheckIn: false,
                      sortedAllTasks: tasks,
                      deliveredTaskIds: _deliveredTaskIds,
                    ),
                  ),
              ],
            );
          }(),
        );
      },
    );
  }

  Widget _buildTaskCard(
    BuildContext context,
    Map<String, dynamic> t, {
    required bool canCheckIn,
    required List<Map<String, dynamic>> sortedAllTasks,
    required Set<String> deliveredTaskIds,
  }) {
    final taskId = t['id'] as String?;
    final source = t['sourceHub'] ?? '';
    final dest = t['destination'] ?? '';
    final date = t['date'];
    final time = t['time'] ?? '';
    final status = t['status'] ?? '';
    final showCheckIn =
        canCheckIn &&
        taskId != null &&
        status != 'Checked in' &&
        status != 'Completed' &&
        status != 'Cancelled' &&
        status != 'Delivered';
    final queueEligible = isQueueEligibleForCheckIn(
      task: t,
      sortedAllTasks: sortedAllTasks,
      deliveredTaskIds: deliveredTaskIds,
    );
    String dateStr = '';
    if (date != null && date is DateTime) {
      dateStr = '${date.day}/${date.month}/${date.year}';
    }
    final checkInPhotoUrl = t['checkInPhotoUrl'] as String?;
    final checkInLat = t['checkInLat'] as double?;
    final checkInLng = t['checkInLng'] as double?;
    final checkInAt = t['checkInAt'];

    String checkInTimeStr = '';
    if (checkInAt is Timestamp) {
      checkInTimeStr = DateFormat(
        'dd/MM/yyyy HH:mm:ss',
      ).format(checkInAt.toDate());
    }
    // ดึง customer จาก sourceHub ก่อน ถ้าไม่มีดึงจาก destination
    final srcCustomerName = (t['sourceHubLinkedCustomerName'] as String? ?? '').trim();
    final srcCustomerCode = (t['sourceHubLinkedCustomerCode'] as String? ?? '').trim();
    final dstCustomerName = (t['destinationLinkedCustomerName'] as String? ?? '').trim();
    final dstCustomerCode = (t['destinationLinkedCustomerCode'] as String? ?? '').trim();
    String customerDisplay = '';
    if (srcCustomerCode.isNotEmpty || srcCustomerName.isNotEmpty) {
      customerDisplay = srcCustomerCode.isNotEmpty && srcCustomerName.isNotEmpty
          ? '$srcCustomerCode - $srcCustomerName'
          : (srcCustomerName.isNotEmpty ? srcCustomerName : srcCustomerCode);
    } else if (dstCustomerCode.isNotEmpty || dstCustomerName.isNotEmpty) {
      customerDisplay = dstCustomerCode.isNotEmpty && dstCustomerName.isNotEmpty
          ? '$dstCustomerCode - $dstCustomerName'
          : (dstCustomerName.isNotEmpty ? dstCustomerName : dstCustomerCode);
    }

    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      child: ListTile(
        leading: checkInPhotoUrl != null
            ? GestureDetector(
                onTap: () => _showImageDialog(context, checkInPhotoUrl),
                child: ClipRRect(
                  borderRadius: BorderRadius.circular(4),
                  child: Image.network(
                    checkInPhotoUrl,
                    width: 50,
                    height: 50,
                    fit: BoxFit.cover,
                  ),
                ),
              )
            : null,
        title: Text(t['taskId'] ?? t['FirstMileTaskId'] ?? 'Task'),
        subtitle: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('$dateStr $time · $status'),
            const SizedBox(height: 4),
            if (customerDisplay.isNotEmpty)
              _infoRow('checkin_customer_label'.tr(), customerDisplay),
            _infoRow('checkin_origin'.tr(), _resolveHubName(source)),
            _infoRow('checkin_destination'.tr(), _resolveHubName(dest)),
            if (((t['helperDriverIds'] as List?) ?? const []).isNotEmpty)
              _infoRow(
                'checkin_helpers'.tr(),
                (t['helperDriverIds'] as List)
                    .map((id) => _driverNameMap[id as String] ?? id as String)
                    .join(', '),
              ),
            if (checkInTimeStr.isNotEmpty) ...[
              const SizedBox(height: 4),
              Text(
                'Checkin Time: $checkInTimeStr',
                style: const TextStyle(fontSize: 12),
              ),
              if (checkInLat != null && checkInLng != null)
                InkWell(
                  onTap: () => launchUrl(
                    Uri.parse(
                      'https://www.google.com/maps/search/?api=1&query=$checkInLat,$checkInLng',
                    ),
                  ),
                  child: Text(
                    'Checkin at : $checkInLat,$checkInLng',
                    style: const TextStyle(
                      fontSize: 12,
                      color: Colors.blue,
                      decoration: TextDecoration.underline,
                    ),
                  ),
                ),
            ],
          ],
        ),
        trailing: showCheckIn
            ? TextButton.icon(
                icon: const Icon(Icons.camera_alt, size: 20),
                label: Text('Check in'.tr()),
                onPressed: !queueEligible
                    ? () {
                        ScaffoldMessenger.of(context).showSnackBar(
                          SnackBar(
                            content: Text(
                              'please_finish_ongoing_task_first'.tr(),
                            ),
                          ),
                        );
                      }
                    : () => _doCheckIn(context, t),
              )
            : null,
      ),
    );
  }

  void _showImageDialog(BuildContext context, String imageUrl) {
    showDialog(
      context: context,
      builder: (_) => Dialog(
        backgroundColor: Colors.transparent,
        child: Stack(
          alignment: Alignment.center,
          children: [
            InteractiveViewer(
              child: Image.network(imageUrl, fit: BoxFit.contain),
            ),
            Positioned(
              right: 0,
              top: 0,
              child: IconButton(
                icon: const Icon(Icons.close, color: Colors.white, size: 32),
                onPressed: () => Navigator.pop(context),
              ),
            ),
          ],
        ),
      ),
    );
  }

  Future<void> _doCheckIn(
    BuildContext context,
    Map<String, dynamic> taskData,
  ) async {
    final taskId = taskData['id'] as String?;
    if (taskId == null || !context.mounted) return;

    final result = await Navigator.push<bool>(
      context,
      MaterialPageRoute(
        builder: (_) => TaskCheckInPage(
          taskId: taskId,
          taskData: taskData,
          driverId: widget.driverId,
          hubNameMap: _hubNameMap,
        ),
      ),
    );

    if (result == true && context.mounted) {
      Navigator.pop(context, true);
    }
  }
}

// ============================================================
// TaskCheckInPage — Check-in for existing tasks (FM/LH)
// Flow: Camera → Preview → Edit/Save
// ============================================================

enum _TaskCheckInStep { camera, preview }

class TaskCheckInPage extends StatefulWidget {
  final String taskId;
  final Map<String, dynamic> taskData;
  final String driverId;
  final Map<String, String> hubNameMap;

  const TaskCheckInPage({
    super.key,
    required this.taskId,
    required this.taskData,
    required this.driverId,
    this.hubNameMap = const {},
  });

  @override
  State<TaskCheckInPage> createState() => _TaskCheckInPageState();
}

class _TaskCheckInPageState extends State<TaskCheckInPage> {
  _TaskCheckInStep _step = _TaskCheckInStep.camera;
  Uint8List? _photoBytes;
  bool _submitting = false;
  bool _cameraOpened = false;

  // Driver data (fetched once)
  Map<String, dynamic>? _driverData;

  // The truck for THIS job. Defaults to the truck the admin put on the task; the driver may
  // correct it here if they actually took a different vehicle — they are responsible for the
  // truck they confirm, for this job only (it does not re-bind them to it).
  final _trucksRepository = TrucksRepository();
  final _driverRepository = DriverRepository();
  List<SelectableTruck> _selectableTrucks = [];
  SelectableTruck? _selectedTruck;
  bool _trucksLoading = true;

  // Helper selection (main driver picks helpers who train/assist on this job)
  List<Map<String, String>> _allDrivers = [];
  final Set<String> _selectedHelperIds = {};

  @override
  void initState() {
    super.initState();
    _fetchDriverData();
    _loadDrivers();
  }

  Future<void> _loadDrivers() async {
    try {
      final selfUid = FirebaseAuth.instance.currentUser?.uid;
      final snap = await FirebaseFirestore.instance.collection('drivers').get();
      final list = <Map<String, String>>[];
      for (final d in snap.docs) {
        final data = d.data();
        final authId = data['authId'] as String?;
        if (authId == null || authId.isEmpty || authId == selfUid) continue;
        final name = '${data['firstName'] ?? ''} ${data['lastName'] ?? ''}'.trim();
        list.add({
          'authId': authId,
          'name': name.isNotEmpty ? name : (data['fullNameTh'] as String? ?? authId),
        });
      }
      list.sort((a, b) => (a['name'] ?? '').compareTo(b['name'] ?? ''));
      if (mounted) setState(() => _allDrivers = list);
    } catch (_) {}
  }

  Future<void> _showHelperPicker() async {
    final query = ValueNotifier<String>('');
    await showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      builder: (ctx) => StatefulBuilder(
        builder: (ctx, setSheet) => DraggableScrollableSheet(
          expand: false,
          initialChildSize: 0.7,
          builder: (ctx, scrollCtrl) => Column(
            children: [
              Padding(
                padding: const EdgeInsets.all(12),
                child: TextField(
                  decoration: InputDecoration(
                    prefixIcon: const Icon(Icons.search),
                    hintText: 'helper_search'.tr(),
                    border: const OutlineInputBorder(),
                  ),
                  onChanged: (v) => query.value = v.toLowerCase(),
                ),
              ),
              Expanded(
                child: ValueListenableBuilder<String>(
                  valueListenable: query,
                  builder: (ctx, q, _) {
                    final filtered = _allDrivers
                        .where((d) => (d['name'] ?? '').toLowerCase().contains(q))
                        .toList();
                    return ListView.builder(
                      controller: scrollCtrl,
                      itemCount: filtered.length,
                      itemBuilder: (ctx, i) {
                        final d = filtered[i];
                        final id = d['authId']!;
                        final checked = _selectedHelperIds.contains(id);
                        return CheckboxListTile(
                          value: checked,
                          title: Text(d['name'] ?? id),
                          onChanged: (v) {
                            setSheet(() {
                              _selectedHelperIds.clear();
                              if (v == true) _selectedHelperIds.add(id);
                            });
                            setState(() {});
                            if (v == true) Navigator.pop(ctx);
                          },
                        );
                      },
                    );
                  },
                ),
              ),
              SafeArea(
                child: Padding(
                  padding: const EdgeInsets.all(12),
                  child: SizedBox(
                    width: double.infinity,
                    child: FilledButton(
                      onPressed: () => Navigator.pop(ctx),
                      child: Text('helper_done'.tr()),
                    ),
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Future<void> _fetchDriverData() async {
    try {
      final drvDoc = await FirebaseFirestore.instance
          .collection('drivers')
          .doc(widget.driverId)
          .get();
      if (drvDoc.exists) _driverData = drvDoc.data();
      await _loadTrucks();
    } catch (_) {
      if (mounted) setState(() => _trucksLoading = false);
    }

    // Open camera right away
    if (mounted && !_cameraOpened) {
      _cameraOpened = true;
      _openCamera();
    }
  }

  /// Loads the trucks this driver may run and preselects the one for this job.
  ///
  /// Order matters: the TASK's truck wins. The driver's home binding is only a fallback for
  /// legacy tasks created before the vehicle was chosen at assign time.
  Future<void> _loadTrucks() async {
    try {
      final fleet = await _trucksRepository.fetchSelectableTrucks(
        subcontractorId: _driverData?['subcontractorId'] as String?,
      );

      final taskTruckId = (widget.taskData['truckId'] as String? ?? '').trim();
      final homeTruckId =
          (_driverData?['currentAssignment']?['truckId'] as String? ?? '').trim();
      final wantedId = taskTruckId.isNotEmpty ? taskTruckId : homeTruckId;

      SelectableTruck? preselected;
      if (wantedId.isNotEmpty) {
        for (final truck in fleet) {
          if (truck.id == wantedId) {
            preselected = truck;
            break;
          }
        }
        // The task's truck may sit outside this driver's company list (admin override) —
        // still show it as the default rather than silently dropping the admin's choice.
        preselected ??= await _trucksRepository.fetchTruck(wantedId);
      }

      if (!mounted) return;
      setState(() {
        _selectableTrucks = fleet;
        _selectedTruck = preselected;
        _trucksLoading = false;
      });
    } catch (_) {
      if (mounted) setState(() => _trucksLoading = false);
    }
  }

  Future<void> _openCamera() async {
    if (kIsWeb) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(const SnackBar(content: Text('Not supported on web')));
      }
      return;
    }

    final source = await showModalBottomSheet<ImageSource>(
      context: context,
      builder: (ctx) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              leading: const Icon(Icons.camera_alt),
              title: Text('refuel_receipt_take_photo'.tr()),
              onTap: () => Navigator.pop(ctx, ImageSource.camera),
            ),
            ListTile(
              leading: const Icon(Icons.photo_library),
              title: Text('refuel_receipt_from_gallery'.tr()),
              onTap: () => Navigator.pop(ctx, ImageSource.gallery),
            ),
          ],
        ),
      ),
    );
    if (source == null || !mounted) {
      if (mounted) Navigator.pop(context);
      return;
    }

    final picker = ImagePicker();
    final xfile = await picker.pickImage(source: source, imageQuality: 85);
    if (xfile == null) {
      // User cancelled camera → go back
      if (mounted) Navigator.pop(context);
      return;
    }
    final bytes = await xfile.readAsBytes();
    if (mounted) {
      setState(() {
        _photoBytes = bytes;
        _step = _TaskCheckInStep.preview;
      });
    }
  }

  String get _taskDisplayId =>
      widget.taskData['taskId'] as String? ??
      widget.taskData['FirstMileTaskId'] as String? ??
      'Task';

  String get _taskType {
    final tt = widget.taskData['taskType'] as String? ?? '';
    if (tt == 'LINE_HAUL') return 'LH';
    if (tt == 'FIRST_MILE') return 'FM';
    final id = _taskDisplayId;
    return id.startsWith('LH') ? 'LH' : 'FM';
  }

  String get _source {
    final id = widget.taskData['sourceHub'] as String? ?? '-';
    return widget.hubNameMap[id] ?? id;
  }

  String get _dest {
    final id = widget.taskData['destination'] as String? ?? '-';
    return widget.hubNameMap[id] ?? id;
  }

  String _metaDisplay(String sidePrefix) {
    final name = (widget.taskData['${sidePrefix}LinkedCustomerName'] as String? ?? '').trim();
    final code = (widget.taskData['${sidePrefix}LinkedCustomerCode'] as String? ?? '').trim();
    if (name.isEmpty && code.isEmpty) return '-';
    if (name.isNotEmpty && code.isNotEmpty) return '$code - $name';
    return name.isNotEmpty ? name : code;
  }

  String _metaLabel(String sidePrefix) {
    final kind = (widget.taskData['${sidePrefix}CustomerLinkKind'] as String? ?? '').trim();
    final side = sidePrefix == 'sourceHub' ? 'checkin_origin'.tr() : 'checkin_destination'.tr();
    final kindLabel = kind == 'partner'
        ? 'checkin_partner_label'.tr()
        : 'checkin_customer_label'.tr();
    return '$side $kindLabel';
  }

  String get _driverName {
    if (_driverData != null) {
      return '${_driverData?['firstName'] ?? ''} ${_driverData?['lastName'] ?? ''}'
          .trim();
    }
    return widget.taskData['driverName'] as String? ?? '-';
  }

  /// The class of the truck confirmed for THIS job (the picker below shows the plate itself).
  /// Never the driver's home binding — they may be running a different vehicle today.
  String get _truckType {
    final confirmed = _selectedTruck?.taskTruckType;
    if (confirmed != null && confirmed.isNotEmpty) return confirmed;
    final fromTask = (widget.taskData['truckType'] as String? ?? '').trim();
    return fromTask.isNotEmpty ? fromTask : '-';
  }

  String get _truckModel {
    final model = _selectedTruck?.model;
    return (model != null && model.isNotEmpty) ? model : '-';
  }

  Future<void> _submit() async {
    if (_photoBytes == null) return;

    final truck = _selectedTruck;
    if (truck == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('truck_required'.tr()),
          backgroundColor: Colors.orange,
        ),
      );
      return;
    }

    setState(() => _submitting = true);
    try {
      final position = await getCurrentPosition();
      final timestamp = DateTime.now();

      // Stamp the confirmed truck onto the task before checking in: the loading phase reads the
      // truck from the task, so this is what ends up on the trip record and on billing.
      await FirebaseFirestore.instance
          .collection('tasks')
          .doc(widget.taskId)
          .update({
        'truckId': truck.id,
        'licensePlate': truck.licensePlate,
        if (truck.taskTruckType != null) 'truckType': truck.taskTruckType,
        'updatedAt': FieldValue.serverTimestamp(),
      });

      await submitCheckIn(
        taskId: widget.taskId,
        imageBytes: _photoBytes!,
        lat: position.latitude,
        lng: position.longitude,
        timestamp: timestamp,
        helperDriverIds: _selectedHelperIds.isEmpty ? null : _selectedHelperIds.toList(),
      );
      await DraftStorageService.instance.saveActiveCheckInTaskId(widget.taskId);

      // "The truck I'm responsible for right now" — drives the fuel/expense forms and the
      // maintenance rule. Non-fatal: the task already carries the truck.
      try {
        await _driverRepository.setActiveTruck(
          driverId: widget.driverId,
          truckId: truck.id,
          truckPlate: truck.licensePlate,
          taskId: widget.taskId,
        );
      } catch (e) {
        debugPrint('setActiveTruck failed (non-fatal): $e');
      }

      if (mounted) {
        Navigator.pop(context, true);
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Check-in saved'.tr())));
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text('Check-in failed: $e'),
            backgroundColor: Colors.red,
          ),
        );
      }
    } finally {
      if (mounted) setState(() => _submitting = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text('Check In ($_taskType)'),
        leading: IconButton(
          icon: const Icon(Icons.arrow_back),
          onPressed: () => Navigator.pop(context),
        ),
      ),
      body: SafeArea(
        top: false,
        child: _step == _TaskCheckInStep.camera
            ? const Center(child: CircularProgressIndicator())
            : _buildPreview(),
      ),
    );
  }

  Widget _buildPreview() {
    return SingleChildScrollView(
      padding: const EdgeInsets.all(20),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            'checkin_preview_title'.tr(),
            style: Theme.of(
              context,
            ).textTheme.titleLarge?.copyWith(fontWeight: FontWeight.bold),
          ),
          const SizedBox(height: 16),

          // Photo preview
          if (_photoBytes != null)
            ClipRRect(
              borderRadius: BorderRadius.circular(12),
              child: Image.memory(
                _photoBytes!,
                width: double.infinity,
                height: 200,
                fit: BoxFit.cover,
              ),
            ),
          const SizedBox(height: 16),

          // Task info card
          Card(
            child: Padding(
              padding: const EdgeInsets.all(16),
              child: Column(
                children: [
                  _previewRow(Icons.assignment, 'Task ID', _taskDisplayId),
                  const Divider(height: 20),
                  _previewRow(
                    Icons.my_location,
                    'checkin_origin'.tr(),
                    _source,
                  ),
                  if (_metaDisplay('sourceHub') != '-') ...[
                    const Divider(height: 20),
                    _previewRow(
                      Icons.business,
                      _metaLabel('sourceHub'),
                      _metaDisplay('sourceHub'),
                    ),
                  ],
                  const Divider(height: 20),
                  _previewRow(Icons.flag, 'checkin_destination'.tr(), _dest),
                  if (_metaDisplay('destination') != '-') ...[
                    const Divider(height: 20),
                    _previewRow(
                      Icons.apartment,
                      _metaLabel('destination'),
                      _metaDisplay('destination'),
                    ),
                  ],
                  const Divider(height: 20),
                  _previewRow(
                    Icons.local_shipping,
                    'checkin_truck_type'.tr(),
                    _truckType != '-' ? '$_truckType ($_truckModel)' : '-',
                  ),
                  const Divider(height: 20),
                  _previewRow(Icons.person, 'checkin_driver'.tr(), _driverName),
                  const Divider(height: 20),
                  _previewRow(
                    Icons.access_time,
                    'loading_phase_timestamp'.tr(),
                    DateFormat('dd/MM/yyyy HH:mm').format(DateTime.now()),
                  ),
                ],
              ),
            ),
          ),

          const SizedBox(height: 16),

          // Truck for this job — defaults to the truck the admin assigned; the driver confirms
          // it, or corrects it if they actually took a different vehicle.
          Card(
            child: Padding(
              padding: const EdgeInsets.all(16),
              child: TruckPickerField(
                trucks: _selectableTrucks,
                selected: _selectedTruck,
                loading: _trucksLoading,
                enabled: !_submitting,
                onChanged: (truck) => setState(() => _selectedTruck = truck),
              ),
            ),
          ),

          // Delivery stops section (if multi-delivery)
          _buildDeliveryStopsSection(),

          const SizedBox(height: 16),

          // Helper selection — main driver picks helpers who train/assist on this job
          Card(
            child: Padding(
              padding: const EdgeInsets.all(16),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      const Icon(Icons.group_add, size: 20),
                      const SizedBox(width: 8),
                      Text(
                        'helper_section_title'.tr(),
                        style: Theme.of(context).textTheme.titleMedium,
                      ),
                    ],
                  ),
                  const SizedBox(height: 8),
                  if (_selectedHelperIds.isEmpty)
                    Text(
                      'helper_none_selected'.tr(),
                      style: TextStyle(color: Colors.grey[600], fontSize: 12),
                    )
                  else
                    Wrap(
                      spacing: 6,
                      runSpacing: 4,
                      children: _selectedHelperIds.map((id) {
                        final d = _allDrivers.firstWhere(
                          (x) => x['authId'] == id,
                          orElse: () => {'name': id, 'authId': id},
                        );
                        return Chip(
                          label: Text(d['name'] ?? id),
                          onDeleted: () => setState(() => _selectedHelperIds.remove(id)),
                        );
                      }).toList(),
                    ),
                  const SizedBox(height: 8),
                  OutlinedButton.icon(
                    icon: const Icon(Icons.person_add),
                    label: Text('helper_select'.tr()),
                    onPressed: _showHelperPicker,
                  ),
                ],
              ),
            ),
          ),

          const SizedBox(height: 24),

          // Action buttons
          Row(
            children: [
              // Retake photo button
              Expanded(
                child: OutlinedButton.icon(
                  icon: const Icon(Icons.camera_alt),
                  label: Text('checkin_retake'.tr()),
                  style: OutlinedButton.styleFrom(
                    minimumSize: const Size(0, 52),
                    textStyle: const TextStyle(
                      fontSize: 16,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  onPressed: _submitting ? null : _openCamera,
                ),
              ),
              const SizedBox(width: 12),
              // Save button
              Expanded(
                flex: 2,
                child: ElevatedButton.icon(
                  icon: _submitting
                      ? const SizedBox(
                          width: 20,
                          height: 20,
                          child: CircularProgressIndicator(
                            color: Colors.white,
                            strokeWidth: 2,
                          ),
                        )
                      : const Icon(Icons.check),
                  label: Text(
                    _submitting
                        ? 'checkin_processing'.tr()
                        : 'checkin_save'.tr(),
                  ),
                  style: ElevatedButton.styleFrom(
                    minimumSize: const Size(0, 52),
                    textStyle: const TextStyle(
                      fontSize: 16,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  onPressed: _submitting ? null : _submit,
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }

  Widget _previewRow(IconData icon, String label, String value) {
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Icon(icon, size: 20, color: Theme.of(context).colorScheme.primary),
        const SizedBox(width: 12),
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                label,
                style: Theme.of(context).textTheme.bodySmall?.copyWith(
                  color: Theme.of(context).colorScheme.onSurfaceVariant,
                ),
              ),
              const SizedBox(height: 2),
              Text(
                value,
                style: const TextStyle(
                  fontWeight: FontWeight.w600,
                  fontSize: 15,
                ),
              ),
            ],
          ),
        ),
      ],
    );
  }

  Widget _buildDeliveryStopsSection() {
    final deliveryStops = widget.taskData['deliveryStops'] as List<dynamic>? ?? [];

    if (deliveryStops.isEmpty) {
      return const SizedBox.shrink();
    }

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const SizedBox(height: 16),
        Card(
          color: Colors.blue.shade50,
          child: Padding(
            padding: const EdgeInsets.all(16),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Icon(Icons.local_shipping,
                      size: 20,
                      color: Colors.blue.shade700,
                    ),
                    const SizedBox(width: 8),
                    Text(
                      'checkin_delivery_stops'.tr(args: ['${deliveryStops.length}']),
                      style: Theme.of(context).textTheme.titleMedium?.copyWith(
                        fontWeight: FontWeight.bold,
                        color: Colors.blue.shade800,
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 12),
                ...deliveryStops.asMap().entries.map((e) {
                  final idx = e.key + 1;
                  final stop = e.value as Map<String, dynamic>;
                  final destination = stop['destination'] as String? ?? 'Unknown';
                  final isCustom = stop['isCustom'] as bool? ?? false;

                  return Padding(
                    padding: const EdgeInsets.only(bottom: 8),
                    child: Row(
                      children: [
                        Container(
                          width: 24,
                          height: 24,
                          decoration: BoxDecoration(
                            shape: BoxShape.circle,
                            color: Colors.blue.shade600,
                          ),
                          child: Center(
                            child: Text(
                              '$idx',
                              style: const TextStyle(
                                color: Colors.white,
                                fontWeight: FontWeight.bold,
                                fontSize: 12,
                              ),
                            ),
                          ),
                        ),
                        const SizedBox(width: 12),
                        Expanded(
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text(
                                destination,
                                style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                                  fontWeight: FontWeight.w500,
                                ),
                              ),
                              if (isCustom)
                                Text(
                                  'custom_stop_note'.tr(),
                                  style: TextStyle(
                                    fontSize: 11,
                                    color: Colors.orange.shade700,
                                  ),
                                ),
                            ],
                          ),
                        ),
                      ],
                    ),
                  );
                }).toList(),
              ],
            ),
          ),
        ),
      ],
    );
  }
}

// Truck type mapping lives in core/utils/truck_type.dart (shared with the web helper).

enum _CheckInStep { form, preview }

class ManualCheckInPage extends StatefulWidget {
  final String driverId;

  /// FIRST_MILE = เรียกเสริม/วน (ต้นทาง Hub → ปลายทาง SOC) | LINE_HAUL = ต้นทาง SOC → ปลายทาง Hub
  final String taskType;

  const ManualCheckInPage({
    super.key,
    required this.driverId,
    this.taskType = 'LINE_HAUL',
  });

  @override
  State<ManualCheckInPage> createState() => _ManualCheckInPageState();
}

class _ManualCheckInPageState extends State<ManualCheckInPage> {
  _CheckInStep _step = _CheckInStep.form;

  String? _origin;
  String? _dest;
  bool _loading = true;
  bool _submitting = false;

  List<HubDoc> _socs = [];
  List<HubDoc> _hubs = [];
  Map<String, dynamic>? _driverData;

  // The driver picks the truck for the job they are creating. Defaults to their home truck when
  // they have one, but it is required — a self-created task with no real truck would bill against
  // the wrong vehicle class.
  final _trucksRepository = TrucksRepository();
  final _driverRepository = DriverRepository();
  List<SelectableTruck> _selectableTrucks = [];
  SelectableTruck? _selectedTruck;
  bool _trucksLoading = true;

  // Photo data for preview
  Uint8List? _photoBytes;

  // Helper selection (main driver picks helpers who train/assist on this job)
  List<Map<String, String>> _allDrivers = [];
  final Set<String> _selectedHelperIds = {};

  @override
  void initState() {
    super.initState();
    _fetchData();
    _loadDrivers();
  }

  Future<void> _loadDrivers() async {
    try {
      final selfUid = FirebaseAuth.instance.currentUser?.uid;
      final snap = await FirebaseFirestore.instance.collection('drivers').get();
      final list = <Map<String, String>>[];
      for (final d in snap.docs) {
        final data = d.data();
        final authId = data['authId'] as String?;
        if (authId == null || authId.isEmpty || authId == selfUid) continue;
        final name = '${data['firstName'] ?? ''} ${data['lastName'] ?? ''}'.trim();
        list.add({
          'authId': authId,
          'name': name.isNotEmpty ? name : (data['fullNameTh'] as String? ?? authId),
        });
      }
      list.sort((a, b) => (a['name'] ?? '').compareTo(b['name'] ?? ''));
      if (mounted) setState(() => _allDrivers = list);
    } catch (_) {}
  }

  Future<void> _showHelperPicker() async {
    final query = ValueNotifier<String>('');
    await showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      builder: (ctx) => StatefulBuilder(
        builder: (ctx, setSheet) => DraggableScrollableSheet(
          expand: false,
          initialChildSize: 0.7,
          builder: (ctx, scrollCtrl) => Column(
            children: [
              Padding(
                padding: const EdgeInsets.all(12),
                child: TextField(
                  decoration: InputDecoration(
                    prefixIcon: const Icon(Icons.search),
                    hintText: 'helper_search'.tr(),
                    border: const OutlineInputBorder(),
                  ),
                  onChanged: (v) => query.value = v.toLowerCase(),
                ),
              ),
              Expanded(
                child: ValueListenableBuilder<String>(
                  valueListenable: query,
                  builder: (ctx, q, _) {
                    final filtered = _allDrivers
                        .where((d) => (d['name'] ?? '').toLowerCase().contains(q))
                        .toList();
                    return ListView.builder(
                      controller: scrollCtrl,
                      itemCount: filtered.length,
                      itemBuilder: (ctx, i) {
                        final d = filtered[i];
                        final id = d['authId']!;
                        final checked = _selectedHelperIds.contains(id);
                        return CheckboxListTile(
                          value: checked,
                          title: Text(d['name'] ?? id),
                          onChanged: (v) {
                            setSheet(() {
                              _selectedHelperIds.clear();
                              if (v == true) _selectedHelperIds.add(id);
                            });
                            setState(() {});
                            if (v == true) Navigator.pop(ctx);
                          },
                        );
                      },
                    );
                  },
                ),
              ),
              SafeArea(
                child: Padding(
                  padding: const EdgeInsets.all(12),
                  child: SizedBox(
                    width: double.infinity,
                    child: FilledButton(
                      onPressed: () => Navigator.pop(ctx),
                      child: Text('helper_done'.tr()),
                    ),
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildHelperCard() {
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                const Icon(Icons.group_add, size: 20),
                const SizedBox(width: 8),
                Text(
                  'helper_section_title'.tr(),
                  style: Theme.of(context).textTheme.titleMedium,
                ),
              ],
            ),
            const SizedBox(height: 8),
            if (_selectedHelperIds.isEmpty)
              Text(
                'helper_none_selected'.tr(),
                style: TextStyle(color: Colors.grey[600], fontSize: 12),
              )
            else
              Wrap(
                spacing: 6,
                runSpacing: 4,
                children: _selectedHelperIds.map((id) {
                  final d = _allDrivers.firstWhere(
                    (x) => x['authId'] == id,
                    orElse: () => {'name': id, 'authId': id},
                  );
                  return Chip(
                    label: Text(d['name'] ?? id),
                    onDeleted: () => setState(() => _selectedHelperIds.remove(id)),
                  );
                }).toList(),
              ),
            const SizedBox(height: 8),
            OutlinedButton.icon(
              icon: const Icon(Icons.person_add),
              label: Text('helper_select'.tr()),
              onPressed: _showHelperPicker,
            ),
          ],
        ),
      ),
    );
  }

  Future<void> _fetchData() async {
    try {
      final drvDoc = await FirebaseFirestore.instance
          .collection('drivers')
          .doc(widget.driverId)
          .get();
      if (drvDoc.exists) _driverData = drvDoc.data();
      await _loadTrucks();

      final all = await fetchAllHubs();

      // HUB vs SOC (aligned with web hubSchema: only HUB | SOC). Standby SOCs: source_id 0xxx — same as loading_phase.
      _hubs = all
          .where((h) => h.stationType == stationTypeHub)
          .toList();
      _socs = all.where((h) {
        if (h.stationType != stationTypeSoc) return false;
        final code = h.sourceId.trim();
        if (code.isEmpty) return false;
        if (code.startsWith('0')) return false; // stand by SOC, not for FM/LH pickers
        return true;
      }).toList();

      // Do NOT auto-select — use placeholders
    } catch (e) {
      debugPrint('Error fetching manual check-in data: $e');
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  /// Trucks the driver may run, defaulting to their home truck when they have one.
  Future<void> _loadTrucks() async {
    try {
      final fleet = await _trucksRepository.fetchSelectableTrucks(
        subcontractorId: _driverData?['subcontractorId'] as String?,
      );
      final homeTruckId =
          (_driverData?['currentAssignment']?['truckId'] as String? ?? '').trim();

      SelectableTruck? preselected;
      for (final truck in fleet) {
        if (truck.id == homeTruckId) {
          preselected = truck;
          break;
        }
      }

      if (!mounted) return;
      setState(() {
        _selectableTrucks = fleet;
        _selectedTruck = preselected;
        _trucksLoading = false;
      });
    } catch (e) {
      debugPrint('Error loading trucks: $e');
      if (mounted) setState(() => _trucksLoading = false);
    }
  }

  String get _driverName {
    return '${_driverData?['firstName'] ?? ''} ${_driverData?['lastName'] ?? ''}'
        .trim();
  }

  String get _licensePlate => _selectedTruck?.licensePlate ?? '-';

  String get _truckType => _selectedTruck?.taskTruckType ?? '-';

  String get _truckModel => _selectedTruck?.model ?? '-';

  Future<void> _takePhotoAndPreview() async {
    if (_origin == null || _dest == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('checkin_select_origin_dest'.tr()),
          backgroundColor: Colors.orange,
        ),
      );
      return;
    }
    // Without a real truck the task would bill against a guessed vehicle class.
    if (_selectedTruck == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('truck_required'.tr()),
          backgroundColor: Colors.orange,
        ),
      );
      return;
    }
    if (kIsWeb) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('Not supported on web')));
      return;
    }

    final source = await showModalBottomSheet<ImageSource>(
      context: context,
      builder: (ctx) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              leading: const Icon(Icons.camera_alt),
              title: Text('refuel_receipt_take_photo'.tr()),
              onTap: () => Navigator.pop(ctx, ImageSource.camera),
            ),
            ListTile(
              leading: const Icon(Icons.photo_library),
              title: Text('refuel_receipt_from_gallery'.tr()),
              onTap: () => Navigator.pop(ctx, ImageSource.gallery),
            ),
          ],
        ),
      ),
    );
    if (source == null || !mounted) return;

    final picker = ImagePicker();
    final xfile = await picker.pickImage(source: source, imageQuality: 85);
    if (xfile == null) return;
    final imageBytes = await xfile.readAsBytes();

    if (mounted) {
      setState(() {
        _photoBytes = imageBytes;
        _step = _CheckInStep.preview;
      });
    }
  }

  Future<void> _submit() async {
    if (_origin == null || _dest == null || _photoBytes == null) return;

    final truck = _selectedTruck;
    if (truck == null) return; // guarded at _takePhotoAndPreview, re-checked before the write

    setState(() => _submitting = true);
    try {
      final now = DateTime.now();

      // Generate sequential task ID client-side (avoids Cloud Function auth issues)
      final dateStr = DateFormat('ddMMyyyy').format(now);
      final prefix = widget.taskType == 'LINE_HAUL' ? 'LH' : 'FM';
      final countSnap = await FirebaseFirestore.instance
          .collection('tasks')
          .where('taskType', isEqualTo: widget.taskType)
          .where('dateStr', isEqualTo: dateStr)
          .count()
          .get();
      final nextNum = (countSnap.count ?? 0) + 1;
      final newTaskId = '$prefix-$dateStr-$nextNum';

      final runOrder = await getNextRunOrderForDriver(widget.driverId);

      final isFm = widget.taskType == 'FIRST_MILE';
      final originHubResolved = isFm
          ? _hubs.where((h) => h.sourceId == _origin).firstOrNull
          : _socs.where((h) => h.sourceId == _origin).firstOrNull;
      final destHubResolved = isFm
          ? _socs.where((h) => h.sourceId == _dest).firstOrNull
          : _hubs.where((h) => h.sourceId == _dest).firstOrNull;
      final hubLinkFields = taskFieldsFromHubDocsForManualCreate(
        originHub: originHubResolved,
        destHub: destHubResolved,
      );

      final docRef = await FirebaseFirestore.instance.collection('tasks').add({
        'taskId': newTaskId,
        'taskType': widget.taskType,
        'createdAt': FieldValue.serverTimestamp(),
        'updatedAt': FieldValue.serverTimestamp(),
        'date': Timestamp.fromDate(now),
        'dateStr': dateStr,
        'time': DateFormat('HH:mm').format(now),
        'destination': _dest,
        'sourceHub': _origin,
        'status': 'Pending',
        // The truck the driver picked for this job — a real fleet truck, so billing resolves the
        // right vehicle class and the trip record carries a usable truckId.
        'truckId': truck.id,
        'licensePlate': truck.licensePlate,
        if (truck.taskTruckType != null) 'truckType': truck.taskTruckType,
        'driverId': widget.driverId,
        'driverName': _driverName,
        'driverPhone': _driverData?['mobile'] ?? '',
        'runOrder': runOrder,
        ...hubLinkFields,
      });

      final pos = await getCurrentPosition();
      await submitCheckIn(
        taskId: docRef.id,
        imageBytes: _photoBytes!,
        lat: pos.latitude,
        lng: pos.longitude,
        timestamp: now,
        helperDriverIds: _selectedHelperIds.isEmpty ? null : _selectedHelperIds.toList(),
      );
      await DraftStorageService.instance.saveActiveCheckInTaskId(docRef.id);

      // "The truck I'm responsible for right now" — drives the fuel/expense forms and the
      // maintenance rule. Non-fatal: the task already carries the truck.
      try {
        await _driverRepository.setActiveTruck(
          driverId: widget.driverId,
          truckId: truck.id,
          truckPlate: truck.licensePlate,
          taskId: docRef.id,
        );
      } catch (e) {
        debugPrint('setActiveTruck failed (non-fatal): $e');
      }

      if (mounted) {
        Navigator.pop(context, true);
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Check-in saved'.tr())));
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Failed: $e'), backgroundColor: Colors.red),
        );
      }
    } finally {
      if (mounted) setState(() => _submitting = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          widget.taskType == 'FIRST_MILE'
              ? 'Manual Check In (FM)'.tr()
              : 'Manual Check In (LH)'.tr(),
        ),
        leading: IconButton(
          icon: const Icon(Icons.arrow_back),
          onPressed: () {
            if (_step == _CheckInStep.preview) {
              setState(() => _step = _CheckInStep.form);
            } else {
              Navigator.pop(context);
            }
          },
        ),
      ),
      body: SafeArea(
        top: false,
        child: _loading
            ? const Center(child: CircularProgressIndicator())
            : _step == _CheckInStep.form
            ? _buildFormStep()
            : _buildPreviewStep(),
      ),
    );
  }

  Widget _buildFormStep() {
    return SingleChildScrollView(
      padding: const EdgeInsets.all(20),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Driver info card
          Card(
            child: Padding(
              padding: const EdgeInsets.all(16),
              child: Row(
                children: [
                  CircleAvatar(
                    backgroundColor: Theme.of(
                      context,
                    ).colorScheme.primary.withOpacity(0.15),
                    child: Icon(
                      Icons.person,
                      color: Theme.of(context).colorScheme.primary,
                    ),
                  ),
                  const SizedBox(width: 12),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          _driverName.isNotEmpty
                              ? _driverName
                              : 'checkin_driver'.tr(),
                          style: const TextStyle(fontWeight: FontWeight.w600),
                        ),
                        if (_truckType != '-') ...[
                          const SizedBox(height: 2),
                          Text(
                            '${'checkin_truck_type'.tr()}: $_truckType ($_truckModel)',
                            style: Theme.of(context).textTheme.bodySmall,
                          ),
                        ],
                      ],
                    ),
                  ),
                ],
              ),
            ),
          ),
          const SizedBox(height: 16),

          // Truck for this job — required. The driver is responsible for the truck they pick,
          // for this job only; it does not bind them to it.
          TruckPickerField(
            trucks: _selectableTrucks,
            selected: _selectedTruck,
            loading: _trucksLoading,
            enabled: !_submitting,
            onChanged: (truck) => setState(() => _selectedTruck = truck),
          ),
          const SizedBox(height: 24),

          // Origin picker — FM: Hub | LH: SOC
          Text(
            'checkin_origin'.tr(),
            style: Theme.of(
              context,
            ).textTheme.titleSmall?.copyWith(fontWeight: FontWeight.w600),
          ),
          const SizedBox(height: 8),
          SearchableHubPicker(
            label: widget.taskType == 'FIRST_MILE'
                ? 'checkin_origin_hub'.tr()
                : 'checkin_origin_soc'.tr(),
            hintText: widget.taskType == 'FIRST_MILE'
                ? 'checkin_select_hub'.tr()
                : 'checkin_select_soc'.tr(),
            value: _origin ?? '',
            hubs: widget.taskType == 'FIRST_MILE' ? _hubs : _socs,
            onSelected: (hub) => setState(() => _origin = hub.sourceId),
            allowAddNew: true,
            stationTypeForNew: widget.taskType == 'FIRST_MILE'
                ? stationTypeHub
                : stationTypeSoc,
            onHubAdded: (hub) => setState(() {
              if (widget.taskType == 'FIRST_MILE') {
                _hubs.add(hub);
              } else {
                _socs.add(hub);
              }
            }),
          ),
          const SizedBox(height: 20),

          // Destination picker — FM: SOC | LH: Hub
          Text(
            'checkin_destination'.tr(),
            style: Theme.of(
              context,
            ).textTheme.titleSmall?.copyWith(fontWeight: FontWeight.w600),
          ),
          const SizedBox(height: 8),
          SearchableHubPicker(
            label: widget.taskType == 'FIRST_MILE'
                ? 'checkin_destination_soc'.tr()
                : 'checkin_destination_hub'.tr(),
            hintText: widget.taskType == 'FIRST_MILE'
                ? 'checkin_select_soc'.tr()
                : 'checkin_select_destination'.tr(),
            value: _dest ?? '',
            hubs: widget.taskType == 'FIRST_MILE' ? _socs : _hubs,
            onSelected: (hub) => setState(() => _dest = hub.sourceId),
            allowAddNew: true,
            stationTypeForNew: widget.taskType == 'FIRST_MILE'
                ? stationTypeSoc
                : stationTypeHub,
            onHubAdded: (hub) => setState(() {
              if (widget.taskType == 'FIRST_MILE') {
                _socs.add(hub);
              } else {
                _hubs.add(hub);
              }
            }),
          ),
          const SizedBox(height: 32),

          // Next button
          SizedBox(
            width: double.infinity,
            height: 52,
            child: ElevatedButton.icon(
              icon: const Icon(Icons.camera_alt),
              label: Text('checkin_take_photo'.tr()),
              style: ElevatedButton.styleFrom(
                textStyle: const TextStyle(
                  fontSize: 16,
                  fontWeight: FontWeight.w600,
                ),
              ),
              onPressed: _takePhotoAndPreview,
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildPreviewStep() {
    final isFm = widget.taskType == 'FIRST_MILE';
    final originResolved = isFm
        ? _hubs.where((h) => h.sourceId == _origin).firstOrNull
        : _socs.where((h) => h.sourceId == _origin).firstOrNull;
    final destResolved = isFm
        ? _socs.where((h) => h.sourceId == _dest).firstOrNull
        : _hubs.where((h) => h.sourceId == _dest).firstOrNull;
    final originDisplay = originResolved != null
        ? '${originResolved.sourceId} - ${originResolved.sourceNameEn}'
        : _origin ?? '-';
    final destDisplay = destResolved != null
        ? '${destResolved.sourceId} - ${destResolved.sourceNameEn}'
        : _dest ?? '-';

    return SingleChildScrollView(
      padding: const EdgeInsets.all(20),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            'checkin_preview_title'.tr(),
            style: Theme.of(
              context,
            ).textTheme.titleLarge?.copyWith(fontWeight: FontWeight.bold),
          ),
          const SizedBox(height: 16),

          // Photo preview
          if (_photoBytes != null)
            ClipRRect(
              borderRadius: BorderRadius.circular(12),
              child: Image.memory(
                _photoBytes!,
                width: double.infinity,
                height: 200,
                fit: BoxFit.cover,
              ),
            ),
          const SizedBox(height: 16),

          // Info card
          Card(
            child: Padding(
              padding: const EdgeInsets.all(16),
              child: Column(
                children: [
                  _previewRow(
                    Icons.my_location,
                    'checkin_origin'.tr(),
                    originDisplay,
                  ),
                  const Divider(height: 20),
                  _previewRow(
                    Icons.flag,
                    'checkin_destination'.tr(),
                    destDisplay,
                  ),
                  const Divider(height: 20),
                  _previewRow(
                    Icons.local_shipping,
                    'checkin_truck_type'.tr(),
                    '$_truckType ($_truckModel)',
                  ),
                  const Divider(height: 20),
                  _previewRow(
                    Icons.credit_card,
                    'checkin_license_plate'.tr(),
                    _licensePlate,
                  ),
                  const Divider(height: 20),
                  _previewRow(Icons.person, 'checkin_driver'.tr(), _driverName),
                  const Divider(height: 20),
                  _previewRow(
                    Icons.phone,
                    'mobile'.tr(),
                    _driverData?['mobile'] as String? ?? '-',
                  ),
                  const Divider(height: 20),
                  _previewRow(
                    Icons.access_time,
                    'loading_phase_timestamp'.tr(),
                    DateFormat('dd/MM/yyyy HH:mm').format(DateTime.now()),
                  ),
                ],
              ),
            ),
          ),

          const SizedBox(height: 16),

          // Helper selection — main driver picks helpers who train/assist on this job
          _buildHelperCard(),

          const SizedBox(height: 24),

          // Action buttons
          Row(
            children: [
              // Edit button
              Expanded(
                child: OutlinedButton.icon(
                  icon: const Icon(Icons.edit),
                  label: Text('checkin_edit'.tr()),
                  style: OutlinedButton.styleFrom(
                    minimumSize: const Size(0, 52),
                    textStyle: const TextStyle(
                      fontSize: 16,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  onPressed: _submitting
                      ? null
                      : () => setState(() => _step = _CheckInStep.form),
                ),
              ),
              const SizedBox(width: 12),
              // Save button
              Expanded(
                flex: 2,
                child: ElevatedButton.icon(
                  icon: _submitting
                      ? const SizedBox(
                          width: 20,
                          height: 20,
                          child: CircularProgressIndicator(
                            color: Colors.white,
                            strokeWidth: 2,
                          ),
                        )
                      : const Icon(Icons.check),
                  label: Text(
                    _submitting
                        ? 'checkin_processing'.tr()
                        : 'checkin_save'.tr(),
                  ),
                  style: ElevatedButton.styleFrom(
                    minimumSize: const Size(0, 52),
                    textStyle: const TextStyle(
                      fontSize: 16,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  onPressed: _submitting ? null : _submit,
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }

  Widget _previewRow(IconData icon, String label, String value) {
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Icon(icon, size: 20, color: Theme.of(context).colorScheme.primary),
        const SizedBox(width: 12),
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                label,
                style: Theme.of(context).textTheme.bodySmall?.copyWith(
                  color: Theme.of(context).colorScheme.onSurfaceVariant,
                ),
              ),
              const SizedBox(height: 2),
              Text(
                value,
                style: const TextStyle(
                  fontWeight: FontWeight.w600,
                  fontSize: 15,
                ),
              ),
            ],
          ),
        ),
      ],
    );
  }
}
