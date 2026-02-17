import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/material.dart';
import 'package:image_picker/image_picker.dart';
import '../../data/repositories/first_mile_task_repository.dart';
import '../../data/repositories/first_mile_checkin_repository.dart';

class CheckInPage extends StatelessWidget {
  final String driverId;

  const CheckInPage({super.key, required this.driverId});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text('my_tasks'.tr()),
      ),
      body: StreamBuilder<List<Map<String, dynamic>>>(
        stream: streamTasksForDriver(driverId),
        builder: (context, snap) {
          if (snap.connectionState == ConnectionState.waiting && !snap.hasData) {
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
          final tasks = snap.data ?? [];
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
          return ListView.builder(
            padding: const EdgeInsets.all(16),
            itemCount: tasks.length,
            itemBuilder: (_, i) {
              final t = tasks[i];
              final taskId = t['id'] as String?;
              final source = t['sourceHub'] ?? '';
              final dest = t['destination'] ?? '';
              final date = t['date'];
              final time = t['time'] ?? '';
              final status = t['status'] ?? '';
              final canCheckIn = taskId != null &&
                  status != 'Checked in' &&
                  status != 'Completed' &&
                  status != 'Cancelled';
              String dateStr = '';
              if (date != null && date is DateTime) {
                dateStr = '${date.day}/${date.month}/${date.year}';
              }
              return Card(
                margin: const EdgeInsets.only(bottom: 8),
                child: ListTile(
                  title: Text('$source → $dest'),
                  subtitle: Text('$dateStr $time · $status'),
                  trailing: canCheckIn
                      ? TextButton.icon(
                          icon: const Icon(Icons.camera_alt, size: 20),
                          label: Text('Check in'.tr()),
                          onPressed: () => _doCheckIn(context, taskId),
                        )
                      : null,
                ),
              );
            },
          );
        },
      ),
    );
  }

  Future<void> _doCheckIn(BuildContext context, String taskId) async {
    if (!context.mounted) return;
    if (kIsWeb && context.mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(
            'On web: select an image file. To use camera, run the app on Android or iOS.'.tr(),
          ),
          duration: const Duration(seconds: 4),
        ),
      );
    }
    final picker = ImagePicker();
    final xfile = await picker.pickImage(source: ImageSource.camera, imageQuality: 85);
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
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Check-in saved'.tr())),
      );
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
