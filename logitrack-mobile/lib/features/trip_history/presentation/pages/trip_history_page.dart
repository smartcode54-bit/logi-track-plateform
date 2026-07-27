import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:easy_localization/easy_localization.dart';
import 'package:firebase_auth/firebase_auth.dart';
import 'package:flutter/material.dart';
import 'package:intl/intl.dart';
import '../../../home/data/models/trip_record.dart' show TripRecord, DeliveryStopProgress;
import '../../../home/data/repositories/driver_repository.dart';
import '../../../home/data/repositories/hubs_repository.dart';
import '../../../home/data/repositories/trip_records_repository.dart';

const String _tripPartnerFilterAll = '__ALL__';
const String _tripPartnerFilterUnspecified = '__NONE__';

/// Get numbered circle emoji for stop index (1-9)
String _getNumberedCircle(int index) {
  const circles = ['①', '②', '③', '④', '⑤', '⑥', '⑦', '⑧', '⑨'];
  if (index > 0 && index <= circles.length) {
    return circles[index - 1];
  }
  return '$index';
}

class TripHistoryPage extends StatefulWidget {
  const TripHistoryPage({super.key});

  @override
  State<TripHistoryPage> createState() => _TripHistoryPageState();
}

class _TripHistoryPageState extends State<TripHistoryPage> {
  List<TripRecord> _allTrips = [];
  List<Map<String, dynamic>> _standbyRecords = [];
  Map<String, String> _sourceIdToName = {};
  bool _loading = true;
  late int _selectedYear;
  late int _selectedMonth; // 1–12
  bool _firstMileExpanded = false;
  bool _lineHaulExpanded = false;
  bool _standbyExpanded = false;

  /// Client-side filter by `partnerCode` (OCR); `__ALL__` = ทั้งหมด, `__NONE__` = ไม่ระบุ
  String _partnerFilter = _tripPartnerFilterAll;

  @override
  void initState() {
    super.initState();
    final now = DateTime.now();
    _selectedYear = now.year;
    _selectedMonth = now.month;
    _loadTrips();
  }

  Future<void> _loadTrips() async {
    final uid = FirebaseAuth.instance.currentUser?.uid;
    if (uid == null || uid.isEmpty) {
      if (mounted) {
        setState(() {
          _loading = false;
        });
      }
      return;
    }
    setState(() => _loading = true);
    try {
      // Trip records may store driverId as auth UID or as driver document ID — query both and merge.
      List<TripRecord> list = [];
      try {
        list = await getTripHistoryByDriver(uid);
      } catch (_) {}
      if (list.isEmpty) {
        final driver = await DriverRepository().getCurrentDriver(uid);
        final driverDocId = driver?['id'] as String?;
        if (driverDocId != null && driverDocId.isNotEmpty) {
          try {
            list = await getTripHistoryByDriver(driverDocId);
          } catch (_) {}
        }
      }

      // Load standby records — no orderBy to avoid composite index requirement; sort client-side
      List<Map<String, dynamic>> standbyList = [];
      try {
        final snapshot = await FirebaseFirestore.instance
            .collection('standby_records')
            .where('driverId', isEqualTo: uid)
            .get();
        standbyList = snapshot.docs.map((doc) => doc.data()).toList();
        standbyList.sort((a, b) {
          final aMs = (a['createdAt'] as Timestamp?)?.millisecondsSinceEpoch ?? 0;
          final bMs = (b['createdAt'] as Timestamp?)?.millisecondsSinceEpoch ?? 0;
          return bMs.compareTo(aMs);
        });
        debugPrint('✅ Standby records loaded: ${standbyList.length} records for driverId=$uid');
      } catch (e) {
        debugPrint('❌ Error loading standby records: $e');
      }

      final hubs = await fetchAllHubs();
      final map = <String, String>{};
      for (final h in hubs) {
        final id = h.sourceId.trim();
        if (id.isEmpty) continue;
        final name = h.sourceNameTh.isNotEmpty
            ? h.sourceNameTh
            : h.sourceNameEn;
        // Display as "code - name" format
        final displayName = name.isNotEmpty && name.toUpperCase() != id.toUpperCase()
            ? '$id - $name'
            : id;
        map[id] = displayName;
      }
      if (mounted) {
        setState(() {
          _allTrips = list;
          _standbyRecords = standbyList;
          _sourceIdToName = map;
          _loading = false;
        });
      }
    } catch (_) {
      if (mounted) setState(() => _loading = false);
    }
  }

