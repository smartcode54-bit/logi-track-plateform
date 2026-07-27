import 'dart:typed_data';
import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/material.dart';
import 'package:geolocator/geolocator.dart';
import 'package:image_picker/image_picker.dart';
import '../../../home/data/repositories/checkin_repository.dart';
import '../../../home/data/services/photo_overlay_service.dart';
import '../../../home/data/repositories/trip_records_repository.dart';
import '../../data/repositories/delivery_trip_repository.dart';
import 'package:cloud_firestore/cloud_firestore.dart';
import '../../../../core/utils/maps_navigation.dart';
import '../../../home/data/repositories/hubs_repository.dart';
import '../../../home/data/services/draft_storage_service.dart';
import '../../../home/data/services/image_compression_service.dart';
import '../../../home/data/services/ocr_screenshot_service.dart';
import '../../../home/presentation/pages/main_layout_scope.dart';
import 'incident_report_page.dart';
import 'delivery_phase_page_multi.dart';

/// ขั้นตอนรูป Delivery (ถ่ายภาพเหมือนการรับงาน)
const List<String> _deliveryPhotoStepKeys = [
  'pre_open',
  'opening',
  'empty_container',
  'runsheet_received',
];

class DeliveryPhasePage extends StatefulWidget {
  const DeliveryPhasePage({super.key, this.savedTripSummary});

  final SavedTripSummary? savedTripSummary;

  @override
  State<DeliveryPhasePage> createState() => _DeliveryPhasePageState();
}

class _DeliveryPhasePageState extends State<DeliveryPhasePage> {
  final Map<String, Uint8List> _deliveryPhotos = {};

  double? _lat;
  double? _lng;
  bool _locationLoading = true;
  bool _saving = false;

  /// true เมื่อเที่ยวนี้ถูกปิด/แก้ไขจากเครื่องอื่นแล้ว — หยุดโชว์ฟอร์ม/ไม่ restore draft
  bool _closedRemotely = false;

  /// Cache position + overlay (ที่อยู่, เข็มทิศ) สำหรับรูป step 2, 3 — ไม่หน่วงซ้ำ
  Position? _cachedOverlayPosition;
  OverlayContext? _cachedOverlayContext;

  bool get _canSubmit =>
      _deliveryPhotos.length == _deliveryPhotoStepKeys.length;

  /// เก็บผลการตรวจ Trip ID เมื่อไม่ตรง เพื่อแสดง debug UI ให้ผู้ใช้เห็นว่าอะไรไม่ตรง
  Map<String, String>? _lastTripIdValidationDebug;

  bool _loadingTrip = false;

  /// สำหรับจับคู่ชื่อปลายทางกับพิกัด Hub ใน Firestore
  List<HubDoc> _hubs = [];

  HubDoc? get _destinationHub {
    final label = widget.savedTripSummary?.destination?.trim();
    if (label == null || label.isEmpty) return null;
    return findHubByDestinationLabel(_hubs, label);
  }

  /// Format location display as "code - name" or just code if name not found
  String _formatLocationDisplay(String? code) {
    if (code == null || code.trim().isEmpty) return '-';
    final trimmed = code.trim();

    // Try to find in hubs list by sourceId or destination
    try {
      final hub = _hubs.firstWhere(
        (h) => h.sourceId.toUpperCase() == trimmed.toUpperCase() ||
               h.sourceNameEn.toUpperCase() == trimmed.toUpperCase(),
      );
      return '${hub.sourceId} - ${hub.sourceNameEn}';
    } catch (_) {
      // Hub not found, return code as-is
      return trimmed;
    }
  }

  @override
  void initState() {
    super.initState();
    _loadLocation();
    _loadHubsForNavigation();
    _initDeliveryFlow();
  }

  /// ตรวจ server ก่อนว่าเที่ยวนี้ยังค้างจริงไหม — ถ้าถูกปิด/แก้ไขจากเครื่องอื่นแล้ว
  /// ให้เคลียร์งานค้าง (draft + summary) กลับหน้าแรก ไม่โชว์ฟอร์ม/ไม่เด้ง dialog โหลด draft
  Future<void> _initDeliveryFlow() async {
    final tripId = widget.savedTripSummary?.tripId.trim() ?? '';
    if (tripId.isNotEmpty) {
      final check = await checkPendingTripOnServer(tripId);
      if (!mounted) return;
      if (isClearablePendingTrip(check)) {
        await _handleRemotelyClosed();
        return;
      }
    }
    await _fetchCurrentTrip();
    if (!mounted || _closedRemotely) return;
    await _tryRestoreDeliveryDraft();
  }

