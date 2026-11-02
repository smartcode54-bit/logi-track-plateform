import 'package:easy_localization/easy_localization.dart';
import 'package:firebase_auth/firebase_auth.dart';
import 'package:flutter/material.dart';
import 'package:intl/intl.dart' as intl;
import '../../../../core/route_observer.dart';
import '../../../../core/utils/driver_identity.dart';
import '../../data/models/vehicle_expense.dart';
import '../../data/repositories/vehicle_expense_repository.dart';
import 'refuel_form_page.dart';
import 'other_expense_form_page.dart';
import '../../../home/data/repositories/driver_repository.dart'; // 🚗 ดึงข้อมูลรถ
import '../../data/repositories/maintenance_repository.dart'; // 🛠️ ฟังก์ชันดึง PM
import 'maintenance_action_page.dart'; // 🛠️ หน้าฟอร์มกดแนบเอกสารซ่อม
import '../utils/maintenance_i18n.dart';

/// กลุ่มของประวัติ — ใช้เป็นทั้งชิปตัวกรองและสี/ไอคอนของรายการ
enum _HistoryGroup { all, fuel, repair, other }

/// หน้าหลัก "จัดการรถ / เปลี่ยนค่าใช้จ่าย" — เลือกบันทึกเติมน้ำมัน หรือค่าใช้จ่ายอื่น + แสดงประวัติล่าสุด
class VehicleExpensePage extends StatefulWidget {
  const VehicleExpensePage({super.key});

  @override
  State<VehicleExpensePage> createState() => _VehicleExpensePageState();
}

