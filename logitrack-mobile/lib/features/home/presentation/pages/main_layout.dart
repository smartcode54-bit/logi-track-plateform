import 'dart:async';
import 'dart:convert';
import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:firebase_auth/firebase_auth.dart';
import 'package:firebase_messaging/firebase_messaging.dart';
import 'package:flutter/material.dart';
import 'package:easy_localization/easy_localization.dart';
import 'package:shared_preferences/shared_preferences.dart';
import '../../../../main.dart' show pendingChatIdFromNotification, pendingBroadcastFromNotification, onChatRoomExited;
import '../../../../core/route_observer.dart';
import '../../../../core/services/mobile_app_version_service.dart';
import '../../../../core/services/mobile_client_heartbeat_service.dart';
import '../../../../core/services/notification_service.dart';
import '../../../chat/data/repositories/chat_repository.dart';
import '../../data/repositories/driver_repository.dart';
import '../../data/services/draft_storage_service.dart' show prefKeyPendingDeliverySummary, DraftStorageService;
import '../../data/repositories/task_repository.dart';
import '../../data/repositories/trip_records_repository.dart';
import 'home_page.dart';
import '../../../loading_phase/presentation/pages/loading_phase_page.dart';
import 'main_layout_scope.dart';
import '../../../delivery_phase/presentation/pages/delivery_phase_page.dart';
import '../../../delivery_phase/presentation/pages/delivery_phase_page_multi.dart';

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

class _MainLayoutState extends State<MainLayout> with RouteAware, WidgetsBindingObserver {
  late int _currentIndex;

  /// สรุปเที่ยวที่เพิ่ง save จาก Loading (แสดงบน Delivery)
  SavedTripSummary? _savedTripSummary;

  /// driverId สำหรับเช็คสถานะเช็คอิน (Check in → Loading → Deliver)
  String? _driverId;
  /// มีงานที่เช็คอินแล้วหรือไม่ — ต้องเช็คอินก่อนถึงจะเข้า Loading ได้
  bool _hasCheckedIn = false;

  /// จำนวนแชทที่ข้อความล่าสุดมาจาก Admin (lastMessageBy != ฉัน) — reset ทุกครั้งจาก snapshot
  int _chatUnreadCount = 0;
  StreamSubscription<QuerySnapshot<Map<String, dynamic>>>? _chatUnreadSubscription;
  bool _routeObserverSubscribed = false;

