import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/material.dart';
import '../../data/repositories/leave_request_repository.dart';
import 'new_leave_request_page.dart';

class LeaveRequestPage extends StatefulWidget {
  const LeaveRequestPage({
    super.key,
    required this.driverId,
    this.driverName,
  });

  final String driverId;
  final String? driverName;

  static String typeLabel(String type) {
    switch (type) {
      case 'SICK':
        return 'leave_request_type_sick'.tr();
      case 'BUSINESS':
        return 'leave_request_type_business'.tr();
      default:
        return type;
    }
  }

  static String statusLabel(String status) {
    switch (status) {
      case 'PENDING':
        return 'leave_request_status_pending'.tr();
      case 'APPROVED':
        return 'leave_request_status_approved'.tr();
      case 'REJECTED':
        return 'leave_request_status_rejected'.tr();
      case 'CANCELLED':
        return 'leave_request_status_cancelled'.tr();
      default:
        return status;
    }
  }

  @override
  State<LeaveRequestPage> createState() => _LeaveRequestPageState();
}

class _LeaveRequestPageState extends State<LeaveRequestPage> {
  final _repo = LeaveRequestRepository();

  Future<void> _openNewRequestPage() async {
    final submitted = await Navigator.push<bool>(
      context,
      MaterialPageRoute(
        builder: (context) => NewLeaveRequestPage(
          driverId: widget.driverId,
          driverName: widget.driverName,
        ),
      ),
    );
    if (mounted && submitted == true) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('leave_request_success'.tr()),
          backgroundColor: Colors.green,
        ),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    if (widget.driverId.isEmpty) {
      return Scaffold(
        appBar: AppBar(title: Text('leave_request_title'.tr())),
        body: Center(
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: Text(
              'leave_request_driver_required'.tr(),
              textAlign: TextAlign.center,
              style: Theme.of(context).textTheme.bodyLarge,
            ),
          ),
        ),
      );
    }

    return Scaffold(
      appBar: AppBar(title: Text('leave_request_title'.tr())),
      body: StreamBuilder<QuerySnapshot<Map<String, dynamic>>>(
        stream: _repo.watchLeaveRequestsForDriver(widget.driverId),
        builder: (context, snapshot) {
          if (snapshot.hasError) {
            return Center(
              child: Padding(
                padding: const EdgeInsets.all(24),
                child: Text('Error: ${snapshot.error}'),
              ),
            );
          }
          if (!snapshot.hasData) {
            return const Center(child: CircularProgressIndicator());
          }
          final docs = snapshot.data!.docs;
          if (docs.isEmpty) {
            return Center(
              child: Padding(
                padding: const EdgeInsets.all(24),
                child: Column(
                  mainAxisAlignment: MainAxisAlignment.center,
                  children: [
                    Icon(
                      Icons.event_busy_outlined,
                      size: 64,
                      color: Theme.of(context).colorScheme.primary.withOpacity(0.5),
                    ),
                    const SizedBox(height: 16),
                    Text(
                      'leave_request_no_requests'.tr(),
                      textAlign: TextAlign.center,
                      style: Theme.of(context).textTheme.bodyLarge,
                    ),
                  ],
                ),
              ),
            );
          }
          return ListView.builder(
            padding: EdgeInsets.fromLTRB(
              16,
              16,
              16,
              88 + MediaQuery.of(context).viewPadding.bottom,
            ),
            itemCount: docs.length,
            itemBuilder: (context, index) {
              final doc = docs[index];
              final d = doc.data();
              final startDate = (d['startDate'] as Timestamp?)?.toDate();
              final endDate = (d['endDate'] as Timestamp?)?.toDate();
              final type = d['type'] as String? ?? '';
              final reason = d['reason'] as String? ?? '';
              final status = d['status'] as String? ?? '';

              return Card(
                margin: const EdgeInsets.only(bottom: 12),
                child: ListTile(
                  title: Text(
                    LeaveRequestPage.typeLabel(type),
                    style: const TextStyle(fontWeight: FontWeight.w600),
                  ),
                  subtitle: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      if (startDate != null && endDate != null)
                        Text(
                          '${DateFormat.yMMMd(context.locale.languageCode).format(startDate)} – ${DateFormat.yMMMd(context.locale.languageCode).format(endDate)}',
                          style: Theme.of(context).textTheme.bodySmall,
                        ),
                      if (reason.isNotEmpty)
                        Text(
                          reason,
                          maxLines: 2,
                          overflow: TextOverflow.ellipsis,
                          style: Theme.of(context).textTheme.bodySmall,
                        ),
                      const SizedBox(height: 4),
                      Chip(
                        label: Text(
                          LeaveRequestPage.statusLabel(status),
                          style: Theme.of(context).textTheme.labelSmall?.copyWith(
                                color: Colors.white,
                                fontWeight: FontWeight.w600,
                              ),
                        ),
                        backgroundColor: status == 'APPROVED'
                            ? Colors.green.shade800
                            : status == 'REJECTED'
                                ? Colors.red.shade800
                                : status == 'PENDING'
                                    ? Colors.orange.shade800
                                    : Colors.grey.shade700,
                      ),
                    ],
                  ),
                  isThreeLine: true,
                ),
              );
            },
          );
        },
      ),
      floatingActionButton: widget.driverId.isNotEmpty
          ? FloatingActionButton.extended(
              onPressed: _openNewRequestPage,
              icon: const Icon(Icons.add),
              label: Text('leave_request_new'.tr()),
            )
          : null,
    );
  }
}
