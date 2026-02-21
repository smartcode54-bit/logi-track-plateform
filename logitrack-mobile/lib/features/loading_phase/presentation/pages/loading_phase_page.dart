import 'dart:typed_data';
import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/material.dart';
import 'package:image_picker/image_picker.dart';
import 'package:intl/intl.dart' as intl;
import '../../../home/data/repositories/first_mile_checkin_repository.dart';
import '../../../home/data/repositories/hubs_repository.dart';
import '../../../home/data/repositories/trip_records_repository.dart';
import '../../../home/data/services/image_compression_service.dart';
import '../../../home/data/services/photo_overlay_service.dart';
import '../../../home/presentation/pages/main_layout_scope.dart';
import '../../../home/presentation/pages/qr_scan_page.dart';
import '../../data/repositories/loading_trip_repository.dart';

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
  final _distanceController = TextEditingController();
  final _parcelCountController = TextEditingController();
  final _sealTimeController = TextEditingController();
  final _totalWeightController = TextEditingController();

  String? _jobType = jobTypeFirstMile;

  Uint8List? _runsheetPhoto;
  final Map<String, Uint8List> _stepPhotos = {};

  bool _saving = false;

  /// Inline duplicate validation (set when user blurs Trip ID / Seal Code)
  String? _tripIdDuplicateError;
  String? _sealCodeDuplicateError;

  final GlobalKey _runsheetSectionKey = GlobalKey();

  List<HubDoc> _allHubs = [];

  double? _lat;
  double? _lng;
  bool _locationLoading = true;

  @override
  void initState() {
    super.initState();
    // Default seal time = now (date + time)
    final now = DateTime.now();
    _sealTimeController.text = intl.DateFormat('yyyy-MM-dd HH:mm').format(now);
    _loadHubs();
    _loadLocation();
    _tripIdController.addListener(_clearTripIdDuplicateError);
    _sealCodeController.addListener(_clearSealCodeDuplicateError);
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
    _tripIdController.removeListener(_clearTripIdDuplicateError);
    _sealCodeController.removeListener(_clearSealCodeDuplicateError);
    _tripIdController.dispose();
    _sealCodeController.dispose();
    _originController.dispose();
    _destinationController.dispose();
    _distanceController.dispose();
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
    if (value != null && mounted) controller.text = value;
  }

  /// แนบรันชีทจาก gallery (ไม่อ่าน OCR) นำภาพไปเก็บใน tripRecording
  Future<void> _pickRunsheetOnly() async {
    final picker = ImagePicker();
    final xfile = await picker.pickImage(
      source: ImageSource.gallery,
      imageQuality: 85,
    );
    if (xfile == null || !mounted) return;
    final imageBytes = await xfile.readAsBytes();
    if (!mounted) return;
    final compressed = await compressImageForUpload(imageBytes);
    if (!mounted) return;
    setState(() => _runsheetPhoto = compressed);
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text('loading_phase_runsheet_added'.tr())),
    );
  }

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
    final compressed = await compressImageForUpload(imageBytes);
    if (!mounted) return;
    setState(() => _stepPhotos[stepKey] = compressed);
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

    // ปิด keyboard ก่อนเปิด preview เพื่อไม่ให้ numeric pad โผล่เมื่อปิด sheet
    FocusScope.of(context).unfocus();

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
        origin: _originController.text.trim(),
        destination: _destinationController.text.trim(),
        distance: _distanceController.text.trim(),
        parcelCount: _parcelCountController.text.trim(),
        sealTime: _sealTimeController.text.trim(),
        totalWeight: _totalWeightController.text.trim(),
        coordination: _lat != null
            ? '${_lat!.toStringAsFixed(6)}, ${_lng!.toStringAsFixed(6)}'
            : '-',
        timestamp: intl.DateFormat(
          'yyyy-MM-dd HH:mm:ss',
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
      if (mounted) setState(() {
        _tripIdDuplicateError = null;
        _sealCodeDuplicateError = null;
      });
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
    _distanceController.clear();
    _parcelCountController.clear();
    _totalWeightController.clear();
    final now = DateTime.now();
    _sealTimeController.text = intl.DateFormat('yyyy-MM-dd HH:mm').format(now);
    setState(() {
      _jobType = jobTypeFirstMile;
      _runsheetPhoto = null;
      _stepPhotos.clear();
      _tripIdDuplicateError = null;
      _sealCodeDuplicateError = null;
    });
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

      // Pre-fetch address + compass ONCE
      final overlayCtx = await fetchOverlayContext(
        position.latitude,
        position.longitude,
      );

      final allStepPhotos = <String, StampedPhotoInput>{};

      // Stamp step photos in parallel to reduce save time
      final stampedEntries = await Future.wait(
        _cameraPhotoStepKeys.map((key) async {
          final rawBytes = _stepPhotos[key]!;
          final stampedBytes = await overlayGeocodingAndTimestamp(
            imageBytes: rawBytes,
            lat: position.latitude,
            lng: position.longitude,
            timestamp: timestamp,
            ctx: overlayCtx,
          );
          return MapEntry(
            key,
            StampedPhotoInput(
              bytes: stampedBytes,
              lat: position.latitude,
              lng: position.longitude,
              timestamp: timestamp,
            ),
          );
        }),
      );
      for (final e in stampedEntries) {
        allStepPhotos[e.key] = e.value;
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

      await submitLoadingPhaseRecord(
        tripId: tripId,
        jobType: _jobType ?? jobTypeFirstMile,
        sealCode: _sealCodeController.text.trim().isEmpty
            ? null
            : _sealCodeController.text.trim(),
        origin: _originController.text.trim().isEmpty
            ? null
            : _originController.text.trim(),
        destination: _destinationController.text.trim().isEmpty
            ? null
            : _destinationController.text.trim(),
        distance: _distanceController.text.trim().isEmpty
            ? null
            : _distanceController.text.trim(),
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
      );

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
    return Scaffold(
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
        child: SingleChildScrollView(
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Text(
                'loading_phase_form_subtitle'.tr(),
                style: Theme.of(
                  context,
                ).textTheme.bodyMedium?.copyWith(color: Colors.grey[600]),
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
                    icon: const Icon(Icons.directions_transit, size: 20),
                  ),
                ],
                selected: {_jobType ?? jobTypeFirstMile},
                onSelectionChanged: (Set<String> selected) =>
                    setState(() => _jobType = selected.first),
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
                      style: Theme.of(
                        context,
                      ).textTheme.bodySmall?.copyWith(color: Colors.grey[600]),
                    ),
                    const SizedBox(height: 8),
                    Card(
                      child: Padding(
                        padding: const EdgeInsets.all(12),
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.stretch,
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
                            OutlinedButton.icon(
                              onPressed: _pickRunsheetOnly,
                              icon: const Icon(Icons.photo_library, size: 20),
                              label: Text(
                                _runsheetPhoto != null
                                    ? 'loading_phase_change_runsheet'.tr()
                                    : 'loading_phase_upload_from_gallery'.tr(),
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

              // Origin / Destination dropdowns
              DropdownMenu<HubDoc>(
                controller: _originController,
                expandedInsets: EdgeInsets.zero,
                label: Text('loading_phase_origin'.tr()),
                hintText: 'loading_phase_origin_hint'.tr(),
                enableSearch: true,
                enableFilter: true,
                inputDecorationTheme: const InputDecorationTheme(
                  border: OutlineInputBorder(),
                ),
                onSelected: (hub) {
                  if (hub != null) _originController.text = hub.sourceNameEn;
                },
                dropdownMenuEntries: _allHubs
                    .map(
                      (h) => DropdownMenuEntry<HubDoc>(
                        value: h,
                        label: '${h.sourceNameEn} (${h.sourceId})',
                      ),
                    )
                    .toList(),
              ),
              const SizedBox(height: 12),
              DropdownMenu<HubDoc>(
                controller: _destinationController,
                expandedInsets: EdgeInsets.zero,
                label: Text('loading_phase_destination'.tr()),
                hintText: 'loading_phase_destination_hint'.tr(),
                enableSearch: true,
                enableFilter: true,
                inputDecorationTheme: const InputDecorationTheme(
                  border: OutlineInputBorder(),
                ),
                onSelected: (hub) {
                  if (hub != null) {
                    _destinationController.text = hub.sourceNameEn;
                  }
                },
                dropdownMenuEntries: _allHubs
                    .map(
                      (h) => DropdownMenuEntry<HubDoc>(
                        value: h,
                        label: '${h.sourceNameEn} (${h.sourceId})',
                      ),
                    )
                    .toList(),
              ),
              const SizedBox(height: 12),

              // Distance
              TextFormField(
                controller: _distanceController,
                decoration: InputDecoration(
                  labelText: 'loading_phase_distance'.tr(),
                  hintText: 'e.g. 120 km',
                  border: const OutlineInputBorder(),
                  prefixIcon: const Icon(Icons.straighten),
                ),
                keyboardType: const TextInputType.numberWithOptions(
                  decimal: true,
                ),
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
                  hintText: 'yyyy-MM-dd HH:mm',
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
                style: Theme.of(
                  context,
                ).textTheme.bodySmall?.copyWith(color: Colors.grey[600]),
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
                            style: const TextStyle(fontWeight: FontWeight.w600),
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
                            icon: const Icon(Icons.camera_alt, size: 20),
                            label: Text('loading_phase_take_photo'.tr()),
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
                onPressed: (_saving || _hasDuplicateError) ? null : _showPreviewAndSubmit,
                icon: _saving
                    ? const SizedBox(
                        width: 20,
                        height: 20,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Icon(Icons.preview),
                label: Text('loading_phase_preview_and_save'.tr()),
                style: FilledButton.styleFrom(
                  padding: const EdgeInsets.symmetric(vertical: 16),
                ),
              ),
            ],
          ),
        ),
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
      controller.text = intl.DateFormat('yyyy-MM-dd HH:mm').format(dt);
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
                    GestureDetector(
                      onTap: () => _showFullImage(context, runsheetPhoto),
                      child: ClipRRect(
                        borderRadius: BorderRadius.circular(8),
                        child: Image.memory(
                          runsheetPhoto,
                          height: 100,
                          fit: BoxFit.cover,
                        ),
                      ),
                    ),
                    const SizedBox(height: 12),
                    // Camera photos (tappable)
                    Text(
                      'loading_phase_photos_step_title'.tr(),
                      style: const TextStyle(fontWeight: FontWeight.w600),
                    ),
                    const SizedBox(height: 8),
                    Row(
                      children: stepPhotos.entries.map((e) {
                        return Expanded(
                          child: Padding(
                            padding: const EdgeInsets.symmetric(horizontal: 2),
                            child: GestureDetector(
                              onTap: () => _showFullImage(context, e.value),
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
                      }).toList(),
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

  /// Show full-screen image preview with pinch-to-zoom
  void _showFullImage(BuildContext context, Uint8List imageBytes) {
    showDialog(
      context: context,
      builder: (ctx) => Dialog(
        backgroundColor: Colors.black,
        insetPadding: const EdgeInsets.all(16),
        child: Stack(
          children: [
            InteractiveViewer(
              child: Image.memory(imageBytes, fit: BoxFit.contain),
            ),
            Positioned(
              top: 8,
              right: 8,
              child: IconButton(
                icon: const Icon(Icons.close, color: Colors.white, size: 28),
                onPressed: () => Navigator.of(ctx).pop(),
              ),
            ),
          ],
        ),
      ),
    );
  }
}
