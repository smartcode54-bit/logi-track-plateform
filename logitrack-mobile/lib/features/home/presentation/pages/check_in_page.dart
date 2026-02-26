import 'package:easy_localization/easy_localization.dart';
import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/material.dart';
import 'package:image_picker/image_picker.dart';
import 'package:intl/intl.dart';
import 'package:url_launcher/url_launcher.dart';
import '../../data/repositories/first_mile_task_repository.dart';
import '../../data/repositories/first_mile_checkin_repository.dart';

enum TaskFilter { all, fm, lh }

class CheckInPage extends StatefulWidget {
  final String driverId;

  const CheckInPage({super.key, required this.driverId});

  @override
  State<CheckInPage> createState() => _CheckInPageState();
}

class _CheckInPageState extends State<CheckInPage> {
  TaskFilter _filter = TaskFilter.all;

  static bool _canCheckIn(Map<String, dynamic> t) {
    final taskId = t['id'] as String?;
    final status = t['status'] ?? '';
    return taskId != null &&
        status != 'Checked in' &&
        status != 'Completed' &&
        status != 'Cancelled';
  }

  static bool _isHistory(Map<String, dynamic> t) {
    final status = t['status'] ?? '';
    return status == 'Checked in' ||
        status == 'Completed' ||
        status == 'Cancelled';
  }

