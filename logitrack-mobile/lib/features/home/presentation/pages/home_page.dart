import 'package:easy_localization/easy_localization.dart';
import 'package:firebase_messaging/firebase_messaging.dart';
import 'package:flutter/material.dart';
import 'package:package_info_plus/package_info_plus.dart';
import '../../../../core/theme/theme_controller.dart';
import '../../../../core/services/fcm_service.dart';
import '../../../../features/auth/data/repositories/auth_repository.dart';
import '../../data/repositories/driver_repository.dart';
import '../../data/repositories/task_repository.dart';
import '../../../../components/quick_action_card.dart';
import '../../../broadcast/data/repositories/broadcast_repository.dart';
import '../../../broadcast/data/services/broadcast_read_tracker.dart';

class HomePage extends StatefulWidget {
  const HomePage({super.key});

  @override
  State<HomePage> createState() => _HomePageState();
}

class _HomePageState extends State<HomePage> {
  final _driverRepository = DriverRepository();
  final _authRepository = AuthRepository();

  Map<String, dynamic>? _driverData;
  String? _driverId;
  bool _isLoading = true;
  String? _error;
  int? _lastReadBroadcastMs;

  /// Shown in the drawer so a driver can read their app version without logging out —
  /// the login screen used to be the only place it appeared. Matters when support asks
  /// "which version are you on?" during a rollout.
  String _appVersion = '';

  @override
  void initState() {
    super.initState();
    _fetchDriverData();
    _setupFcmForeground();
    _loadLastReadBroadcast();
    _loadAppVersion();
  }

  Future<void> _loadAppVersion() async {
    try {
      final info = await PackageInfo.fromPlatform();
      // Tag non-prod builds: a dev APK loose on a real driver's phone talks to the dev project,
      // and this is the fastest way for support to spot it.
      const flavor = String.fromEnvironment('FLAVOR', defaultValue: 'dev');
      final suffix = flavor == 'prod' ? '' : ' · ${flavor.toUpperCase()}';
      if (mounted) {
        setState(
          () => _appVersion = 'v${info.version} (${info.buildNumber})$suffix',
        );
      }
    } catch (_) {
      // Version is a nice-to-have caption; never let it break the drawer.
    }
  }

  Future<void> _loadLastReadBroadcast() async {
    final ms = await getLastReadBroadcastMs();
    if (mounted) setState(() => _lastReadBroadcastMs = ms);
  }

