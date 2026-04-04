import 'package:easy_localization/easy_localization.dart';
import 'package:firebase_auth/firebase_auth.dart';
import 'package:flutter/material.dart';
import 'package:intl/intl.dart' as intl;
import '../../../../core/route_observer.dart';
import '../../data/models/vehicle_expense.dart';
import '../../data/repositories/vehicle_expense_repository.dart';
import 'refuel_form_page.dart';
import 'other_expense_form_page.dart';
import '../../../home/data/repositories/driver_repository.dart'; // 🚗 ดึงข้อมูลรถ
import '../../data/repositories/maintenance_repository.dart'; // 🛠️ ฟังก์ชันดึง PM
import 'maintenance_action_page.dart'; // 🛠️ หน้าฟอร์มกดแนบเอกสารซ่อม
import '../utils/maintenance_i18n.dart';

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
  final ScrollController _historyScrollController = ScrollController();
  
  Map<String, dynamic>? _driverData; // 🚗 ข้อมูลพขร.
  final _driverRepository = DriverRepository(); // 🚗 เพื่อดึง info รถ

  /// รถที่พขร. ขับ — Firestore เก็บที่ currentAssignment.truckId (ไม่ใช่ top-level truckId)
  String _truckIdForMaintenance(Map<String, dynamic>? driver) {
    if (driver == null) return '';
    final top = driver['truckId'] as String?;
    if (top != null && top.isNotEmpty) return top;
    final ca = driver['currentAssignment'];
    if (ca is Map) {
      final tid = ca['truckId'] as String?;
      if (tid != null && tid.isNotEmpty) return tid;
    }
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
    _historyScrollController.dispose();
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
      final fromServer = await getVehicleExpensesByDriver(uid, limit: 20);
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

  Future<void> _openRefuelForm() async {
    final result = await Navigator.of(
      context,
    ).push<bool>(MaterialPageRoute(builder: (_) => const RefuelFormPage()));
    if (result == true) _loadRecent();
  }

  Future<void> _openOtherExpenseForm() async {
    final result = await Navigator.of(context).push<bool>(
      MaterialPageRoute(builder: (_) => const OtherExpenseFormPage()),
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
                    padding: const EdgeInsets.all(16.0),
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
                        // 🛠️ แผงจัดการซ่อมบำรุง (Maintenance CTA)
                        StreamBuilder<List<Map<String, dynamic>>>(
                          stream: MaintenanceRepository().streamActiveMaintenance(
                            _truckIdForMaintenance(_driverData),
                          ),
                          builder: (context, snap) {
                            final tasks = snap.data ?? [];
                            if (tasks.isEmpty) return const SizedBox.shrink();
                            final task = tasks.first;
                            final status = task['status'] as String? ?? '';
                            final serviceLabel = trMaintenanceServiceType(
                              task['serviceType'] as String?,
                            );

                            return Card(
                              color: Colors.amber.shade50,
                              elevation: 2,
                              margin: const EdgeInsets.only(bottom: 16),
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
                                    style: const TextStyle(fontWeight: FontWeight.bold, fontSize: 16, color: Colors.black87)
                                  ),
                                  subtitle: Padding(
                                    padding: const EdgeInsets.only(top: 4),
                                    child: Text(
                                      'maintenance_vehicle_status_line'.tr(
                                        namedArgs: {
                                          'status': trMaintenanceStatus(status),
                                        },
                                      ),
                                      style: TextStyle(color: Colors.amber.shade900, fontWeight: FontWeight.bold)
                                    ),
                                  ),
                                  trailing: ElevatedButton(
                                    style: ElevatedButton.styleFrom(
                                      backgroundColor: Colors.amber.shade700,
                                      foregroundColor: Colors.white,
                                      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
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
                                    child: Text('maintenance_vehicle_card_cta'.tr()),
                                  ),
                                ),
                              ),
                            );
                          },
                        ),
                        const SizedBox(height: 8),
                        _buildActionCard(
                          context,
                          icon: Icons.local_gas_station,
                          iconColor: Colors.blue,
                          title: 'vehicle_expense_refuel'.tr(),
                          subtitle: 'vehicle_expense_refuel_hint'.tr(),
                          onTap: _openRefuelForm,
                        ),
                        const SizedBox(height: 12),
                        _buildActionCard(
                          context,
                          icon: Icons.receipt_long,
                          iconColor: Colors.orange,
                          title: 'vehicle_expense_other'.tr(),
                          subtitle: 'vehicle_expense_other_hint'.tr(),
                          onTap: _openOtherExpenseForm,
                        ),
                        const SizedBox(height: 28),
                        Text(
                          'vehicle_expense_recent'.tr(),
                          style: Theme.of(context).textTheme.titleMedium
                              ?.copyWith(fontWeight: FontWeight.bold),
                        ),
                        const SizedBox(height: 12),
                        Container(
                          constraints: BoxConstraints(
                            maxHeight:
                                MediaQuery.of(context).size.height * 0.45,
                          ),
                          decoration: BoxDecoration(
                            color: Theme.of(
                              context,
                            ).colorScheme.surfaceContainerLowest,
                            borderRadius: BorderRadius.circular(12),
                            border: Border.all(
                              color: Theme.of(context).dividerColor,
                            ),
                            boxShadow: [
                              BoxShadow(
                                color: Theme.of(
                                  context,
                                ).colorScheme.shadow.withOpacity(0.06),
                                blurRadius: 8,
                                offset: const Offset(0, 2),
                              ),
                            ],
                          ),
                          clipBehavior: Clip.antiAlias,
                          child: Scrollbar(
                            controller: _historyScrollController,
                            thumbVisibility: true,
                            thickness: 6,
                            radius: const Radius.circular(3),
                            child: _recentList.isEmpty
                                ? Padding(
                                    padding: const EdgeInsets.symmetric(
                                      vertical: 24.0,
                                    ),
                                    child: Center(
                                      child: Text(
                                        'vehicle_expense_no_records'.tr(),
                                        style: Theme.of(context)
                                            .textTheme
                                            .bodyMedium
                                            ?.copyWith(
                                              color: Theme.of(context)
                                                  .colorScheme
                                                  .onSurface
                                                  .withOpacity(0.6),
                                            ),
                                      ),
                                    ),
                                  )
                                : ListView.builder(
                                    controller: _historyScrollController,
                                    padding: const EdgeInsets.fromLTRB(
                                      12,
                                      8,
                                      8,
                                      12,
                                    ),
                                    physics: const BouncingScrollPhysics(
                                      parent: AlwaysScrollableScrollPhysics(),
                                    ),
                                    itemCount: _recentList.length,
                                    itemBuilder: (context, index) =>
                                        _buildRecordTile(
                                          context,
                                          _recentList[index],
                                        ),
                                  ),
                          ),
                        ),
                      ],
                    ),
                  ),
                ],
              ),
            ),
    );
  }

  Widget _buildActionCard(
    BuildContext context, {
    required IconData icon,
    required Color iconColor,
    required String title,
    required String subtitle,
    required VoidCallback onTap,
  }) {
    return Material(
      color: Theme.of(context).cardTheme.color ?? Theme.of(context).cardColor,
      borderRadius: BorderRadius.circular(12),
      clipBehavior: Clip.antiAlias,
      child: InkWell(
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.all(16.0),
          child: Row(
            children: [
              CircleAvatar(
                backgroundColor: iconColor.withOpacity(0.15),
                radius: 28,
                child: Icon(icon, color: iconColor, size: 28),
              ),
              const SizedBox(width: 16),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      title,
                      style: Theme.of(context).textTheme.titleMedium?.copyWith(
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                    const SizedBox(height: 4),
                    Text(
                      subtitle,
                      style: Theme.of(context).textTheme.bodySmall?.copyWith(
                        color: Theme.of(
                          context,
                        ).colorScheme.onSurface.withOpacity(0.6),
                      ),
                    ),
                  ],
                ),
              ),
              Icon(
                Icons.chevron_right,
                color: Theme.of(context).colorScheme.onSurface.withOpacity(0.5),
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
    final dateStr = intl.DateFormat('dd/MM/yyyy HH:mm').format(e.date);
    final typeLabel = e.isFuel
        ? 'vehicle_expense_refuel'.tr()
        : _categoryLabel(e.category);
    final isLocalPending = _isPendingLocally(e);

    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      child: InkWell(
        onTap: () async {
          if (e.status == 'REJECTED') {
            if (e.type == VehicleExpenseType.fuel) {
              final result = await Navigator.of(context).push<bool>(
                MaterialPageRoute(
                  builder: (_) => RefuelFormPage(initialData: e),
                ),
              );
              if (result == true) _loadRecent();
            } else {
              // TODO: Implement Edit form for rejected other items (if needed)
              ScaffoldMessenger.of(context).showSnackBar(
                const SnackBar(content: Text('Edit feature coming soon...')),
              );
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
                  backgroundColor: e.isFuel
                      ? Colors.blue.withOpacity(0.15)
                      : Colors.orange.withOpacity(0.15),
                  child: Icon(
                    e.isFuel ? Icons.local_gas_station : Icons.receipt_long,
                    color: e.isFuel ? Colors.blue : Colors.orange,
                    size: 22,
                  ),
                ),
                title: Text(typeLabel),
                subtitle: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    const SizedBox(height: 4),
                    isLocalPending
                        ? Text(
                            'vehicle_expense_syncing'.tr(),
                            style: Theme.of(context).textTheme.labelSmall
                                ?.copyWith(
                                  color: Colors.grey,
                                  fontWeight: FontWeight.w600,
                                ),
                          )
                        : _buildStatusBadge(context, e.status),
                    const SizedBox(height: 4),
                    Text(dateStr),
                  ],
                ),
                trailing: Text(
                  '฿${e.amount.toStringAsFixed(0)}',
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
