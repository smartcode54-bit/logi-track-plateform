import 'dart:typed_data';
import 'package:easy_localization/easy_localization.dart';
import 'package:firebase_auth/firebase_auth.dart';
import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/material.dart';
import 'package:image_picker/image_picker.dart';
import '../../../home/data/repositories/checkin_repository.dart';
import '../../../home/data/services/image_compression_service.dart';
import '../../../home/presentation/pages/main_layout_scope.dart';
import '../../data/repositories/incident_report_repository.dart';

/// หน้ารายงานปัญหา: เหตุการจัดส่งล่าช้า (combobox+search) + รูป 3 ภาพ
class IncidentReportPage extends StatefulWidget {
  const IncidentReportPage({super.key, this.savedTripSummary});

  final SavedTripSummary? savedTripSummary;

  @override
  State<IncidentReportPage> createState() => _IncidentReportPageState();
}

class _IncidentReportPageState extends State<IncidentReportPage> {
  final Map<String, Uint8List> _photos =
      {}; // 'map', 'situation1', 'situation2'
  static const List<String> _photoKeys = ['map', 'situation1', 'situation2'];
  String? _selectedDelayCause;
  bool _saving = false;
  double? _lat;
  double? _lng;
  bool _locationLoading = true;

  /// รูปรายงานปัญหา: อย่างน้อย 1 รูป
  bool get _hasAtLeastOnePhoto => _photoKeys.any(
    (key) =>
        _photos.containsKey(key) &&
        _photos[key] != null &&
        _photos[key]!.isNotEmpty,
  );

  @override
  void initState() {
    super.initState();
    _loadLocation();
  }

