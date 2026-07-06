import 'dart:typed_data';
import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/material.dart';
import 'package:image_picker/image_picker.dart';
import '../../../home/data/repositories/checkin_repository.dart';
import '../../data/repositories/maintenance_repository.dart';
import '../utils/maintenance_i18n.dart';
import '../utils/maintenance_maps_launch.dart';

/// สูงสุดเท่าหน้ารับงาน (รันชีท) — แนบใบเสร็จซ่อมได้หลายใบ
const int _kMaxMaintenanceReceiptPhotos = 4;

class MaintenanceActionPage extends StatefulWidget {
  final Map<String, dynamic> task;

  const MaintenanceActionPage({super.key, required this.task});

  @override
  State<MaintenanceActionPage> createState() => _MaintenanceActionPageState();
}

class _MaintenanceActionPageState extends State<MaintenanceActionPage> {
  final _amountController = TextEditingController();
  final _formKey = GlobalKey<FormState>();

  final List<Uint8List> _invoicePhotos = [];
  bool _saving = false;

  @override
  void dispose() {
    _amountController.dispose();
    super.dispose();
  }

  Future<void> _pickFirstReceiptFromCamera() async {
    if (_invoicePhotos.length >= _kMaxMaintenanceReceiptPhotos) return;
    final picker = ImagePicker();
    final xfile = await picker.pickImage(
      source: ImageSource.camera,
      imageQuality: 85,
    );
    if (xfile == null || !mounted) return;
    final bytes = await xfile.readAsBytes();
    if (!mounted) return;
    final compressed = await stampOverlayAndCompressForEvidence(bytes);
    if (!mounted) return;
    setState(() => _invoicePhotos.add(compressed));
  }

