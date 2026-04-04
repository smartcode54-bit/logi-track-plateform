import 'dart:typed_data';
import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/material.dart';
import 'package:image_picker/image_picker.dart';
import '../../../home/data/repositories/checkin_repository.dart';
import '../../data/repositories/maintenance_repository.dart';
import '../utils/maintenance_i18n.dart';

class MaintenanceActionPage extends StatefulWidget {
  final Map<String, dynamic> task;

  const MaintenanceActionPage({super.key, required this.task});

  @override
  State<MaintenanceActionPage> createState() => _MaintenanceActionPageState();
}

class _MaintenanceActionPageState extends State<MaintenanceActionPage> {
  final _amountController = TextEditingController();
  final _formKey = GlobalKey<FormState>();

  Uint8List? _invoicePhoto;
  bool _saving = false;

  @override
  void dispose() {
    _amountController.dispose();
    super.dispose();
  }

  Future<void> _pickInvoicePhoto() async {
    final picker = ImagePicker();
    final xfile = await picker.pickImage(source: ImageSource.camera, imageQuality: 85);
    if (xfile == null || !mounted) return;
    final bytes = await xfile.readAsBytes();
    if (!mounted) return;
    final compressed = await stampOverlayAndCompressForEvidence(bytes);
    if (!mounted) return;
    setState(() => _invoicePhoto = compressed);
  }

  Future<void> _handleCheckIn() async {
    setState(() => _saving = true);
    try {
      await MaintenanceRepository().checkInMaintenance(widget.task['id']);
      if (mounted) {
        Navigator.pop(context, true);
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('maintenance_checkin_success'.tr())),
        );
      }
    } catch (e) {
      if (mounted) {
        setState(() => _saving = false);
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(
              'maintenance_error_generic'.tr(namedArgs: {'error': e.toString()}),
            ),
          ),
        );
      }
    }
  }

  Future<void> _handleSubmitCompletion() async {
    if (!_formKey.currentState!.validate() || _invoicePhoto == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('maintenance_form_incomplete'.tr())),
      );
      return;
    }
    setState(() => _saving = true);
    try {
      final amount = double.parse(_amountController.text.trim());
      final uploadedUrl = await MaintenanceRepository().uploadMaintenancePhoto(
        taskId: widget.task['id'],
        photoType: 'invoice',
        imageBytes: _invoicePhoto!,
      );
      await MaintenanceRepository().submitMaintenanceCompletion(
        widget.task['id'],
        invoiceUrl: uploadedUrl,
        invoiceAmount: amount,
      );
      if (mounted) {
        Navigator.pop(context, true);
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('maintenance_submit_success'.tr())),
        );
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
    final locationRaw = widget.task['locationName'] as String?;
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
                        style: const TextStyle(fontWeight: FontWeight.bold, fontSize: 16),
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
                        style: const TextStyle(fontWeight: FontWeight.bold, fontSize: 16),
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
                      InkWell(
                        onTap: _pickInvoicePhoto,
                        child: Container(
                          height: 200,
                          decoration: BoxDecoration(
                            border: Border.all(color: Colors.grey),
                            borderRadius: BorderRadius.circular(8),
                            color: Colors.grey[100],
                          ),
                          child: _invoicePhoto != null
                              ? Image.memory(_invoicePhoto!, fit: BoxFit.cover)
                              : Column(
                                  mainAxisAlignment: MainAxisAlignment.center,
                                  children: [
                                    const Icon(Icons.camera_alt, size: 40, color: Colors.blue),
                                    const SizedBox(height: 8),
                                    Text(
                                      'maintenance_attach_receipt_tap'.tr(),
                                      style: TextStyle(color: Colors.grey[700]),
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
                  padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
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
            Text('${'maintenance_info_location_label'.tr()}: $locationDisplay'),
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
