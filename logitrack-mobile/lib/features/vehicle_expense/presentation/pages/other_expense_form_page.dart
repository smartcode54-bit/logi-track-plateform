import 'dart:typed_data';
import 'package:easy_localization/easy_localization.dart';
import 'package:firebase_auth/firebase_auth.dart';
import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/material.dart';
import 'package:image_picker/image_picker.dart';
import 'package:intl/intl.dart' as intl;
import '../../../home/data/repositories/checkin_repository.dart';
import '../../../home/data/services/image_compression_service.dart';
import '../../data/models/vehicle_expense.dart';
import '../../data/repositories/vehicle_expense_repository.dart';

/// รูปแบบวันที่เวลา: dd/MM/yyyy HH:mm:ss (เหมือนหน้าเติมน้ำมัน)
final DateFormat _dateTimeFormat = DateFormat('dd/MM/yyyy HH:mm:ss');

/// หน้าบันทึกค่าใช้จ่ายอื่นๆ: วันที่เวลา, ประเภท, ยอดเงิน, ถ่ายภาพใบเสร็จ (template overlay), สถานที่ lat,lng
/// ไม่มีรายละเอียด/หมายเหตุ; ก่อนบันทึกมี Preview; ตอน Save disable ทุก input + spinning + กำลังบันทึก...
class OtherExpenseFormPage extends StatefulWidget {
  const OtherExpenseFormPage({super.key});

  @override
  State<OtherExpenseFormPage> createState() => _OtherExpenseFormPageState();
}

class _OtherExpenseFormPageState extends State<OtherExpenseFormPage> {
  final _formKey = GlobalKey<FormState>();
  final _dateTimeController = TextEditingController();
  final _amountController = TextEditingController();

  DateTime _selectedDateTime = DateTime.now();
  OtherExpenseCategory _category = OtherExpenseCategory.other;
  Uint8List? _receiptPhoto;
  String? _refillLocation;
  bool _locationLoading = false;
  String? _locationError;
  bool _saving = false;
  String? _saveError;

  @override
  void initState() {
    super.initState();
    _updateDateTimeDisplay();
    WidgetsBinding.instance.addPostFrameCallback((_) => _getLocation());
  }

  void _updateDateTimeDisplay() {
    _dateTimeController.text = _dateTimeFormat.format(_selectedDateTime);
  }

  @override
  void dispose() {
    _dateTimeController.dispose();
    _amountController.dispose();
    super.dispose();
  }

  Future<void> _pickDateTime() async {
    final date = await showDatePicker(
      context: context,
      initialDate: _selectedDateTime,
      firstDate: DateTime(2020),
      lastDate: DateTime.now().add(const Duration(days: 1)),
    );
    if (date == null || !mounted) return;
    final time = await showTimePicker(
      context: context,
      initialTime: TimeOfDay.fromDateTime(_selectedDateTime),
    );
    if (time == null || !mounted) return;
    setState(() {
      _selectedDateTime = DateTime(
        date.year,
        date.month,
        date.day,
        time.hour,
        time.minute,
      );
      _updateDateTimeDisplay();
    });
  }

  Future<void> _getLocation() async {
    setState(() {
      _locationError = null;
      _locationLoading = true;
    });
    try {
      final position = await getCurrentPosition();
      if (mounted) {
        setState(() {
          _refillLocation = '${position.latitude},${position.longitude}';
          _locationLoading = false;
        });
      }
    } catch (e) {
      if (mounted) {
        setState(() {
          _locationError = e.toString();
          _locationLoading = false;
        });
      }
    }
  }

  /// ถ่ายภาพใบเสร็จ: กล้อง = stamp overlay (วันเวลา สถานที่ พิกัด), แกลเลอรี = compress อย่างเดียว
  Future<void> _pickReceiptPhoto() async {
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
    final xfile = await picker.pickImage(source: source, imageQuality: 85);
    if (xfile == null || !mounted) return;
    final imageBytes = await xfile.readAsBytes();
    if (!mounted) return;
    final compressed = source == ImageSource.camera
        ? await stampOverlayAndCompressForEvidence(imageBytes)
        : await compressImageForUpload(imageBytes);
    if (!mounted) return;
    setState(() => _receiptPhoto = compressed);
  }

