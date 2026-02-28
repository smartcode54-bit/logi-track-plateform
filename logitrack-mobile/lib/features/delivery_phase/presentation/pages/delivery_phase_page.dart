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
import '../../../home/data/services/draft_storage_service.dart';
import '../../../home/data/services/image_compression_service.dart';
import '../../../home/data/services/ocr_screenshot_service.dart';
import '../../../home/presentation/pages/main_layout_scope.dart';
import 'incident_report_page.dart';

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

  /// Cache position + overlay (ที่อยู่, เข็มทิศ) สำหรับรูป step 2, 3 — ไม่หน่วงซ้ำ
  Position? _cachedOverlayPosition;
  OverlayContext? _cachedOverlayContext;

  bool get _canSubmit =>
      _deliveryPhotos.length == _deliveryPhotoStepKeys.length;

  @override
  void initState() {
    super.initState();
    _loadLocation();
    WidgetsBinding.instance.addPostFrameCallback(
      (_) => _tryRestoreDeliveryDraft(),
    );
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
    final expected = _normalizeTripId(widget.savedTripSummary?.tripId);
    if (expected.isEmpty) return true;
    if (kIsWeb) return true;
    try {
      final result = await runOcrOnImageBytes(imageBytes, imagePath: imagePath);
      final fromImage = _normalizeTripId(result.tripId);
      if (fromImage.isEmpty) {
        if (mounted && showDialogOnError) {
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
        return false;
      }
      if (fromImage != expected) {
        if (mounted && showDialogOnError) {
          await showDialog(
            context: context,
            builder: (ctx) => AlertDialog(
              title: Text('delivery_trip_id_mismatch_title'.tr()),
              content: Text(
                '${'delivery_trip_id_mismatch_message'.tr()}\n${'delivery_trip_id_expected'.tr()}: ${widget.savedTripSummary!.tripId}\n${'delivery_trip_id_in_image'.tr()}: ${result.tripId}',
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
        return false;
      }
      return true;
    } catch (_) {
      if (mounted) {
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

  /// ถ่ายภาพหรืออัปโหลดรูป: 3 ขั้นแรกเปิดกล้อง (ใช้ฟังก์ชันร่วม stamp overlay), ขั้นรันชีทอัปโหลดจากแกลเลอรี
  Future<void> _takeDeliveryPhoto(String stepKey) async {
    if (kIsWeb) return;
    final picker = ImagePicker();
    final isRunsheetReceived = stepKey == 'runsheet_received';
    final xfile = await picker.pickImage(
      source: isRunsheetReceived ? ImageSource.gallery : ImageSource.camera,
      imageQuality: 85,
    );
    if (xfile == null || !mounted) return;
    List<int> imageBytes = await xfile.readAsBytes();
    if (!mounted) return;
    if (isRunsheetReceived) {
      final ok = await _validateRunsheetTripId(
        imageBytes,
        imagePath: xfile.path,
      );
      if (!mounted) return;
      if (!ok) return;
    }
    Uint8List compressed;
    if (isRunsheetReceived) {
      compressed = await compressImageForUpload(imageBytes);
    } else {
      if (_cachedOverlayPosition == null || _cachedOverlayContext == null) {
        try {
          final pos = await getCurrentPosition();
          final ctx = await fetchOverlayContext(pos.latitude, pos.longitude);
          if (mounted)
            setState(() {
              _cachedOverlayPosition = pos;
              _cachedOverlayContext = ctx;
            });
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
    if (!mounted) return;
    if (!runsheetOk) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('delivery_trip_id_mismatch_cannot_confirm'.tr()),
          backgroundColor: Colors.red,
        ),
      );
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
      await submitDeliveryPhaseRecord(
        tripId: tripId,
        taskId: widget.savedTripSummary?.taskId,
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
          ],
        ),
        body: Stack(
          children: [
            AbsorbPointer(
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
                              mainAxisAlignment: MainAxisAlignment.spaceBetween,
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
                                    borderRadius: BorderRadius.circular(12),
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
                              '${'loading_phase_origin'.tr()}  ${widget.savedTripSummary?.origin?.trim().isNotEmpty == true ? widget.savedTripSummary!.origin! : '-'}',
                              style: const TextStyle(
                                color: Colors.white70,
                                fontSize: 14,
                              ),
                              maxLines: 2,
                              overflow: TextOverflow.ellipsis,
                            ),
                            const SizedBox(height: 6),
                            Text(
                              '${'loading_phase_destination'.tr()}  ${widget.savedTripSummary?.destination?.trim().isNotEmpty == true ? widget.savedTripSummary!.destination! : '-'}',
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
                            if (widget.savedTripSummary?.sealCode != null &&
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
                    const SizedBox(height: 24),

                    // Photo capture header (เหมือนการรับงาน)
                    Text(
                      'mandatory_evidence'.tr(),
                      style: Theme.of(context).textTheme.titleLarge?.copyWith(
                        fontWeight: FontWeight.bold,
                        color: isDarkMode ? Colors.white : darkNavy,
                      ),
                    ),
                    const SizedBox(height: 8),
                    Text(
                      'mandatory_evidence_desc'.tr(),
                      style: TextStyle(
                        color: isDarkMode ? Colors.grey[400] : Colors.grey[600],
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
                    const SizedBox(height: 24),
                  ],
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