  static const String _prefKeyPendingDelivery = prefKeyPendingDeliverySummary;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _currentIndex = widget.initialTabIndex ?? 0;
    _savedTripSummary = widget.initialTripSummary;
    if (_savedTripSummary != null) _savePendingDeliverySummary(_savedTripSummary);
    // โหลดงานที่ยังไม่ส่ง (ค้างอยู่) หลังปิดแอป — ให้เมนู Delivery ยังแสดงงานค้าง
    if (_savedTripSummary == null) {
      WidgetsBinding.instance.addPostFrameCallback((_) => _loadPendingDeliverySummary());
    }
    WidgetsBinding.instance.addPostFrameCallback((_) => _loadDriverAndCheckInStatus());
    _setupChatFcm();
    _setupChatUnreadBadge();
    onChatRoomExited = _refreshChatUnreadFromServer;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _handlePendingChatNotification();
      _handlePendingBroadcastNotification();
    });
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
  void didPopNext() {
    _refreshChatUnreadFromServer();
  }

  void _setupChatFcm() {
    // Foreground: แจ้งเตือนด้วย system notification + เสียง (channel chat ใช้ custom sound จาก MainActivity)
    FirebaseMessaging.onMessage.listen((RemoteMessage message) async {
      if (!mounted) return;
      final type = message.data['type'] as String?;
      final chatId = message.data['chatId'] as String?;
      if (type == 'chat' && chatId != null && chatId.isNotEmpty) {
        final title = message.notification?.title ?? message.data['title'] ?? 'แชท';
        final body = message.notification?.body ?? message.data['body'] ?? 'ข้อความใหม่จาก Admin';
        try {
          await showChatNotification(
            title: title,
            body: body,
            chatId: chatId,
            id: chatId.hashCode & 0x7FFFFFFF,
          );
        } catch (e) {
          debugPrint('showChatNotification error: $e');
        }
      }
    });
    // เปิดแอปจาก notification (background): broadcast → หน้า Broadcast, chat → ห้องแชท
    FirebaseMessaging.onMessageOpenedApp.listen((RemoteMessage message) {
      if (!mounted) return;
      final type = message.data['type'] as String?;
      final chatId = message.data['chatId'] as String?;
      if (type == 'broadcast') {
        Navigator.of(context).pushNamed('/broadcast');
      } else if (type == 'chat' && chatId != null && chatId.isNotEmpty) {
        Navigator.of(context).pushNamed('/chat-room', arguments: chatId);
      }
    });
  }

  void _handlePendingChatNotification() {
    final chatId = pendingChatIdFromNotification;
    if (chatId != null && chatId.isNotEmpty && mounted) {
      pendingChatIdFromNotification = null;
      Navigator.of(context).pushNamed('/chat-room', arguments: chatId);
    }
  }

  void _handlePendingBroadcastNotification() {
    if (pendingBroadcastFromNotification && mounted) {
      pendingBroadcastFromNotification = false;
      Navigator.of(context).pushNamed('/broadcast');
    }
  }

  void _setupChatUnreadBadge() {
    final uid = FirebaseAuth.instance.currentUser?.uid;
    if (uid == null) return;
    final repo = ChatRepository();
    _chatUnreadSubscription = repo.watchChatsForDriver(uid).listen((snap) {
      if (!mounted) return;
      final count = _computeUnreadCount(snap, uid);
      setState(() => _chatUnreadCount = count);
    });
  }

  /// นับเฉพาะแชทที่ยังไม่ปิด ข้อความล่าสุดมาจาก Admin และ driver ยังไม่ได้อ่าน (lastReadByDriver อยู่ก่อน lastMessageAt)
  int _computeUnreadCount(QuerySnapshot<Map<String, dynamic>> snap, String uid) {
    int count = 0;
    for (final doc in snap.docs) {
      final data = doc.data();
      if (data['status'] == 'closed') continue;
      final lastBy = data['lastMessageBy']?.toString().trim();
      if (lastBy == null || lastBy.isEmpty) continue;
      if (lastBy == uid) continue;
      final lastMessageAt = data['lastMessageAt'];
      final lastReadByDriver = data['lastReadByDriver'];
      if (lastMessageAt != null && lastReadByDriver != null &&
          lastMessageAt is Timestamp && lastReadByDriver is Timestamp) {
        if (lastMessageAt.compareTo(lastReadByDriver) <= 0) continue;
      }
      count++;
    }
    return count;
  }

  /// กลับจากห้องแชท → ดึงแชทจาก server แล้วอัปเดต badge ให้ตรงกับข้อมูลล่าสุด (reset ไม่นับต่อเนื่อง).
  Future<void> _refreshChatUnreadFromServer() async {
    final uid = FirebaseAuth.instance.currentUser?.uid;
    if (uid == null || !mounted) return;
    try {
      final snap = await ChatRepository().getChatsForDriverFromServer(uid);
      if (!mounted) return;
      final count = _computeUnreadCount(snap, uid);
      setState(() => _chatUnreadCount = count);
    } catch (_) {}
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      _onAppResumed();
    }
  }

  Future<void> _onAppResumed() async {
    if (!mounted) return;
    // Force ID token refresh so revoked refresh tokens fail immediately
    // (otherwise the app can stay "logged in" until the old ID token expires).
    final user = FirebaseAuth.instance.currentUser;
    if (user != null) {
      try {
        await user.getIdToken(true);
      } on FirebaseAuthException catch (e) {
        debugPrint('[MainLayout] token refresh on resume: ${e.code}');
        // Only sign out when the credential is genuinely revoked/invalid.
        // Transient errors (e.g. network-request-failed while the radio is
        // still reconnecting after the screen wakes) must NOT log the driver
        // out — the existing token stays valid and works once online.
        if (_isRevokedAuthCode(e.code)) {
          await FirebaseAuth.instance.signOut();
          return;
        }
      } catch (e) {
        // Unknown/transient failure (App Check, connectivity) — keep the
        // session and retry on the next resume.
        debugPrint('[MainLayout] token refresh on resume: $e');
      }
    }
    if (!mounted) return;
    await MobileAppVersionService.instance.ensureAllowedToRun(context);
    if (!mounted) return;
    await MobileClientHeartbeatService.instance.onResumed(_driverId);
    if (!mounted) return;
    // เผื่อเที่ยวถูกปิด/แก้จากเครื่องอื่นตอนแอปพัก — เคลียร์งานค้างให้รับงานรอบใหม่ได้
    await _validatePendingDeliveryAndClearIfDelivered();
  }

  /// True only for auth error codes that mean the session is no longer valid
  /// (account disabled, refresh token revoked/expired). Network and other
  /// transient errors are excluded so a wake-from-sleep offline blip does not
  /// force a logout.
  static bool _isRevokedAuthCode(String code) => const {
        'user-disabled',
        'user-token-expired',
        'user-not-found',
        'invalid-user-token',
        'token-expired',
        'user-token-revoked',
      }.contains(code);

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    onChatRoomExited = null;
    if (_routeObserverSubscribed) {
      routeObserver.unsubscribe(this);
    }
    _chatUnreadSubscription?.cancel();
    super.dispose();
  }

  Future<void> _loadPendingDeliverySummary() async {
    try {
      final currentUid = FirebaseAuth.instance.currentUser?.uid;
      final prefs = await SharedPreferences.getInstance();
      final raw = prefs.getString(_prefKeyPendingDelivery);
      if (raw != null && raw.isNotEmpty) {
        final outer = jsonDecode(raw) as Map<String, dynamic>?;
        if (outer != null) {
          final storedUid = outer['uid'] as String?;
          final summaryMap = outer['summary'] as Map<String, dynamic>?;
          if (storedUid != null && storedUid == currentUid && summaryMap != null) {
            // ข้อมูลเป็นของ user คนนี้ — โหลดและ verify กับ Firestore
            final summary = SavedTripSummary.fromJson(summaryMap);
            if (summary != null && summary.tripId.trim().isNotEmpty) {
              final status = await getTripStatus(summary.tripId);
              if (status == null || status == 'delivered') {
                await _savePendingDeliverySummary(null);
                if (mounted) setState(() => _savedTripSummary = null);
                return;
              }
              if (mounted) setState(() => _savedTripSummary = summary);
            }
            return;
          } else {
            // ข้อมูลเป็นของ user คนอื่น หรือ format เก่า (ไม่มี uid) — ทิ้งทันที
            await prefs.remove(_prefKeyPendingDelivery);
          }
        }
      }
      // ไม่มีในเครื่อง หรือถูกทิ้ง → ดึงจาก Firestore (งานค้าง status in_transit ของ Driver คนนี้)
      final inTransit = await getPendingInTransitTrip(currentUid);
      if (inTransit == null || !mounted) return;
      final summary = SavedTripSummary.fromJson(inTransit);
      if (summary != null && mounted) {
        setState(() => _savedTripSummary = summary);
        _savePendingDeliverySummary(summary);
      }
    } catch (_) {}
  }

  Future<void> _savePendingDeliverySummary(SavedTripSummary? summary) async {
    try {
      final prefs = await SharedPreferences.getInstance();
      if (summary == null) {
        await prefs.remove(_prefKeyPendingDelivery);
      } else {
        final uid = FirebaseAuth.instance.currentUser?.uid;
        // เก็บ uid ของเจ้าของข้อมูลไว้ด้วย เพื่อป้องกัน user อื่นมาโหลดผิด
        final data = {'uid': uid, 'summary': summary.toJson()};
        await prefs.setString(_prefKeyPendingDelivery, jsonEncode(data));
      }
    } catch (_) {}
  }

  /// โหลด driverId และสถานะว่ามีงานเช็คอินแล้วหรือไม่ (ขั้นตอน: เช็คอิน → Loading → ส่งของ)
  Future<void> _loadDriverAndCheckInStatus() async {
    try {
      final uid = FirebaseAuth.instance.currentUser?.uid;
      if (uid == null || !mounted) return;
      final driver = await DriverRepository().getCurrentDriver(uid);
      if (!mounted) return;
      final driverId = driver?['id'] as String?;
      if (driverId == null || driverId.isEmpty) {
        setState(() => _driverId = null);
        setState(() => _hasCheckedIn = false);
        return;
      }
      setState(() => _driverId = driverId);
      final hasCheckedIn = await hasCheckedInTask(driverId);
      if (mounted) setState(() => _hasCheckedIn = hasCheckedIn);
      unawaited(MobileClientHeartbeatService.instance.onLoginResolved(driverId));
      if (mounted) {
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (mounted) {
            unawaited(MobileAppVersionService.instance.ensureAllowedToRun(context));
          }
        });
      }
    } catch (_) {}
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
    if (summary != null) _savePendingDeliverySummary(summary);
  }

  /// งานที่รับยังไม่ส่ง → ต้องส่งก่อน จึงจะกด Pick up รับงานใหม่ได้
  bool get _hasActiveDelivery => _savedTripSummary != null;

  /// Disable Pick up (Loading) เมื่อ: มีงานที่รับแล้วยังไม่ส่ง หรือ ยังไม่เช็คอิน (ขั้นตอน: เช็คอิน → Loading → ส่งของ)
  bool get _isPickupDisabled => _hasActiveDelivery || !_hasCheckedIn;

  /// Disable Delivery เมื่อยังไม่มีการรับงาน
  bool get _isDeliveryDisabled => !_hasActiveDelivery;

  void _onDeliveryCompleted() {
    setState(() {
      _savedTripSummary = null;
      _currentIndex = 0; // กลับไปหน้า Home หลังบันทึกส่งสำเร็จ
    });
    _savePendingDeliverySummary(null); // เคลียร์งานค้างจาก storage
  }

  /// โหลด multi-stop delivery จากแท็บไม่สำเร็จ — ห้าม Navigator.pop เด็ดขาด (จะ pop ทั้ง MainLayout เหลือจอว่างสีดำ)
  void _onEmbeddedMultiDeliveryAbort(String message) {
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(message)),
    );
    setState(() {
      _savedTripSummary = null;
      _currentIndex = 0;
    });
    _savePendingDeliverySummary(null);
  }

  /// Build delivery page — single vs multi based on task
  Widget _buildDeliveryPage() {
    final summary = _savedTripSummary;

    // If no task, show single delivery
    if (summary?.taskId == null) {
      return DeliveryPhasePage(savedTripSummary: summary);
    }

    // Check task for isMultiDelivery flag
    return FutureBuilder<DocumentSnapshot>(
      future: FirebaseFirestore.instance
          .collection('tasks')
          .doc(summary!.taskId)
          .get(),
      builder: (context, snapshot) {
        if (!snapshot.hasData || !snapshot.data!.exists) {
          return DeliveryPhasePage(savedTripSummary: summary);
        }

        final taskData = snapshot.data!.data() as Map<String, dynamic>?;
        final isMulti = taskData?['isMultiDelivery'] == true;
        final deliveryStops = taskData?['deliveryStops'] as List?;
        final hasMultiStops = (deliveryStops?.length ?? 0) > 1;

        if (isMulti || hasMultiStops) {
          return DeliveryPhasePageMulti(
            savedTripSummary: summary,
            embeddedInBottomNav: true,
          );
        }

        return DeliveryPhasePage(savedTripSummary: summary);
      },
    );
  }

  List<Widget> get _screens => [
    const HomePage(),
    const LoadingPhasePage(),
    _buildDeliveryPage(),
  ];

  Future<void> _onItemTapped(int index) async {
    if (index == 3) {
      Navigator.of(context).pushNamed('/vehicle-expense');
      return;
    }
    if (index == 4) {
      Navigator.of(context).pushNamed('/chat');
      return;
    }
    if (index == 0) {
      _loadDriverAndCheckInStatus(); // รีเฟรชสถานะเช็คอินเมื่อกลับมาแท็บหน้าแรก
    }
    if (index == 1) {
      await _loadDriverAndCheckInStatus(); // ดึงสถานะเช็คอินล่าสุดก่อนเช็ค
      if (!mounted) return;
      // ถ้าเที่ยวค้างถูกปิด/แก้จากเครื่องอื่นแล้ว ให้เคลียร์ก่อน จะได้ไม่บล็อกการรับงานใหม่
      if (_hasActiveDelivery) {
        await _validatePendingDeliveryAndClearIfDelivered();
        if (!mounted) return;
      }
      if (_isPickupDisabled) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(
              _hasActiveDelivery
                  ? 'nav_pickup_disabled_hint'.tr()
                  : 'nav_loading_require_checkin'.tr(),
            ),
          ),
        );
        return;
      }
    }
    if (index == 2 && _isDeliveryDisabled) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('nav_delivery_disabled_hint'.tr())),
      );
      return;
    }
    if (mounted) setState(() => _currentIndex = index);
    if (index == 2 && _savedTripSummary != null) {
      _validatePendingDeliveryAndClearIfDelivered();
    }
  }

  /// เช็คกับ **server โดยตรง** ว่าเที่ยวค้างถูกปิด (delivered) หรือถูกลบ/แก้ไขจากเครื่องอื่นแล้วหรือยัง
  /// ถ้าใช่ → เคลียร์งานค้าง (summary + draft) เพื่อให้รับงานรอบใหม่ได้ทันที
  /// ใช้ server source กัน Firestore cache ค้างเป็น in_transit เมื่อแอดมินปิด/แก้งานบนเว็บ
  Future<void> _validatePendingDeliveryAndClearIfDelivered() async {
    final summary = _savedTripSummary;
    if (summary == null || summary.tripId.trim().isEmpty) return;
    final check = await checkPendingTripOnServer(summary.tripId);
    // เคลียร์เฉพาะเมื่อยืนยันได้ว่า delivered/cancelled/doc หาย — ไม่เคลียร์ตอน unknown (ออฟไลน์) หรือ active
    if (isClearablePendingTrip(check)) {
      await _savePendingDeliverySummary(null);
      await DraftStorageService.instance.clearDeliveryDraft();
      if (mounted) {
        setState(() {
          _savedTripSummary = null;
          if (_currentIndex == 2) _currentIndex = 0;
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return MainLayoutScope(
      goToDeliveryTab: _goToDeliveryTab,
      onDeliveryCompleted: _onDeliveryCompleted,
      onEmbeddedMultiDeliveryAbort: _onEmbeddedMultiDeliveryAbort,
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
          BottomNavigationBarItem(
            icon: Badge(
              isLabelVisible: _chatUnreadCount > 0,
              label: Text(_chatUnreadCount > 99 ? '99+' : '$_chatUnreadCount'),
              backgroundColor: Colors.red,
              child: const Icon(Icons.chat_bubble_outline),
            ),
            label: 'nav_chat'.tr(),
          ),
        ],
      ),
    ),
  );
  }
}
