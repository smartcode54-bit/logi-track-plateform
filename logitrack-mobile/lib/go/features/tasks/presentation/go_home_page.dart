import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/material.dart';
import 'package:flutter_bloc/flutter_bloc.dart';

import '../../../../core/route_observer.dart';
import '../../../app/go_badge.dart';
import '../../../app/go_services.dart';
import '../../../app/go_unavailable_page.dart';
import '../../../core/error_text.dart';
import '../../../core/session/session_bloc.dart';
import '../../check_in/presentation/go_check_in_page.dart';
import '../../delivery/presentation/go_delivery_page.dart';
import '../../loading/presentation/go_loading_page.dart';
import '../../trips/domain/mobile_trip.dart';
import '../domain/driver_task.dart';
import '../domain/queue_gate.dart';
import 'task_list_bloc.dart';

/// The gotest home (spec mobile-go-core-loop R10, R11, R14, R16): the driver's queue and the next step of each job;
/// the tabs and menus not yet on the Go API show the GO-TEST notice.
class GoHomePage extends StatefulWidget {
  const GoHomePage({super.key, required this.services, required this.driverName});
  final GoServices services;
  final String driverName;

  @override
  State<GoHomePage> createState() => _GoHomePageState();
}

class _GoHomePageState extends State<GoHomePage> with WidgetsBindingObserver, RouteAware {
  late final TaskListBloc _bloc =
      TaskListBloc(tasks: widget.services.tasks, trips: widget.services.trips)..add(TaskListRefreshed());
  int _tab = 0;
  bool _routeVisible = true;
  bool _foreground = true;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _syncPolling();
    widget.services.heartbeat.pulse();
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final route = ModalRoute.of(context);
    if (route != null) routeObserver.subscribe(this, route);
  }

  @override
  void dispose() {
    routeObserver.unsubscribe(this);
    WidgetsBinding.instance.removeObserver(this);
    _bloc.close();
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    final fg = state == AppLifecycleState.resumed;
    if (fg && !_foreground) {
      _bloc.add(TaskListRefreshed());
      widget.services.heartbeat.pulse();
      widget.services.checkVersion(context);
    }
    _foreground = fg;
    _syncPolling();
  }

  // RouteAware: Home covered by another page (check-in, loading, ...) stops polling; back on top refreshes.
  @override
  void didPushNext() {
    _routeVisible = false;
    _syncPolling();
  }

  @override
  void didPopNext() {
    _routeVisible = true;
    _bloc.add(TaskListRefreshed());
    _syncPolling();
  }

  void _syncPolling() {
    final visible = _routeVisible && _foreground && _tab == 0;
    _bloc.add(visible ? TaskListPollingStarted() : TaskListPollingStopped());
  }

  Future<void> _open(Widget page) async {
    await Navigator.of(context).push(MaterialPageRoute<Object?>(builder: (_) => page));
    widget.services.heartbeat.pulse();
  }

  Future<void> _deliver(String tripNo) async {
    final trip = await widget.services.trips.byNumber(tripNo);
    if (!mounted) return;
    if (trip == null || !trip.inTransit) {
      _bloc.add(TaskListRefreshed());
      return;
    }
    await _open(GoDeliveryPage(trip: trip, uploads: widget.services.uploads, trips: widget.services.trips));
  }

  @override
  Widget build(BuildContext context) {
    final s = widget.services;
    return BlocProvider.value(
      value: _bloc,
      child: Scaffold(
        appBar: goAppBar(widget.driverName),
        drawer: _Drawer(versionLabel: s.versionLabel),
        body: _tab == 0 ? _TaskList(onCheckIn: _checkIn, onLoad: _load, onDeliver: _deliver, onResume: _resume) : const GoUnavailableNotice(),
        bottomNavigationBar: NavigationBar(
          selectedIndex: _tab,
          onDestinationSelected: (i) {
            setState(() => _tab = i);
            _syncPolling();
          },
          destinations: [
            NavigationDestination(icon: const Icon(Icons.home_outlined), label: 'nav_home'.tr()),
            NavigationDestination(icon: const Icon(Icons.local_shipping_outlined), label: 'nav_vehicle'.tr()),
            NavigationDestination(icon: const Icon(Icons.chat_bubble_outline), label: 'nav_chat'.tr()),
          ],
        ),
      ),
    );
  }

  void _checkIn(DriverTask t) => _open(GoCheckInPage(
        task: t,
        uploads: widget.services.uploads,
        trips: widget.services.trips,
        trucks: widget.services.trucks,
        installId: widget.services.installId,
      ));

  void _load(DriverTask t) =>
      _open(GoLoadingPage(task: t, uploads: widget.services.uploads, trips: widget.services.trips));

  void _resume(MobileTrip trip) =>
      _open(GoDeliveryPage(trip: trip, uploads: widget.services.uploads, trips: widget.services.trips));
}

class _TaskList extends StatelessWidget {
  const _TaskList({required this.onCheckIn, required this.onLoad, required this.onDeliver, required this.onResume});
  final void Function(DriverTask) onCheckIn;
  final void Function(DriverTask) onLoad;
  final void Function(String tripNo) onDeliver;
  final void Function(MobileTrip) onResume;

