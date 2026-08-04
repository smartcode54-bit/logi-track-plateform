import 'dart:typed_data';
import 'package:easy_localization/easy_localization.dart';
import 'package:firebase_auth/firebase_auth.dart';
import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/material.dart';
import 'package:image_picker/image_picker.dart';
import '../../../home/data/repositories/checkin_repository.dart';
import '../../../home/data/services/image_compression_service.dart';
import '../../../home/data/services/photo_overlay_service.dart';
import '../../data/repositories/standby_repository.dart';

/// หน้า Standby: บันทึกหลักฐานเมื่อ Driver เช็คอินแล้วไม่มีงานจัดส่ง
class StandbyPage extends StatefulWidget {
  const StandbyPage({
    super.key,
    required this.taskId,
    required this.tripId,
    required this.origin,
    required this.destination,
    required this.startedAt,
    this.truckId,
    this.truckLicensePlate,
    this.customerId,
    this.customerResolvedFrom,
  });

  final String? taskId;
  final String? tripId;
  final String origin;
  final String destination;
  final DateTime startedAt;
  final String? truckId;
  final String? truckLicensePlate;

  /// ลูกค้าที่จะวางบิลของ standby นี้ — resolve มาแล้วจากหน้าก่อนหน้า (ADR 0008 §1).
  /// null = resolve ไม่ได้ → record จะถูกบันทึกพร้อมธง customerResolved:false ไม่บล็อกการบันทึก
  final String? customerId;

  /// ที่มาของ [customerId] — 'task' | 'origin_hub'
  final String? customerResolvedFrom;

  @override
  State<StandbyPage> createState() => _StandbyPageState();
}

class _StandbyPageState extends State<StandbyPage> {
  final Map<String, Uint8List> _photos = {};
  static const _worksheetKey = 'customer_worksheet';
  static const _siteKey = 'site_photo';

  final _noteController = TextEditingController();
  bool _saving = false;
  double? _lat;
  double? _lng;

  bool get _hasWorksheetPhoto =>
      _photos[_worksheetKey] != null && _photos[_worksheetKey]!.isNotEmpty;
  bool get _hasSitePhoto =>
      _photos[_siteKey] != null && _photos[_siteKey]!.isNotEmpty;
  bool get _canSubmit => _hasWorksheetPhoto && _hasSitePhoto && !_saving;

  @override
  void initState() {
    super.initState();
    _loadLocation();
  }

  @override
  void dispose() {
    _noteController.dispose();
    super.dispose();
  }