  Future<void> _addReceiptPhoto() async {
    if (_invoicePhotos.isEmpty ||
        _invoicePhotos.length >= _kMaxMaintenanceReceiptPhotos) {
      return;
    }
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
    final xfile = await picker.pickImage(source: source, imageQuality: 85);
    if (xfile == null || !mounted) return;
    final imageBytes = await xfile.readAsBytes();
    if (!mounted) return;
    final compressed =
        await stampOverlayAndCompressForEvidence(imageBytes);
    if (!mounted) return;
    setState(() {
      if (_invoicePhotos.length < _kMaxMaintenanceReceiptPhotos) {
        _invoicePhotos.add(compressed);
      }
    });
    if (mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('maintenance_receipt_photo_added'.tr())),
      );
    }
  }

  void _removeReceiptAt(int index) {
    if (index < 0 || index >= _invoicePhotos.length) return;
    setState(() => _invoicePhotos.removeAt(index));
  }

  Future<void> _handleCheckIn() async {
    setState(() => _saving = true);
    try {
      await MaintenanceRepository().checkInMaintenance(widget.task['id']);
      if (mounted) {
        setState(() => _saving = false);
        // Blocking confirmation — must be acknowledged before we pop, so a successful
        // check-in can never "disappear silently."
        await showDialog<void>(
          context: context,
          barrierDismissible: false,
          builder: (ctx) => AlertDialog(
            icon: const Icon(Icons.check_circle, color: Colors.green, size: 48),
            title: Text('maintenance_submit_success_title'.tr()),
            content: Text('maintenance_checkin_success'.tr()),
            actions: [
              FilledButton(
                onPressed: () => Navigator.of(ctx).pop(),
                child: Text('loading_phase_submit_success_ok'.tr()),
              ),
            ],
          ),
        );
        if (!mounted) return;
        Navigator.pop(context, true);
      }
    } catch (e) {
      if (mounted) {
        setState(() => _saving = false);
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(
              'maintenance_error_generic'
                  .tr(namedArgs: {'error': e.toString()}),
            ),
          ),
        );
      }
    }
  }

  Future<void> _handleSubmitCompletion() async {
    if (!_formKey.currentState!.validate() || _invoicePhotos.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('maintenance_form_incomplete'.tr())),
      );
      return;
    }
    setState(() => _saving = true);
    try {
      final amount = double.parse(_amountController.text.trim());
      final urls = <String>[];
      for (var i = 0; i < _invoicePhotos.length; i++) {
        final uploadedUrl =
            await MaintenanceRepository().uploadMaintenancePhoto(
          taskId: widget.task['id'],
          photoType: 'invoice_$i',
          imageBytes: _invoicePhotos[i],
        );
        urls.add(uploadedUrl);
      }
      await MaintenanceRepository().submitMaintenanceCompletion(
        widget.task['id'],
        invoiceUrls: urls,
        invoiceAmount: amount,
      );
      if (mounted) {
        setState(() => _saving = false);
        // Blocking confirmation — must be acknowledged before we pop, so a successful
        // submit can never "disappear silently."
        await showDialog<void>(
          context: context,
          barrierDismissible: false,
          builder: (ctx) => AlertDialog(
            icon: const Icon(Icons.check_circle, color: Colors.green, size: 48),
            title: Text('maintenance_submit_success_title'.tr()),
            content: Text('maintenance_submit_success'.tr()),
            actions: [
              FilledButton(
                onPressed: () => Navigator.of(ctx).pop(),
                child: Text('loading_phase_submit_success_ok'.tr()),
              ),
            ],
          ),
        );
        if (!mounted) return;
        Navigator.pop(context, true);
      }
    } catch (e) {
      if (mounted) {
        setState(() => _saving = false);
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(
              '${'maintenance_upload_error'.tr()}: $e',
            ),
          ),
        );
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final statusRaw = widget.task['status'] as String? ?? '';
    final serviceLabel = trMaintenanceServiceType(
      widget.task['serviceType'] as String?,
    );
    final notes = widget.task['notes'] as String? ?? '';
    final locationRaw = rawMaintenanceLocation(widget.task);
    final locationDisplay = (locationRaw == null || locationRaw.trim().isEmpty)
        ? 'maintenance_location_unspecified'.tr()
        : locationRaw;

    return Scaffold(
      appBar: AppBar(
        title: Text(serviceLabel),
      ),
      body: _saving
          ? const Center(child: CircularProgressIndicator())
          : SingleChildScrollView(
              padding: const EdgeInsets.all(16.0),
              child: Form(
                key: _formKey,
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    _buildInfoSection(
                      serviceLabel,
                      notes,
                      locationDisplay,
                      statusRaw,
                    ),
                    const SizedBox(height: 24),

                    if (statusRaw == 'Scheduled' ||
                        statusRaw == 'PM Booking' ||
                        statusRaw == 'in_progress') ...[
                      Text(
                        'maintenance_step1_title'.tr(),
                        style: const TextStyle(
                          fontWeight: FontWeight.bold,
                          fontSize: 16,
                        ),
                      ),
                      const SizedBox(height: 12),
                      ElevatedButton.icon(
                        style: ElevatedButton.styleFrom(
                          backgroundColor: Colors.blue,
                          padding: const EdgeInsets.symmetric(vertical: 16),
                        ),
                        onPressed: _handleCheckIn,
                        icon: const Icon(Icons.location_on),
                        label: Text(
                          'maintenance_step1_button'.tr(),
                          style: const TextStyle(fontSize: 16),
                        ),
                      ),
                    ],

                    if (statusRaw == 'In-Progress') ...[
                      Text(
                        'maintenance_step2_title'.tr(),
                        style: const TextStyle(
                          fontWeight: FontWeight.bold,
                          fontSize: 16,
                        ),
                      ),
                      const SizedBox(height: 16),
                      TextFormField(
                        controller: _amountController,
                        keyboardType: TextInputType.number,
                        decoration: InputDecoration(
                          labelText: 'maintenance_invoice_amount_label'.tr(),
                          hintText: 'maintenance_invoice_amount_hint'.tr(),
                        ),
                        validator: (value) {
                          if (value == null || value.isEmpty) {
                            return 'maintenance_amount_required'.tr();
                          }
                          if (double.tryParse(value) == null) {
                            return 'maintenance_amount_invalid'.tr();
                          }
                          return null;
                        },
                      ),
                      const SizedBox(height: 16),
                      Text('maintenance_attach_receipt_label'.tr()),
                      const SizedBox(height: 8),
                      Card(
                        child: Padding(
                          padding: const EdgeInsets.all(12),
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.stretch,
                            children: [
                              if (_invoicePhotos.isNotEmpty) ...[
                                SizedBox(
                                  height: 124,
                                  child: ListView.separated(
                                    scrollDirection: Axis.horizontal,
                                    itemCount: _invoicePhotos.length,
                                    separatorBuilder: (context, index) =>
                                        const SizedBox(width: 8),
                                    itemBuilder: (ctx, i) => Stack(
                                      clipBehavior: Clip.none,
                                      children: [
                                        ClipRRect(
                                          borderRadius:
                                              BorderRadius.circular(8),
                                          child: Image.memory(
                                            _invoicePhotos[i],
                                            height: 120,
                                            width: 96,
                                            fit: BoxFit.cover,
                                          ),
                                        ),
                                        Positioned(
                                          top: 2,
                                          right: 2,
                                          child: Material(
                                            color: Colors.black54,
                                            shape: const CircleBorder(),
                                            child: InkWell(
                                              customBorder:
                                                  const CircleBorder(),
                                              onTap: () =>
                                                  _removeReceiptAt(i),
                                              child: const Padding(
                                                padding: EdgeInsets.all(4),
                                                child: Icon(
                                                  Icons.close,
                                                  size: 16,
                                                  color: Colors.white,
                                                ),
                                              ),
                                            ),
                                          ),
                                        ),
                                      ],
                                    ),
                                  ),
                                ),
                                Text(
                                  'maintenance_receipt_photos_count'.tr(
                                    namedArgs: {
                                      'n': '${_invoicePhotos.length}',
                                      'max':
                                          '$_kMaxMaintenanceReceiptPhotos',
                                    },
                                  ),
                                  style: Theme.of(context)
                                      .textTheme
                                      .bodySmall
                                      ?.copyWith(color: Colors.grey[700]),
                                ),
                                const SizedBox(height: 8),
                              ],
                              OutlinedButton.icon(
                                onPressed: _invoicePhotos.length >=
                                        _kMaxMaintenanceReceiptPhotos
                                    ? null
                                    : (_invoicePhotos.isEmpty
                                        ? _pickFirstReceiptFromCamera
                                        : _addReceiptPhoto),
                                icon: Icon(
                                  _invoicePhotos.isEmpty
                                      ? Icons.camera_alt
                                      : Icons.add_photo_alternate_outlined,
                                  size: 20,
                                ),
                                label: Text(
                                  _invoicePhotos.isEmpty
                                      ? 'maintenance_attach_receipt_tap'.tr()
                                      : 'maintenance_add_receipt_photo'.tr(),
                                ),
                              ),
                            ],
                          ),
                        ),
                      ),
                      const SizedBox(height: 24),
                      ElevatedButton(
                        style: ElevatedButton.styleFrom(
                          backgroundColor: Colors.green,
                          padding: const EdgeInsets.symmetric(vertical: 16),
                        ),
                        onPressed: _handleSubmitCompletion,
                        child: Text(
                          'maintenance_submit_done_button'.tr(),
                          style: const TextStyle(fontSize: 16),
                        ),
                      ),
                    ],
                  ],
                ),
              ),
            ),
    );
  }

  Widget _buildInfoSection(
    String serviceDisplay,
    String notes,
    String locationDisplay,
    String statusRaw,
  ) {
    final statusDisplay = trMaintenanceStatus(statusRaw);
    final nav = maintenanceProviderCoords(widget.task);
    Color statusColor = Colors.orange;
    if (statusRaw == 'In-Progress') statusColor = Colors.blue;
    if (statusRaw == 'Scheduled') statusColor = Colors.teal;
    if (statusRaw == 'PM Booking' || statusRaw == 'in_progress') {
      statusColor = Colors.deepOrange;
    }

    return Card(
      elevation: 3,
      child: Padding(
        padding: const EdgeInsets.all(16.0),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Expanded(
                  child: Text(
                    'maintenance_info_status_header'.tr(),
                    style: const TextStyle(fontWeight: FontWeight.bold),
                  ),
                ),
                Container(
                  padding:
                      const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                  decoration: BoxDecoration(
                    color: statusColor.withValues(alpha: 0.15),
                    borderRadius: BorderRadius.circular(8),
                    border: Border.all(color: statusColor),
                  ),
                  child: Text(
                    statusDisplay,
                    style: TextStyle(
                      color: statusColor,
                      fontWeight: FontWeight.bold,
                      fontSize: 12,
                    ),
                  ),
                ),
              ],
            ),
            const Divider(),
            const SizedBox(height: 8),
            Text(
              '${'maintenance_info_type_label'.tr()}: $serviceDisplay',
              style: const TextStyle(fontWeight: FontWeight.bold),
            ),
            const SizedBox(height: 8),
            Text(
                '${'maintenance_info_location_label'.tr()}: $locationDisplay'),
            if (nav != null) ...[
              const SizedBox(height: 12),
              SizedBox(
                width: double.infinity,
                child: OutlinedButton.icon(
                  onPressed: () async {
                    final ok = await openGoogleMapsDirectionsFromHere(
                      destinationLat: nav.lat,
                      destinationLng: nav.lng,
                    );
                    if (!ok) {
                      if (!mounted) return;
                      ScaffoldMessenger.of(context).showSnackBar(
                        SnackBar(
                          content: Text('maintenance_maps_open_failed'.tr()),
                        ),
                      );
                    }
                  },
                  icon: const Icon(Icons.navigation, size: 20),
                  label: Text('maintenance_navigate'.tr()),
                ),
              ),
            ],
            const SizedBox(height: 8),
            Text(
              '${'maintenance_info_notes_label'.tr()}: $notes',
              style: const TextStyle(color: Colors.grey),
            ),
          ],
        ),
      ),
    );
  }
}
