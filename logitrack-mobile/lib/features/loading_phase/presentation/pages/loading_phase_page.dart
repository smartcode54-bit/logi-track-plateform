import 'dart:async';
import 'dart:typed_data';
import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:easy_localization/easy_localization.dart';
import 'package:firebase_auth/firebase_auth.dart';
import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/material.dart';
import 'package:image_picker/image_picker.dart';
import 'package:intl/intl.dart' as intl;
import 'package:geolocator/geolocator.dart';
import '../../../home/data/repositories/checkin_repository.dart';
import '../../../home/data/repositories/hubs_repository.dart';
import '../../../home/data/services/photo_overlay_service.dart';
import '../../../home/data/models/trip_record.dart';
import '../../../home/data/repositories/hub_soc_distances_repository.dart';
import '../../../home/data/repositories/trip_records_repository.dart';
import '../../../home/data/services/draft_storage_service.dart';
import '../../../home/data/services/image_compression_service.dart';
import '../../../home/data/services/ocr_screenshot_service.dart';
import '../../../home/presentation/pages/main_layout_scope.dart';
import '../../../home/presentation/pages/qr_scan_page.dart';
import '../../data/repositories/loading_trip_repository.dart';
import '../../../../core/presentation/widgets/searchable_hub_picker.dart';

/// ขั้นตอนรูป Loading (ไม่รวม runsheet ที่ย้ายขึ้นไปข้างบน)
const List<String> _cameraPhotoStepKeys = ['pre_close', 'closing', 'seal'];

class LoadingPhasePage extends StatefulWidget {
  const LoadingPhasePage({super.key});

  @override
  State<LoadingPhasePage> createState() => _LoadingPhasePageState();
}

class _LoadingPhasePageState extends State<LoadingPhasePage> {
  final _formKey = GlobalKey<FormState>();
  final _tripIdController = TextEditingController();
  final _sealCodeController = TextEditingController();
  final _originController = TextEditingController();
  final _destinationController = TextEditingController();
  final _parcelCountController = TextEditingController();
  final _sealTimeController = TextEditingController();
  final _totalWeightController = TextEditingController();

  String? _jobType = jobTypeFirstMile;

  Uint8List? _runsheetPhoto;
  final Map<String, Uint8List> _stepPhotos = {};

  bool _ocrLoading = false;
  bool _saving = false;

  /// Inline duplicate validation (set when user blurs Trip ID / Seal Code)
  String? _tripIdDuplicateError;
  String? _sealCodeDuplicateError;

  final GlobalKey _runsheetSectionKey = GlobalKey();

  List<HubDoc> _allHubs = [];

  /// SOC standby (รหัส 0XXX) ไม่แสดงใน dropdown ต้นทาง/ปลายทาง
  static bool _isSocStandby(HubDoc h) =>
      h.stationType == stationTypeSoc && h.sourceId.trim().startsWith('0');

  /// FM: ต้นทาง = HUB, ปลายทาง = SOC เท่านั้น | LH: ต้นทาง = SOC, ปลายทาง = HUB เท่านั้น (ไม่รวม SOC standby 0XXX)
  List<HubDoc> get _originOptions => (_jobType == jobTypeFirstMile)
      ? _allHubs.where((h) => h.stationType == stationTypeHub).toList()
      : _allHubs
            .where((h) => h.stationType == stationTypeSoc && !_isSocStandby(h))
            .toList();
  List<HubDoc> get _destinationOptions => (_jobType == jobTypeFirstMile)
      ? _allHubs
            .where((h) => h.stationType == stationTypeSoc && !_isSocStandby(h))
            .toList()
      : _allHubs.where((h) => h.stationType == stationTypeHub).toList();

  double? _lat;
  double? _lng;
  bool _locationLoading = true;

  /// Cache position + overlay สำหรับรูป step 2, 3 — ไม่หน่วงซ้ำ
  Position? _cachedOverlayPosition;
  OverlayContext? _cachedOverlayContext;

  Timer? _draftSaveTimer;
  static const Duration _draftDebounce = Duration(milliseconds: 800);