  @override
  void dispose() {
    super.dispose();
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
    } catch (_) {
      if (mounted) setState(() => _locationLoading = false);
    }
  }

  /// ถ่าย/แนบรูป: จากกล้องใช้ฟังก์ชันร่วม stamp overlay เพื่อหลักฐาน, จากแกลเลอรี compress อย่างเดียว
  Future<void> _pickPhoto(String photoKey, {bool fromCamera = true}) async {
    if (kIsWeb) return;
    final picker = ImagePicker();
    final xfile = await picker.pickImage(
      source: fromCamera ? ImageSource.camera : ImageSource.gallery,
      imageQuality: 85,
    );
    if (xfile == null || !mounted) return;
    final bytes = await xfile.readAsBytes();
    if (!mounted) return;
    final Uint8List result = fromCamera
        ? await stampOverlayAndCompressForEvidence(bytes)
        : await compressImageForUpload(bytes);
    if (!mounted) return;
    setState(() => _photos[photoKey] = result);
  }

  void _showPhotoSourceSheet(String photoKey) {
    showModalBottomSheet<void>(
      context: context,
      builder: (ctx) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              leading: const Icon(Icons.camera_alt),
              title: Text('incident_take_photo'.tr()),
              onTap: () {
                Navigator.pop(ctx);
                _pickPhoto(photoKey, fromCamera: true);
              },
            ),
            ListTile(
              leading: const Icon(Icons.photo_library),
              title: Text('incident_attach_photo'.tr()),
              onTap: () {
                Navigator.pop(ctx);
                _pickPhoto(photoKey, fromCamera: false);
              },
            ),
          ],
        ),
      ),
    );
  }

  Future<void> _submit() async {
    if (_selectedDelayCause == null || _selectedDelayCause!.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('incident_delay_cause_required'.tr()),
          backgroundColor: Colors.orange,
        ),
      );
      return;
    }
    if (!_hasAtLeastOnePhoto) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('incident_photos_at_least_one'.tr()),
          backgroundColor: Colors.orange,
        ),
      );
      return;
    }
    final uid = FirebaseAuth.instance.currentUser?.uid;
    if (uid == null || uid.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('user_not_authenticated'.tr()),
          backgroundColor: Colors.red,
        ),
      );
      return;
    }

    setState(() => _saving = true);
    try {
      await submitIncidentReport(
        driverId: uid,
        tripId: widget.savedTripSummary?.tripId,
        delayCause: _selectedDelayCause,
        mapImage: _photos['map']?.isNotEmpty == true ? _photos['map']! : null,
        situation1Image: _photos['situation1']?.isNotEmpty == true
            ? _photos['situation1']!
            : null,
        situation2Image: _photos['situation2']?.isNotEmpty == true
            ? _photos['situation2']!
            : null,
        lat: _lat,
        lng: _lng,
        truckId: widget.savedTripSummary?.truckId,
        truckLicensePlate: widget.savedTripSummary?.truckLicensePlate,
      );
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('incident_saved'.tr()),
          backgroundColor: Colors.green,
        ),
      );
      Navigator.of(context).pop(true);
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text('${'incident_submit_failed'.tr()}$e'),
            backgroundColor: Colors.red,
          ),
        );
      }
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    const darkNavy = Color(0xFF0F172A);
    final isDarkMode = Theme.of(context).brightness == Brightness.dark;

    return PopScope(
      canPop: !_saving,
      child: Scaffold(
        backgroundColor: isDarkMode
            ? Theme.of(context).scaffoldBackgroundColor
            : Colors.grey[50],
        appBar: AppBar(
          title: Text(
            'incident_report_title'.tr(),
            style: const TextStyle(color: Colors.white),
          ),
          backgroundColor: darkNavy,
          iconTheme: const IconThemeData(color: Colors.white),
        ),
        body: Stack(
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
                    Text(
                      'incident_report_subtitle'.tr(),
                      style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                        color: isDarkMode ? Colors.white70 : Colors.black87,
                      ),
                    ),
                    const SizedBox(height: 16),
                    if (widget.savedTripSummary != null)
                      Card(
                        color: darkNavy.withOpacity(0.8),
                        child: Padding(
                          padding: const EdgeInsets.all(12.0),
                          child: Text(
                            'Trip ID: ${widget.savedTripSummary!.tripId}',
                            style: const TextStyle(
                              color: Colors.white70,
                              fontWeight: FontWeight.w600,
                            ),
                          ),
                        ),
                      ),
                    const SizedBox(height: 16),
                    Text(
                      'incident_delay_cause_label'.tr(),
                      style: Theme.of(context).textTheme.titleSmall?.copyWith(
                        fontWeight: FontWeight.w600,
                        color: isDarkMode ? Colors.white70 : Colors.black87,
                      ),
                    ),
                    const SizedBox(height: 8),
                    _buildDelayCauseComboBox(context, isDarkMode),
                    const SizedBox(height: 24),
                    _buildPhotoSlot(
                      key: 'map',
                      titleKey: 'incident_photo_map',
                      descKey: 'incident_photo_map_desc',
                      isDarkMode: isDarkMode,
                    ),
                    const SizedBox(height: 16),
                    _buildPhotoSlot(
                      key: 'situation1',
                      titleKey: 'incident_photo_situation1',
                      descKey: 'incident_photo_situation1_desc',
                      isDarkMode: isDarkMode,
                    ),
                    const SizedBox(height: 16),
                    _buildPhotoSlot(
                      key: 'situation2',
                      titleKey: 'incident_photo_situation2',
                      descKey: 'incident_photo_situation2_desc',
                      isDarkMode: isDarkMode,
                    ),
                    if (_locationLoading)
                      const Padding(
                        padding: EdgeInsets.only(top: 16),
                        child: Center(child: CircularProgressIndicator()),
                      ),
                    const SizedBox(height: 24),
                    FilledButton.icon(
                      onPressed:
                          _saving ||
                              !_hasAtLeastOnePhoto ||
                              _selectedDelayCause == null
                          ? null
                          : _submit,
                      icon: _saving
                          ? const SizedBox(
                              width: 20,
                              height: 20,
                              child: CircularProgressIndicator(strokeWidth: 2),
                            )
                          : const Icon(Icons.send),
                      label: Text('incident_submit'.tr()),
                      style: FilledButton.styleFrom(
                        backgroundColor: Colors.orange,
                        foregroundColor: Colors.white,
                        padding: const EdgeInsets.symmetric(vertical: 16),
                      ),
                    ),
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

  /// Combobox + search สำหรับเลือกเหตุที่ทำให้การจัดส่งล่าช้า (ปิดแก้ไขตอน save)
  Widget _buildDelayCauseComboBox(BuildContext context, bool isDarkMode) {
    return IgnorePointer(
      ignoring: _saving,
      child: Opacity(
        opacity: _saving ? 0.6 : 1,
        child: InkWell(
          onTap: () => _showDelayCausePicker(context, isDarkMode),
          borderRadius: BorderRadius.circular(4),
          child: InputDecorator(
            decoration: InputDecoration(
              hintText: 'incident_delay_cause_hint'.tr(),
              border: const OutlineInputBorder(),
              suffixIcon: const Icon(Icons.arrow_drop_down),
              errorText:
                  _selectedDelayCause == null || _selectedDelayCause!.isEmpty
                  ? null
                  : null,
            ),
            child: Text(
              _selectedDelayCause != null ? _selectedDelayCause!.tr() : '',
              style: TextStyle(
                color: _selectedDelayCause != null
                    ? (isDarkMode ? Colors.white : Colors.black87)
                    : (isDarkMode ? Colors.white54 : Colors.black38),
              ),
            ),
          ),
        ),
      ),
    );
  }

  void _showDelayCausePicker(BuildContext context, bool isDarkMode) {
    showModalBottomSheet<String?>(
      context: context,
      isScrollControlled: true,
      builder: (ctx) => _DelayCausePickerSheet(
        selectedKey: _selectedDelayCause,
        onSelect: (key) => Navigator.pop(ctx, key),
      ),
    ).then((result) {
      if (result != null && mounted) {
        setState(() => _selectedDelayCause = result);
      }
    });
  }

  Widget _buildPhotoSlot({
    required String key,
    required String titleKey,
    required String descKey,
    required bool isDarkMode,
  }) {
    final hasPhoto = _photos[key] != null && _photos[key]!.isNotEmpty;
    return IgnorePointer(
      ignoring: _saving,
      child: Opacity(
        opacity: _saving ? 0.6 : 1,
        child: Card(
          elevation: 2,
          child: Padding(
            padding: const EdgeInsets.all(12.0),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  titleKey.tr(),
                  style: TextStyle(
                    fontWeight: FontWeight.bold,
                    color: isDarkMode ? Colors.white : Colors.black87,
                  ),
                ),
                const SizedBox(height: 4),
                Text(
                  descKey.tr(),
                  style: TextStyle(
                    fontSize: 12,
                    color: isDarkMode ? Colors.white60 : Colors.black54,
                  ),
                ),
                const SizedBox(height: 12),
                InkWell(
                  onTap: () => _showPhotoSourceSheet(key),
                  borderRadius: BorderRadius.circular(12),
                  child: Container(
                    height: 140,
                    decoration: BoxDecoration(
                      color: isDarkMode ? Colors.grey[800] : Colors.grey[200],
                      borderRadius: BorderRadius.circular(12),
                      border: Border.all(
                        color: hasPhoto ? Colors.green : Colors.grey,
                        width: hasPhoto ? 2 : 1,
                      ),
                    ),
                    child: hasPhoto
                        ? ClipRRect(
                            borderRadius: BorderRadius.circular(12),
                            child: Image.memory(
                              _photos[key]!,
                              fit: BoxFit.cover,
                              width: double.infinity,
                            ),
                          )
                        : Center(
                            child: Column(
                              mainAxisAlignment: MainAxisAlignment.center,
                              children: [
                                Icon(
                                  Icons.add_photo_alternate,
                                  size: 40,
                                  color: isDarkMode
                                      ? Colors.white38
                                      : Colors.grey[600],
                                ),
                                const SizedBox(height: 8),
                                Text(
                                  'incident_attach_or_take_photo'.tr(),
                                  style: TextStyle(
                                    color: isDarkMode
                                        ? Colors.white54
                                        : Colors.grey[600],
                                  ),
                                ),
                              ],
                            ),
                          ),
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

/// Bottom sheet content for selecting incident delay cause. Owns [TextEditingController]
/// and [FocusNode] so they are disposed when the sheet is closed, avoiding
/// _dependents.isEmpty assertion from disposing them too early in the parent.
class _DelayCausePickerSheet extends StatefulWidget {
  const _DelayCausePickerSheet({
    required this.selectedKey,
    required this.onSelect,
  });

  final String? selectedKey;
  final void Function(String key) onSelect;

  @override
  State<_DelayCausePickerSheet> createState() => _DelayCausePickerSheetState();
}

class _DelayCausePickerSheetState extends State<_DelayCausePickerSheet> {
  late final TextEditingController _searchController;
  late final FocusNode _searchFocus;

  @override
  void initState() {
    super.initState();
    _searchController = TextEditingController();
    _searchFocus = FocusNode();
  }

  @override
  void dispose() {
    _searchController.dispose();
    _searchFocus.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return StatefulBuilder(
      builder: (context, setModalState) {
        final query = _searchController.text.trim().toLowerCase();
        final filtered = query.isEmpty
            ? List<String>.from(incidentDelayCauseOptionKeys)
            : incidentDelayCauseOptionKeys
                  .where((key) => key.tr().toLowerCase().contains(query))
                  .toList();
        return DraggableScrollableSheet(
          initialChildSize: 0.6,
          minChildSize: 0.3,
          maxChildSize: 0.9,
          expand: false,
          builder: (context, scrollController) => Padding(
            padding: EdgeInsets.only(
              bottom: MediaQuery.of(context).viewInsets.bottom,
            ),
            child: Column(
              mainAxisSize: MainAxisSize.max,
              children: [
                Padding(
                  padding: const EdgeInsets.all(12.0),
                  child: TextField(
                    controller: _searchController,
                    focusNode: _searchFocus,
                    onChanged: (_) => setModalState(() {}),
                    decoration: InputDecoration(
                      hintText: 'incident_delay_cause_search_hint'.tr(),
                      prefixIcon: const Icon(Icons.search),
                      border: const OutlineInputBorder(),
                      contentPadding: const EdgeInsets.symmetric(
                        horizontal: 12,
                        vertical: 12,
                      ),
                    ),
                    autofocus: true,
                  ),
                ),
                Expanded(
                  child: ListView.builder(
                    controller: scrollController,
                    itemCount: filtered.length,
                    itemBuilder: (context, index) {
                      final optionKey = filtered[index];
                      final isSelected = widget.selectedKey == optionKey;
                      return ListTile(
                        title: Text(optionKey.tr()),
                        selected: isSelected,
                        leading: isSelected
                            ? const Icon(
                                Icons.check_circle,
                                color: Colors.green,
                              )
                            : null,
                        onTap: () => widget.onSelect(optionKey),
                      );
                    },
                  ),
                ),
              ],
            ),
          ),
        );
      },
    );
  }
}
