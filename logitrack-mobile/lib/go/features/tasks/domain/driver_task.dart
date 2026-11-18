/// Task statuses as the Go API writes them (canonical, lower case). Anything else is [TaskStatus.unknown]: shown,
/// never acted on.
enum TaskStatus { pending, assigned, checkedIn, inTransit, completed, cancelled, unknown }

TaskStatus taskStatusOf(String? raw) => switch (raw) {
      'pending' => TaskStatus.pending,
      'assigned' => TaskStatus.assigned,
      'checked_in' => TaskStatus.checkedIn,
      'in_transit' => TaskStatus.inTransit,
      'completed' => TaskStatus.completed,
      'cancelled' => TaskStatus.cancelled,
      _ => TaskStatus.unknown,
    };

/// The trip of a task (`task.trip`): its id, number and status.
class TaskTripRef {
  TaskTripRef({required this.id, required this.tripNo, required this.status});
  final String id;
  final String tripNo;
  final String status;

  bool get delivered => status == 'delivered';
}

/// One task of the driver's queue (`GET /v1/mobile/tasks`, ADR 0028 order).
class DriverTask {
  DriverTask({
    required this.id,
    required this.taskNo,
    required this.taskType,
    required this.status,
    required this.planAt,
    required this.source,
    required this.destination,
    required this.isMultiDelivery,
    this.truckId,
    this.truckType,
    this.licensePlate,
    this.jobCategory,
    this.trip,
  });

  final String id;
  final String taskNo;
  final String taskType;
  final TaskStatus status;
  final DateTime? planAt;
  final String source;
  final String destination;
  final bool isMultiDelivery;
  final String? truckId;
  final String? truckType;
  final String? licensePlate;
  final String? jobCategory;
  final TaskTripRef? trip;

  factory DriverTask.fromJson(Map<String, dynamic> j) {
    String label(Object? place, Object? raw) {
      if (place is Map && (place['label'] as String?)?.isNotEmpty == true) return place['label'] as String;
      return (raw as String?) ?? '';
    }

    final trip = j['trip'];
    return DriverTask(
      id: j['id'] as String? ?? '',
      taskNo: j['taskNo'] as String? ?? '',
      taskType: j['taskType'] as String? ?? '',
      status: taskStatusOf(j['status'] as String?),
      planAt: DateTime.tryParse(j['planAt'] as String? ?? '')?.toLocal(),
      source: label(j['source'], j['sourceHub']),
      destination: label(j['destinationPlace'], j['destination']),
      isMultiDelivery: j['isMultiDelivery'] == true,
      truckId: j['truckId'] as String?,
      truckType: j['truckType'] as String?,
      licensePlate: j['licensePlate'] as String?,
      jobCategory: j['jobCategory'] as String?,
      trip: trip is Map<String, dynamic>
          ? TaskTripRef(
              id: trip['id'] as String? ?? '',
              tripNo: trip['tripNo'] as String? ?? '',
              status: trip['status'] as String? ?? '',
            )
          : null,
    );
  }
}