  @override
  Widget build(BuildContext context) {
    return StreamBuilder<List<Map<String, dynamic>>>(
      stream: streamTasksForDriver(widget.driverId),
      builder: (context, snap) {
        final tasks = snap.data ?? [];
        final hasOngoingTask = tasks.any((t) {
          final st = t['status'] as String? ?? '';
          return st != 'Pending' &&
              st != 'Assigned' &&
              st != 'Completed' &&
              st != 'Cancelled';
        });

        return Scaffold(
          appBar: AppBar(
            title: Text('my_tasks'.tr()),
            actions: [
              IconButton(
                icon: const Icon(Icons.add),
                onPressed: () {
                  if (hasOngoingTask) {
                    ScaffoldMessenger.of(context).showSnackBar(
                      SnackBar(
                        content: Text('please_finish_ongoing_task_first'.tr()),
                      ),
                    );
                    return;
                  }
                  showModalBottomSheet(
                    context: context,
                    isScrollControlled: true,
                    builder: (_) =>
                        _ManualCheckInSheet(driverId: widget.driverId),
                  );
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
              final firstMileTaskId = t['FirstMileTaskId'] as String? ?? '';
              if (_filter == TaskFilter.fm) {
                return firstMileTaskId.startsWith('FM');
              } else if (_filter == TaskFilter.lh) {
                return firstMileTaskId.startsWith('LH');
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
                      hasOngoingTask: hasOngoingTask,
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
                      hasOngoingTask: hasOngoingTask,
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
    required bool hasOngoingTask,
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
        status != 'Cancelled';
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
        title: Text('$source → $dest'),
        subtitle: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('$dateStr $time · $status'),
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
                onPressed: hasOngoingTask
                    ? () {
                        ScaffoldMessenger.of(context).showSnackBar(
                          SnackBar(
                            content: Text(
                              'please_finish_ongoing_task_first'.tr(),
                            ),
                          ),
                        );
                      }
                    : () => _doCheckIn(context, taskId!),
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

  Future<void> _doCheckIn(BuildContext context, String taskId) async {
    if (!context.mounted) return;
    if (kIsWeb && context.mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(
            'On web: select an image file. To use camera, run the app on Android or iOS.'
                .tr(),
          ),
          duration: const Duration(seconds: 4),
        ),
      );
    }
    final picker = ImagePicker();
    final xfile = await picker.pickImage(
      source: ImageSource.camera,
      imageQuality: 85,
    );
    if (xfile == null || !context.mounted) return;
    final imageBytes = await xfile.readAsBytes();
    if (!context.mounted) return;
    final timestamp = DateTime.now();

    bool dialogOpen = false;
    try {
      if (!context.mounted) return;
      showDialog(
        context: context,
        barrierDismissible: false,
        builder: (_) => const Center(child: CircularProgressIndicator()),
      );
      dialogOpen = true;
      final position = await getCurrentPosition();
      if (!context.mounted) return;
      if (dialogOpen) {
        Navigator.of(context).pop();
        dialogOpen = false;
      }
      await submitCheckIn(
        taskId: taskId,
        imageBytes: imageBytes,
        lat: position.latitude,
        lng: position.longitude,
        timestamp: timestamp,
      );
      if (!context.mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text('Check-in saved'.tr())));
      // Stream จะอัปเดตจาก Firestore อัตโนมัติ → งานที่เช็คอินจะย้ายไป History (reset สถานะงานวันนี้)
    } catch (e) {
      if (context.mounted) {
        if (dialogOpen) Navigator.of(context).pop();
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text('Check-in failed: $e'),
            backgroundColor: Colors.red,
          ),
        );
      }
    }
  }
}

class _ManualCheckInSheet extends StatefulWidget {
  final String driverId;
  const _ManualCheckInSheet({required this.driverId});

  @override
  State<_ManualCheckInSheet> createState() => _ManualCheckInSheetState();
}

class _ManualCheckInSheetState extends State<_ManualCheckInSheet> {
  String? _origin;
  String? _dest;
  String _truckType = 'PICKUP';
  bool _loading = true;
  bool _submitting = false;

  List<Map<String, dynamic>> _socs = [];
  List<Map<String, dynamic>> _hubs = [];
  Map<String, dynamic>? _driverData;
  Map<String, dynamic>? _assignedTruck;

  @override
  void initState() {
    super.initState();
    _fetchData();
  }

  Future<void> _fetchData() async {
    try {
      final drvDoc = await FirebaseFirestore.instance
          .collection('drivers')
          .doc(widget.driverId)
          .get();
      if (drvDoc.exists) {
        _driverData = drvDoc.data();
        final truckId = _driverData?['currentAssignment']?['truckId'];
        if (truckId != null) {
          final truckDoc = await FirebaseFirestore.instance
              .collection('trucks')
              .doc(truckId)
              .get();
          if (truckDoc.exists) _assignedTruck = truckDoc.data();
          final tt = _assignedTruck?['type'] as String?;
          if (tt == '4 Wheels' || tt == '4 Wheels Jumbo')
            _truckType = '4WJ';
          else if (tt == '6 Wheels')
            _truckType = '6WH';
          else if (tt == '10 Wheels')
            _truckType = '10WH';
          else if (tt == '18 Wheels')
            _truckType = '18WH';
          else if (tt == 'Van')
            _truckType = 'VAN';
        }
      }

      final hubsSnap = await FirebaseFirestore.instance
          .collection('hubs')
          .get();
      final all = hubsSnap.docs.map((d) => d.data()).toList();

      _hubs = all.where((h) {
        final st = (h['station_type'] ?? '').toString().toUpperCase();
        return st != 'SOC' && st != 'RETURN_CENTER' && !st.startsWith('SOC');
      }).toList();

      _socs = all.where((h) {
        final st = (h['station_type'] ?? '').toString().toUpperCase();
        final code = (h['source_id'] ?? h['hubId'] ?? '').toString();
        return st.startsWith('SOC') && !RegExp(r'^\d').hasMatch(code);
      }).toList();

      if (_socs.isNotEmpty)
        _origin = (_socs[0]['source_id'] ?? _socs[0]['hubId']).toString();
      if (_hubs.isNotEmpty)
        _dest = (_hubs[0]['source_id'] ?? _hubs[0]['hubId']).toString();
    } catch (e) {
      debugPrint('Error fetching manual check-in data: $e');
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _submit() async {
    if (_origin == null || _dest == null) return;
    if (kIsWeb) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('Not supported on web')));
      return;
    }

    final picker = ImagePicker();
    final xfile = await picker.pickImage(
      source: ImageSource.camera,
      imageQuality: 85,
    );
    if (xfile == null) return;
    final imageBytes = await xfile.readAsBytes();

    setState(() => _submitting = true);
    try {
      final now = DateTime.now();
      final startOfDay = DateTime(now.year, now.month, now.day);
      final endOfDay = DateTime(now.year, now.month, now.day, 23, 59, 59, 999);
      final snap = await FirebaseFirestore.instance
          .collection('first_mile_tasks')
          .where('date', isGreaterThanOrEqualTo: startOfDay)
          .where('date', isLessThanOrEqualTo: endOfDay)
          .count()
          .get();
      final count = snap.count ?? 0;
      final runningN = (count + 1).toString().padLeft(3, '0');
      final dateStr = DateFormat('ddMMyyyy').format(now);
      final newId = 'FM-$dateStr-$_dest-$runningN';

      final dName =
          '${_driverData?['firstName'] ?? ''} ${_driverData?['lastName'] ?? ''}'
              .trim();

      final docRef = await FirebaseFirestore.instance
          .collection('first_mile_tasks')
          .add({
            'FirstMileTaskId': newId,
            'createdAt': FieldValue.serverTimestamp(),
            'updatedAt': FieldValue.serverTimestamp(),
            'date': now,
            'time': DateFormat('HH:mm').format(now),
            'destination': _dest,
            'sourceHub': _origin,
            'status': 'Pending',
            'truckType': _truckType,
            'driverId': widget.driverId,
            'driverName': dName,
            'driverPhone': _driverData?['mobile'] ?? '',
            'licensePlate':
                _driverData?['currentAssignment']?['truckPlate'] ?? '',
          });

      final pos = await getCurrentPosition();
      await submitCheckIn(
        taskId: docRef.id,
        imageBytes: imageBytes,
        lat: pos.latitude,
        lng: pos.longitude,
        timestamp: now,
      );

      if (mounted) {
        Navigator.pop(context);
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Check-in saved'.tr())));
      }
    } catch (e) {
      if (mounted)
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Failed: $e'), backgroundColor: Colors.red),
        );
    } finally {
      if (mounted) setState(() => _submitting = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_loading) {
      return const SizedBox(
        height: 300,
        child: Center(child: CircularProgressIndicator()),
      );
    }
    return Padding(
      padding: EdgeInsets.only(
        left: 16,
        right: 16,
        top: 16,
        bottom: MediaQuery.of(context).viewInsets.bottom + 16,
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              Text(
                'Manual Check In (LH)'.tr(),
                style: Theme.of(context).textTheme.titleLarge,
              ),
              IconButton(
                icon: const Icon(Icons.close),
                onPressed: () => Navigator.pop(context),
              ),
            ],
          ),
          const SizedBox(height: 16),
          DropdownButtonFormField<String>(
            decoration: InputDecoration(labelText: 'Origin (SOC)'.tr()),
            value: _origin,
            isExpanded: true,
            items: _socs.map((e) {
              final id = (e['source_id'] ?? e['hubId']).toString();
              return DropdownMenuItem(value: id, child: Text(id));
            }).toList(),
            onChanged: (v) => setState(() => _origin = v),
          ),
          const SizedBox(height: 12),
          DropdownButtonFormField<String>(
            decoration: InputDecoration(labelText: 'Destination (Hub)'.tr()),
            value: _dest,
            isExpanded: true,
            items: _hubs.map((e) {
              final id = (e['source_id'] ?? e['hubId']).toString();
              return DropdownMenuItem(value: id, child: Text(id));
            }).toList(),
            onChanged: (v) => setState(() => _dest = v),
          ),
          const SizedBox(height: 12),
          DropdownButtonFormField<String>(
            decoration: InputDecoration(labelText: 'Truck Type'.tr()),
            value: _truckType,
            items: [
              'PICKUP',
              '4WJ',
              '6WH',
              '10WH',
              '18WH',
              'VAN',
            ].map((e) => DropdownMenuItem(value: e, child: Text(e))).toList(),
            onChanged: (v) => setState(() => _truckType = v ?? 'PICKUP'),
          ),
          const SizedBox(height: 24),
          SizedBox(
            width: double.infinity,
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
                  : const Icon(Icons.camera_alt),
              label: Text(
                _submitting ? 'Processing...' : 'Take Photo & Check-in'.tr(),
              ),
              onPressed: _submitting ? null : _submit,
            ),
          ),
        ],
      ),
    );
  }
}