  Future<void> _loadLocation() async {
    try {
      final pos = await getCurrentPosition();
      if (mounted) {
        setState(() {
          _lat = pos.latitude;
          _lng = pos.longitude;
        });
      }
    } catch (_) {}
  }

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
              title: Text('standby_take_photo'.tr()),
              onTap: () {
                Navigator.pop(ctx);
                _pickPhoto(photoKey, fromCamera: true);
              },
            ),
            ListTile(
              leading: const Icon(Icons.photo_library),
              title: Text('standby_attach_photo'.tr()),
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
    if (!_canSubmit) return;
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
      await submitStandbyRecord(
        driverId: uid,
        taskId: widget.taskId,
        tripId: widget.tripId,
        startLocation: widget.origin,
        endLocation: widget.destination,
        startedAt: widget.startedAt,
        note: _noteController.text,
        worksheetImage: _photos[_worksheetKey]?.toList(),
        siteImage: _photos[_siteKey]?.toList(),
        lat: _lat,
        lng: _lng,
        truckId: widget.truckId,
        truckLicensePlate: widget.truckLicensePlate,
        customerId: widget.customerId,
        customerResolvedFrom: widget.customerResolvedFrom,
      );
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('standby_saved'.tr()),
          backgroundColor: Colors.green,
        ),
      );
      Navigator.of(context).pushNamedAndRemoveUntil('/home', (_) => false);
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text('${'standby_save_failed'.tr()} $e'),
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
            'standby_title'.tr(),
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
                    // Trip info summary card
                    Card(
                      color: darkNavy.withOpacity(isDarkMode ? 0.6 : 0.85),
                      child: Padding(
                        padding: const EdgeInsets.all(12.0),
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Text(
                              '${widget.origin}  →  ${widget.destination}',
                              style: const TextStyle(
                                color: Colors.white,
                                fontWeight: FontWeight.w600,
                              ),
                            ),
                            const SizedBox(height: 4),
                            Text(
                              '${'standby_checked_in_at'.tr()}: ${_formatDateTime(widget.startedAt)}',
                              style: const TextStyle(
                                color: Colors.white70,
                                fontSize: 12,
                              ),
                            ),
                          ],
                        ),
                      ),
                    ),
                    const SizedBox(height: 20),

                    // Photo 1: customer worksheet
                    _buildPhotoSlot(
                      key: _worksheetKey,
                      titleKey: 'standby_worksheet_photo',
                      descKey: 'standby_worksheet_photo_desc',
                      isDarkMode: isDarkMode,
                    ),
                    const SizedBox(height: 16),

                    // Photo 2: site photo
                    _buildPhotoSlot(
                      key: _siteKey,
                      titleKey: 'standby_site_photo',
                      descKey: 'standby_site_photo_desc',
                      isDarkMode: isDarkMode,
                    ),
                    const SizedBox(height: 16),

                    // Note field (optional)
                    TextField(
                      controller: _noteController,
                      enabled: !_saving,
                      maxLines: 3,
                      decoration: InputDecoration(
                        labelText: 'standby_note_label'.tr(),
                        hintText: 'standby_note_hint'.tr(),
                        border: const OutlineInputBorder(),
                      ),
                    ),
                    const SizedBox(height: 24),

                    // Submit button
                    FilledButton.icon(
                      onPressed: _canSubmit ? _submit : null,
                      icon: _saving
                          ? const SizedBox(
                              width: 20,
                              height: 20,
                              child:
                                  CircularProgressIndicator(strokeWidth: 2),
                            )
                          : const Icon(Icons.check_circle_outline),
                      label: Text(
                        _saving
                            ? 'standby_saving'.tr()
                            : 'standby_submit'.tr(),
                      ),
                      style: FilledButton.styleFrom(
                        backgroundColor: Colors.orange.shade600,
                        foregroundColor: Colors.white,
                        padding:
                            const EdgeInsets.symmetric(vertical: 16),
                      ),
                    ),
                  ],
                ),
              ),
              ),
            ),
            if (_saving)
              const Positioned.fill(
                  child: ModalBarrier(color: Colors.black38)),
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
                          child:
                              CircularProgressIndicator(strokeWidth: 2),
                        ),
                        const SizedBox(height: 16),
                        Text('standby_saving'.tr()),
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
                Row(
                  children: [
                    Text(
                      titleKey.tr(),
                      style: TextStyle(
                        fontWeight: FontWeight.bold,
                        color: isDarkMode ? Colors.white : Colors.black87,
                      ),
                    ),
                    const SizedBox(width: 6),
                    Icon(
                      hasPhoto ? Icons.check_circle : Icons.circle_outlined,
                      size: 16,
                      color: hasPhoto ? Colors.green : Colors.grey,
                    ),
                  ],
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
                  child: hasPhoto
                      ? ClipRRect(
                          borderRadius: BorderRadius.circular(12),
                          child: Image.memory(
                            _photos[key]!,
                            height: 160,
                            width: double.infinity,
                            fit: BoxFit.cover,
                          ),
                        )
                      : Container(
                          height: 120,
                          decoration: BoxDecoration(
                            color: isDarkMode
                                ? Colors.white10
                                : Colors.grey[200],
                            borderRadius: BorderRadius.circular(12),
                            border: Border.all(
                              color: isDarkMode
                                  ? Colors.white24
                                  : Colors.grey.shade400,
                              width: 1.5,
                            ),
                          ),
                          child: Center(
                            child: Column(
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                Icon(
                                  Icons.add_a_photo_outlined,
                                  size: 36,
                                  color: isDarkMode
                                      ? Colors.white38
                                      : Colors.grey,
                                ),
                                const SizedBox(height: 8),
                                Text(
                                  'standby_add_photo'.tr(),
                                  style: TextStyle(
                                    color: isDarkMode
                                        ? Colors.white38
                                        : Colors.grey,
                                    fontSize: 12,
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

  String _formatDateTime(DateTime dt) {
    return '${dt.day.toString().padLeft(2, '0')}/'
        '${dt.month.toString().padLeft(2, '0')}/'
        '${dt.year}  '
        '${dt.hour.toString().padLeft(2, '0')}:'
        '${dt.minute.toString().padLeft(2, '0')}';
  }
}