  /// Filter standby records by selected month
  List<Map<String, dynamic>> _standbyInSelectedMonth() {
    return _standbyRecords.where((record) {
      final createdAt = record['createdAt'];
      if (createdAt is Timestamp) {
        final dt = createdAt.toDate();
        return dt.year == _selectedYear && dt.month == _selectedMonth;
      }
      return false;
    }).toList();
  }

  String _getSourceDisplayName(String? code) {
    if (code == null || code.trim().isEmpty) return '–';
    return _sourceIdToName[code.trim()] ?? code;
  }

  /// ปี: ปัจจุบันย้อนหลัง 5 ปี
  List<int> get _yearOptions {
    final now = DateTime.now();
    return List.generate(6, (i) => now.year - i);
  }

  /// เดือน: 1–12 (ใช้กับ DateFormat MMM สำหรับ label)
  static const List<int> _monthNumbers = [
    1,
    2,
    3,
    4,
    5,
    6,
    7,
    8,
    9,
    10,
    11,
    12,
  ];

  String _monthName(int month) {
    final locale = context.locale.languageCode;
    final d = DateTime(_selectedYear, month);
    if (locale == 'th') {
      return DateFormat.MMM('th').format(d);
    }
    return DateFormat.MMM().format(d);
  }

  bool _isInSelectedYearMonth(TripRecord t) {
    final created = t.createdAt;
    if (created == null) return false;
    return created.year == _selectedYear && created.month == _selectedMonth;
  }

  List<TripRecord> _tripsInSelectedMonth() {
    final list = _allTrips.where(_isInSelectedYearMonth).toList();
    list.sort((a, b) {
      final at = a.createdAt ?? DateTime(0);
      final bt = b.createdAt ?? DateTime(0);
      return bt.compareTo(at);
    });
    return list;
  }

  Set<String> _distinctPartnerCodesInMonth() {
    final codes = <String>{};
    for (final t in _tripsInSelectedMonth()) {
      final c = t.partnerCode?.trim();
      if (c != null && c.isNotEmpty) codes.add(c);
    }
    return codes;
  }

  bool _tripMatchesPartnerFilter(TripRecord t) {
    if (_partnerFilter == _tripPartnerFilterAll) return true;
    final pc = t.partnerCode?.trim() ?? '';
    if (_partnerFilter == _tripPartnerFilterUnspecified) return pc.isEmpty;
    return pc == _partnerFilter;
  }

  List<TripRecord> _filteredByPartner(List<TripRecord> list) =>
      list.where(_tripMatchesPartnerFilter).toList();