  @override
  void initState() {
    super.initState();
    // Default seal time = now (date + time)
    final now = DateTime.now();
    _sealTimeController.text = intl.DateFormat(
      'dd-MM-yyyy HH:mm:ss',
    ).format(now);
    _loadHubs();
    _loadLocation();
    _tripIdController.addListener(_clearTripIdDuplicateError);
    _sealCodeController.addListener(_clearSealCodeDuplicateError);
    _tripIdController.addListener(_scheduleSaveDraft);
    _sealCodeController.addListener(_scheduleSaveDraft);
    _originController.addListener(_scheduleSaveDraft);
    _destinationController.addListener(_scheduleSaveDraft);
    _parcelCountController.addListener(_scheduleSaveDraft);
    _sealTimeController.addListener(_scheduleSaveDraft);
    _totalWeightController.addListener(_scheduleSaveDraft);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _tryRestoreLoadingDraft();
      _lockJobTypeByActiveTask();
    });
  }

  Future<void> _lockJobTypeByActiveTask() async {
    final activeTaskId = await DraftStorageService.instance
        .loadActiveCheckInTaskId();
    if (activeTaskId == null) return;

    try {
      final taskDoc = await FirebaseFirestore.instance
          .collection('tasks')
          .doc(activeTaskId)
          .get();
      if (!taskDoc.exists) return;
      final data = taskDoc.data();
      final taskType = data?['taskType'] as String?;
      if (taskType != null && mounted) {
        setState(() {
          _jobType = (taskType == 'LINE_HAUL')
              ? jobTypeLineHaul
              : jobTypeFirstMile;
        });
      }
    } catch (e) {
      debugPrint('Error locking job type: $e');
    }
  }

  void _scheduleSaveDraft() {
    _draftSaveTimer?.cancel();
    _draftSaveTimer = Timer(_draftDebounce, _saveLoadingDraft);
  }

  Future<void> _saveLoadingDraft() async {
    if (!mounted) return;
    await DraftStorageService.instance.saveLoadingDraft(
      tripId: _tripIdController.text,
      sealCode: _sealCodeController.text,
      origin: _originController.text,
      destination: _destinationController.text,
      distance: '', // ระยะทางดึงจาก hub_soc_distances เท่านั้น ไม่มีช่องในฟอร์ม
      parcelCount: _parcelCountController.text,
      sealTime: _sealTimeController.text,
      totalWeight: _totalWeightController.text,
      jobType: _jobType,
      runsheetPhoto: _runsheetPhoto,
      stepPhotos: _stepPhotos.isNotEmpty ? _stepPhotos : null,
    );
  }

  Future<void> _tryRestoreLoadingDraft() async {
    final draft = await DraftStorageService.instance.loadLoadingDraft();
    if (draft == null || !mounted) return;
    final hasData =
        draft.tripId.isNotEmpty ||
        draft.sealCode.isNotEmpty ||
        draft.runsheetPath != null ||
        draft.stepPhotoPaths.isNotEmpty;
    if (!hasData) return;
    final confirm = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text('draft_restore_title'.tr()),
        content: Text('draft_restore_loading_message'.tr()),
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
    if (!mounted) return;
    if (confirm != true) {
      DraftStorageService.instance.clearLoadingDraft();
      return;
    }
    _tripIdController.text = draft.tripId;
    _sealCodeController.text = draft.sealCode;
    _originController.text = draft.origin;
    _destinationController.text = draft.destination;
    _parcelCountController.text = draft.parcelCount;
    _sealTimeController.text = draft.sealTime;
    _totalWeightController.text = draft.totalWeight;
    if (draft.jobType != null) _jobType = draft.jobType;
    if (draft.runsheetPath != null) {
      final bytes = await DraftStorageService.instance.loadLoadingDraftPhoto(
        draft.runsheetPath!,
      );
      if (bytes != null && mounted) _runsheetPhoto = Uint8List.fromList(bytes);
    }
    final stepPhotos = <String, Uint8List>{};
    for (final e in draft.stepPhotoPaths.entries) {
      final bytes = await DraftStorageService.instance.loadLoadingDraftPhoto(
        e.value,
      );
      if (bytes != null) stepPhotos[e.key] = Uint8List.fromList(bytes);
    }
    if (mounted) setState(() => _stepPhotos.addAll(stepPhotos));
  }

  void _clearTripIdDuplicateError() {
    if (_tripIdDuplicateError != null && mounted) {
      setState(() => _tripIdDuplicateError = null);
    }
  }

  void _clearSealCodeDuplicateError() {
    if (_sealCodeDuplicateError != null && mounted) {
      setState(() => _sealCodeDuplicateError = null);
    }
  }

  Future<void> _loadHubs() async {
    try {
      final hubs = await fetchAllHubs();
      if (mounted) setState(() => _allHubs = hubs);
    } catch (e) {
      debugPrint('Failed to load hubs: $e');
    }
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

  @override
  void dispose() {
    _draftSaveTimer?.cancel();
    _tripIdController.removeListener(_clearTripIdDuplicateError);
    _sealCodeController.removeListener(_clearSealCodeDuplicateError);
    _tripIdController.removeListener(_scheduleSaveDraft);
    _sealCodeController.removeListener(_scheduleSaveDraft);
    _originController.removeListener(_scheduleSaveDraft);
    _destinationController.removeListener(_scheduleSaveDraft);
    _parcelCountController.removeListener(_scheduleSaveDraft);
    _sealTimeController.removeListener(_scheduleSaveDraft);
    _totalWeightController.removeListener(_scheduleSaveDraft);
    _tripIdController.dispose();
    _sealCodeController.dispose();
    _originController.dispose();
    _destinationController.dispose();
    _parcelCountController.dispose();
    _sealTimeController.dispose();
    _totalWeightController.dispose();
    super.dispose();
  }

  Future<void> _scanAndSet(TextEditingController controller) async {
    if (kIsWeb) return;
    final value = await Navigator.of(
      context,
    ).push<String>(MaterialPageRoute(builder: (_) => const QrScanPage()));
    if (value != null && mounted) {
      if (controller == _tripIdController) {
        final match = RegExp(r'LT[A-Za-z0-9\-]{8,}').firstMatch(value);
        controller.text = match?.group(0) ?? value;
      } else if (controller == _sealCodeController) {
        final match = RegExp(r'SPX[A-Za-z0-9\-]{5,}').firstMatch(value);
        controller.text = match?.group(0) ?? value;
      } else {
        controller.text = value;
      }
    }
  }

  /// แนบรันชีทจาก gallery แล้วรัน OCR สกัด Trip ID/Seal/ต้นทาง-ปลายทาง เติมฟอร์ม
  Future<void> _pickRunsheetAndOcr() async {
    final picker = ImagePicker();
    final xfile = await picker.pickImage(
      source: ImageSource.gallery,
      imageQuality: 85,
    );
    if (xfile == null || !mounted) return;
    final imageBytes = await xfile.readAsBytes();
    if (!mounted) return;

    setState(() => _ocrLoading = true);
    try {
      final result = await runOcrOnImageBytes(imageBytes);
      if (!mounted) return;
      final compressed = await compressImageForUpload(imageBytes);
      if (!mounted) return;
      setState(() {
        _runsheetPhoto = compressed;
        _ocrLoading = false;
        if (result.tripId != null) _tripIdController.text = result.tripId!;
        if (result.sealCode != null) {
          _sealCodeController.text = result.sealCode!;
        }
        if (result.parcelCount != null) {
          _parcelCountController.text = result.parcelCount!;
        }
        if (result.sealTime != null) {
          _sealTimeController.text = result.sealTime!;
        }
        if (result.totalWeight != null) {
          _totalWeightController.text = result.totalWeight!;
        }
      });
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text('loading_phase_ocr_done'.tr())));
      final ocrTripId = result.tripId?.trim();
      if (ocrTripId != null && ocrTripId.isNotEmpty) {
        final ocrSeal = result.sealCode?.trim();
        final duplicate = await checkDuplicateTripIdAndSeal(
          tripId: ocrTripId,
          sealCode: (ocrSeal != null && ocrSeal.isNotEmpty) ? ocrSeal : null,
        );
        if (duplicate.hasDuplicate && mounted) {
          setState(() {
            _tripIdDuplicateError = duplicate.tripIdExists
                ? 'loading_phase_duplicate_trip_id'.tr()
                : null;
            _sealCodeDuplicateError = duplicate.sealCodeExists
                ? 'loading_phase_duplicate_seal_code'.tr()
                : null;
          });
          final msg = duplicate.tripIdExists && duplicate.sealCodeExists
              ? 'loading_phase_duplicate_trip_and_seal'.tr()
              : duplicate.tripIdExists
              ? 'loading_phase_duplicate_trip_id'.tr()
              : 'loading_phase_duplicate_seal_code'.tr();
          ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(content: Text(msg), backgroundColor: Colors.orange),
          );
          _scrollToRunsheetSection();
        }
      }
      if (mounted) _saveLoadingDraft();
    } catch (e) {
      if (mounted) {
        setState(() => _ocrLoading = false);
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text('${'loading_phase_ocr_failed'.tr()} $e'),
            backgroundColor: Colors.red,
          ),
        );
      }
    }
  }

  /// ถ่ายภาพขั้นตอน: stamp overlay (วันเวลา สถานที่ พิกัด เข็มทิศ) + compress — รูป 2, 3 reuse cache
  Future<void> _takeStepPhoto(String stepKey) async {
    if (kIsWeb) return;
    final picker = ImagePicker();
    final xfile = await picker.pickImage(
      source: ImageSource.camera,
      imageQuality: 85,
    );
    if (xfile == null || !mounted) return;
    final imageBytes = await xfile.readAsBytes();
    if (!mounted) return;
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
    final compressed = await stampOverlayAndCompressForEvidence(
      imageBytes,
      position: _cachedOverlayPosition,
      overlayContext: _cachedOverlayContext,
    );
    if (!mounted) return;
    setState(() => _stepPhotos[stepKey] = compressed);
    _saveLoadingDraft();
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(SnackBar(content: Text('loading_phase_photo_stamped'.tr())));
  }

  bool get _hasDuplicateError =>
      _tripIdDuplicateError != null || _sealCodeDuplicateError != null;

  Future<void> _showPreviewAndSubmit() async {
    if (!_formKey.currentState!.validate()) return;
    final tripId = _tripIdController.text.trim();
    if (tripId.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('loading_phase_trip_id_required'.tr()),
          backgroundColor: Colors.orange,
        ),
      );
      return;
    }
    if (_hasDuplicateError) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('loading_phase_fix_duplicate_to_save'.tr()),
          backgroundColor: Colors.red,
        ),
      );
      _scrollToRunsheetSection();
      return;
    }
    if (_runsheetPhoto == null ||
        _stepPhotos.length != _cameraPhotoStepKeys.length) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('loading_phase_photos_required'.tr()),
          backgroundColor: Colors.orange,
        ),
      );
      return;
    }

    // FM: ต้นทาง=HUB ปลายทาง=SOC | LH: ต้นทาง=SOC ปลายทาง=HUB
    final originName = _originController.text.trim();
    final destName = _destinationController.text.trim();
    HubDoc? originHub;
    HubDoc? destHub;
    for (final h in _allHubs) {
      if (h.sourceNameEn == originName) originHub = h;
      if (h.sourceNameEn == destName) destHub = h;
    }
    final isFm = _jobType == jobTypeFirstMile;
    if (originName.isEmpty || destName.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('loading_phase_origin_dest_required'.tr()),
          backgroundColor: Colors.orange,
        ),
      );
      return;
    }
    if (originHub == null || destHub == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('loading_phase_origin_dest_required'.tr()),
          backgroundColor: Colors.orange,
        ),
      );
      return;
    }
    final originOk = isFm
        ? originHub.stationType == stationTypeHub
        : originHub.stationType == stationTypeSoc;
    final destOk = isFm
        ? destHub.stationType == stationTypeSoc
        : destHub.stationType == stationTypeHub;
    if (!originOk || !destOk) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(
            isFm
                ? 'loading_phase_origin_dest_rule_fm'.tr()
                : 'loading_phase_origin_dest_rule_lh'.tr(),
          ),
          backgroundColor: Colors.orange,
        ),
      );
      return;
    }

    // ปิด keyboard ก่อนเปิด preview เพื่อไม่ให้ numeric pad โผล่เมื่อปิด sheet
    FocusScope.of(context).unfocus();

    // ดึงระยะทาง/เวลาเดินทางจาก hub_soc_distances ตามต้นทาง-ปลายทางที่เลือก
    HubSocDistanceResult? hubSocResult;
    if (originName.isNotEmpty && destName.isNotEmpty) {
      hubSocResult = await fetchHubSocDistance(
        originSourceId: originHub.sourceId,
        destinationSourceId: destHub.sourceId,
        originStationType: originHub.stationType,
        destinationStationType: destHub.stationType,
      );
    }
    if (!mounted) return;
    final distanceDisplay = hubSocResult != null
        ? '${hubSocResult.distanceKm == hubSocResult.distanceKm.roundToDouble() ? hubSocResult.distanceKm.round() : hubSocResult.distanceKm.toStringAsFixed(2)} km'
        : '-';
    String staDisplay = '';
    if (hubSocResult != null) {
      final sealStr = _sealTimeController.text.trim();
      if (sealStr.isNotEmpty) {
        try {
          final seal = intl.DateFormat('dd-MM-yyyy HH:mm:ss').parse(sealStr);
          final sta = seal.add(
            Duration(minutes: hubSocResult.durationMinutes.round()),
          );
          staDisplay = intl.DateFormat('dd-MM-yyyy HH:mm:ss').format(sta);
        } catch (_) {}
      }
    }

    final confirmed = await showModalBottomSheet<bool>(
      context: context,
      isScrollControlled: true,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (ctx) => _PreviewSheet(
        jobType: _jobType ?? jobTypeFirstMile,
        tripId: tripId,
        sealCode: _sealCodeController.text.trim(),
        origin: originName,
        destination: destName,
        distance: distanceDisplay.isEmpty ? '-' : distanceDisplay,
        staDisplay: staDisplay.isEmpty ? '-' : staDisplay,
        parcelCount: _parcelCountController.text.trim(),
        sealTime: _sealTimeController.text.trim(),
        totalWeight: _totalWeightController.text.trim(),
        coordination: _lat != null
            ? '${_lat!.toStringAsFixed(6)}, ${_lng!.toStringAsFixed(6)}'
            : '-',
        timestamp: intl.DateFormat(
          'dd-MM-yyyy HH:mm:ss',
        ).format(DateTime.now()),
        runsheetPhoto: _runsheetPhoto!,
        stepPhotos: _stepPhotos,
      ),
    );

    if (confirmed == true) {
      // ปิด focus อีกครั้งหลังปิด sheet เพื่อไม่ให้แป้นตัวเลขโผล่ระหว่าง/หลัง save
      if (mounted) FocusScope.of(context).unfocus();
      _doSubmit();
    }
  }

  /// Validate duplicate when user leaves Trip ID or Seal Code field.
  Future<void> _validateDuplicateOnBlur() async {
    final tripId = _tripIdController.text.trim();
    if (tripId.isEmpty) {
      if (mounted) {
        setState(() {
          _tripIdDuplicateError = null;
          _sealCodeDuplicateError = null;
        });
      }
      return;
    }
    final sealCode = _sealCodeController.text.trim();
    final duplicate = await checkDuplicateTripIdAndSeal(
      tripId: tripId,
      sealCode: sealCode.isEmpty ? null : sealCode,
    );
    if (!mounted) return;
    setState(() {
      _tripIdDuplicateError = duplicate.tripIdExists
          ? 'loading_phase_duplicate_trip_id'.tr()
          : null;
      _sealCodeDuplicateError = duplicate.sealCodeExists
          ? 'loading_phase_duplicate_seal_code'.tr()
          : null;
    });
    _scrollToRunsheetSection();
  }

  /// เลื่อนหน้าจอไปที่ส่วนรันชีท (เมื่อตรวจพบซ้ำ)
  void _scrollToRunsheetSection() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      final ctx = _runsheetSectionKey.currentContext;
      if (ctx != null) {
        Scrollable.ensureVisible(
          ctx,
          duration: const Duration(milliseconds: 400),
          curve: Curves.easeInOut,
          alignment: 0.2,
        );
      }
    });
  }

  /// ยืนยันก่อนล้างฟอร์ม (เรียกจากปุ่ม Clear form)
  Future<void> _confirmClearForm() async {
    final confirm = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text('loading_phase_clear_form'.tr()),
        content: Text('loading_phase_clear_form_confirm'.tr()),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: Text(MaterialLocalizations.of(ctx).cancelButtonLabel),
          ),
          FilledButton(
            onPressed: () => Navigator.of(ctx).pop(true),
            child: Text('loading_phase_clear_form'.tr()),
          ),
        ],
      ),
    );
    if (confirm == true && mounted) _clearForm();
  }

  /// ล้างฟอร์มและรูปทั้งหมด หลัง save สำเร็จ
  void _clearForm() {
    _tripIdController.clear();
    _sealCodeController.clear();
    _originController.clear();
    _destinationController.clear();
    _parcelCountController.clear();
    _totalWeightController.clear();
    final now = DateTime.now();
    _sealTimeController.text = intl.DateFormat(
      'dd-MM-yyyy HH:mm:ss',
    ).format(now);
    setState(() {
      _jobType = jobTypeFirstMile;
      _runsheetPhoto = null;
      _stepPhotos.clear();
      _tripIdDuplicateError = null;
      _sealCodeDuplicateError = null;
    });
    DraftStorageService.instance.clearLoadingDraft();
  }

  /// Save ช้าเพราะ: (1) getCurrentPosition (2) fetchOverlayContext (3) วน overlay 3 รูป (4) อัปโหลด 4 รูป + บันทึก Firestore
  Future<void> _doSubmit() async {
    setState(() => _saving = true);
    try {
      final tripId = _tripIdController.text.trim();
      final sealCode = _sealCodeController.text.trim();

      final duplicate = await checkDuplicateTripIdAndSeal(
        tripId: tripId,
        sealCode: sealCode.isEmpty ? null : sealCode,
      );
      if (duplicate.hasDuplicate && mounted) {
        setState(() => _saving = false);
        String msg;
        if (duplicate.tripIdExists && duplicate.sealCodeExists) {
          msg = 'loading_phase_duplicate_trip_and_seal'.tr();
        } else if (duplicate.tripIdExists) {
          msg = 'loading_phase_duplicate_trip_id'.tr();
        } else {
          msg = 'loading_phase_duplicate_seal_code'.tr();
        }
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(msg), backgroundColor: Colors.red),
        );
        return;
      }

      final position = await getCurrentPosition();
      final timestamp = DateTime.now();

      final allStepPhotos = <String, StampedPhotoInput>{};
      // รูปขั้นตอน (pre_close, closing, seal) มี overlay stamp ไว้แล้วตอนถ่าย — ใช้ตรงๆ
      for (final key in _cameraPhotoStepKeys) {
        allStepPhotos[key] = StampedPhotoInput(
          bytes: _stepPhotos[key]!,
          lat: position.latitude,
          lng: position.longitude,
          timestamp: timestamp,
        );
      }

      allStepPhotos['runsheet'] = StampedPhotoInput(
        bytes: _runsheetPhoto!,
        lat: position.latitude,
        lng: position.longitude,
        timestamp: timestamp,
      );

      final parcelText = _parcelCountController.text.trim();
      final parcelCount = parcelText.isNotEmpty
          ? int.tryParse(parcelText)
          : null;

      // ดึงระยะทาง/เวลาเดินทางจาก hub_soc_distances สำหรับบันทึก trip_records (STD/STA/ระยะทาง)
      final originName = _originController.text.trim();
      final destName = _destinationController.text.trim();
      HubDoc? originHub;
      HubDoc? destHub;
      for (final h in _allHubs) {
        if (h.sourceNameEn == originName) originHub = h;
        if (h.sourceNameEn == destName) destHub = h;
      }
      HubSocDistanceResult? hubSocResult;
      if (originName.isNotEmpty &&
          destName.isNotEmpty &&
          originHub != null &&
          destHub != null) {
        hubSocResult = await fetchHubSocDistance(
          originSourceId: originHub.sourceId,
          destinationSourceId: destHub.sourceId,
          originStationType: originHub.stationType,
          destinationStationType: destHub.stationType,
        );
      }

      final activeTaskId = await DraftStorageService.instance
          .loadActiveCheckInTaskId();

      await submitLoadingPhaseRecord(
        tripId: tripId,
        jobType: _jobType ?? jobTypeFirstMile,
        driverId: FirebaseAuth.instance.currentUser?.uid,
        taskId: activeTaskId,
        sealCode: _sealCodeController.text.trim().isEmpty
            ? null
            : _sealCodeController.text.trim(),
        origin: originName.isEmpty ? null : originName,
        destination: destName.isEmpty ? null : destName,
        distance: null,
        distanceFromHubSocKm: hubSocResult?.distanceKm,
        durationMinutes: hubSocResult?.durationMinutes,
        parcelCount: parcelCount,
        sealTime: _sealTimeController.text.trim().isEmpty
            ? null
            : _sealTimeController.text.trim(),
        totalWeight: _totalWeightController.text.trim().isEmpty
            ? null
            : _totalWeightController.text.trim(),
        lat: _lat,
        lng: _lng,
        stepPhotos: allStepPhotos,
        ocrData: TripOcrData(
          tripId: tripId,
          sealCode: sealCode.isEmpty ? null : sealCode,
        ),
      );
      if (!mounted) return;
      final origin = _originController.text.trim();
      final destination = _destinationController.text.trim();
      final jobType = _jobType ?? jobTypeFirstMile;

      setState(() => _saving = false);
      _clearForm();

      final summary = SavedTripSummary(
        tripId: tripId,
        origin: origin.isEmpty ? null : origin,
        destination: destination.isEmpty ? null : destination,
        sealCode: sealCode.isEmpty ? null : sealCode,
        jobType: jobType,
        taskId: activeTaskId,
      );

      // Clear the task ID from local storage since it is now safely recorded in the remote trip record
      await DraftStorageService.instance.clearActiveCheckInTaskId();

      final scope = MainLayoutScope.of(context);
      if (scope != null) {
        scope.goToDeliveryTab(summary);
      } else {
        Navigator.of(context).pushNamedAndRemoveUntil(
          '/home',
          (_) => false,
          arguments: {
            'tab': 2,
            'tripId': tripId,
            'origin': origin.isEmpty ? null : origin,
            'destination': destination.isEmpty ? null : destination,
            'sealCode': sealCode.isEmpty ? null : sealCode,
            'jobType': jobType,
          },
        );
      }

      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text('loading_phase_saved'.tr())));
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

  String _stepTitleKey(String stepKey) {
    switch (stepKey) {
      case 'pre_close':
        return 'loading_phase_photo_pre_close';
      case 'closing':
        return 'loading_phase_photo_closing';
      case 'seal':
        return 'loading_phase_photo_seal';
      default:
        return stepKey;
    }
  }

  String _stepDescKey(String stepKey) {
    switch (stepKey) {
      case 'pre_close':
        return 'loading_phase_photo_pre_close_desc';
      case 'closing':
        return 'loading_phase_photo_closing_desc';
      case 'seal':
        return 'loading_phase_photo_seal_desc';
      default:
        return stepKey;
    }
  }

  @override
  Widget build(BuildContext context) {
    return PopScope(
      canPop: !_saving,
      child: Stack(
        children: [
          Scaffold(
            appBar: AppBar(
              title: Text('loading_phase_form_title'.tr()),
              actions: [
                Padding(
                  padding: const EdgeInsets.only(right: 8),
                  child: TextButton.icon(
                    onPressed: _saving ? null : _confirmClearForm,
                    icon: const Icon(Icons.clear_all, size: 20),
                    label: Text('loading_phase_clear_form'.tr()),
                  ),
                ),
              ],
            ),
            body: Form(
              key: _formKey,
              child: AbsorbPointer(
                absorbing: _saving,
                child: SingleChildScrollView(
                  padding: const EdgeInsets.all(16),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      Text(
                        'loading_phase_form_subtitle'.tr(),
                        style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                          color: Colors.grey[600],
                        ),
                      ),
                      const SizedBox(height: 20),

                      // ========== STEP 1: ประเภทงาน ==========
                      _sectionTitle('loading_phase_job_type'.tr()),
                      const SizedBox(height: 8),
                      SegmentedButton<String>(
                        segments: [
                          ButtonSegment(
                            value: jobTypeFirstMile,
                            label: Text('loading_phase_first_mile'.tr()),
                            icon: const Icon(Icons.local_shipping, size: 20),
                          ),
                          ButtonSegment(
                            value: jobTypeLineHaul,
                            label: Text('loading_phase_line_haul'.tr()),
                            icon: const Icon(
                              Icons.directions_transit,
                              size: 20,
                            ),
                          ),
                        ],
                        selected: {_jobType ?? jobTypeFirstMile},
                        onSelectionChanged: (Set<String> selected) {
                          final newType = selected.first;
                          setState(() {
                            _jobType = newType;
                            // ล้างต้นทาง/ปลายทางเมื่อสลับประเภทงาน เพื่อให้เลือกตามกฎ FM/LH ใหม่
                            _originController.clear();
                            _destinationController.clear();
                          });
                        },
                      ),
                      const SizedBox(height: 24),

                      // ========== STEP 2: อัปโหลดรันชีท ==========
                      KeyedSubtree(
                        key: _runsheetSectionKey,
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.stretch,
                          children: [
                            _sectionTitle('loading_phase_photo_runsheet'.tr()),
                            Text(
                              'loading_phase_photo_runsheet_desc'.tr(),
                              style: Theme.of(context).textTheme.bodySmall
                                  ?.copyWith(color: Colors.grey[600]),
                            ),
                            const SizedBox(height: 8),
                            Card(
                              child: Padding(
                                padding: const EdgeInsets.all(12),
                                child: Column(
                                  crossAxisAlignment:
                                      CrossAxisAlignment.stretch,
                                  children: [
                                    if (_runsheetPhoto != null) ...[
                                      ClipRRect(
                                        borderRadius: BorderRadius.circular(8),
                                        child: Image.memory(
                                          _runsheetPhoto!,
                                          height: 150,
                                          width: double.infinity,
                                          fit: BoxFit.cover,
                                        ),
                                      ),
                                      const SizedBox(height: 8),
                                    ],
                                    if (_ocrLoading)
                                      const Padding(
                                        padding: EdgeInsets.symmetric(
                                          vertical: 12,
                                        ),
                                        child: Row(
                                          mainAxisAlignment:
                                              MainAxisAlignment.center,
                                          children: [
                                            SizedBox(
                                              width: 20,
                                              height: 20,
                                              child: CircularProgressIndicator(
                                                strokeWidth: 2,
                                              ),
                                            ),
                                            SizedBox(width: 12),
                                            Text('กำลังอ่านเอกสาร...'),
                                          ],
                                        ),
                                      ),
                                    OutlinedButton.icon(
                                      onPressed: _ocrLoading
                                          ? null
                                          : _pickRunsheetAndOcr,
                                      icon: const Icon(
                                        Icons.photo_library,
                                        size: 20,
                                      ),
                                      label: Text(
                                        _runsheetPhoto != null
                                            ? 'loading_phase_change_runsheet'
                                                  .tr()
                                            : 'loading_phase_upload_from_gallery'
                                                  .tr(),
                                      ),
                                    ),
                                  ],
                                ),
                              ),
                            ),
                          ],
                        ),
                      ),
                      const SizedBox(height: 24),

                      // ========== STEP 3-4: ข้อมูล ==========
                      _sectionTitle('loading_phase_trip_details'.tr()),
                      const SizedBox(height: 8),
                      TextFormField(
                        controller: _tripIdController,
                        readOnly: true,
                        decoration: InputDecoration(
                          labelText: 'loading_phase_trip_id'.tr(),
                          hintText: 'LTQ...',
                          border: const OutlineInputBorder(),
                          errorText: _tripIdDuplicateError,
                          suffixIcon: IconButton(
                            icon: const Icon(Icons.qr_code_scanner),
                            tooltip: 'loading_phase_scan_qr'.tr(),
                            onPressed: () => _scanAndSet(_tripIdController),
                          ),
                        ),
                        onTap: _validateDuplicateOnBlur,
                      ),
                      const SizedBox(height: 12),
                      TextFormField(
                        controller: _sealCodeController,
                        readOnly: true,
                        decoration: InputDecoration(
                          labelText: 'loading_phase_seal_code'.tr(),
                          hintText: 'SPX...',
                          border: const OutlineInputBorder(),
                          errorText: _sealCodeDuplicateError,
                          suffixIcon: IconButton(
                            icon: const Icon(Icons.qr_code_scanner),
                            tooltip: 'loading_phase_scan_qr'.tr(),
                            onPressed: () => _scanAndSet(_sealCodeController),
                          ),
                        ),
                        onTap: _validateDuplicateOnBlur,
                      ),
                      const SizedBox(height: 12),

                      // Origin / Destination — FM: ต้นทาง=HUB ปลายทาง=SOC | LH: ต้นทาง=SOC ปลายทาง=HUB
                      SearchableHubPicker(
                        label: 'loading_phase_origin'.tr(),
                        hintText: _jobType == jobTypeFirstMile
                            ? 'loading_phase_origin_hint_fm'.tr()
                            : 'loading_phase_origin_hint_lh'.tr(),
                        value: _originController.text,
                        hubs: _originOptions,
                        onSelected: (hub) {
                          setState(
                            () => _originController.text = hub.sourceNameEn,
                          );
                        },
                      ),
                      const SizedBox(height: 12),
                      SearchableHubPicker(
                        label: 'loading_phase_destination'.tr(),
                        hintText: _jobType == jobTypeFirstMile
                            ? 'loading_phase_destination_hint_fm'.tr()
                            : 'loading_phase_destination_hint_lh'.tr(),
                        value: _destinationController.text,
                        hubs: _destinationOptions,
                        onSelected: (hub) {
                          setState(
                            () =>
                                _destinationController.text = hub.sourceNameEn,
                          );
                        },
                      ),
                      const SizedBox(height: 12),

                      // Parcel Count
                      TextFormField(
                        controller: _parcelCountController,
                        decoration: InputDecoration(
                          labelText: 'loading_phase_parcel_count'.tr(),
                          hintText: 'e.g. 547',
                          border: const OutlineInputBorder(),
                          prefixIcon: const Icon(Icons.inventory_2),
                        ),
                        keyboardType: const TextInputType.numberWithOptions(
                          decimal: true,
                        ),
                      ),
                      const SizedBox(height: 12),

                      // Total Weight
                      TextFormField(
                        controller: _totalWeightController,
                        decoration: InputDecoration(
                          labelText: 'loading_phase_total_weight'.tr(),
                          hintText: 'e.g. 608.985 kg',
                          border: const OutlineInputBorder(),
                          prefixIcon: const Icon(Icons.scale),
                        ),
                        keyboardType: const TextInputType.numberWithOptions(
                          decimal: true,
                        ),
                      ),
                      const SizedBox(height: 12),

                      // Seal Time (default = now, date + time picker)
                      TextFormField(
                        controller: _sealTimeController,
                        decoration: InputDecoration(
                          labelText: 'loading_phase_seal_time'.tr(),
                          hintText: 'dd-MM-yyyy HH:mm:ss',
                          border: const OutlineInputBorder(),
                          prefixIcon: const Icon(Icons.lock_clock),
                        ),
                        onTap: () => _pickDateTime(_sealTimeController),
                        readOnly: true,
                      ),
                      const SizedBox(height: 16),

                      // Coordination (auto-fill, read-only)
                      Card(
                        child: Padding(
                          padding: const EdgeInsets.all(12),
                          child: _locationLoading
                              ? const Center(child: CircularProgressIndicator())
                              : Row(
                                  children: [
                                    const Icon(
                                      Icons.location_on,
                                      color: Colors.red,
                                      size: 20,
                                    ),
                                    const SizedBox(width: 8),
                                    Expanded(
                                      child: Text(
                                        _lat != null
                                            ? '${_lat!.toStringAsFixed(6)}, ${_lng!.toStringAsFixed(6)}'
                                            : '-',
                                        style: const TextStyle(
                                          fontWeight: FontWeight.w500,
                                        ),
                                      ),
                                    ),
                                    IconButton(
                                      icon: const Icon(Icons.refresh, size: 20),
                                      onPressed: () {
                                        setState(() => _locationLoading = true);
                                        _loadLocation();
                                      },
                                    ),
                                  ],
                                ),
                        ),
                      ),
                      const SizedBox(height: 24),

                      // ========== STEP 5: ถ่ายรูป 3 ขั้นตอน ==========
                      _sectionTitle('loading_phase_photos_step_title'.tr()),
                      Text(
                        'loading_phase_photos_step_subtitle'.tr(),
                        style: Theme.of(context).textTheme.bodySmall?.copyWith(
                          color: Colors.grey[600],
                        ),
                      ),
                      const SizedBox(height: 12),
                      ..._cameraPhotoStepKeys.map((stepKey) {
                        final photo = _stepPhotos[stepKey];
                        return Padding(
                          padding: const EdgeInsets.only(bottom: 16),
                          child: Card(
                            child: Padding(
                              padding: const EdgeInsets.all(12),
                              child: Column(
                                crossAxisAlignment: CrossAxisAlignment.stretch,
                                children: [
                                  Text(
                                    _stepTitleKey(stepKey).tr(),
                                    style: const TextStyle(
                                      fontWeight: FontWeight.w600,
                                    ),
                                  ),
                                  const SizedBox(height: 4),
                                  Text(
                                    _stepDescKey(stepKey).tr(),
                                    style: Theme.of(context).textTheme.bodySmall
                                        ?.copyWith(color: Colors.grey[600]),
                                  ),
                                  const SizedBox(height: 8),
                                  if (photo != null) ...[
                                    ClipRRect(
                                      borderRadius: BorderRadius.circular(8),
                                      child: Image.memory(
                                        photo,
                                        height: 120,
                                        width: double.infinity,
                                        fit: BoxFit.cover,
                                      ),
                                    ),
                                    const SizedBox(height: 8),
                                  ],
                                  OutlinedButton.icon(
                                    onPressed: () => _takeStepPhoto(stepKey),
                                    icon: const Icon(
                                      Icons.camera_alt,
                                      size: 20,
                                    ),
                                    label: Text(
                                      'loading_phase_take_photo'.tr(),
                                    ),
                                  ),
                                ],
                              ),
                            ),
                          ),
                        );
                      }),
                      const SizedBox(height: 24),

                      // ========== STEP 6: Preview & Submit ==========
                      FilledButton.icon(
                        onPressed: (_saving || _hasDuplicateError)
                            ? null
                            : _showPreviewAndSubmit,
                        icon: _saving
                            ? const SizedBox(
                                width: 20,
                                height: 20,
                                child: CircularProgressIndicator(
                                  strokeWidth: 2,
                                ),
                              )
                            : const Icon(Icons.preview),
                        label: Text(
                          _saving
                              ? 'loading_phase_saving'.tr()
                              : 'loading_phase_preview_and_save'.tr(),
                        ),
                        style: FilledButton.styleFrom(
                          padding: const EdgeInsets.symmetric(vertical: 16),
                        ),
                      ),
                    ],
                  ),
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
                        child: CircularProgressIndicator(strokeWidth: 2),
                      ),
                      const SizedBox(height: 16),
                      Text('loading_phase_saving'.tr()),
                    ],
                  ),
                ),
              ),
            ),
        ],
      ),
    );
  }

  Widget _sectionTitle(String text) {
    return Text(
      text,
      style: Theme.of(
        context,
      ).textTheme.titleSmall?.copyWith(fontWeight: FontWeight.w600),
    );
  }

  Future<void> _pickDateTime(TextEditingController controller) async {
    final now = DateTime.now();
    final date = await showDatePicker(
      context: context,
      initialDate: now,
      firstDate: DateTime(2020),
      lastDate: DateTime(2100),
    );
    if (date == null || !mounted) return;
    final time = await showTimePicker(
      context: context,
      initialTime: TimeOfDay.fromDateTime(now),
    );
    if (time != null && mounted) {
      final dt = DateTime(
        date.year,
        date.month,
        date.day,
        time.hour,
        time.minute,
      );
      controller.text = intl.DateFormat('dd-MM-yyyy HH:mm:ss').format(dt);
    }
  }
}