  Future<void> _submit() async {
    FocusScope.of(context).unfocus();
    if (!_formKey.currentState!.validate()) return;
    final driverId = FirebaseAuth.instance.currentUser?.uid;
    if (driverId == null || driverId.isEmpty) {
      setState(() => _saveError = 'user_not_authenticated'.tr());
      return;
    }
    final amount = double.tryParse(_amountController.text.trim()) ?? 0;
    if (amount <= 0) {
      setState(() => _saveError = 'vehicle_expense_amount_required'.tr());
      return;
    }
    setState(() => _saveError = null);
    FocusScope.of(context).unfocus();

    final confirmed = await showModalBottomSheet<bool>(
      context: context,
      isScrollControlled: true,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (ctx) => _OtherExpensePreviewSheet(
        dateTime: _dateTimeController.text,
        category: _category,
        amount: _amountController.text.trim(),
        refillLocation: _refillLocation,
        receiptPhoto: _receiptPhoto,
      ),
    );
    if (confirmed == true && mounted) await _doSave(driverId);
  }

  Future<void> _doSave(String driverId) async {
    setState(() {
      _saving = true;
      _saveError = null;
    });
    try {
      final amount = double.tryParse(_amountController.text.trim()) ?? 0;
      final expense = VehicleExpense(
        driverId: driverId,
        type: VehicleExpenseType.other,
        date: _selectedDateTime,
        amount: amount,
        category: _category,
        refillLocation: _refillLocation,
        description: null,
        note: null,
      );
      await saveVehicleExpense(
        expense,
        receiptPhoto: _receiptPhoto != null
            ? List<int>.from(_receiptPhoto!)
            : null,
      );
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('vehicle_expense_saved'.tr())));
        Navigator.of(context).pop(true);
      }
    } on VehicleExpenseOfflineSavedException catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('vehicle_expense_saved_offline'.tr())),
        );
        Navigator.of(context).pop(true);
      }
    } catch (e) {
      if (mounted) {
        setState(() {
          _saving = false;
          _saveError = e.toString();
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text('vehicle_expense_other'.tr()),
        leading: IconButton(
          icon: const Icon(Icons.arrow_back),
          onPressed: () => Navigator.of(context).pop(),
        ),
      ),
      body: Stack(
        children: [
          Form(
            key: _formKey,
            child: SingleChildScrollView(
              padding: const EdgeInsets.all(16.0),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Text(
                    'vehicle_expense_other_form_hint'.tr(),
                    style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                      color: Theme.of(
                        context,
                      ).colorScheme.onSurface.withOpacity(0.7),
                    ),
                  ),
                  const SizedBox(height: 20),
                  // วันที่เวลา — เหมือนหน้าเติมน้ำมัน
                  Text(
                    'refuel_date_time'.tr(),
                    style: Theme.of(context).textTheme.labelMedium?.copyWith(
                      color: Theme.of(
                        context,
                      ).colorScheme.onSurface.withOpacity(0.8),
                    ),
                  ),
                  const SizedBox(height: 6),
                  Material(
                    color: Theme.of(
                      context,
                    ).colorScheme.surfaceContainerHighest,
                    borderRadius: BorderRadius.circular(12),
                    child: InkWell(
                      onTap: _saving ? null : _pickDateTime,
                      borderRadius: BorderRadius.circular(12),
                      child: Container(
                        padding: const EdgeInsets.symmetric(
                          horizontal: 16,
                          vertical: 14,
                        ),
                        decoration: BoxDecoration(
                          borderRadius: BorderRadius.circular(12),
                          border: Border.all(
                            color: Theme.of(context).dividerColor,
                          ),
                        ),
                        child: Row(
                          children: [
                            Icon(
                              Icons.access_time,
                              size: 24,
                              color: Theme.of(context).colorScheme.onSurface,
                            ),
                            const SizedBox(width: 12),
                            Expanded(
                              child: Text(
                                _dateTimeController.text,
                                style: Theme.of(context).textTheme.bodyLarge
                                    ?.copyWith(fontWeight: FontWeight.w500),
                              ),
                            ),
                          ],
                        ),
                      ),
                    ),
                  ),
                  const SizedBox(height: 20),
                  // ประเภทค่าใช้จ่าย
                  DropdownButtonFormField<OtherExpenseCategory>(
                    value: _category,
                    decoration: InputDecoration(
                      labelText: 'vehicle_expense_category'.tr(),
                    ),
                    items: [
                      DropdownMenuItem(
                        value: OtherExpenseCategory.tireRepair,
                        child: Text('vehicle_expense_category_tire'.tr()),
                      ),
                      DropdownMenuItem(
                        value: OtherExpenseCategory.maintenance,
                        child: Text(
                          'vehicle_expense_category_maintenance'.tr(),
                        ),
                      ),
                      DropdownMenuItem(
                        value: OtherExpenseCategory.toll,
                        child: Text('vehicle_expense_category_toll'.tr()),
                      ),
                      DropdownMenuItem(
                        value: OtherExpenseCategory.parking,
                        child: Text('vehicle_expense_category_parking'.tr()),
                      ),
                      DropdownMenuItem(
                        value: OtherExpenseCategory.other,
                        child: Text('vehicle_expense_category_other'.tr()),
                      ),
                    ],
                    onChanged: _saving
                        ? null
                        : (v) => setState(
                            () => _category = v ?? OtherExpenseCategory.other,
                          ),
                  ),
                  const SizedBox(height: 16),
                  // ยอดเงิน
                  TextFormField(
                    controller: _amountController,
                    readOnly: _saving,
                    decoration: InputDecoration(
                      labelText: 'vehicle_expense_amount'.tr(),
                      hintText: 'vehicle_expense_amount_hint'.tr(),
                      prefixText: '฿ ',
                    ),
                    keyboardType: const TextInputType.numberWithOptions(
                      decimal: true,
                    ),
                    validator: (v) {
                      final n = double.tryParse(v?.trim() ?? '');
                      if (n == null || n <= 0)
                        return 'vehicle_expense_amount_required'.tr();
                      return null;
                    },
                  ),
                  const SizedBox(height: 20),
                  // ถ่ายภาพใบเสร็จ + template overlay
                  Text(
                    'other_expense_receipt_title'.tr(),
                    style: Theme.of(context).textTheme.titleMedium?.copyWith(
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    'other_expense_receipt_subtitle'.tr(),
                    style: Theme.of(context).textTheme.bodySmall?.copyWith(
                      color: Theme.of(
                        context,
                      ).colorScheme.onSurface.withOpacity(0.6),
                    ),
                  ),
                  const SizedBox(height: 8),
                  if (_receiptPhoto != null) ...[
                    ClipRRect(
                      borderRadius: BorderRadius.circular(8),
                      child: Image.memory(
                        _receiptPhoto!,
                        height: 160,
                        width: double.infinity,
                        fit: BoxFit.cover,
                      ),
                    ),
                    const SizedBox(height: 8),
                  ],
                  OutlinedButton.icon(
                    onPressed: _saving ? null : _pickReceiptPhoto,
                    icon: const Icon(Icons.receipt_long, size: 20),
                    label: Text(
                      _receiptPhoto != null
                          ? 'other_expense_receipt_change'.tr()
                          : 'other_expense_receipt_take'.tr(),
                    ),
                  ),
                  const SizedBox(height: 20),
                  // สถานที่ (lat,lng) — เหมือนหน้าเติมน้ำมัน
                  Text(
                    'vehicle_expense_refill_location'.tr(),
                    style: Theme.of(context).textTheme.labelMedium?.copyWith(
                      color: Theme.of(
                        context,
                      ).colorScheme.onSurface.withOpacity(0.8),
                    ),
                  ),
                  const SizedBox(height: 6),
                  Container(
                    padding: const EdgeInsets.symmetric(
                      horizontal: 16,
                      vertical: 14,
                    ),
                    decoration: BoxDecoration(
                      color: Theme.of(
                        context,
                      ).colorScheme.surfaceContainerHighest,
                      borderRadius: BorderRadius.circular(12),
                      border: Border.all(color: Theme.of(context).dividerColor),
                    ),
                    child: Row(
                      children: [
                        Icon(
                          Icons.location_on,
                          size: 24,
                          color: Colors.red.shade700,
                        ),
                        const SizedBox(width: 12),
                        Expanded(
                          child: Text(
                            _refillLocation ??
                                'vehicle_expense_refill_location_get'.tr(),
                            style: Theme.of(context).textTheme.bodyLarge
                                ?.copyWith(
                                  fontWeight: FontWeight.w500,
                                  color: _refillLocation != null
                                      ? Theme.of(context).colorScheme.onSurface
                                      : Theme.of(context).colorScheme.onSurface
                                            .withOpacity(0.6),
                                ),
                          ),
                        ),
                        if (_locationLoading)
                          const SizedBox(
                            width: 24,
                            height: 24,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          )
                        else
                          IconButton(
                            icon: Icon(
                              Icons.refresh,
                              color: Theme.of(context).colorScheme.onSurface,
                            ),
                            onPressed: _saving ? null : _getLocation,
                            tooltip: 'vehicle_expense_refill_location_get'.tr(),
                          ),
                      ],
                    ),
                  ),
                  if (_locationError != null) ...[
                    const SizedBox(height: 4),
                    Text(
                      _locationError!,
                      style: TextStyle(
                        color: Theme.of(context).colorScheme.error,
                        fontSize: 12,
                      ),
                    ),
                  ],
                  if (_saveError != null) ...[
                    const SizedBox(height: 16),
                    Text(
                      _saveError!,
                      style: TextStyle(
                        color: Theme.of(context).colorScheme.error,
                      ),
                    ),
                  ],
                  const SizedBox(height: 24),
                  ElevatedButton(
                    onPressed: _saving ? null : _submit,
                    child: _saving
                        ? const SizedBox(
                            height: 24,
                            width: 24,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          )
                        : Text('vehicle_expense_save'.tr()),
                  ),
                ],
              ),
            ),
          ),
          // ตอน Save: disable ทุก input + overlay spinning + กำลังบันทึก...
          if (_saving)
            Positioned.fill(
              child: AbsorbPointer(
                child: ColoredBox(
                  color: Theme.of(
                    context,
                  ).colorScheme.onSurface.withOpacity(0.3),
                  child: Center(
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        const CircularProgressIndicator(),
                        const SizedBox(height: 16),
                        Text(
                          'vehicle_expense_saving'.tr(),
                          style: Theme.of(context).textTheme.bodyLarge
                              ?.copyWith(
                                color: Theme.of(context).colorScheme.onSurface,
                                fontWeight: FontWeight.w500,
                              ),
                        ),
                      ],
                    ),
                  ),
                ),
              ),
            ),
        ],
      ),
    );
  }
}