  @override
  Widget build(BuildContext context) {
    final inMonth =
        _filteredByPartner(_tripsInSelectedMonth());
    final firstMile = inMonth
        .where((t) => t.jobType == 'first_mile' && t.status != 'standby')
        .toList();
    final lineHaul = inMonth
        .where((t) => t.jobType == 'line_haul' && t.status != 'standby')
        .toList();
    final standbyInMonth = _standbyInSelectedMonth();
    final partnerCodesSorted = _distinctPartnerCodesInMonth().toList()..sort();

    return Scaffold(
      appBar: AppBar(
        title: Text('trip_history_title'.tr()),
        actions: [
          if (!_loading)
            IconButton(icon: const Icon(Icons.refresh), onPressed: _loadTrips),
        ],
      ),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : RefreshIndicator(
              onRefresh: _loadTrips,
              child: SingleChildScrollView(
                physics: const AlwaysScrollableScrollPhysics(),
                padding: EdgeInsets.fromLTRB(
                  16,
                  16,
                  16,
                  16 + MediaQuery.of(context).viewPadding.bottom,
                ),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    // Filter: ปี + เดือน (default = ปีเดือนปัจจุบัน)
                    Row(
                      children: [
                        Expanded(
                          child: DropdownButtonFormField<int>(
                            initialValue: _selectedYear,
                            decoration: InputDecoration(
                              labelText: 'trip_history_filter_year'.tr(),
                              border: const OutlineInputBorder(),
                            ),
                            items: _yearOptions
                                .map(
                                  (y) => DropdownMenuItem(
                                    value: y,
                                    child: Text('$y'),
                                  ),
                                )
                                .toList(),
                            onChanged: (v) {
                              if (v != null) {
                                setState(() {
                                  _selectedYear = v;
                                  _partnerFilter = _tripPartnerFilterAll;
                                });
                              }
                            },
                          ),
                        ),
                        const SizedBox(width: 12),
                        Expanded(
                          child: DropdownButtonFormField<int>(
                            initialValue: _selectedMonth,
                            decoration: InputDecoration(
                              labelText: 'trip_history_filter_month'.tr(),
                              border: const OutlineInputBorder(),
                            ),
                            items: _monthNumbers
                                .map(
                                  (m) => DropdownMenuItem(
                                    value: m,
                                    child: Text(_monthName(m)),
                                  ),
                                )
                                .toList(),
                            onChanged: (v) {
                              if (v != null) {
                                setState(() {
                                  _selectedMonth = v;
                                  _partnerFilter = _tripPartnerFilterAll;
                                });
                              }
                            },
                          ),
                        ),
                      ],
                    ),
                    const SizedBox(height: 12),
                    DropdownButtonFormField<String>(
                      value: partnerCodesSorted.contains(_partnerFilter) ||
                              _partnerFilter == _tripPartnerFilterAll ||
                              _partnerFilter == _tripPartnerFilterUnspecified
                          ? _partnerFilter
                          : _tripPartnerFilterAll,
                      decoration: InputDecoration(
                        labelText: 'trip_history_filter_partner'.tr(),
                        border: const OutlineInputBorder(),
                      ),
                      items: [
                        DropdownMenuItem(
                          value: _tripPartnerFilterAll,
                          child: Text('trip_history_partner_all'.tr()),
                        ),
                        DropdownMenuItem(
                          value: _tripPartnerFilterUnspecified,
                          child: Text('trip_history_partner_unspecified'.tr()),
                        ),
                        ...partnerCodesSorted.map(
                          (c) => DropdownMenuItem(value: c, child: Text(c)),
                        ),
                      ],
                      onChanged: (v) {
                        if (v != null) setState(() => _partnerFilter = v);
                      },
                    ),
                    const SizedBox(height: 20),

                    // Mini dashboard
                    Row(
                      children: [
                        _StatCard(
                          label: 'trip_history_total'.tr(),
                          value: '${inMonth.length}',
                          icon: Icons.local_shipping,
                          color: Colors.blue,
                        ),
                        const SizedBox(width: 12),
                        _StatCard(
                          label: 'trip_history_first_mile'.tr(),
                          value: '${firstMile.length}',
                          icon: Icons.pin_drop,
                          color: Colors.indigo,
                        ),
                        const SizedBox(width: 12),
                        _StatCard(
                          label: 'trip_history_line_haul'.tr(),
                          value: '${lineHaul.length}',
                          icon: Icons.fire_truck,
                          color: Colors.orange,
                        ),
                      ],
                    ),
                    const SizedBox(height: 24),

                    // First Mile card – แสดงแค่ยอดสรุป คลิกจึงขยายเห็น List
                    _SectionCard(
                      title: 'trip_history_card_first_mile'.tr(),
                      count: firstMile.length,
                      color: Colors.indigo,
                      trips: firstMile,
                      expanded: _firstMileExpanded,
                      getSourceDisplayName: _getSourceDisplayName,
                      onTap: () => setState(
                        () => _firstMileExpanded = !_firstMileExpanded,
                      ),
                    ),
                    const SizedBox(height: 16),

                    // Line Haul card
                    _SectionCard(
                      title: 'trip_history_card_line_haul'.tr(),
                      count: lineHaul.length,
                      color: Colors.orange,
                      trips: lineHaul,
                      expanded: _lineHaulExpanded,
                      getSourceDisplayName: _getSourceDisplayName,
                      onTap: () => setState(
                        () => _lineHaulExpanded = !_lineHaulExpanded,
                      ),
                    ),
                    const SizedBox(height: 16),

                    // Standby card
                    if (standbyInMonth.isNotEmpty)
                      _StandbyCard(
                        title: 'trip_history_card_standby'.tr(),
                        count: standbyInMonth.length,
                        standbyRecords: standbyInMonth,
                        expanded: _standbyExpanded,
                        onTap: () => setState(
                          () => _standbyExpanded = !_standbyExpanded,
                        ),
                      ),
                  ],
                ),
              ),
            ),
    );
  }
}

class _StatCard extends StatelessWidget {
  const _StatCard({
    required this.label,
    required this.value,
    required this.icon,
    required this.color,
  });

