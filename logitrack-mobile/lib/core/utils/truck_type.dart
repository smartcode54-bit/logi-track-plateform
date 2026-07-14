/// Maps the truck master's `type` (trucks/{id}.type — full words, set in the web truck form)
/// to the abbreviated vehicle class carried on a task (tasks.truckType).
///
/// Billing selects the rate card from tasks.truckType, so this mapping is the only place the two
/// vocabularies are allowed to meet. Mirror of logitrack-web/lib/truckType.ts — keep both in sync.
///
/// '4 Wheels' is legacy (no longer offered in the truck form) and still maps to 4WJ.
const Map<String, String> kTruckDocTypeToTaskType = <String, String>{
  'Pickup': '4W',
  '4 Wheels': '4WJ',
  '4 Wheels Jumbo': '4WJ',
  '6 Wheels': '6WH',
  '10 Wheels': '10WH',
  '18 Wheels': '18WH',
  'Van': 'VAN',
};

const List<String> kTaskTruckTypes = <String>[
  '4W',
  '4WJ',
  '6WH',
  '10WH',
  '18WH',
  'VAN',
];

/// Derives a task's truckType from a truck doc's `type`.
///
/// Returns null for an unknown/blank type — callers must not invent a class, because a wrong
/// class silently bills the trip against the wrong rate card.
String? taskTruckTypeFromTruckDoc(String? type) {
  final raw = (type ?? '').trim();
  if (raw.isEmpty) return null;
  final mapped = kTruckDocTypeToTaskType[raw];
  if (mapped != null) return mapped;
  // Some legacy truck docs already store the abbreviation.
  final upper = raw.toUpperCase();
  return kTaskTruckTypes.contains(upper) ? upper : null;
}