// ===== Preview Bottom Sheet =====

class _PreviewSheet extends StatelessWidget {
  final String jobType;
  final String tripId;
  final String sealCode;
  final String origin;
  final String destination;
  final String distance;

  /// STA = เวลาที่ใช้ในการเดินทาง (sealTime + durationMinutes จาก hub_soc_distances)
  final String staDisplay;
  final String parcelCount;
  final String sealTime;
  final String totalWeight;
  final String coordination;
  final String timestamp;
  final Uint8List runsheetPhoto;
  final Map<String, Uint8List> stepPhotos;

  const _PreviewSheet({
    required this.jobType,
    required this.tripId,
    required this.sealCode,
    required this.origin,
    required this.destination,
    required this.distance,
    required this.staDisplay,
    required this.parcelCount,
    required this.sealTime,
    required this.totalWeight,
    required this.coordination,
    required this.timestamp,
    required this.runsheetPhoto,
    required this.stepPhotos,
  });

  @override
  Widget build(BuildContext context) {
    return DraggableScrollableSheet(
      expand: false,
      initialChildSize: 0.85,
      maxChildSize: 0.95,
      minChildSize: 0.5,
      builder: (context, scrollController) {
        return Container(
          decoration: BoxDecoration(
            color: Theme.of(context).scaffoldBackgroundColor,
            borderRadius: const BorderRadius.vertical(top: Radius.circular(20)),
          ),
          child: Column(
            children: [
              Container(
                margin: const EdgeInsets.only(top: 12, bottom: 8),
                width: 40,
                height: 4,
                decoration: BoxDecoration(
                  color: Colors.grey[400],
                  borderRadius: BorderRadius.circular(2),
                ),
              ),
              Padding(
                padding: const EdgeInsets.symmetric(
                  horizontal: 16,
                  vertical: 4,
                ),
                child: Text(
                  'loading_phase_preview_title'.tr(),
                  style: Theme.of(context).textTheme.titleMedium?.copyWith(
                    fontWeight: FontWeight.bold,
                  ),
                ),
              ),
              const Divider(),
              Expanded(
                child: ListView(
                  controller: scrollController,
                  padding: const EdgeInsets.all(16),
                  children: [
                    // Data rows — skip if value is empty
                    ..._buildRows(),
                    const SizedBox(height: 12),
                    // Runsheet photo (tappable)
                    Text(
                      'loading_phase_photo_runsheet'.tr(),
                      style: const TextStyle(fontWeight: FontWeight.w600),
                    ),
                    const SizedBox(height: 8),
                    _buildPreviewThumb(
                      context,
                      runsheetPhoto,
                      height: 100,
                      allImages: [
                        runsheetPhoto,
                        ...stepPhotos.entries.map((e) => e.value),
                      ],
                      initialIndex: 0,
                    ),
                    const SizedBox(height: 12),
                    // Camera photos (tappable) — เลื่อนซ้าย/ขวาได้ใน full-screen
                    Text(
                      'loading_phase_photos_step_title'.tr(),
                      style: const TextStyle(fontWeight: FontWeight.w600),
                    ),
                    const SizedBox(height: 8),
                    Row(
                      children: stepPhotos.entries.toList().asMap().entries.map(
                        (entry) {
                          final i = entry.key;
                          final e = entry.value;
                          final allImages = [
                            runsheetPhoto,
                            ...stepPhotos.entries.map((x) => x.value),
                          ];
                          return Expanded(
                            child: Padding(
                              padding: const EdgeInsets.symmetric(
                                horizontal: 2,
                              ),
                              child: GestureDetector(
                                onTap: () => _showFullImage(
                                  context,
                                  allImages,
                                  initialIndex: 1 + i,
                                ),
                                child: ClipRRect(
                                  borderRadius: BorderRadius.circular(8),
                                  child: Image.memory(
                                    e.value,
                                    height: 80,
                                    fit: BoxFit.cover,
                                  ),
                                ),
                              ),
                            ),
                          );
                        },
                      ).toList(),
                    ),
                  ],
                ),
              ),
              const Divider(height: 1),
              Padding(
                padding: const EdgeInsets.all(16),
                child: Row(
                  children: [
                    Expanded(
                      child: OutlinedButton.icon(
                        onPressed: () => Navigator.of(context).pop(false),
                        icon: const Icon(Icons.edit),
                        label: Text('loading_phase_edit'.tr()),
                        style: OutlinedButton.styleFrom(
                          padding: const EdgeInsets.symmetric(vertical: 14),
                        ),
                      ),
                    ),
                    const SizedBox(width: 12),
                    Expanded(
                      child: FilledButton.icon(
                        onPressed: () => Navigator.of(context).pop(true),
                        icon: const Icon(Icons.check_circle),
                        label: Text('loading_phase_confirm'.tr()),
                        style: FilledButton.styleFrom(
                          padding: const EdgeInsets.symmetric(vertical: 14),
                        ),
                      ),
                    ),
                  ],
                ),
              ),
            ],
          ),
        );
      },
    );
  }