  final String label;
  final String value;
  final IconData icon;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Expanded(
      child: Card(
        elevation: 2,
        child: Padding(
          padding: const EdgeInsets.symmetric(vertical: 12, horizontal: 8),
          child: Column(
            children: [
              Icon(icon, color: color, size: 28),
              const SizedBox(height: 6),
              Text(
                value,
                style: Theme.of(
                  context,
                ).textTheme.titleLarge?.copyWith(fontWeight: FontWeight.bold),
              ),
              const SizedBox(height: 2),
              Text(
                label,
                style: Theme.of(
                  context,
                ).textTheme.bodySmall?.copyWith(color: Colors.grey[600]),
                textAlign: TextAlign.center,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _SectionCard extends StatelessWidget {
  const _SectionCard({
    required this.title,
    required this.count,
    required this.color,
    required this.trips,
    required this.expanded,
    required this.getSourceDisplayName,
    required this.onTap,
  });

  final String title;
  final int count;
  final Color color;
  final List<TripRecord> trips;
  final bool expanded;
  final String Function(String?) getSourceDisplayName;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Card(
      elevation: 2,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: BorderSide(color: color.withOpacity(0.5), width: 1),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          // ส่วนยอดสรุป – คลิกเพื่อขยาย/ย่อ
          InkWell(
            onTap: onTap,
            borderRadius: BorderRadius.circular(12),
            child: Padding(
              padding: const EdgeInsets.all(16),
              child: Row(
                children: [
                  Container(
                    width: 8,
                    height: 24,
                    decoration: BoxDecoration(
                      color: color,
                      borderRadius: BorderRadius.circular(4),
                    ),
                  ),
                  const SizedBox(width: 10),
                  Expanded(
                    child: Text(
                      title,
                      style: Theme.of(context).textTheme.titleMedium?.copyWith(
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                  ),
                  Chip(
                    label: Text('$count'),
                    backgroundColor: color.withOpacity(0.15),
                  ),
                  const SizedBox(width: 8),
                  Icon(
                    expanded ? Icons.expand_less : Icons.expand_more,
                    color: Colors.grey[600],
                  ),
                ],
              ),
            ),
          ),
          // รายการและรายละเอียด – แสดงเมื่อขยายแล้ว
          if (expanded) ...[
            const Divider(height: 1),
            if (trips.isEmpty)
              Padding(
                padding: const EdgeInsets.all(24),
                child: Text(
                  'trip_history_no_trips'.tr(),
                  style: TextStyle(color: Colors.grey[600]),
                  textAlign: TextAlign.center,
                ),
              )
            else
              ListView.separated(
                shrinkWrap: true,
                physics: const NeverScrollableScrollPhysics(),
                itemCount: trips.length,
                separatorBuilder: (_, __) => const Divider(height: 1),
                itemBuilder: (context, i) {
                  final t = trips[i];
                  final tripCreateDate = t.createdAt != null
                      ? DateFormat('dd/MM/yy HH:mm').format(t.createdAt!)
                      : '–';
                  final tripId =
                      t.spxTripId ??
                      (t.id != null && t.id!.length > 12
                          ? t.id!.substring(0, 12)
                          : t.id) ??
                      '–';

                  // Multi-delivery: show all stops
                  final isMultiDelivery = t.deliveryStopsProgress != null &&
                      t.deliveryStopsProgress!.isNotEmpty;

                  final subtitleParts = <String>[tripCreateDate];

                  if (isMultiDelivery) {
                    // Show origin once (code - name format)
                    final originDisplay = t.origin != null ? getSourceDisplayName(t.origin) : '–';
                    subtitleParts.add('From: $originDisplay');
                    subtitleParts.add('');

                    // Show all stops with numbers and delivery time (name only)
                    for (int stopIdx = 0; stopIdx < t.deliveryStopsProgress!.length; stopIdx++) {
                      final stop = t.deliveryStopsProgress![stopIdx];
                      final stopNum = _getNumberedCircle(stopIdx + 1);
                      // Use destination name from mapping (same as single delivery)
                      final stopName = getSourceDisplayName(stop.destination);
                      final stopDeliveryTime = stop.deliveredAt != null
                          ? DateFormat('HH:mm').format(stop.deliveredAt!)
                          : '–';
                      subtitleParts.add('$stopNum $stopName @ $stopDeliveryTime');
                    }
                  } else {
                    // Single delivery: show origin + destination + delivery time
                    final originLabel = 'checkin_origin'.tr();
                    final destLabel = 'trip_history_dest'.tr();
                    final originName = getSourceDisplayName(t.origin);
                    final destName = getSourceDisplayName(t.destination);
                    final deliveryTime = t.deliveredTimestamp != null
                        ? DateFormat('HH:mm').format(t.deliveredTimestamp!)
                        : '–';
                    subtitleParts.add('$originLabel : $originName');
                    subtitleParts.add('$destLabel : $destName @ $deliveryTime');
                  }

                  return ListTile(
                    title: Text(
                      tripId,
                      style: const TextStyle(
                        fontFamily: 'monospace',
                        fontSize: 13,
                      ),
                    ),
                    subtitle: Text(
                      subtitleParts.join('\n'),
                      style: TextStyle(fontSize: 12, color: Colors.grey[600]),
                      maxLines: 15,
                      overflow: TextOverflow.ellipsis,
                    ),
                    trailing: _StatusChip(status: t.status, trip: t),
                  );
                },
              ),
          ],
        ],
      ),
    );
  }
}

class _StatusChip extends StatelessWidget {
  const _StatusChip({required this.status, this.trip});

  final String status;
  final TripRecord? trip;

  Color get _color {
    switch (status) {
      case 'delivered':
        return Colors.green;
      case 'in_transit':
        return Colors.amber;
      case 'loading':
        return Colors.grey;
      case 'standby':
        return Colors.teal;
      default:
        return Colors.blueGrey;
    }
  }

  @override
  Widget build(BuildContext context) {
    final key = 'trip_history_status_$status';
    final label = key.tr();
    return Chip(
      label: Text(
        label.length > 20 ? status : label,
        style: const TextStyle(fontSize: 11),
      ),
      backgroundColor: _color.withOpacity(0.15),
      padding: EdgeInsets.zero,
      materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
    );
  }
}

class _StandbyCard extends StatelessWidget {
  const _StandbyCard({
    required this.title,
    required this.count,
    required this.standbyRecords,
    required this.expanded,
    required this.onTap,
  });

  final String title;
  final int count;
  final List<Map<String, dynamic>> standbyRecords;
  final bool expanded;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    const color = Colors.teal;
    return Card(
      elevation: 2,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: const BorderSide(color: Colors.teal, width: 1),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          // ส่วนยอดสรุป – คลิกเพื่อขยาย/ย่อ
          InkWell(
            onTap: onTap,
            borderRadius: BorderRadius.circular(12),
            child: Padding(
              padding: const EdgeInsets.all(16),
              child: Row(
                children: [
                  Container(
                    width: 8,
                    height: 24,
                    decoration: BoxDecoration(
                      color: color,
                      borderRadius: BorderRadius.circular(4),
                    ),
                  ),
                  const SizedBox(width: 10),
                  Expanded(
                    child: Text(
                      title,
                      style: Theme.of(context).textTheme.titleMedium?.copyWith(
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                  ),
                  Chip(
                    label: Text('$count'),
                    backgroundColor: color.withOpacity(0.15),
                  ),
                  const SizedBox(width: 8),
                  Icon(
                    expanded ? Icons.expand_less : Icons.expand_more,
                    color: Colors.grey[600],
                  ),
                ],
              ),
            ),
          ),
          // รายการและรายละเอียด – แสดงเมื่อขยายแล้ว
          if (expanded) ...[
            const Divider(height: 1),
            if (standbyRecords.isEmpty)
              Padding(
                padding: const EdgeInsets.all(24),
                child: Text(
                  'trip_history_no_trips'.tr(),
                  style: TextStyle(color: Colors.grey[600]),
                  textAlign: TextAlign.center,
                ),
              )
            else
              ListView.separated(
                shrinkWrap: true,
                physics: const NeverScrollableScrollPhysics(),
                itemCount: standbyRecords.length,
                separatorBuilder: (_, __) => const Divider(height: 1),
                itemBuilder: (context, i) {
                  final record = standbyRecords[i];
                  final createdAt = record['createdAt'];
                  final recordDate = createdAt is Timestamp
                      ? DateFormat('dd/MM/yy HH:mm').format(createdAt.toDate())
                      : '–';
                  final startLoc = record['startLocation'] as String? ?? '–';
                  final endLoc = record['endLocation'] as String? ?? '–';
                  final durationMin = record['durationMinutes'] as int? ?? 0;
                  final note = record['note'] as String?;

                  return ListTile(
                    title: Text(
                      '$startLoc → $endLoc',
                      style: const TextStyle(
                        fontFamily: 'monospace',
                        fontSize: 13,
                        fontWeight: FontWeight.w500,
                      ),
                    ),
                    subtitle: Text(
                      '$recordDate · ${durationMin} min${note != null && note.isNotEmpty ? '\n$note' : ''}',
                      style: TextStyle(fontSize: 12, color: Colors.grey[600]),
                      maxLines: 3,
                      overflow: TextOverflow.ellipsis,
                    ),
                  );
                },
              ),
          ],
        ],
      ),
    );
  }
}