  void _setupFcmForeground() {
    FirebaseMessaging.onMessage.listen((RemoteMessage message) {
      if (message.notification != null && mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(message.notification!.body ?? 'New task assigned'),
            action: SnackBarAction(label: 'OK', onPressed: () {}),
          ),
        );
      }
    });
  }

  Future<void> _fetchDriverData() async {
    try {
      final user = _authRepository.currentUser;
      if (user != null) {
        final data = await _driverRepository.getCurrentDriver(user.uid);
        if (mounted) {
          setState(() {
            _driverData = data;
            _driverId = data?['id'] as String?;
            _isLoading = false;
          });
          if (data != null && data['id'] != null) {
            initFcmAndSaveToken(data['id'] as String);
          }
          saveFcmTokenToUser(user.uid);
        }
      } else {
        if (mounted) {
          setState(() {
            _error = "user_not_authenticated".tr();
            _isLoading = false;
          });
        }
      }
    } catch (e) {
      if (mounted) {
        setState(() {
          _error = e.toString();
          _isLoading = false;
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_isLoading) {
      return const Scaffold(body: Center(child: CircularProgressIndicator()));
    }

    if (_error != null) {
      return Scaffold(body: Center(child: Text("Error: $_error")));
    }

    final firstName = _driverData?['firstName'] ?? 'Driver';
    final lastName = _driverData?['lastName'] ?? '';
    final displayName = '$firstName $lastName'.trim();

    return Scaffold(
      drawer: Drawer(
        child: ListView(
          padding: EdgeInsets.zero,
          children: [
            DrawerHeader(
              decoration: BoxDecoration(
                color: Theme.of(context).colorScheme.primary,
              ),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                mainAxisAlignment: MainAxisAlignment.end,
                children: [
                  CircleAvatar(
                    radius: 28,
                    backgroundColor: Colors.white.withOpacity(0.3),
                    child: Text(
                      displayName.isNotEmpty
                          ? displayName[0].toUpperCase()
                          : 'D',
                      style: const TextStyle(
                        fontSize: 28,
                        fontWeight: FontWeight.bold,
                        color: Colors.white,
                      ),
                    ),
                  ),
                  const SizedBox(height: 12),
                  Text(
                    displayName,
                    style: const TextStyle(
                      color: Colors.white,
                      fontSize: 18,
                      fontWeight: FontWeight.bold,
                    ),
                  ),
                  Text(
                    _authRepository.currentUser?.email ?? '',
                    style: TextStyle(
                      color: Colors.white.withOpacity(0.9),
                      fontSize: 14,
                    ),
                  ),
                ],
              ),
            ),
            ListTile(
              leading: const Icon(Icons.person_outline),
              title: Text('driver_profile'.tr()),
              onTap: () {
                Navigator.pop(context);
                Navigator.pushNamed(context, '/profile');
              },
            ),
            ListTile(
              leading: const Icon(Icons.map_outlined),
              title: Text('menu_hubs_reference'.tr()),
              onTap: () {
                Navigator.pop(context);
                Navigator.pushNamed(context, '/hubs-reference');
              },
            ),
            ListTile(
              leading: const Icon(Icons.calendar_month_outlined),
              title: Text('menu_working_holiday_calendar'.tr()),
              onTap: () {
                Navigator.pop(context);
                Navigator.pushNamed(context, '/working-holiday-calendar');
              },
            ),
            ListTile(
              leading: const Icon(Icons.event_busy_outlined),
              title: Text('menu_leave_request'.tr()),
              onTap: () {
                Navigator.pop(context);
                Navigator.pushNamed(
                  context,
                  '/leave-request',
                  arguments: {'driverId': _driverId, 'driverName': displayName},
                );
              },
            ),
            ListTile(
              leading: const Icon(Icons.language),
              title: Text('language'.tr()),
              subtitle: Text(
                context.locale.languageCode == 'th'
                    ? 'language_th'.tr()
                    : 'language_en'.tr(),
              ),
              onTap: () {
                Navigator.pop(context);
                _showLanguageSheet(context);
              },
            ),
            const Divider(),
            ListTile(
              leading: const Icon(Icons.logout),
              title: Text('logout'.tr()),
              onTap: () async {
                Navigator.pop(context);
                await _authRepository.signOut();
                if (context.mounted) {
                  Navigator.pushReplacementNamed(context, '/');
                }
              },
            ),
            // App version caption. Lives here rather than in the DrawerHeader because that header
            // is a fixed 160dp and a fourth line overflows once the system font scale is raised.
            if (_appVersion.isNotEmpty)
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 8, 16, 16),
                child: Text(
                  _appVersion,
                  textAlign: TextAlign.center,
                  style: Theme.of(context).textTheme.bodySmall?.copyWith(
                    color: Theme.of(context).hintColor,
                  ),
                ),
              ),
          ],
        ),
      ),
      appBar: AppBar(
        title: Text('app_title'.tr()),
        leading: Builder(
          builder: (context) => IconButton(
            icon: const Icon(Icons.menu),
            onPressed: () => Scaffold.of(context).openDrawer(),
          ),
        ),
        actions: [
          Tooltip(
            message: 'theme_toggle_tooltip'.tr(),
            child: IconButton(
              icon: const Icon(Icons.lightbulb_outline),
              onPressed: () => ThemeController().toggleTheme(),
            ),
          ),
        ],
      ),
      body: RefreshIndicator(
        onRefresh: () async {
          await _fetchDriverData();
        },
        child: CustomScrollView(
          physics: const AlwaysScrollableScrollPhysics(),
          slivers: [
            SliverPadding(
              padding: const EdgeInsets.all(16.0),
              sliver: SliverToBoxAdapter(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      'welcome_user'.tr(args: [displayName]),
                      style: Theme.of(context).textTheme.headlineSmall,
                    ),
                    const SizedBox(height: 16),
                    StreamBuilder<int?>(
                      stream: BroadcastRepository()
                          .watchLatestBroadcastSentAtMs(),
                      builder: (context, latestSnap) {
                        final latestMs = latestSnap.data;
                        final hasNew =
                            latestMs != null &&
                            latestMs > (_lastReadBroadcastMs ?? 0);
                        return InkWell(
                          onTap: () async {
                            await Navigator.pushNamed(context, '/broadcast');
                            if (!mounted) return;
                            await _loadLastReadBroadcast();
                          },
                          borderRadius: BorderRadius.circular(12),
                          child: Container(
                            padding: const EdgeInsets.symmetric(
                              horizontal: 16,
                              vertical: 12,
                            ),
                            decoration: BoxDecoration(
                              color: Theme.of(
                                context,
                              ).colorScheme.primaryContainer.withOpacity(0.5),
                              borderRadius: BorderRadius.circular(12),
                              border: Border.all(
                                color: Theme.of(
                                  context,
                                ).colorScheme.primary.withOpacity(0.3),
                              ),
                            ),
                            child: Row(
                              children: [
                                Stack(
                                  clipBehavior: Clip.none,
                                  children: [
                                    Icon(
                                      Icons.campaign_outlined,
                                      color: Theme.of(
                                        context,
                                      ).colorScheme.primary,
                                      size: 28,
                                    ),
                                    if (hasNew)
                                      Positioned(
                                        right: -4,
                                        top: -4,
                                        child: Container(
                                          width: 10,
                                          height: 10,
                                          decoration: const BoxDecoration(
                                            color: Colors.red,
                                            shape: BoxShape.circle,
                                          ),
                                        ),
                                      ),
                                  ],
                                ),
                                const SizedBox(width: 12),
                                Expanded(
                                  child: Text(
                                    'menu_broadcast'.tr(),
                                    style: Theme.of(context)
                                        .textTheme
                                        .titleMedium
                                        ?.copyWith(fontWeight: FontWeight.w600),
                                  ),
                                ),
                                if (hasNew)
                                  Padding(
                                    padding: const EdgeInsets.only(right: 8),
                                    child: Text(
                                      'broadcast_new'.tr(),
                                      style: TextStyle(
                                        fontSize: 12,
                                        color: Theme.of(
                                          context,
                                        ).colorScheme.error,
                                        fontWeight: FontWeight.w600,
                                      ),
                                    ),
                                  ),
                                Icon(
                                  Icons.chevron_right,
                                  color: Theme.of(
                                    context,
                                  ).colorScheme.onSurfaceVariant,
                                ),
                              ],
                            ),
                          ),
                        );
                      },
                    ),
                    const SizedBox(height: 24),
                    StreamBuilder<List<Map<String, dynamic>>>(
                      stream: streamTasksForDriver(_driverId ?? ''),
                      builder: (context, snap) {
                        final tasks = snap.data ?? [];
                        final activeCount = tasks.where((t) {
                          final s = t['status'] as String?;
                          return s != 'Completed' && s != 'Cancelled';
                        }).length;
                        return _buildSummaryCard(
                          context,
                          taskCount: activeCount,
                          totalCount: tasks.length,
                        );
                      },
                    ),
                    const SizedBox(height: 24),
                    Text(
                      'quick_actions'.tr(),
                      style: Theme.of(context).textTheme.titleLarge,
                    ),
                    const SizedBox(height: 16),
                  ],
                ),
              ),
            ),
            SliverPadding(
              padding: const EdgeInsets.symmetric(horizontal: 16.0),
              sliver: SliverGrid.count(
                crossAxisCount: 2,
                mainAxisSpacing: 16.0,
                crossAxisSpacing: 16.0,
                childAspectRatio: 1.2,
                children: [
                  QuickActionCard(
                    icon: Icons.history,
                    label: 'history'.tr(),
                    onTap: () => Navigator.pushNamed(context, '/trip-history'),
                  ),
                ],
              ),
            ),
            const SliverPadding(padding: EdgeInsets.only(bottom: 32.0)),
          ],
        ),
      ),
    );
  }

  Widget _buildSummaryCard(
    BuildContext context, {
    int taskCount = 0,
    int totalCount = 0,
  }) {
    final hasTasks = totalCount > 0;
    return Card(
      elevation: 4,
      clipBehavior: Clip.antiAlias,
      child: InkWell(
        onTap: () async {
          final result = await Navigator.pushNamed(
            context,
            '/check-in',
            arguments: _driverId,
          );
          if (result == true && context.mounted) {
            Navigator.pushReplacementNamed(
              context,
              '/home',
              arguments: {'tab': 1},
            );
          }
        },
        child: Padding(
          padding: const EdgeInsets.all(16.0),
          child: Column(
            children: [
              Row(
                mainAxisAlignment: MainAxisAlignment.spaceBetween,
                children: [
                  Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        "${'todays_status'.tr()}, ${DateFormat('dd/MM/yyyy').format(DateTime.now())}",
                        style: Theme.of(context).textTheme.titleMedium,
                      ),
                      const SizedBox(height: 8),
                      Text(
                        hasTasks
                            ? (taskCount == 1 ? '1 task' : '$taskCount tasks')
                            : 'no_active_tasks'.tr(),
                        style: TextStyle(
                          color: hasTasks
                              ? Theme.of(context).colorScheme.primary
                              : Colors.grey,
                        ),
                      ),
                      const SizedBox(height: 4),
                      Text(
                        'todays_status_subtitle'.tr(),
                        style: Theme.of(context).textTheme.bodySmall?.copyWith(
                          color: Theme.of(
                            context,
                          ).colorScheme.onSurfaceVariant.withOpacity(0.8),
                        ),
                      ),
                    ],
                  ),
                  Icon(
                    Icons.assignment_turned_in,
                    size: 40,
                    color: hasTasks
                        ? Theme.of(context).colorScheme.primary
                        : Colors.green,
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }

  void _showLanguageSheet(BuildContext context) {
    showModalBottomSheet<void>(
      context: context,
      builder: (context) => SafeArea(
        child: Padding(
          padding: const EdgeInsets.symmetric(vertical: 16),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Text(
                'language'.tr(),
                style: Theme.of(
                  context,
                ).textTheme.titleMedium?.copyWith(fontWeight: FontWeight.w600),
              ),
              const SizedBox(height: 16),
              ListTile(
                title: Text('language_en'.tr()),
                trailing: context.locale.languageCode == 'en'
                    ? Icon(
                        Icons.radio_button_checked,
                        color: Theme.of(context).colorScheme.primary,
                      )
                    : const Icon(Icons.radio_button_off),
                onTap: () async {
                  await context.setLocale(const Locale('en'));
                  if (context.mounted) {
                    Navigator.pop(context);
                    setState(() {});
                  }
                },
              ),
              ListTile(
                title: Text('language_th'.tr()),
                trailing: context.locale.languageCode == 'th'
                    ? Icon(
                        Icons.radio_button_checked,
                        color: Theme.of(context).colorScheme.primary,
                      )
                    : const Icon(Icons.radio_button_off),
                onTap: () async {
                  await context.setLocale(const Locale('th'));
                  if (context.mounted) {
                    Navigator.pop(context);
                    setState(() {});
                  }
                },
              ),
            ],
          ),
        ),
      ),
    );
  }
}