  /// Build data rows, skipping empty values
  List<Widget> _buildRows() {
    return [
      _row(
        'loading_phase_job_type'.tr(),
        jobType == jobTypeFirstMile ? 'First Mile' : 'Line Haul',
      ),
      _row('loading_phase_trip_id'.tr(), tripId),
      _row('loading_phase_seal_code'.tr(), sealCode),
      _row('loading_phase_origin'.tr(), origin),
      _row('loading_phase_destination'.tr(), destination),
      _row('loading_phase_distance'.tr(), distance),
      _row('loading_phase_sta'.tr(), staDisplay),
      _row('loading_phase_parcel_count'.tr(), parcelCount),
      _row('loading_phase_total_weight'.tr(), totalWeight),
      _row('loading_phase_seal_time'.tr(), sealTime),
      _row('loading_phase_coordination'.tr(), coordination),
      _row('loading_phase_timestamp'.tr(), timestamp),
    ];
  }

  Widget _row(String label, String value) {
    if (value.isEmpty || value == '-') return const SizedBox.shrink();
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 120,
            child: Text(
              label,
              style: const TextStyle(
                fontWeight: FontWeight.w500,
                color: Colors.grey,
              ),
            ),
          ),
          Expanded(
            child: Text(
              value,
              style: const TextStyle(fontWeight: FontWeight.w500),
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildPreviewThumb(
    BuildContext context,
    Uint8List imageBytes, {
    required double height,
    required List<Uint8List> allImages,
    required int initialIndex,
  }) {
    return GestureDetector(
      onTap: () =>
          _showFullImage(context, allImages, initialIndex: initialIndex),
      child: ClipRRect(
        borderRadius: BorderRadius.circular(8),
        child: Image.memory(imageBytes, height: height, fit: BoxFit.cover),
      ),
    );
  }

  /// Full-screen preview: เลื่อนซ้าย/ขวาดูรูปทั้งหมดได้, pinch-to-zoom
  void _showFullImage(
    BuildContext context,
    List<Uint8List> images, {
    int initialIndex = 0,
  }) {
    if (images.isEmpty) return;
    showDialog(
      context: context,
      builder: (ctx) => _FullScreenPreviewDialog(
        images: images,
        initialIndex: initialIndex.clamp(0, images.length - 1),
        onClose: () => Navigator.of(ctx).pop(),
      ),
    );
  }
}

/// Dialog แสดงรูปเต็มจอ — เลื่อนซ้าย/ขวาได้ตามรูปที่มี
class _FullScreenPreviewDialog extends StatefulWidget {
  final List<Uint8List> images;
  final int initialIndex;
  final VoidCallback onClose;

  const _FullScreenPreviewDialog({
    required this.images,
    required this.initialIndex,
    required this.onClose,
  });

  @override
  State<_FullScreenPreviewDialog> createState() =>
      _FullScreenPreviewDialogState();
}

class _FullScreenPreviewDialogState extends State<_FullScreenPreviewDialog> {
  late PageController _pageController;
  late int _currentIndex;

  @override
  void initState() {
    super.initState();
    _pageController = PageController(initialPage: widget.initialIndex);
    _currentIndex = widget.initialIndex;
    _pageController.addListener(_onPageChanged);
  }

  void _onPageChanged() {
    final page = _pageController.page?.round() ?? _currentIndex;
    if (page != _currentIndex && mounted) {
      setState(() => _currentIndex = page);
    }
  }

  @override
  void dispose() {
    _pageController.removeListener(_onPageChanged);
    _pageController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Dialog(
      backgroundColor: Colors.black,
      insetPadding: const EdgeInsets.all(16),
      child: Stack(
        children: [
          PageView.builder(
            controller: _pageController,
            itemCount: widget.images.length,
            itemBuilder: (context, index) => InteractiveViewer(
              child: Image.memory(widget.images[index], fit: BoxFit.contain),
            ),
          ),
          Positioned(
            top: 8,
            right: 8,
            child: IconButton(
              icon: const Icon(Icons.close, color: Colors.white, size: 28),
              onPressed: widget.onClose,
            ),
          ),
          if (widget.images.length > 1)
            Positioned(
              bottom: 24,
              left: 0,
              right: 0,
              child: Center(
                child: Container(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 12,
                    vertical: 6,
                  ),
                  decoration: BoxDecoration(
                    color: Colors.black54,
                    borderRadius: BorderRadius.circular(20),
                  ),
                  child: Text(
                    '${_currentIndex + 1} / ${widget.images.length}',
                    style: const TextStyle(color: Colors.white, fontSize: 14),
                  ),
                ),
              ),
            ),
        ],
      ),
    );
  }
}