/// Preview sheet ก่อนบันทึก — แสดงวันที่เวลา, ประเภท, ยอด, สถานที่, รูปใบเสร็จ (เหมือนหน้าเติมน้ำมัน)
class _OtherExpensePreviewSheet extends StatelessWidget {
  final String dateTime;
  final OtherExpenseCategory category;
  final String amount;
  final String? refillLocation;
  final Uint8List? receiptPhoto;

  const _OtherExpensePreviewSheet({
    required this.dateTime,
    required this.category,
    required this.amount,
    this.refillLocation,
    this.receiptPhoto,
  });

  static String _categoryLabel(OtherExpenseCategory c) {
    switch (c) {
      case OtherExpenseCategory.tireRepair:
        return 'vehicle_expense_category_tire'.tr();
      case OtherExpenseCategory.maintenance:
        return 'vehicle_expense_category_maintenance'.tr();
      case OtherExpenseCategory.toll:
        return 'vehicle_expense_category_toll'.tr();
      case OtherExpenseCategory.parking:
        return 'vehicle_expense_category_parking'.tr();
      case OtherExpenseCategory.other:
        return 'vehicle_expense_category_other'.tr();
    }
  }

  @override
  Widget build(BuildContext context) {
    return DraggableScrollableSheet(
      expand: false,
      initialChildSize: 0.7,
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
                  'other_expense_preview_title'.tr(),
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
                    _row('refuel_date_time'.tr(), dateTime),
                    _row(
                      'vehicle_expense_category'.tr(),
                      _categoryLabel(category),
                    ),
                    _row(
                      'vehicle_expense_amount'.tr(),
                      amount.isEmpty ? '' : '฿ $amount',
                    ),
                    _row(
                      'vehicle_expense_refill_location'.tr(),
                      refillLocation ?? '',
                    ),
                    const SizedBox(height: 12),
                    if (receiptPhoto != null && receiptPhoto!.isNotEmpty) ...[
                      Text(
                        'other_expense_receipt_title'.tr(),
                        style: const TextStyle(fontWeight: FontWeight.w600),
                      ),
                      const SizedBox(height: 8),
                      GestureDetector(
                        onTap: () => _showFullImage(context, receiptPhoto!),
                        child: ClipRRect(
                          borderRadius: BorderRadius.circular(8),
                          child: Image.memory(
                            receiptPhoto!,
                            height: 120,
                            fit: BoxFit.cover,
                          ),
                        ),
                      ),
                    ],
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
                        label: Text('other_expense_preview_edit'.tr()),
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
                        label: Text('other_expense_preview_confirm'.tr()),
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

  Widget _row(String label, String value) {
    if (value.isEmpty) return const SizedBox.shrink();
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
