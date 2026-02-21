import 'package:flutter/material.dart';
import 'package:easy_localization/easy_localization.dart';
import 'home_page.dart';
import '../../../loading_phase/presentation/pages/loading_phase_page.dart';
import 'main_layout_scope.dart';
import '../../../delivery_phase/presentation/pages/delivery_phase_page.dart';

class MainLayout extends StatefulWidget {
  const MainLayout({
    super.key,
    this.initialTabIndex,
    this.initialTripSummary,
  });

  final int? initialTabIndex;
  final SavedTripSummary? initialTripSummary;

  @override
  State<MainLayout> createState() => _MainLayoutState();
}

class _MainLayoutState extends State<MainLayout> {
  late int _currentIndex;

  /// สรุปเที่ยวที่เพิ่ง save จาก Loading (แสดงบน Delivery)
  SavedTripSummary? _savedTripSummary;

  @override
  void initState() {
    super.initState();
    _currentIndex = widget.initialTabIndex ?? 0;
    _savedTripSummary = widget.initialTripSummary;
  }

  @override
  void didUpdateWidget(MainLayout oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (widget.initialTabIndex != null && widget.initialTabIndex != oldWidget.initialTabIndex) {
      _currentIndex = widget.initialTabIndex!;
    }
    if (widget.initialTripSummary != null) {
      _savedTripSummary = widget.initialTripSummary;
    }
  }

  void _goToDeliveryTab(SavedTripSummary? summary) {
    setState(() {
      _currentIndex = 2;
      if (summary != null) _savedTripSummary = summary;
    });
  }

  /// งานที่รับยังไม่ส่ง → ต้องส่งก่อน จึงจะกด Pick up รับงานใหม่ได้
  bool get _hasActiveDelivery => _savedTripSummary != null;

  /// Disable Pick up เมื่อมีงานที่รับแล้วยังไม่ส่ง
  bool get _isPickupDisabled => _hasActiveDelivery;

  /// Disable Delivery เมื่อยังไม่มีการรับงาน
  bool get _isDeliveryDisabled => !_hasActiveDelivery;

  void _onDeliveryCompleted() {
    setState(() => _savedTripSummary = null);
  }

  List<Widget> get _screens => [
    const HomePage(),
    const LoadingPhasePage(),
    DeliveryPhasePage(savedTripSummary: _savedTripSummary),
  ];

  void _onItemTapped(int index) {
    if (index == 3) {
      _showVehicleBottomSheet(context);
      return;
    }
    if (index == 1 && _isPickupDisabled) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('nav_pickup_disabled_hint'.tr())),
      );
      return;
    }
    if (index == 2 && _isDeliveryDisabled) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('nav_delivery_disabled_hint'.tr())),
      );
      return;
    }
    setState(() => _currentIndex = index);
  }

  void _showVehicleBottomSheet(BuildContext context) {
    showModalBottomSheet(
      context: context,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (ctx) {
        return SafeArea(
          child: Padding(
            padding: const EdgeInsets.symmetric(
              vertical: 24.0,
              horizontal: 16.0,
            ),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Text(
                  'nav_vehicle'.tr(),
                  style: Theme.of(
                    context,
                  ).textTheme.titleLarge?.copyWith(fontWeight: FontWeight.bold),
                  textAlign: TextAlign.center,
                ),
                const SizedBox(height: 24),
                ListTile(
                  leading: CircleAvatar(
                    backgroundColor: Colors.blue.withOpacity(0.1),
                    child: const Icon(
                      Icons.local_gas_station,
                      color: Colors.blue,
                    ),
                  ),
                  title: Text('vehicle_refuel'.tr()),
                  onTap: () {
                    Navigator.pop(ctx);
                    // Handle refuel action
                  },
                ),
                ListTile(
                  leading: CircleAvatar(
                    backgroundColor: Colors.orange.withOpacity(0.1),
                    child: const Icon(Icons.tire_repair, color: Colors.orange),
                  ),
                  title: Text('vehicle_tire_repair'.tr()),
                  onTap: () {
                    Navigator.pop(ctx);
                    // Handle tire repair action
                  },
                ),
                ListTile(
                  leading: CircleAvatar(
                    backgroundColor: Colors.grey.withOpacity(0.1),
                    child: const Icon(Icons.car_repair, color: Colors.grey),
                  ),
                  title: Text('vehicle_other'.tr()),
                  onTap: () {
                    Navigator.pop(ctx);
                    // Handle other car related action
                  },
                ),
              ],
            ),
          ),
        );
      },
    );
  }

  @override
  Widget build(BuildContext context) {
    return MainLayoutScope(
      goToDeliveryTab: _goToDeliveryTab,
      onDeliveryCompleted: _onDeliveryCompleted,
      child: Scaffold(
        body: IndexedStack(index: _currentIndex, children: _screens),
      bottomNavigationBar: BottomNavigationBar(
        currentIndex: _currentIndex,
        onTap: _onItemTapped,
        type: BottomNavigationBarType.fixed,
        selectedItemColor: Theme.of(context).colorScheme.primary,
        unselectedItemColor: Colors.grey,
        showUnselectedLabels: true,
        items: [
          BottomNavigationBarItem(
            icon: const Icon(Icons.home),
            label: 'nav_home'.tr(),
          ),
          BottomNavigationBarItem(
            icon: Icon(
              Icons.local_shipping,
              color: _isPickupDisabled ? Colors.grey : null,
            ),
            label: 'nav_pickup'.tr(),
          ),
          BottomNavigationBarItem(
            icon: Badge(
              isLabelVisible: _hasActiveDelivery,
              backgroundColor: Colors.red,
              child: Icon(
                Icons.inventory,
                color: _isDeliveryDisabled ? Colors.grey : null,
              ),
            ),
            label: 'nav_delivery'.tr(),
          ),
          BottomNavigationBarItem(
            icon: const Icon(Icons.directions_car),
            label: 'nav_vehicle'.tr(),
          ),
        ],
      ),
    ),
  );
  }
}