  @override
  Widget build(BuildContext context) {
    return BlocBuilder<TaskListBloc, TaskListState>(builder: (context, state) {
      final bloc = context.read<TaskListBloc>();
      final updated = state.updatedAt;
      return RefreshIndicator(
        onRefresh: () async => bloc.add(TaskListRefreshed()),
        child: ListView(padding: const EdgeInsets.all(12), children: [
          if (updated != null)
            Text('tasks_last_updated'.tr(namedArgs: {'time': DateFormat.Hm().format(updated)}),
                style: Theme.of(context).textTheme.bodySmall),
          if (state.error != null)
            Card(
              color: Theme.of(context).colorScheme.errorContainer,
              child: ListTile(
                title: Text(apiErrorText(state.error!)),
                trailing: TextButton(onPressed: () => bloc.add(TaskListRefreshed()), child: Text('retry'.tr())),
              ),
            ),
          if (state.pendingTrip != null)
            Card(
              child: ListTile(
                leading: const Icon(Icons.local_shipping),
                title: Text(state.pendingTrip!.tripNo),
                subtitle: Text('go_pending_trip'.tr()),
                trailing: FilledButton(
                    onPressed: () => onResume(state.pendingTrip!), child: Text('go_submit_delivery'.tr())),
              ),
            ),
          if (state.tasks.isEmpty && !state.loading && state.error == null)
            Padding(padding: const EdgeInsets.all(32), child: Center(child: Text('tasks_empty'.tr()))),
          if (state.loading && state.tasks.isEmpty) const Padding(padding: EdgeInsets.all(32), child: Center(child: CircularProgressIndicator())),
          for (final t in state.tasks)
            _TaskCard(
              task: t,
              eligible: isQueueEligibleForCheckIn(t, state.tasks),
              onCheckIn: () => onCheckIn(t),
              onLoad: () => onLoad(t),
              onDeliver: t.trip == null ? null : () => onDeliver(t.trip!.tripNo),
            ),
        ]),
      );
    });
  }
}

class _TaskCard extends StatelessWidget {
  const _TaskCard({
    required this.task,
    required this.eligible,
    required this.onCheckIn,
    required this.onLoad,
    required this.onDeliver,
  });

  final DriverTask task;
  final bool eligible;
  final VoidCallback onCheckIn;
  final VoidCallback onLoad;
  final VoidCallback? onDeliver;

  @override
  Widget build(BuildContext context) {
    Widget action;
    String? hint;
    if (task.isMultiDelivery && task.status != TaskStatus.pending && task.status != TaskStatus.assigned) {
      action = const SizedBox.shrink();
      hint = 'gotest_multi_drop_unavailable'.tr();
    } else {
      switch (task.status) {
        case TaskStatus.pending:
        case TaskStatus.assigned:
          action = FilledButton(onPressed: eligible ? onCheckIn : null, child: Text('check_in_confirm_job'.tr()));
          if (!eligible) hint = 'task_blocked_by_queue'.tr();
        case TaskStatus.checkedIn:
          action = task.isMultiDelivery
              ? const SizedBox.shrink()
              : FilledButton(onPressed: onLoad, child: Text('go_submit_loading'.tr()));
          if (task.isMultiDelivery) hint = 'gotest_multi_drop_unavailable'.tr();
        case TaskStatus.inTransit:
          action = FilledButton(onPressed: onDeliver, child: Text('go_submit_delivery'.tr()));
        case TaskStatus.completed:
        case TaskStatus.cancelled:
        case TaskStatus.unknown:
          action = const SizedBox.shrink();
      }
    }
    final plan = task.planAt == null ? '' : DateFormat('dd/MM HH:mm').format(task.planAt!);
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          Row(children: [
            Expanded(child: Text(task.taskNo, style: Theme.of(context).textTheme.titleMedium)),
            Chip(label: Text('go_status_${task.status.name}'.tr())),
          ]),
          Text('${task.source} → ${task.destination}'),
          if (plan.isNotEmpty) Text(plan, style: Theme.of(context).textTheme.bodySmall),
          if (task.licensePlate != null) Text('${task.licensePlate} · ${task.truckType ?? ''}'),
          if (hint != null)
            Padding(
              padding: const EdgeInsets.only(top: 6),
              child: Text(hint, style: TextStyle(color: Theme.of(context).colorScheme.error)),
            ),
          const SizedBox(height: 8),
          Align(alignment: Alignment.centerRight, child: action),
        ]),
      ),
    );
  }
}

class _Drawer extends StatelessWidget {
  const _Drawer({required this.versionLabel});
  final String versionLabel;

  static const _unavailable = [
    ('go_menu_profile', Icons.person_outline),
    ('go_menu_trip_history', Icons.history),
    ('go_menu_broadcast', Icons.campaign_outlined),
    ('go_menu_holiday', Icons.calendar_month_outlined),
    ('go_menu_leave', Icons.event_busy_outlined),
    ('go_menu_hubs', Icons.place_outlined),
    ('go_menu_vehicle_expense', Icons.receipt_long_outlined),
  ];

  @override
  Widget build(BuildContext context) {
    return Drawer(
      child: SafeArea(
        child: ListView(children: [
          ListTile(title: Row(children: [const Text('LogiTrack'), const GoBadge()]), subtitle: Text(versionLabel)),
          const Divider(),
          for (final (key, icon) in _unavailable)
            ListTile(
              leading: Icon(icon),
              title: Text(key.tr()),
              trailing: const Icon(Icons.lock_outline, size: 18),
              onTap: () => Navigator.of(context)
                  .push(MaterialPageRoute<void>(builder: (_) => GoUnavailablePage(titleKey: key))),
            ),
          const Divider(),
          ListTile(
            leading: const Icon(Icons.logout),
            title: Text('logout'.tr()),
            onTap: () => context.read<SessionBloc>().add(SessionLogoutRequested()),
          ),
        ]),
      ),
    );
  }
}
