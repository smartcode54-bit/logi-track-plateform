import 'dart:typed_data';
import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/material.dart';
import 'package:image_picker/image_picker.dart';
import '../../data/repositories/first_mile_checkin_repository.dart';
import '../../data/repositories/hubs_repository.dart';
import '../../data/repositories/trip_records_repository.dart';
import '../../data/services/photo_overlay_service.dart';
import '../../data/services/ocr_screenshot_service.dart';
import 'qr_scan_page.dart';

/// ขั้นตอนรูป Loading ตาม shared-docs/.vibe-rules.md (Loading Checklist 4 รูป)
const List<String> _loadingPhotoStepKeys = [
  'pre_close', // ก่อนปิดตู้
  'closing', // ระหว่างปิดตู้
  'seal', // ซีล
  'runsheet', // รันชีท/เอกสารส่งมอบ
];

/// หน้างาน Loading Phase: ผู้ใช้กรอก Trip ID, Seal Code, เส้นทาง (manual) ก่อน
/// Trip ID + Seal Code อ่านจาก QR/Barcode ได้ รูปใส่ทีละรูปตาม 4 ขั้นตอน
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

  String? _jobType = jobTypeFirstMile;

  /// รูปตามขั้นตอน (key = pre_close | closing | seal | runsheet)
  final Map<String, Uint8List> _stepPhotos = {};
  bool _ocrLoading = false;
  bool _saving = false;

  List<HubDoc> _allHubs = [];

  @override
  void initState() {
    super.initState();
    _loadHubs();
  }

  Future<void> _loadHubs() async {
    try {
      final hubs = await fetchAllHubs();
      if (mounted) {
        setState(() {
          _allHubs = hubs;
        });
      }
    } catch (e) {
      debugPrint('Failed to load hubs: $e');
    }
  }

  @override
  void dispose() {
    _tripIdController.dispose();
    _sealCodeController.dispose();
    _originController.dispose();
    _destinationController.dispose();
    super.dispose();
  }

  Future<void> _scanAndSet(TextEditingController controller) async {
    if (kIsWeb) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('loading_phase_scan_web_hint'.tr())),
        );
      }
      return;
    }
    final value = await Navigator.of(
      context,
    ).push<String>(MaterialPageRoute(builder: (_) => const QrScanPage()));
    if (value != null && mounted) controller.text = value;
  }

  Future<void> _takeStepPhoto(String stepKey) async {
    if (kIsWeb) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('loading_phase_camera_web_hint'.tr())),
        );
      }
      return;
    }
    final picker = ImagePicker();
    final xfile = await picker.pickImage(
      source: ImageSource.camera,
      imageQuality: 85,
    );
    if (xfile == null || !mounted) return;
    final imageBytes = await xfile.readAsBytes();
    if (!mounted) return;

    bool dialogOpen = false;
    try {
      if (mounted) {
        showDialog(
          context: context,
          barrierDismissible: false,
          builder: (_) => const Center(child: CircularProgressIndicator()),
        );
        dialogOpen = true;
      }

      // Save raw bytes now. We will stamp them on submit.
      if (mounted && dialogOpen) {
        Navigator.of(context).pop();
        dialogOpen = false;
      }
      if (!mounted) return;
      setState(() {
        _stepPhotos[stepKey] = imageBytes;
      });
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('loading_phase_photo_stamped'.tr())),
      );
    } catch (e) {
      if (mounted) {
        if (dialogOpen) Navigator.of(context).pop();
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(
              '${'loading_phase_photo_failed'.tr()} ${e.toString()}',
            ),
            backgroundColor: Colors.red,
          ),
        );
      }
    }
  }

  Future<void> _pickScreenshotAndRunOcr() async {
    if (kIsWeb) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('loading_phase_ocr_web_hint'.tr())),
        );
      }
      return;
    }
    setState(() => _ocrLoading = true);
    try {
      final picker = ImagePicker();
      final result = await pickScreenshotAndRunOcr(
        pickImageFromGallery: () =>
            picker.pickImage(source: ImageSource.gallery, imageQuality: 90),
      );
      if (mounted) {
        setState(() {
          _ocrLoading = false;
          if (result.tripId != null) _tripIdController.text = result.tripId!;
          if (result.sealCode != null)
            _sealCodeController.text = result.sealCode!;
          if (result.routeInfo != null) {
            // Fallback for old formatting if Origin is not explicitly found
            if (_originController.text.isEmpty && result.origin == null) {
              _originController.text = result.routeInfo!;
            }
          }
          if (result.origin != null) {
            _originController.text = result.origin!;
          }
          if (result.destination != null) {
            _destinationController.text = result.destination!;
          }
        });
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('loading_phase_ocr_done'.tr())));
      }
    } catch (e) {
      if (mounted) {
        setState(() => _ocrLoading = false);
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text('${'loading_phase_ocr_failed'.tr()} ${e.toString()}'),
            backgroundColor: Colors.red,
          ),
        );
      }
    }
  }

  Future<void> _submitForm() async {
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
    if (_stepPhotos.length != _loadingPhotoStepKeys.length) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('loading_phase_photos_required'.tr()),
          backgroundColor: Colors.orange,
        ),
      );
      return;
    }
    setState(() => _saving = true);
    try {
      final position = await getCurrentPosition();
      final timestamp = DateTime.now();

      final stepPhotos = <String, StampedPhotoInput>{};
      for (final key in _loadingPhotoStepKeys) {
        final rawBytes = _stepPhotos[key]!;
        final stampedBytes = await overlayGeocodingAndTimestamp(
          imageBytes: rawBytes,
          lat: position.latitude,
          lng: position.longitude,
          timestamp: timestamp,
        );
        stepPhotos[key] = StampedPhotoInput(
          bytes: stampedBytes,
          lat: position.latitude,
          lng: position.longitude,
          timestamp: timestamp,
        );
      }
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
        stepPhotos: stepPhotos,
      );
      if (!mounted) return;
      setState(() => _saving = false);
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
      case 'runsheet':
        return 'loading_phase_photo_runsheet';
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
      case 'runsheet':
        return 'loading_phase_photo_runsheet_desc';
      default:
        return stepKey;
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: Text('loading_phase_form_title'.tr())),
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

              // ประเภทงาน
              Text(
                'loading_phase_job_type'.tr(),
                style: Theme.of(
                  context,
                ).textTheme.titleSmall?.copyWith(fontWeight: FontWeight.w600),
              ),
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
              const SizedBox(height: 20),

              // --- Manual input ก่อน: Trip ID, Seal Code, เส้นทาง (อ่านจาก QR/Barcode ได้) ---
              TextFormField(
                controller: _tripIdController,
                decoration: InputDecoration(
                  labelText: 'loading_phase_trip_id'.tr(),
                  hintText: 'LTQ...',
                  border: const OutlineInputBorder(),
                  suffixIcon: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      IconButton(
                        icon: const Icon(Icons.qr_code_scanner),
                        tooltip: 'loading_phase_scan_qr'.tr(),
                        onPressed: () => _scanAndSet(_tripIdController),
                      ),
                      IconButton(
                        icon: const Icon(Icons.document_scanner),
                        tooltip: 'loading_phase_add_screenshot'.tr(),
                        onPressed: _ocrLoading
                            ? null
                            : _pickScreenshotAndRunOcr,
                      ),
                    ],
                  ),
                ),
              ),
              const SizedBox(height: 16),

              TextFormField(
                controller: _sealCodeController,
                decoration: InputDecoration(
                  labelText: 'loading_phase_seal_code'.tr(),
                  hintText: 'SPX...',
                  border: const OutlineInputBorder(),
                  suffixIcon: IconButton(
                    icon: const Icon(Icons.qr_code_scanner),
                    tooltip: 'loading_phase_scan_qr'.tr(),
                    onPressed: () => _scanAndSet(_sealCodeController),
                  ),
                ),
              ),
              const SizedBox(height: 16),

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
                onSelected: (HubDoc? hub) {
                  if (hub != null) {
                    _originController.text = hub.sourceNameEn;
                  }
                },
                dropdownMenuEntries: _allHubs.map((hub) {
                  return DropdownMenuEntry<HubDoc>(
                    value: hub,
                    label: '${hub.sourceNameEn} (${hub.sourceId})',
                  );
                }).toList(),
              ),
              const SizedBox(height: 16),
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
                onSelected: (HubDoc? hub) {
                  if (hub != null) {
                    _destinationController.text = hub.sourceNameEn;
                  }
                },
                dropdownMenuEntries: _allHubs.map((hub) {
                  return DropdownMenuEntry<HubDoc>(
                    value: hub,
                    label: '${hub.sourceNameEn} (${hub.sourceId})',
                  );
                }).toList(),
              ),
              const SizedBox(height: 8),
              Text(
                'loading_phase_add_screenshot_subtitle'.tr(),
                style: Theme.of(
                  context,
                ).textTheme.bodySmall?.copyWith(color: Colors.grey[600]),
              ),
              const SizedBox(height: 24),

              // --- รูปถ่ายตามขั้นตอน (4 รูป ตาม .vibe-rules.md) ---
              Text(
                'loading_phase_photos_step_title'.tr(),
                style: Theme.of(
                  context,
                ).textTheme.titleSmall?.copyWith(fontWeight: FontWeight.w600),
              ),
              Text(
                'loading_phase_photos_step_subtitle'.tr(),
                style: Theme.of(
                  context,
                ).textTheme.bodySmall?.copyWith(color: Colors.grey[600]),
              ),
              const SizedBox(height: 12),
              ..._loadingPhotoStepKeys.map((stepKey) {
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
                            OutlinedButton.icon(
                              onPressed: () => _takeStepPhoto(stepKey),
                              icon: const Icon(Icons.camera_alt, size: 20),
                              label: Text('loading_phase_take_photo'.tr()),
                            ),
                          ] else
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

              FilledButton.icon(
                onPressed: _saving ? null : _submitForm,
                icon: _saving
                    ? const SizedBox(
                        width: 20,
                        height: 20,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Icon(Icons.save),
                label: Text('loading_phase_save'.tr()),
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
}

class StampedPhoto {
  final Uint8List bytes;
  final double lat;
  final double lng;
  final DateTime timestamp;

  StampedPhoto({
    required this.bytes,
    required this.lat,
    required this.lng,
    required this.timestamp,
  });
}
