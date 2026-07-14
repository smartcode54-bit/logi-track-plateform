import 'package:cloud_firestore/cloud_firestore.dart';

import '../../../../core/utils/truck_type.dart';

/// A truck the driver may pick for a job.
class SelectableTruck {
  const SelectableTruck({
    required this.id,
    required this.licensePlate,
    required this.docType,
    this.model,
  });

  final String id;
  final String licensePlate;

  /// Raw trucks/{id}.type ('6 Wheels'). Use [taskTruckType] for the task/billing class.
  final String docType;
  final String? model;

  /// Abbreviated class written onto the task (tasks.truckType). Null when the truck's
  /// type is unrecognised — such a truck must not be used, or billing picks the wrong rate.
  String? get taskTruckType => taskTruckTypeFromTruckDoc(docType);

  factory SelectableTruck.fromDoc(
    DocumentSnapshot<Map<String, dynamic>> doc,
  ) {
    final data = doc.data() ?? <String, dynamic>{};
    return SelectableTruck(
      id: doc.id,
      licensePlate: (data['licensePlate'] as String? ?? '').trim(),
      docType: (data['type'] as String? ?? '').trim(),
      model: (data['model'] as String?)?.trim(),
    );
  }
}

class TrucksRepository {
  final FirebaseFirestore _firestore = FirebaseFirestore.instance;

  /// Trucks this driver may run: their own company's fleet.
  ///
  /// A subcontractor's driver sees only that subcontractor's trucks; an own-fleet driver sees
  /// own-fleet trucks. This is what keeps "pick your own truck" from letting a partner book a
  /// trip or a fuel receipt onto someone else's vehicle.
  Future<List<SelectableTruck>> fetchSelectableTrucks({
    String? subcontractorId,
  }) async {
    final trucks = _firestore.collection('trucks');
    final partnerId = (subcontractorId ?? '').trim();

    final snapshot = partnerId.isNotEmpty
        ? await trucks.where('subcontractorId', isEqualTo: partnerId).get()
        : await trucks.where('ownershipType', isEqualTo: 'own').get();

    final result = snapshot.docs
        .map(SelectableTruck.fromDoc)
        .where((truck) => truck.licensePlate.isNotEmpty)
        .toList();
    result.sort((a, b) => a.licensePlate.compareTo(b.licensePlate));
    return result;
  }

  /// A single truck by id — used to resolve the truck already assigned to a task.
  Future<SelectableTruck?> fetchTruck(String truckId) async {
    if (truckId.trim().isEmpty) return null;
    final doc = await _firestore.collection('trucks').doc(truckId.trim()).get();
    if (!doc.exists) return null;
    return SelectableTruck.fromDoc(doc);
  }
}