class _VehicleExpensePageState extends State<VehicleExpensePage>
    with RouteAware {
  // .. state vars
  List<VehicleExpense> _recentList = [];
  bool _loading = true;
  bool _refreshing = false;
  String? _error;
  bool _routeObserverSubscribed = false;
  _HistoryGroup _historyFilter = _HistoryGroup.all;

  /// Cached so a rebuild (e.g. tapping a filter chip) does not resubscribe and flash the cards.
  Stream<List<Map<String, dynamic>>>? _activeMaintenanceStream;
  String? _activeMaintenanceTruckId;

  Map<String, dynamic>? _driverData; // 🚗 ข้อมูลพขร.
  final _driverRepository = DriverRepository(); // 🚗 เพื่อดึง info รถ

  /// รถที่พขร. ขับอยู่ตอนนี้ — `activeTruck` (รถของงานที่เช็คอินไว้) มาก่อน currentAssignment (รถประจำ)
  ///
  /// The truck is chosen per task, so a driver may be running a vehicle other than the one they
  /// are bound to. Expenses and maintenance must follow the truck they are actually driving,
  /// otherwise a receipt for truck B gets booked against truck A.
  String _truckIdForMaintenance(Map<String, dynamic>? driver) {
    if (driver == null) return '';
    final active = driver['activeTruck'];
    if (active is Map) {
      final tid = active['truckId'] as String?;
      if (tid != null && tid.isNotEmpty) return tid;
    }
    final ca = driver['currentAssignment'];
    if (ca is Map) {
      final tid = ca['truckId'] as String?;
      if (tid != null && tid.isNotEmpty) return tid;
    }
    final top = driver['truckId'] as String?;
    if (top != null && top.isNotEmpty) return top;
    return '';
  }

  @override
  void initState() {
    super.initState();
    _loadRecent();
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (!_routeObserverSubscribed) {
      final route = ModalRoute.of(context);
      if (route != null) {
        routeObserver.subscribe(this, route);
        _routeObserverSubscribed = true;
      }
    }
  }

  @override
  void dispose() {
    routeObserver.unsubscribe(this);
    super.dispose();
  }

  @override
  void didPopNext() {
    // กลับมาแสดงหน้านี้ (เช่น กลับจากฟอร์มหรือหน้าอื่น) → โหลดประวัติใหม่
    _loadRecent();
  }

  Future<void> _loadRecent() async {
    final uid = FirebaseAuth.instance.currentUser?.uid;
    if (uid == null || uid.isEmpty) {
      setState(() {
        _loading = false;
        _refreshing = false;
        _error = 'user_not_authenticated'.tr();
      });
      return;
    }
    final alreadyHaveList = _recentList.isNotEmpty;
    setState(() {
      if (alreadyHaveList) {
        _refreshing = true;
      } else {
        _loading = true;
      }
    });
    try {
      await syncPendingVehicleExpenses(uid);
      _driverData = await _driverRepository.getCurrentDriver(uid);
      // Past Auth UIDs (authIdHistory): refuels saved before an email / Google re-link carry
      // the old uid and would otherwise drop out of this history.
      final fromServer = await getVehicleExpensesByDriver(
        uid,
        limit: 20,
        pastDriverIds: driverIdentityKeys(_driverData),
      );
      final truckId = _truckIdForMaintenance(_driverData);
      final fromCompletedMaintenance =
          await MaintenanceRepository().fetchCompletedMaintenanceAsExpenses(
        truckId: truckId,
        driverId: uid,
        limit: 24,
      );

      final pending = await getPendingVehicleExpenses(uid);
      final merged = <VehicleExpense>[
        ...fromServer,
        ...fromCompletedMaintenance,
        ...pending,
      ];
      merged.sort((a, b) => (b.date).compareTo(a.date));
      final list = List<VehicleExpense>.from(merged.take(30));
      if (mounted) {
        setState(() {
          _recentList = list;
          _loading = false;
          _refreshing = false;
          _error = null;
        });
      }
    } catch (e) {
      if (mounted) {
        setState(() {
          _loading = false;
          _refreshing = false;
          _error = e.toString();
        });
      }
    }
  }

  /// ทะเบียนรถที่ขับอยู่ตอนนี้ — ต้องมาจากรถของงาน (activeTruck) ก่อนรถประจำ
  String? _truckPlateForExpense(Map<String, dynamic>? driver) {
    if (driver == null) return null;
    final active = driver['activeTruck'];
    if (active is Map) {
      final plate = active['truckPlate'] as String?;
      if (plate != null && plate.isNotEmpty) return plate;
    }
    final ca = driver['currentAssignment'];
    if (ca is Map) {
      final plate = ca['truckPlate'] as String?;
      if (plate != null && plate.isNotEmpty) return plate;
    }
    return null;
  }

  /// Fetch fresh driver data right before opening any expense form,
  /// so spare drivers who switch trucks mid-shift always record the correct plate.
  Future<Map<String, dynamic>?> _fetchFreshDriverData() async {
    final uid = FirebaseAuth.instance.currentUser?.uid;
    if (uid == null || uid.isEmpty) return _driverData;
    try {
      final fresh = await _driverRepository.getCurrentDriver(uid);
      if (fresh != null && mounted) setState(() => _driverData = fresh);
      return fresh ?? _driverData;
    } catch (_) {
      return _driverData;
    }
  }

  Future<void> _openRefuelForm() async {
    final driver = await _fetchFreshDriverData();
    final truckId = _truckIdForMaintenance(driver);
    final truckPlate = _truckPlateForExpense(driver);
    final result = await Navigator.of(context).push<bool>(MaterialPageRoute(
      builder: (_) => RefuelFormPage(
        truckId: truckId.isEmpty ? null : truckId,
        truckLicensePlate: truckPlate,
        pastDriverIds: driverIdentityKeys(driver),
      ),
    ));
    if (result == true) _loadRecent();
  }

  Future<void> _openOtherExpenseForm() async {
    final driver = await _fetchFreshDriverData();
    final truckId = _truckIdForMaintenance(driver);
    final truckPlate = _truckPlateForExpense(driver);
    final result = await Navigator.of(context).push<bool>(
      MaterialPageRoute(
        builder: (_) => OtherExpenseFormPage(
          truckId: truckId.isEmpty ? null : truckId,
          truckLicensePlate: truckPlate,
        ),
      ),
    );
    if (result == true) _loadRecent();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text('vehicle_expense_title'.tr()),
        leading: IconButton(
          icon: const Icon(Icons.arrow_back),
          onPressed: () => Navigator.of(context).pop(),
        ),
      ),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : _error != null
          ? Center(
              child: Padding(
                padding: const EdgeInsets.all(24.0),
                child: Column(
                  mainAxisAlignment: MainAxisAlignment.center,
                  children: [
                    Icon(
                      Icons.error_outline,
                      size: 48,
                      color: Theme.of(context).colorScheme.error,
                    ),
                    const SizedBox(height: 16),
                    Text(_error!, textAlign: TextAlign.center),
                  ],
                ),
              ),
            )
          : RefreshIndicator(
              onRefresh: _loadRecent,
              child: Stack(
                children: [
                  SingleChildScrollView(
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
                        if (_refreshing)
                          const Padding(
                            padding: EdgeInsets.only(bottom: 8.0),
                            child: Center(
                              child: SizedBox(
                                height: 24,
                                width: 24,
                                child: CircularProgressIndicator(
                                  strokeWidth: 2,
                                ),
                              ),
                            ),
                          ),
                        Text(
                          'vehicle_expense_subtitle'.tr(),
                          style: Theme.of(context).textTheme.bodyMedium
                              ?.copyWith(
                                color: Theme.of(
                                  context,
                                ).colorScheme.onSurface.withOpacity(0.7),
                              ),
                        ),
                        const SizedBox(height: 16),
                        // 🛠️ งานซ่อมบำรุงที่ยังไม่ปิด — ทุกงาน ไม่ใช่แค่งานแรก
                        StreamBuilder<List<Map<String, dynamic>>>(
                          stream: _activeMaintenanceStreamFor(
                            _truckIdForMaintenance(_driverData),
                          ),
                          builder: (context, snap) {
                            final tasks = snap.data ?? [];
                            if (tasks.isEmpty) return const SizedBox.shrink();
                            return Padding(
                              padding: const EdgeInsets.only(bottom: 20),
                              child: Column(
                                crossAxisAlignment: CrossAxisAlignment.stretch,
                                children: [
                                  _buildSectionHeader(
                                    context,
                                    icon: Icons.build_circle_outlined,
                                    title: 'maintenance_active_section'.tr(),
                                    count: tasks.length,
                                  ),
                                  for (final task in tasks)
                                    _buildActiveMaintenanceCard(context, task),
                                ],
                              ),
                            );
                          },
                        ),
                        _buildSectionHeader(
                          context,
                          icon: Icons.add_card_outlined,
                          title: 'vehicle_expense_record_section'.tr(),
                        ),
                        Row(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Expanded(
                              child: _buildSquareActionCard(
                                context,
                                icon: Icons.local_gas_station,
                                iconColor: Colors.blue,
                                title: 'vehicle_expense_refuel'.tr(),
                                subtitle: 'vehicle_expense_refuel_hint'.tr(),
                                onTap: _openRefuelForm,
                              ),
                            ),
                            const SizedBox(width: 12),
                            Expanded(
                              child: _buildSquareActionCard(
                                context,
                                icon: Icons.receipt_long,
                                iconColor: Colors.orange,
                                title: 'vehicle_expense_other'.tr(),
                                subtitle: 'vehicle_expense_other_hint'.tr(),
                                onTap: _openOtherExpenseForm,
                              ),
                            ),
                          ],
                        ),
                        const SizedBox(height: 28),
                        _buildSectionHeader(
                          context,
                          icon: Icons.history,
                          title: 'vehicle_expense_recent'.tr(),
                        ),
                        ..._buildHistory(context),
                      ],
                    ),
                  ),
                ],
              ),
            ),
    );
  }

  Stream<List<Map<String, dynamic>>> _activeMaintenanceStreamFor(
    String truckId,
  ) {
    if (_activeMaintenanceStream == null ||
        _activeMaintenanceTruckId != truckId) {
      _activeMaintenanceTruckId = truckId;
      _activeMaintenanceStream =
          MaintenanceRepository().streamActiveMaintenance(truckId);
    }
    return _activeMaintenanceStream!;
  }

  /// หัวข้อแต่ละส่วนของหน้า — ไอคอน + ชื่อ (+ จำนวน ถ้ามี)
  Widget _buildSectionHeader(
    BuildContext context, {
    required IconData icon,
    required String title,
    int? count,
  }) {
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.only(bottom: 10),
      child: Row(
        children: [
          Icon(icon, size: 20, color: theme.colorScheme.primary),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              count == null ? title : '$title ($count)',
              style: theme.textTheme.titleMedium?.copyWith(
                fontWeight: FontWeight.bold,
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildActiveMaintenanceCard(
    BuildContext context,
    Map<String, dynamic> task,
  ) {
    final status = task['status'] as String? ?? '';
    final serviceLabel = trMaintenanceServiceType(
      task['serviceType'] as String?,
    );
    final loc = rawMaintenanceLocation(task);
    return Card(
      color: Colors.amber.shade50,
      elevation: 2,
      margin: const EdgeInsets.only(bottom: 10),
      shape: RoundedRectangleBorder(
        side: BorderSide(color: Colors.amber.shade400, width: 1),
        borderRadius: BorderRadius.circular(12),
      ),
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 8.0, horizontal: 4.0),
        child: ListTile(
          leading: CircleAvatar(
            backgroundColor: Colors.amber.shade600,
            child: const Icon(Icons.build_circle_outlined, color: Colors.white),
          ),
          title: Text(
            serviceLabel,
            style: const TextStyle(
              fontWeight: FontWeight.bold,
              fontSize: 16,
              color: Colors.black87,
            ),
          ),
          subtitle: Padding(
            padding: const EdgeInsets.only(top: 4),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  'maintenance_vehicle_status_line'.tr(
                    namedArgs: {'status': trMaintenanceStatus(status)},
                  ),
                  style: TextStyle(
                    color: Colors.amber.shade900,
                    fontWeight: FontWeight.bold,
                  ),
                ),
                if (loc != null)
                  Text(
                    loc,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                      color: Colors.amber.shade800,
                      fontSize: 13,
                    ),
                  ),
              ],
            ),
          ),
          trailing: FilledButton.tonal(
            style: FilledButton.styleFrom(
              visualDensity: VisualDensity.compact,
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
              backgroundColor: Colors.amber.shade200,
              foregroundColor: Colors.amber.shade900,
            ),
            onPressed: () async {
              final result = await Navigator.push(
                context,
                MaterialPageRoute(
                  builder: (_) => MaintenanceActionPage(task: task),
                ),
              );
              if (result == true) _loadRecent();
            },
            child: Text(
              'maintenance_vehicle_card_cta'.tr(),
              style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w600),
            ),
          ),
        ),
      ),
    );
  }

  /// ซ่อมบำรุงที่แอดมินปิดแล้ว + ปะยาง = ซ่อม; ทางด่วน/ที่จอด/อื่นๆ = อื่นๆ
  _HistoryGroup _groupOf(VehicleExpense e) {
    if (e.isFuel) return _HistoryGroup.fuel;
    switch (e.category) {
      case OtherExpenseCategory.maintenance:
      case OtherExpenseCategory.tireRepair:
        return _HistoryGroup.repair;
      default:
        return _HistoryGroup.other;
    }
  }

  String _groupLabel(_HistoryGroup g) {
    switch (g) {
      case _HistoryGroup.all:
        return 'vehicle_expense_filter_all'.tr();
      case _HistoryGroup.fuel:
        return 'vehicle_expense_filter_fuel'.tr();
      case _HistoryGroup.repair:
        return 'vehicle_expense_filter_repair'.tr();
      case _HistoryGroup.other:
        return 'vehicle_expense_filter_other'.tr();
    }
  }

  Color _groupColor(_HistoryGroup g) {
    switch (g) {
      case _HistoryGroup.fuel:
        return Colors.blue;
      case _HistoryGroup.repair:
        return Colors.teal;
      case _HistoryGroup.all:
      case _HistoryGroup.other:
        return Colors.orange;
    }
  }

  /// "พฤศจิกายน 2569" (TH, พ.ศ.) / "November 2026" (EN)
  String _monthLabel(BuildContext context, DateTime month) {
    final lang = context.locale.languageCode;
    final name = intl.DateFormat.MMMM(lang).format(month);
    final year = lang == 'th' ? month.year + 543 : month.year;
    return '$name $year';
  }

  /// ชิปกรองตามประเภท + รายการแบ่งตามเดือน พร้อมยอดรวมของเดือน
  List<Widget> _buildHistory(BuildContext context) {
    final theme = Theme.of(context);
    final counts = {for (final g in _HistoryGroup.values) g: 0};
    for (final e in _recentList) {
      counts[_groupOf(e)] = counts[_groupOf(e)]! + 1;
    }
    counts[_HistoryGroup.all] = _recentList.length;

    final visible = _historyFilter == _HistoryGroup.all
        ? _recentList
        : _recentList.where((e) => _groupOf(e) == _historyFilter).toList();

    // _recentList เรียงใหม่→เก่าแล้ว — Map คงลำดับที่ใส่ เดือนจึงเรียงใหม่→เก่าตาม
    final byMonth = <DateTime, List<VehicleExpense>>{};
    for (final e in visible) {
      byMonth.putIfAbsent(DateTime(e.date.year, e.date.month), () => []).add(e);
    }

    final money = intl.NumberFormat('#,##0');
    final muted = theme.colorScheme.onSurface.withOpacity(0.6);

    return [
      Wrap(
        spacing: 8,
        runSpacing: 4,
        children: [
          for (final g in _HistoryGroup.values)
            ChoiceChip(
              label: Text('${_groupLabel(g)} (${counts[g]})'),
              selected: _historyFilter == g,
              onSelected: (_) => setState(() => _historyFilter = g),
              visualDensity: VisualDensity.compact,
            ),
        ],
      ),
      const SizedBox(height: 12),
      if (visible.isEmpty)
        Padding(
          padding: const EdgeInsets.symmetric(vertical: 24.0),
          child: Center(
            child: Text(
              _recentList.isEmpty
                  ? 'vehicle_expense_no_records'.tr()
                  : 'vehicle_expense_no_records_filtered'.tr(),
              style: theme.textTheme.bodyMedium?.copyWith(color: muted),
            ),
          ),
        ),
      for (final entry in byMonth.entries) ...[
        Padding(
          padding: const EdgeInsets.fromLTRB(4, 8, 4, 6),
          child: Row(
            children: [
              Expanded(
                child: Text(
                  _monthLabel(context, entry.key),
                  style: theme.textTheme.titleSmall?.copyWith(
                    fontWeight: FontWeight.bold,
                  ),
                ),
              ),
              Text(
                'vehicle_expense_month_total'.tr(
                  namedArgs: {
                    // ไม่นับรายการที่ถูกปฏิเสธ — ไม่ใช่ค่าใช้จ่ายจริง
                    'amount': money.format(
                      entry.value
                          .where((e) => e.status.toUpperCase() != 'REJECTED')
                          .fold<double>(0, (sum, e) => sum + e.amount),
                    ),
                  },
                ),
                style: theme.textTheme.bodySmall?.copyWith(
                  color: muted,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ],
          ),
        ),
        for (final e in entry.value) _buildRecordTile(context, e),
      ],
    ];
  }

  /// การ์ดปุ่มสี่เหลี่ยมสำหรับวางคู่ในแถว (Row) — ไอคอนมุมบน ข้อความจัดกลาง
  Widget _buildSquareActionCard(
    BuildContext context, {
    required IconData icon,
    required Color iconColor,
    required String title,
    required String subtitle,
    required VoidCallback onTap,
  }) {
    final cardColor =
        Theme.of(context).cardTheme.color ?? Theme.of(context).cardColor;
    return Material(
      color: cardColor,
      borderRadius: BorderRadius.circular(12),
      clipBehavior: Clip.antiAlias,
      elevation: 1,
      shadowColor: Theme.of(context).colorScheme.shadow.withOpacity(0.12),
      child: InkWell(
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 14),
          child: Column(
            mainAxisAlignment: MainAxisAlignment.center,
            crossAxisAlignment: CrossAxisAlignment.center,
            mainAxisSize: MainAxisSize.min,
            children: [
              CircleAvatar(
                backgroundColor: iconColor.withOpacity(0.15),
                radius: 24,
                child: Icon(icon, color: iconColor, size: 26),
              ),
              const SizedBox(height: 10),
              Text(
                title,
                textAlign: TextAlign.center,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                style: Theme.of(context).textTheme.titleSmall?.copyWith(
                      fontWeight: FontWeight.w600,
                    ),
              ),
              const SizedBox(height: 4),
              Text(
                subtitle,
                textAlign: TextAlign.center,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                style: Theme.of(context).textTheme.bodySmall?.copyWith(
                      color: Theme.of(context)
                          .colorScheme
                          .onSurface
                          .withOpacity(0.6),
                      height: 1.25,
                    ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  bool _isPendingLocally(VehicleExpense e) =>
      e.id != null && e.id!.startsWith('pending_');

  Widget _buildStatusBadge(BuildContext context, String status) {
    IconData icon;
    Color color;
    String label;

    switch (status.toUpperCase()) {
      case 'APPROVED':
        icon = Icons.check_circle;
        color = Colors.green;
        label = 'approved'.tr();
        break;
      case 'REJECTED':
        icon = Icons.cancel;
        color = Colors.red;
        label = 'rejected'.tr();
        break;
      case 'PENDING':
      default:
        icon = Icons.hourglass_empty;
        color = Colors.orange;
        label = 'pending_approval'.tr();
        break;
    }

    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      decoration: BoxDecoration(
        color: color.withOpacity(0.1),
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: color.withOpacity(0.5)),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 12, color: color),
          const SizedBox(width: 4),
          Text(
            label,
            style: TextStyle(
              color: color,
              fontSize: 10,
              fontWeight: FontWeight.bold,
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildRecordTile(BuildContext context, VehicleExpense e) {
    // หัวเดือนบอกเดือน/ปีแล้ว — แสดงแค่วันและเวลา
    final dateStr = intl.DateFormat('dd/MM HH:mm').format(e.date);
    final typeLabel = e.isFuel
        ? 'vehicle_expense_refuel'.tr()
        : _categoryLabel(e.category);
    final description = e.description?.trim() ?? '';
    final groupColor = _groupColor(_groupOf(e));
    final isLocalPending = _isPendingLocally(e);

    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      child: InkWell(
        onTap: () async {
          if (e.status == 'REJECTED') {
            if (e.type == VehicleExpenseType.fuel) {
              final result = await Navigator.of(context).push<bool>(
                MaterialPageRoute(
                  builder: (_) => RefuelFormPage(
                    initialData: e,
                    pastDriverIds: driverIdentityKeys(_driverData),
                  ),
                ),
              );
              if (result == true) _loadRecent();
            } else {
              final result = await Navigator.of(context).push<bool>(
                MaterialPageRoute(
                  builder: (_) => OtherExpenseFormPage(initialData: e),
                ),
              );
              if (result == true) _loadRecent();
            }
          }
        },
        child: Padding(
          padding: const EdgeInsets.symmetric(vertical: 4.0),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              ListTile(
                leading: CircleAvatar(
                  backgroundColor: groupColor.withOpacity(0.15),
                  child: Icon(_expenseIcon(e), color: groupColor, size: 22),
                ),
                title: Text(typeLabel),
                subtitle: Padding(
                  padding: const EdgeInsets.only(top: 4),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      if (description.isNotEmpty)
                        Padding(
                          padding: const EdgeInsets.only(bottom: 4),
                          child: Text(
                            description,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            style: Theme.of(context).textTheme.bodySmall,
                          ),
                        ),
                      Wrap(
                        spacing: 8,
                        runSpacing: 4,
                        crossAxisAlignment: WrapCrossAlignment.center,
                        children: [
                          if (isLocalPending)
                            Text(
                              'vehicle_expense_syncing'.tr(),
                              style: Theme.of(context)
                                  .textTheme
                                  .labelSmall
                                  ?.copyWith(
                                    color: Colors.grey,
                                    fontWeight: FontWeight.w600,
                                  ),
                            )
                          else
                            _buildStatusBadge(context, e.status),
                          Text(
                            dateStr,
                            style: Theme.of(context)
                                .textTheme
                                .bodySmall
                                ?.copyWith(
                                  color: Theme.of(context)
                                      .colorScheme
                                      .onSurface
                                      .withOpacity(0.75),
                                ),
                          ),
                        ],
                      ),
                    ],
                  ),
                ),
                trailing: Text(
                  '฿${intl.NumberFormat('#,##0').format(e.amount)}',
                  style: Theme.of(context).textTheme.titleMedium?.copyWith(
                    fontWeight: FontWeight.w600,
                    color: Theme.of(context).colorScheme.primary,
                  ),
                ),
              ),
              if (e.status == 'REJECTED' &&
                  e.adminNote != null &&
                  e.adminNote!.isNotEmpty)
                Padding(
                  padding: const EdgeInsets.only(
                    left: 72,
                    right: 16,
                    bottom: 12,
                  ),
                  child: Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      const Icon(
                        Icons.info_outline,
                        size: 14,
                        color: Colors.red,
                      ),
                      const SizedBox(width: 4),
                      Expanded(
                        child: Text(
                          '${'admin_note'.tr()}: ${e.adminNote}',
                          style: TextStyle(
                            color: Colors.red.shade700,
                            fontSize: 12,
                            fontStyle: FontStyle.italic,
                          ),
                        ),
                      ),
                    ],
                  ),
                ),
            ],
          ),
        ),
      ),
    );
  }

  IconData _expenseIcon(VehicleExpense e) {
    if (e.isFuel) return Icons.local_gas_station;
    switch (e.category) {
      case OtherExpenseCategory.maintenance:
        return Icons.build;
      case OtherExpenseCategory.tireRepair:
        return Icons.tire_repair;
      case OtherExpenseCategory.toll:
        return Icons.toll;
      case OtherExpenseCategory.parking:
        return Icons.local_parking;
      case OtherExpenseCategory.other:
      case null:
        return Icons.receipt_long;
    }
  }

  String _categoryLabel(OtherExpenseCategory? c) {
    if (c == null) return 'vehicle_expense_other'.tr();
    switch (c) {
      case OtherExpenseCategory.tireRepair:
        return 'vehicle_expense_category_tire'.tr();
      case OtherExpenseCategory.maintenance:
        return 'vehicle_expense_category_maintenance'.tr();
      case OtherExpenseCategory.toll:
        return 'vehicle_expense_category_toll'.tr();
      case OtherExpenseCategory.parking:
        return 'vehicle_expense_category_parking'.tr();
      case OtherExpenseCategory.other:
        return 'vehicle_expense_category_other'.tr();
    }
  }
}