  /// เที่ยวถูกปิด/แก้ไขจากเครื่องอื่นแล้ว — ล้าง draft + งานค้าง แล้วกลับหน้าแรก
  Future<void> _handleRemotelyClosed() async {
    _closedRemotely = true;
    await DraftStorageService.instance.clearDeliveryDraft();
    if (!mounted) return;
    setState(() => _deliveryPhotos.clear());
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text('delivery_already_closed'.tr())),
    );
    MainLayoutScope.maybeOf(context)?.onDeliveryCompleted?.call();
  }

  /// ผู้ใช้กดล้างงานค้างเอง — ตรวจ server ก่อน: ถ้าปิด/หายแล้วล้างทันที,
  /// ถ้ายัง in_transit อยู่จริงให้ยืนยันแบบเข้ม (force) เผื่อกรณีเที่ยวเดิมถูกทิ้งค้าง (นำใบงานผิดเข้ามา)
  Future<void> _promptClearStuckJob() async {
    final tripId = widget.savedTripSummary?.tripId.trim() ?? '';
    if (tripId.isEmpty) {
      await _forceClearStuckJob();
      return;
    }
    final check = await checkPendingTripOnServer(tripId);
    if (!mounted) return;
    if (isClearablePendingTrip(check)) {
      await _handleRemotelyClosed();
      return;
    }
    if (check == PendingTripCheck.unknown) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('delivery_clear_stuck_offline'.tr()),
          backgroundColor: Colors.orange,
        ),
      );
      return;
    }
    // ยัง active บน server — เตือนเข้มก่อน force clear
    final confirm = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text('delivery_clear_stuck_title'.tr()),
        content: Text('delivery_clear_stuck_active_warning'.tr()),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: Text('manual_checkin_cancel'.tr()),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, true),
            child: Text('delivery_clear_stuck_confirm'.tr()),
          ),
        ],
      ),
    );
    if (confirm == true) await _forceClearStuckJob();
  }

  /// ล้างงานค้างในเครื่อง (draft + summary) โดยไม่แตะสถานะบน server
  Future<void> _forceClearStuckJob() async {
    _closedRemotely = true;
    await DraftStorageService.instance.clearDeliveryDraft();
    if (!mounted) return;
    setState(() => _deliveryPhotos.clear());
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text('delivery_clear_stuck_done'.tr())),
    );
    MainLayoutScope.maybeOf(context)?.onDeliveryCompleted?.call();
  }

  Future<void> _loadHubsForNavigation() async {
    try {
      final list = await fetchAllHubs();
      if (mounted) setState(() => _hubs = list);
    } catch (_) {}
  }

  Future<void> _openNavigationToDestination() async {
    final destName = widget.savedTripSummary?.destination?.trim();
    if (destName == null || destName.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('delivery_navigation_no_destination'.tr())),
      );
      return;
    }
    try {
      final pos = await getCurrentPosition();
      final hub = _destinationHub;
      final useCoords = hub != null && hub.hasCoordinates;
      final ok = await openGoogleMapsDrivingDirections(
        originLat: pos.latitude,
        originLng: pos.longitude,
        destLat: useCoords ? hub.latitude : null,
        destLng: useCoords ? hub.longitude : null,
        destinationPlaceName: useCoords ? null : destName,
      );
      if (!mounted) return;
      if (!ok) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('delivery_navigation_could_not_open'.tr())),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(
              '${'delivery_navigation_location_error'.tr()} $e',
            ),
            backgroundColor: Colors.orange,
          ),
        );
      }
    }
  }

  Future<void> _fetchCurrentTrip() async {
    final tripId = widget.savedTripSummary?.tripId;
    if (tripId == null || tripId.isEmpty) return;
    setState(() => _loadingTrip = true);
    try {
      final tripSnap = await FirebaseFirestore.instance
          .collection(tripRecordsCollection)
          .doc(tripId)
          .get();

      final tripData = tripSnap.data() as Map<String, dynamic>?;

      void goMulti() {
        if (!mounted) return;
        Navigator.push<void>(
          context,
          MaterialPageRoute<void>(
            builder: (ctx) => DeliveryPhasePageMulti(
              savedTripSummary: widget.savedTripSummary,
              embeddedInBottomNav: false,
            ),
          ),
        );
      }

      if (tripData != null && tripData['isMultiDelivery'] == true) {
        goMulti();
        return;
      }

      String? taskIdLookup;
      final tid = tripData?['taskId'];
      if (tid is String && tid.trim().isNotEmpty) {
        taskIdLookup = tid.trim();
      } else {
        final s = widget.savedTripSummary?.taskId?.trim();
        if (s != null && s.isNotEmpty) taskIdLookup = s;
      }

      if (taskIdLookup != null && taskIdLookup.isNotEmpty) {
        final taskSnap = await FirebaseFirestore.instance
            .collection('tasks')
            .doc(taskIdLookup)
            .get();
        final td = taskSnap.data();
        final stopsN = (td?['deliveryStops'] as List?)?.length ?? 0;
        if (td != null && (td['isMultiDelivery'] == true || stopsN >= 2)) {
          await FirebaseFirestore.instance
              .collection(tripRecordsCollection)
              .doc(tripId)
              .set(
                {
                  'isMultiDelivery': true,
                  'updatedAt': FieldValue.serverTimestamp(),
                },
                SetOptions(merge: true),
              );
          goMulti();
          return;
        }
      }
    } catch (e) {
      debugPrint('Error fetching trip: $e');
    }
    if (mounted) setState(() => _loadingTrip = false);
  }

  Future<void> _saveDeliveryDraft() async {
    final s = widget.savedTripSummary;
    if (s == null || s.tripId.trim().isEmpty) return;
    if (!mounted) return;
    await DraftStorageService.instance.saveDeliveryDraft(
      tripId: s.tripId,
      origin: s.origin,
      destination: s.destination,
      sealCode: s.sealCode,
      jobType: s.jobType,
      taskId: s.taskId,
      photos: Map.from(_deliveryPhotos),
    );
  }

  Future<void> _tryRestoreDeliveryDraft() async {
    final s = widget.savedTripSummary;
    if (s == null || s.tripId.trim().isEmpty) return;
    final draft = await DraftStorageService.instance.loadDeliveryDraft();
    if (draft == null || draft.tripId.trim() != s.tripId.trim()) return;
    if (draft.photoPaths.isEmpty || !mounted) return;
    final confirm = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text('draft_restore_title'.tr()),
        content: Text('draft_restore_delivery_message'.tr()),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: Text('draft_restore_discard'.tr()),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, true),
            child: Text('draft_restore_restore'.tr()),
          ),
        ],
      ),
    );
    if (confirm != true || !mounted) return;
    final restored = <String, Uint8List>{};
    for (final e in draft.photoPaths.entries) {
      final bytes = await DraftStorageService.instance.loadDeliveryDraftPhoto(
        e.value,
      );
      if (bytes != null) restored[e.key] = Uint8List.fromList(bytes);
    }
    if (mounted) setState(() => _deliveryPhotos.addAll(restored));
  }

  Future<void> _loadLocation() async {
    try {
      final position = await getCurrentPosition();
      if (mounted) {
        setState(() {
          _lat = position.latitude;
          _lng = position.longitude;
          _locationLoading = false;
        });
      }
    } catch (e) {
      if (mounted) setState(() => _locationLoading = false);
    }
  }

  /// ปกติแล้ว Trip ID เป็น LT + ตัวอักษร/ตัวเลข ใช้เปรียบเทียบแบบไม่สนใจตัวพิมพ์
  static String _normalizeTripId(String? v) =>
      (v ?? '').trim().toUpperCase().replaceAll(RegExp(r'\s+'), '');

  /// ตรวจว่า Trip ID ในภาพ (จาก OCR) ตรงกับเที่ยวนี้หรือไม่
  /// [showDialogOnError] true = แสดง dialog เมื่อไม่ตรง/ไม่พบ (ใช้ตอนอัปโหลด), false = ไม่แสดง dialog (ใช้ตอนกดยืนยันส่ง)
  Future<bool> _validateRunsheetTripId(
    List<int> imageBytes, {
    String? imagePath,
    bool showDialogOnError = true,
  }) async {
    final rawExpected = widget.savedTripSummary?.tripId ?? '';
    final expected = _normalizeTripId(rawExpected);
    if (expected.isEmpty) return true;
    if (kIsWeb) return true;
    try {
      final result = await runOcrOnImageBytes(imageBytes, imagePath: imagePath);
      final rawFromImage = result.tripId ?? '';
      final fromImage = _normalizeTripId(rawFromImage);
      if (fromImage.isEmpty) {
        if (mounted) {
          setState(() {
            _lastTripIdValidationDebug = {
              'errorType': 'not_found',
              'expected': rawExpected,
              'normalizedExpected': expected,
              'fromImage': rawFromImage.isEmpty ? '-' : rawFromImage,
              'normalizedFromImage': '-',
            };
          });
          if (showDialogOnError) {
            await showDialog(
              context: context,
              builder: (ctx) => AlertDialog(
                title: Text('delivery_trip_id_mismatch_title'.tr()),
                content: Text('delivery_trip_id_not_found_in_image'.tr()),
                actions: [
                  TextButton(
                    onPressed: () => Navigator.pop(ctx),
                    child: Text(MaterialLocalizations.of(ctx).okButtonLabel),
                  ),
                ],
              ),
            );
          }
        }
        return false;
      }
      if (fromImage != expected) {
        if (mounted) {
          setState(() {
            _lastTripIdValidationDebug = {
              'errorType': 'mismatch',
              'expected': rawExpected,
              'normalizedExpected': expected,
              'fromImage': rawFromImage,
              'normalizedFromImage': fromImage,
            };
          });
          if (showDialogOnError) {
            await showDialog(
              context: context,
              builder: (ctx) => AlertDialog(
                title: Text('delivery_trip_id_mismatch_title'.tr()),
                content: Text(
                  '${'delivery_trip_id_mismatch_message'.tr()}\n${'delivery_trip_id_expected'.tr()}: $rawExpected\n${'delivery_trip_id_in_image'.tr()}: $rawFromImage',
                ),
                actions: [
                  TextButton(
                    onPressed: () => Navigator.pop(ctx),
                    child: Text(MaterialLocalizations.of(ctx).okButtonLabel),
                  ),
                ],
              ),
            );
          }
        }
        return false;
      }
      if (mounted) setState(() => _lastTripIdValidationDebug = null);
      return true;
    } catch (_) {
      if (mounted) {
        setState(() {
          _lastTripIdValidationDebug = {
            'errorType': 'ocr_failed',
            'expected': rawExpected,
            'normalizedExpected': expected,
            'fromImage': '-',
            'normalizedFromImage': '-',
          };
        });
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text('delivery_trip_id_ocr_failed'.tr()),
            backgroundColor: Colors.orange,
          ),
        );
      }
      return false;
    }
  }

  /// ถ่ายภาพหรืออัปโหลดรูป: ทุกขั้นให้เลือกระหว่าง camera และ gallery ได้
  Future<void> _takeDeliveryPhoto(String stepKey) async {
    if (kIsWeb) return;
    final source = await showModalBottomSheet<ImageSource>(
      context: context,
      builder: (ctx) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              leading: const Icon(Icons.camera_alt),
              title: Text('refuel_receipt_take_photo'.tr()),
              onTap: () => Navigator.pop(ctx, ImageSource.camera),
            ),
            ListTile(
              leading: const Icon(Icons.photo_library),
              title: Text('refuel_receipt_from_gallery'.tr()),
              onTap: () => Navigator.pop(ctx, ImageSource.gallery),
            ),
          ],
        ),
      ),
    );
    if (source == null || !mounted) return;

    final picker = ImagePicker();
    final isRunsheetReceived = stepKey == 'runsheet_received';
    final xfile = await picker.pickImage(source: source, imageQuality: 85);
    if (xfile == null || !mounted) return;
    List<int> imageBytes = await xfile.readAsBytes();
    if (!mounted) return;
    if (isRunsheetReceived) {
      // บันทึกรันชีทเฉพาะเมื่อ Trip ID ตรง — ถ้าใส่ผิดแล้วเปลี่ยนเป็นใบที่ถูก แอปจะรับใบที่ถูก
      final ok = await _validateRunsheetTripId(
        imageBytes,
        imagePath: xfile.path,
      );
      if (!mounted) return;
      if (!ok) {
        return; // ไม่ตรงหรือ OCR ไม่พบ → ไม่เก็บรูปผิด ให้ผู้ใช้เลือกรันชีทที่ถูกใหม่
      }
    }
    Uint8List compressed;
    if (isRunsheetReceived) {
      compressed = await compressImageForUpload(imageBytes);
    } else {
      if (_cachedOverlayPosition == null || _cachedOverlayContext == null) {
        try {
          final pos = await getCurrentPosition();
          final ctx = await fetchOverlayContext(pos.latitude, pos.longitude);
          if (mounted) {
            setState(() {
              _cachedOverlayPosition = pos;
              _cachedOverlayContext = ctx;
            });
          }
        } catch (_) {}
      }
      if (!mounted) return;
      compressed = await stampOverlayAndCompressForEvidence(
        imageBytes,
        position: _cachedOverlayPosition,
        overlayContext: _cachedOverlayContext,
      );
    }
    if (!mounted) return;
    setState(() => _deliveryPhotos[stepKey] = compressed);
    _saveDeliveryDraft();
    if (mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(
            isRunsheetReceived
                ? 'delivery_runsheet_uploaded'.tr()
                : 'loading_phase_photo_stamped'.tr(),
          ),
        ),
      );
    }
  }

  Future<void> _submitDelivery() async {
    FocusScope.of(context).unfocus();
    final tripId = widget.savedTripSummary?.tripId.trim();
    if (tripId == null || tripId.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('loading_phase_trip_id_required'.tr()),
          backgroundColor: Colors.orange,
        ),
      );
      return;
    }
    if (!_canSubmit) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('loading_phase_photos_required'.tr()),
          backgroundColor: Colors.orange,
        ),
      );
      return;
    }
    if (_lat == null || _lng == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('delivery_location_required'.tr()),
          backgroundColor: Colors.orange,
        ),
      );
      return;
    }

    // เช็ค Trip ID ในภาพรันชีทตรงกับเที่ยวนี้ก่อนยืนยันส่ง (ไม่แสดง dialog แค่ return false)
    // ตรงจุดนี้ไม่มี xfile.path แล้ว (เพราะถูกเก็บเป็น bytes ใน state แล้ว)
    // จึงใช้เฉพาะ bytes ในการเช็ค OCR ล้วนๆ (QR อ่านไม่ได้จาก bytes)
    // แต่มันได้เช็คผ่าน QR ตอนถ่าย/เลือกรูปแล้วใน _takeDeliveryPhoto ถือเป็นการ double check ด้วยข้อความ
    final runsheetBytes = _deliveryPhotos['runsheet_received']!;
    final runsheetOk = await _validateRunsheetTripId(
      runsheetBytes,
      showDialogOnError: false,
    );
    if (!runsheetOk) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text('delivery_trip_id_mismatch_message'.tr()),
            backgroundColor: Colors.orange,
          ),
        );
      }
      return;
    }

    setState(() => _saving = true);
    try {
      final position = await getCurrentPosition();
      final timestamp = DateTime.now();
      final deliveryPhotos = <String, StampedPhotoInput>{};
      for (final key in _deliveryPhotoStepKeys) {
        final bytes = _deliveryPhotos[key]!;
        deliveryPhotos[key] = StampedPhotoInput(
          bytes: bytes,
          lat: position.latitude,
          lng: position.longitude,
          timestamp: timestamp,
        );
      }

      // Resolve taskId: from SavedTripSummary, or fallback from Firestore trip record
      String? resolvedTaskId = widget.savedTripSummary?.taskId;
      if ((resolvedTaskId == null || resolvedTaskId.isEmpty) &&
          tripId.isNotEmpty) {
        try {
          final tripDoc = await FirebaseFirestore.instance
              .collection(tripRecordsCollection)
              .doc(tripId)
              .get();
          if (tripDoc.exists) {
            resolvedTaskId = tripDoc.data()?['taskId'] as String?;
          }
        } catch (_) {}
      }

      await submitDeliveryPhaseRecord(
        tripId: tripId,
        taskId: resolvedTaskId,
        deliveryPhotos: deliveryPhotos,
        deliveredLat: position.latitude,
        deliveredLng: position.longitude,
      );
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text('delivery_saved'.tr())));
      DraftStorageService.instance.clearDeliveryDraft();
      MainLayoutScope.of(context)?.onDeliveryCompleted?.call();
      setState(() {
        _saving = false;
        _deliveryPhotos.clear();
      });
    } catch (e) {
      if (mounted) {
        setState(() => _saving = false);
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text('${'loading_phase_save_failed'.tr()} $e'),
            backgroundColor: Colors.red,
          ),
        );
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDarkMode = Theme.of(context).brightness == Brightness.dark;
    const darkNavy = Color(0xFF0F172A);
    final destName = widget.savedTripSummary?.destination?.trim() ?? '';

    return PopScope(
      canPop: !_saving,
      child: Scaffold(
        backgroundColor: isDarkMode
            ? Theme.of(context).scaffoldBackgroundColor
            : Colors.grey[50],
        appBar: AppBar(
          title: Text(
            'nav_delivery'.tr(),
            style: const TextStyle(color: Colors.white),
          ),
          backgroundColor: darkNavy,
          iconTheme: const IconThemeData(color: Colors.white),
          actions: [
            TextButton.icon(
              onPressed: _saving
                  ? null
                  : () async {
                      await Navigator.of(context).push<bool>(
                        MaterialPageRoute<bool>(
                          builder: (context) => IncidentReportPage(
                            savedTripSummary: widget.savedTripSummary,
                          ),
                        ),
                      );
                    },
              icon: const Icon(
                Icons.warning_amber_rounded,
                color: Colors.orange,
              ),
              label: Text(
                'report_incident'.tr(),
                style: const TextStyle(
                  color: Colors.orange,
                  fontWeight: FontWeight.bold,
                ),
              ),
            ),
            // เมนูล้างงานค้าง — กรณีเที่ยวถูกปิด/แก้ไขจากเครื่องอื่น (เว็บแอดมิน) แล้วยังค้างในมือถือ
            PopupMenuButton<String>(
              enabled: !_saving,
              icon: const Icon(Icons.more_vert, color: Colors.white),
              onSelected: (v) {
                if (v == 'clear_stuck') _promptClearStuckJob();
              },
              itemBuilder: (_) => [
                PopupMenuItem<String>(
                  value: 'clear_stuck',
                  child: Text('delivery_clear_stuck'.tr()),
                ),
              ],
            ),
          ],
        ),
        body: _loadingTrip
            ? const Center(child: CircularProgressIndicator())
            : Stack(
                children: [
                  SafeArea(
                    top: false,
                    child: AbsorbPointer(
                      absorbing: _saving,
                      child: SingleChildScrollView(
                        padding: const EdgeInsets.all(16.0),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.stretch,
                        children: [
                          // Header Summary Card
                          Card(
                            elevation: 4,
                            color: darkNavy,
                            shape: RoundedRectangleBorder(
                              borderRadius: BorderRadius.circular(16),
                            ),
                            child: Padding(
                              padding: const EdgeInsets.all(20.0),
                              child: Column(
                                crossAxisAlignment: CrossAxisAlignment.start,
                                children: [
                                  Row(
                                    mainAxisAlignment:
                                        MainAxisAlignment.spaceBetween,
                                    children: [
                                      Text(
                                        'active_delivery'.tr(),
                                        style: const TextStyle(
                                          color: Colors.white70,
                                          fontSize: 16,
                                          fontWeight: FontWeight.w500,
                                        ),
                                      ),
                                      Container(
                                        padding: const EdgeInsets.symmetric(
                                          horizontal: 12,
                                          vertical: 4,
                                        ),
                                        decoration: BoxDecoration(
                                          color: Colors.green.withOpacity(0.2),
                                          borderRadius: BorderRadius.circular(
                                            12,
                                          ),
                                          border: Border.all(
                                            color: Colors.green.shade300,
                                          ),
                                        ),
                                        child: Text(
                                          'status_in_transit'.tr(),
                                          style: const TextStyle(
                                            color: Colors.greenAccent,
                                            fontWeight: FontWeight.bold,
                                            fontSize: 12,
                                          ),
                                        ),
                                      ),
                                    ],
                                  ),
                                  const SizedBox(height: 16),
                                  Text(
                                    '${'loading_phase_origin'.tr()}  ${_formatLocationDisplay(widget.savedTripSummary?.origin)}',
                                    style: const TextStyle(
                                      color: Colors.white70,
                                      fontSize: 14,
                                    ),
                                    maxLines: 2,
                                    overflow: TextOverflow.ellipsis,
                                  ),
                                  const SizedBox(height: 6),
                                  Text(
                                    '${'loading_phase_destination'.tr()}  ${_formatLocationDisplay(widget.savedTripSummary?.destination)}',
                                    style: const TextStyle(
                                      color: Colors.white,
                                      fontSize: 15,
                                      fontWeight: FontWeight.w600,
                                    ),
                                    maxLines: 2,
                                    overflow: TextOverflow.ellipsis,
                                  ),
                                  const SizedBox(height: 12),
                                  Text(
                                    'Trip ID: ${widget.savedTripSummary?.tripId ?? '-'}',
                                    style: const TextStyle(
                                      color: Colors.white54,
                                      fontSize: 13,
                                    ),
                                  ),
                                  if (widget.savedTripSummary?.sealCode !=
                                          null &&
                                      widget.savedTripSummary!.sealCode!
                                          .trim()
                                          .isNotEmpty) ...[
                                    const SizedBox(height: 2),
                                    Text(
                                      '${'loading_phase_seal_code'.tr()}: ${widget.savedTripSummary!.sealCode}',
                                      style: const TextStyle(
                                        color: Colors.white54,
                                        fontSize: 13,
                                      ),
                                    ),
                                  ],
                                ],
                              ),
                            ),
                          ),
                          const SizedBox(height: 16),

                          // นำทาง (in_transit) — คลิกทั้งการ์ด
                          Card(
                            elevation: 2,
                            shape: RoundedRectangleBorder(
                              borderRadius: BorderRadius.circular(12),
                            ),
                            clipBehavior: Clip.antiAlias,
                            child: InkWell(
                              onTap: destName.isEmpty || _locationLoading
                                  ? null
                                  : _openNavigationToDestination,
                              child: Padding(
                                padding: const EdgeInsets.symmetric(
                                  horizontal: 12,
                                  vertical: 12,
                                ),
                                child: Row(
                                  children: [
                                    Icon(
                                      Icons.near_me_outlined,
                                      size: 20,
                                      color: Theme.of(context)
                                          .colorScheme
                                          .primary,
                                    ),
                                    const SizedBox(width: 4),
                                    Icon(
                                      Icons.arrow_forward,
                                      size: 18,
                                      color: Colors.grey[600],
                                    ),
                                    const SizedBox(width: 4),
                                    Icon(
                                      Icons.place_outlined,
                                      size: 22,
                                      color: Theme.of(context)
                                          .colorScheme
                                          .error,
                                    ),
                                    const SizedBox(width: 10),
                                    Text(
                                      'delivery_navigation_title'.tr(),
                                      style: Theme.of(context)
                                          .textTheme
                                          .titleSmall
                                          ?.copyWith(
                                            fontWeight: FontWeight.w600,
                                          ),
                                    ),
                                    const Spacer(),
                                    if (_locationLoading)
                                      const SizedBox(
                                        width: 22,
                                        height: 22,
                                        child: CircularProgressIndicator(
                                          strokeWidth: 2,
                                        ),
                                      )
                                    else
                                      Icon(
                                        Icons.map_outlined,
                                        size: 26,
                                        color: Theme.of(context)
                                            .colorScheme
                                            .tertiary,
                                      ),
                                  ],
                                ),
                              ),
                            ),
                          ),
                          const SizedBox(height: 24),

                          // Photo capture header (เหมือนการรับงาน)
                          Text(
                            'mandatory_evidence'.tr(),
                            style: Theme.of(context).textTheme.titleLarge
                                ?.copyWith(
                                  fontWeight: FontWeight.bold,
                                  color: isDarkMode ? Colors.white : darkNavy,
                                ),
                          ),
                          const SizedBox(height: 8),
                          Text(
                            'mandatory_evidence_desc'.tr(),
                            style: TextStyle(
                              color: isDarkMode
                                  ? Colors.grey[400]
                                  : Colors.grey[600],
                              fontSize: 14,
                            ),
                          ),
                          const SizedBox(height: 16),

                          // Photo capture tiles (ถ่ายภาพแทนการเช็ค)
                          _buildPhotoCaptureTile(
                            title: 'delivery_photo_pre_open'.tr(),
                            subtitle: 'delivery_photo_pre_open_desc'.tr(),
                            stepKey: 'pre_open',
                            icon: Icons.camera_alt,
                          ),
                          const SizedBox(height: 12),
                          _buildPhotoCaptureTile(
                            title: 'delivery_photo_opening'.tr(),
                            subtitle: 'delivery_photo_opening_desc'.tr(),
                            stepKey: 'opening',
                            icon: Icons.camera_alt,
                          ),
                          const SizedBox(height: 12),
                          _buildPhotoCaptureTile(
                            title: 'delivery_photo_empty'.tr(),
                            subtitle: 'delivery_photo_empty_desc'.tr(),
                            stepKey: 'empty_container',
                            icon: Icons.camera_alt,
                          ),
                          const SizedBox(height: 12),
                          _buildPhotoCaptureTile(
                            title: 'delivery_photo_received'.tr(),
                            subtitle: 'delivery_photo_received_desc'.tr(),
                            stepKey: 'runsheet_received',
                            icon: Icons.photo_library,
                            useGallery: true,
                          ),

                          const SizedBox(height: 32),

                          // Submit Button
                          ElevatedButton(
                            onPressed: (_canSubmit && !_saving)
                                ? _submitDelivery
                                : null,
                            style: ElevatedButton.styleFrom(
                              backgroundColor: Colors.blueAccent,
                              padding: const EdgeInsets.symmetric(vertical: 16),
                              shape: RoundedRectangleBorder(
                                borderRadius: BorderRadius.circular(12),
                              ),
                              disabledBackgroundColor: Colors.grey.shade400,
                            ),
                            child: _saving
                                ? const SizedBox(
                                    height: 24,
                                    width: 24,
                                    child: CircularProgressIndicator(
                                      strokeWidth: 2,
                                      color: Colors.white,
                                    ),
                                  )
                                : Text(
                                    'submit_delivery'.tr(),
                                    style: const TextStyle(
                                      fontSize: 18,
                                      fontWeight: FontWeight.bold,
                                      color: Colors.white,
                                    ),
                                  ),
                          ),
                          if (_lastTripIdValidationDebug != null)
                            _buildTripIdValidationDebugCard(),
                          const SizedBox(height: 24),
                        ],
                      ),
                    ),
                  ),
                  ),
                  if (_saving)
                    Positioned.fill(child: ModalBarrier(color: Colors.black38)),
                  if (_saving)
                    Center(
                      child: Card(
                        child: Padding(
                          padding: const EdgeInsets.all(24),
                          child: Column(
                            mainAxisSize: MainAxisSize.min,
                            children: [
                              const SizedBox(
                                width: 32,
                                height: 32,
                                child: CircularProgressIndicator(
                                  strokeWidth: 2,
                                ),
                              ),
                              const SizedBox(height: 16),
                              Text(
                                'loading_phase_saving'.tr(),
                                style: const TextStyle(fontSize: 14),
                              ),
                            ],
                          ),
                        ),
                      ),
                    ),
                ],
              ),
      ),
    );
  }

  String _tripIdErrorTypeLabel(String errorType) {
    switch (errorType) {
      case 'not_found':
        return 'delivery_trip_id_debug_error_not_found'.tr();
      case 'mismatch':
        return 'delivery_trip_id_debug_error_mismatch'.tr();
      case 'ocr_failed':
        return 'delivery_trip_id_debug_error_ocr_failed'.tr();
      default:
        return errorType;
    }
  }

  /// การ์ดแสดงจุดที่ตรวจสอบ Trip ID เมื่อไม่ตรง (เพื่อ debug ว่าอะไรไม่ตรง)
  Widget _buildTripIdValidationDebugCard() {
    final d = _lastTripIdValidationDebug!;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark
        ? Colors.orange.shade900.withOpacity(0.25)
        : Colors.orange.shade50;
    final fg = isDark ? Colors.orange.shade200 : Colors.orange.shade900;
    final mono = isDark ? Colors.orange.shade100 : Colors.black87;
    return Padding(
      padding: const EdgeInsets.only(top: 12),
      child: Card(
        color: bg,
        child: Padding(
          padding: const EdgeInsets.all(12),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Icon(Icons.info_outline, color: fg, size: 20),
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text(
                      'delivery_trip_id_check_debug'.tr(),
                      style: TextStyle(
                        fontWeight: FontWeight.w600,
                        color: fg,
                        fontSize: 13,
                      ),
                    ),
                  ),
                  IconButton(
                    icon: Icon(Icons.close, color: fg, size: 18),
                    onPressed: () =>
                        setState(() => _lastTripIdValidationDebug = null),
                    padding: EdgeInsets.zero,
                    constraints: const BoxConstraints(
                      minWidth: 32,
                      minHeight: 32,
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 8),
              _tripIdDebugRow(
                'delivery_trip_id_debug_expected'.tr(),
                d['expected'] ?? '-',
                mono,
              ),
              _tripIdDebugRow(
                'delivery_trip_id_debug_normalized_expected'.tr(),
                d['normalizedExpected'] ?? '-',
                mono,
              ),
              _tripIdDebugRow(
                'delivery_trip_id_debug_from_image'.tr(),
                d['fromImage'] ?? '-',
                mono,
              ),
              _tripIdDebugRow(
                'delivery_trip_id_debug_normalized_from_image'.tr(),
                d['normalizedFromImage'] ?? '-',
                mono,
              ),
              if (d['errorType'] != null) ...[
                const SizedBox(height: 4),
                Text(
                  _tripIdErrorTypeLabel(d['errorType']!),
                  style: TextStyle(
                    fontSize: 11,
                    color: fg,
                    fontStyle: FontStyle.italic,
                  ),
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }

  Widget _tripIdDebugRow(String label, String value, Color valueColor) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 4),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 160,
            child: Text(
              label,
              style: TextStyle(
                fontSize: 12,
                color: Theme.of(context).brightness == Brightness.dark
                    ? Colors.orange.shade300
                    : Colors.black87,
              ),
            ),
          ),
          Expanded(
            child: SelectableText(
              value,
              style: TextStyle(
                fontSize: 12,
                fontFamily: 'monospace',
                color: valueColor,
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildPhotoCaptureTile({
    required String title,
    required String subtitle,
    required String stepKey,
    required IconData icon,
    bool useGallery = false,
  }) {
    final isCaptured = _deliveryPhotos.containsKey(stepKey);
    return InkWell(
      onTap: () => _takeDeliveryPhoto(stepKey),
      borderRadius: BorderRadius.circular(12),
      child: Container(
        padding: const EdgeInsets.all(16),
        decoration: BoxDecoration(
          color: isCaptured
              ? Colors.green.withOpacity(0.05)
              : Theme.of(context).cardColor,
          borderRadius: BorderRadius.circular(12),
          border: Border.all(
            color: isCaptured ? Colors.green : Colors.grey.shade300,
            width: 1.5,
          ),
        ),
        child: Row(
          children: [
            if (isCaptured && _deliveryPhotos[stepKey] != null)
              ClipRRect(
                borderRadius: BorderRadius.circular(8),
                child: Image.memory(
                  _deliveryPhotos[stepKey]!,
                  height: 56,
                  width: 56,
                  fit: BoxFit.cover,
                ),
              )
            else
              Container(
                padding: const EdgeInsets.all(12),
                decoration: BoxDecoration(
                  color: Colors.blueAccent.withOpacity(0.1),
                  shape: BoxShape.circle,
                ),
                child: Icon(icon, color: Colors.blueAccent),
              ),
            const SizedBox(width: 16),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    title,
                    style: const TextStyle(
                      fontWeight: FontWeight.bold,
                      fontSize: 16,
                    ),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    subtitle,
                    style: TextStyle(color: Colors.grey.shade600, fontSize: 12),
                  ),
                ],
              ),
            ),
            if (isCaptured)
              const Icon(Icons.check_circle, color: Colors.green, size: 28)
            else
              Icon(
                useGallery ? Icons.photo_library : Icons.camera_alt,
                color: Colors.grey.shade400,
                size: 24,
              ),
          ],
        ),
      ),
    );
  }
}
