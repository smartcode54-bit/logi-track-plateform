import 'package:easy_localization/easy_localization.dart';
import 'package:firebase_auth/firebase_auth.dart';
import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:image_picker/image_picker.dart';
import '../../../home/data/repositories/checkin_repository.dart';
import '../../../home/data/services/cloud_vision_ocr_service.dart';
import '../../../home/data/services/fuel_receipt_ocr_service.dart';
import '../../../home/data/services/image_compression_service.dart';
import '../../data/models/vehicle_expense.dart';
import '../../data/repositories/vehicle_expense_repository.dart';

/// รูปแบบวันที่เวลา: dd/MM/yyyy HH:mm:ss
final DateFormat _dateTimeFormat = DateFormat('dd/MM/yyyy HH:mm:ss');

/// แปลงช่องยอดเงิน/ลิตรที่พิมพ์เอง (ตัด comma คั่นหลักพัน ถ้ามี)
double? _parseDecimalInput(String raw) {
  var t = raw.trim().replaceAll(RegExp(r'\s'), '');
  if (t.isEmpty) return null;
  t = t.replaceAll(',', '');
  return double.tryParse(t);
}

/// OCR อาจอ่านเลขทะเบียนเข้าช่องเลขไมล์ — ใช้เกณฑ์เดียวกับ fuel_receipt_ocr_service
String? _sanitizeOcrOdometerForForm(String raw) {
  final digits = raw.replaceAll(RegExp(r'\D'), '');
  if (digits.length < 5 || digits.length > 8) return null;
  final v = int.tryParse(digits);
  if (v == null || v < 10000 || v > 9999999) return null;
  return digits;
}

class RefuelFormPage extends StatefulWidget {
  final VehicleExpense? initialData;
  final String? truckId;
  final String? truckLicensePlate;

  const RefuelFormPage({super.key, this.initialData, this.truckId, this.truckLicensePlate});

  @override
  State<RefuelFormPage> createState() => _RefuelFormPageState();
}

class _RefuelFormPageState extends State<RefuelFormPage> {
  final _formKey = GlobalKey<FormState>();
  final _dateTimeController = TextEditingController();
  final _amountController = TextEditingController();
  final _volumeController = TextEditingController();
  final _pricePerLiterController = TextEditingController();
  final _odometerController = TextEditingController();
  final _taxIdController = TextEditingController();
  final _taxInvIdController = TextEditingController();

  /// วันที่เวลา (Current date/time format dd/MM/yyyy HH:mm:ss)
  DateTime _selectedDateTime = DateTime.now();
  Uint8List? _receiptPhoto;
  Uint8List? _odometerPhoto;
  bool _ocrLoading = false;
  String? _ocrError;

  /// สถานที่เติม "lat,lng" จาก getCurrentPosition (วิธีเดียวกับ loading_phase / delivery_phase)
  String? _refillLocation;
  bool _locationLoading = false;
  String? _locationError;
  bool _saving = false;
  String? _saveError;

  @override
  void initState() {
    super.initState();
    _amountController.addListener(_updatePricePerLiter);
    _volumeController.addListener(_updatePricePerLiter);

    if (widget.initialData != null) {
      final data = widget.initialData!;
      _selectedDateTime = data.date;
      _amountController.text = data.amount > 0
          ? data.amount.toStringAsFixed(2)
          : '';
      if (data.volumeLiters != null) {
        _volumeController.text = data.volumeLiters!.toStringAsFixed(2);
      }
      if (data.odometer != null) {
        _odometerController.text = data.odometer.toString();
      }
      if (data.stationTaxId != null) _taxIdController.text = data.stationTaxId!;
      if (data.taxInvId != null) _taxInvIdController.text = data.taxInvId!;
      _refillLocation = data.refillLocation;

      // We can't easily populate _receiptPhoto / _odometerPhoto from network URL
      // without downloading bytes, so we force the user to re-take/re-pick for Edit mode.
    }

    _updateDateTimeDisplay();
    WidgetsBinding.instance.addPostFrameCallback((_) => _getRefillLocation());
  }

  /// คำนวณราคาต่อลิตรจาก ยอดเงิน/ปริมาณลิตร (ช่องราคา/ลิตรเป็น read-only)
  void _updatePricePerLiter() {
    final amount = _parseDecimalInput(_amountController.text);
    final volume = _parseDecimalInput(_volumeController.text);
    if (amount != null && volume != null && volume > 0) {
      final price = amount / volume;
      if (_pricePerLiterController.text != price.toStringAsFixed(2)) {
        _pricePerLiterController.text = price.toStringAsFixed(2);
      }
    } else {
      if (_pricePerLiterController.text.isNotEmpty) {
        _pricePerLiterController.clear();
      }
    }
  }

  void _updateDateTimeDisplay() {
    _dateTimeController.text = _dateTimeFormat.format(_selectedDateTime);
  }

  @override
  void dispose() {
    _amountController.removeListener(_updatePricePerLiter);
    _volumeController.removeListener(_updatePricePerLiter);
    _dateTimeController.dispose();
    _amountController.dispose();
    _volumeController.dispose();
    _pricePerLiterController.dispose();
    _odometerController.dispose();
    _taxIdController.dispose();
    _taxInvIdController.dispose();
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

  /// ถ่ายรูปบิลน้ำมัน หรือเลือกจากแกลเลอรี แล้วรัน OCR สกัด จำนวนลิตร ยอดเงิน TAX ID สถานี TAX INV ID เลขไมล์
  Future<void> _pickReceiptAndOcr() async {
    if (kIsWeb) return;
    final picker = ImagePicker();
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
    final xfile = await picker.pickImage(source: source, imageQuality: 85);
    if (xfile == null || !mounted) return;
    final imageBytes = await xfile.readAsBytes();
    if (!mounted) return;

    setState(() {
      _ocrLoading = true;
      _ocrError = null;
    });
    try {
      // ถ่ายจากกล้อง → ใส่ overlay (วันเวลา สถานที่ พิกัด เข็มทิศ) เหมือนจุดอื่น; จากแกลเลอรี → compress อย่างเดียว
      final compressed = source == ImageSource.camera
          ? await stampOverlayAndCompressForEvidence(imageBytes)
          : await compressImageForUpload(imageBytes);
      if (!mounted) return;

      if (!isCloudVisionApiKeyConfigured()) {
        setState(() {
          _receiptPhoto = compressed;
          _ocrLoading = false;
        });
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text('ocr_missing_api_key_hint'.tr()),
            backgroundColor: Colors.orange,
          ),
        );
        return;
      }

      final result = await runFuelReceiptOcrOnImageBytes(imageBytes);
      if (!mounted) return;
      setState(() {
        _receiptPhoto = compressed;
        _ocrLoading = false;
        if (result.totalAmount != null) {
          _amountController.text = result.totalAmount!.toStringAsFixed(2);
        }
        if (result.liter != null) {
          _volumeController.text = result.liter!.toStringAsFixed(2);
        }
        if (result.taxId != null && result.taxId!.isNotEmpty) {
          _taxIdController.text = result.taxId!;
        }
        if (result.taxInvNo != null && result.taxInvNo!.isNotEmpty) {
          _taxInvIdController.text = result.taxInvNo!;
        }
        if (result.odometer != null) {
          final o = _sanitizeOcrOdometerForForm(result.odometer!);
          if (o != null) _odometerController.text = o;
        }
        _updatePricePerLiter();
      });
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('refuel_ocr_done'.tr())));
      }
    } catch (e) {
      if (mounted) {
        setState(() {
          _ocrLoading = false;
          _ocrError = e.toString();
        });
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text('${'refuel_ocr_failed'.tr()} $e'),
            backgroundColor: Colors.red,
          ),
        );
      }
    }
  }

  /// ยืนยันก่อนล้างฟอร์ม (เหมือน loading_phase_page)
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
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(true),
            child: Text('loading_phase_clear_form'.tr()),
          ),
        ],
      ),
    );
    if (confirm == true && mounted) _clearForm();
  }

  void _clearForm() {
    _dateTimeController.text = _dateTimeFormat.format(DateTime.now());
    _selectedDateTime = DateTime.now();
    _amountController.clear();
    _volumeController.clear();
    _pricePerLiterController.clear();
    _odometerController.clear();
    _taxIdController.clear();
    _taxInvIdController.clear();
    setState(() {
      _receiptPhoto = null;
      _odometerPhoto = null;
      _ocrError = null;
      _saveError = null;
    });
    _getRefillLocation();
  }

  /// ถ่ายรูปบันทึกเลขไมล์ (หน้าปัด/ตัวเลขไมล์) — จากกล้องใช้ template overlay เหมือนจุดอื่น
  Future<void> _pickOdometerPhoto() async {
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
    final bytes = await xfile.readAsBytes();
    if (!mounted) return;
    // ถ่ายจากกล้อง → stamp overlay (วันเวลา สถานที่ พิกัด เข็มทิศ); จากแกลเลอรี → compress อย่างเดียว
    final compressed = source == ImageSource.camera
        ? await stampOverlayAndCompressForEvidence(bytes)
        : await compressImageForUpload(bytes);
    if (!mounted) return;
    setState(() => _odometerPhoto = compressed);
  }

  Future<void> _getRefillLocation() async {
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

  /// กด Save → validate แล้วแสดง preview ให้ driver ยืนยันหรือแก้ไข
  Future<void> _submit() async {
    FocusScope.of(context).unfocus();
    if (!_formKey.currentState!.validate()) return;
    final driverId = FirebaseAuth.instance.currentUser?.uid;
    if (driverId == null || driverId.isEmpty) {
      setState(() => _saveError = 'user_not_authenticated'.tr());
      return;
    }
    final amount = _parseDecimalInput(_amountController.text) ?? 0;
    if (amount <= 0) {
      setState(() => _saveError = 'vehicle_expense_amount_required'.tr());
      return;
    }
    final taxInvId = _taxInvIdController.text.trim();
    if (taxInvId.isNotEmpty) {
      final isDuplicate = await existsTaxInvIdForDriver(driverId, taxInvId);
      if (isDuplicate && mounted) {
        setState(() => _saveError = 'vehicle_expense_tax_inv_duplicate'.tr());
        return;
      }
    }

    // บังคับถ่ายรูปบิลน้ำมัน (เพื่อให้มั่นใจว่าผ่านกระบวนการ OCR หรือมีหลักฐาน)
    if (_receiptPhoto == null) {
      setState(() => _saveError = 'refuel_receipt_take_photo_required'.tr());
      return;
    }

    // บังคับถ่ายรูปหน้าปัดไมล์เสมอ
    if (_odometerPhoto == null) {
      setState(() => _saveError = 'refuel_odometer_take_photo_required'.tr());
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
      builder: (ctx) => _RefuelPreviewSheet(
        dateTime: _dateTimeController.text,
        taxId: _taxIdController.text.trim(),
        taxInvId: _taxInvIdController.text.trim(),
        amount: _amountController.text.trim(),
        volume: _volumeController.text.trim(),
        pricePerLiter: _pricePerLiterController.text.trim(),
        odometer: _odometerController.text.trim(),
        refillLocation: _refillLocation,
        receiptPhoto: _receiptPhoto,
        odometerPhoto: _odometerPhoto,
      ),
    );
    if (confirmed == true && mounted) await _doSave(driverId);
  }

  /// บันทึกจริงหลัง driver ยืนยันจาก preview
  Future<void> _doSave(String driverId) async {
    setState(() {
      _saving = true;
      _saveError = null;
    });
    try {
      final amount = _parseDecimalInput(_amountController.text) ?? 0;
      final vol = _parseDecimalInput(_volumeController.text);
      final odo = int.tryParse(_odometerController.text.trim());
      final taxId = _taxIdController.text.trim().isEmpty
          ? null
          : _taxIdController.text.trim();
      final taxInv = _taxInvIdController.text.trim().isEmpty
          ? null
          : _taxInvIdController.text.trim();

      final expense = VehicleExpense(
        id: widget.initialData?.id, // Use existing ID if editing
        driverId: driverId,
        type: VehicleExpenseType.fuel,
        date: _selectedDateTime,
        amount: amount,
        volumeLiters: vol,
        pricePerLiter: (vol != null && vol > 0) ? (amount / vol) : null,
        odometer: odo,
        stationTaxId: taxId,
        taxInvId: taxInv,
        refillLocation: _refillLocation,
        status: 'PENDING',
        adminNote: null,
        truckId: widget.initialData?.truckId ?? widget.truckId,
        truckLicensePlate: widget.initialData?.truckLicensePlate ?? widget.truckLicensePlate,
      );
      await saveVehicleExpense(
        expense,
        receiptPhoto: _receiptPhoto != null
            ? List<int>.from(_receiptPhoto!)
            : null,
        odometerPhoto: _odometerPhoto != null
            ? List<int>.from(_odometerPhoto!)
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
    final refuelOcrReadingLabel = 'refuel_ocr_reading'.tr();
    return Scaffold(
      appBar: AppBar(
        title: Text('vehicle_expense_refuel'.tr()),
        leading: IconButton(
          icon: const Icon(Icons.arrow_back),
          onPressed: () => Navigator.of(context).pop(),
        ),
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
      body: Stack(
        children: [
          SafeArea(
            top: false,
            child: Form(
              key: _formKey,
              child: SingleChildScrollView(
                padding: const EdgeInsets.all(16.0),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Text(
                    'vehicle_expense_refuel_form_hint'.tr(),
                    style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                      color: Theme.of(
                        context,
                      ).colorScheme.onSurface.withOpacity(0.7),
                    ),
                  ),
                  const SizedBox(height: 20),
                  // วันที่เวลา — UI แบบกล่องมี icon นาฬิกา
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
                  // ถ่ายรูปบิลน้ำมัน (OCR: จำนวนลิตร, ยอดเงิน, ชื่อสถานี, เลขไมล์)
                  Text(
                    'refuel_receipt_title'.tr(),
                    style: Theme.of(context).textTheme.titleMedium?.copyWith(
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    'refuel_receipt_subtitle'.tr(),
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
                  if (_ocrLoading)
                    Padding(
                      padding: const EdgeInsets.symmetric(vertical: 12),
                      child: Row(
                        mainAxisAlignment: MainAxisAlignment.center,
                        children: [
                          const SizedBox(
                            width: 20,
                            height: 20,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          ),
                          const SizedBox(width: 12),
                          Text(refuelOcrReadingLabel),
                        ],
                      ),
                    ),
                  if (_ocrError != null)
                    Padding(
                      padding: const EdgeInsets.only(bottom: 8),
                      child: Text(
                        _ocrError!,
                        style: TextStyle(
                          color: Theme.of(context).colorScheme.error,
                          fontSize: 12,
                        ),
                      ),
                    ),
                  OutlinedButton.icon(
                    onPressed: (_saving || _ocrLoading)
                        ? null
                        : _pickReceiptAndOcr,
                    icon: const Icon(Icons.receipt_long, size: 20),
                    label: Text(
                      _receiptPhoto != null
                          ? 'refuel_receipt_change'.tr()
                          : 'refuel_receipt_take'.tr(),
                    ),
                  ),
                  const SizedBox(height: 20),
                  // ลำดับ: TAX ID, TAX INV ID, ยอดเงิน, ปริมาณลิตร, ราคาต่อลิตร (คำนวณ/ disable)
                  TextFormField(
                    controller: _taxIdController,
                    readOnly: _saving,
                    decoration: InputDecoration(
                      labelText: 'refuel_station_tax_id'.tr(),
                      hintText: 'refuel_station_tax_id_hint'.tr(),
                    ),
                    keyboardType: TextInputType.number,
                  ),
                  const SizedBox(height: 16),
                  TextFormField(
                    controller: _taxInvIdController,
                    readOnly: _saving,
                    decoration: InputDecoration(
                      labelText: 'refuel_tax_inv_id'.tr(),
                      hintText: 'refuel_tax_inv_id_hint'.tr(),
                    ),
                    keyboardType: TextInputType.number,
                  ),
                  const SizedBox(height: 16),
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
                    inputFormatters: [
                      FilteringTextInputFormatter.allow(RegExp(r'[0-9.,]')),
                    ],
                    validator: (v) {
                      final n = _parseDecimalInput(v ?? '');
                      if (n == null || n <= 0) {
                        return 'vehicle_expense_amount_required'.tr();
                      }
                      return null;
                    },
                  ),
                  const SizedBox(height: 16),
                  TextFormField(
                    controller: _volumeController,
                    readOnly: _saving,
                    decoration: InputDecoration(
                      labelText: 'vehicle_expense_volume'.tr(),
                      hintText: 'vehicle_expense_volume_hint'.tr(),
                    ),
                    keyboardType: const TextInputType.numberWithOptions(
                      decimal: true,
                    ),
                    inputFormatters: [
                      FilteringTextInputFormatter.allow(RegExp(r'[0-9.,]')),
                    ],
                  ),
                  const SizedBox(height: 16),
                  TextFormField(
                    controller: _pricePerLiterController,
                    readOnly: true,
                    decoration: InputDecoration(
                      labelText: 'vehicle_expense_price_per_liter'.tr(),
                      hintText: 'vehicle_expense_price_per_liter_hint'.tr(),
                      prefixText: '฿ ',
                    ),
                  ),
                  const SizedBox(height: 16),
                  TextFormField(
                    controller: _odometerController,
                    readOnly: _saving,
                    decoration: InputDecoration(
                      labelText: 'vehicle_expense_odometer'.tr(),
                      hintText: 'vehicle_expense_odometer_hint'.tr(),
                    ),
                    keyboardType: TextInputType.number,
                    inputFormatters: [
                      FilteringTextInputFormatter.digitsOnly,
                    ],
                  ),
                  const SizedBox(height: 16),
                  // หลังกรอกเลขไมล์: ถ่ายรูปบันทึกเลขไมล์
                  Text(
                    'refuel_odometer_photo_title'.tr(),
                    style: Theme.of(context).textTheme.titleMedium?.copyWith(
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    'refuel_odometer_photo_subtitle'.tr(),
                    style: Theme.of(context).textTheme.bodySmall?.copyWith(
                      color: Theme.of(
                        context,
                      ).colorScheme.onSurface.withOpacity(0.6),
                    ),
                  ),
                  const SizedBox(height: 8),
                  if (_odometerPhoto != null) ...[
                    ClipRRect(
                      borderRadius: BorderRadius.circular(8),
                      child: Image.memory(
                        _odometerPhoto!,
                        height: 140,
                        width: double.infinity,
                        fit: BoxFit.cover,
                      ),
                    ),
                    const SizedBox(height: 8),
                  ],
                  OutlinedButton.icon(
                    onPressed: _saving ? null : _pickOdometerPhoto,
                    icon: const Icon(Icons.speed, size: 20),
                    label: Text(
                      _odometerPhoto != null
                          ? 'refuel_odometer_photo_change'.tr()
                          : 'refuel_odometer_photo_take'.tr(),
                    ),
                  ),
                  const SizedBox(height: 16),
                  // สถานที่เติม — UI แบบกล่องมี icon สถานที่ + ปุ่ม refresh
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
                            onPressed: _saving ? null : _getRefillLocation,
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
                        color: Colors.red[600],
                        fontWeight: FontWeight.w500,
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
          ),
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

/// Bottom sheet preview รายการเติมน้ำมัน (UI เหมือน loading_phase_preview)
class _RefuelPreviewSheet extends StatelessWidget {
  final String dateTime;
  final String taxId;
  final String taxInvId;
  final String amount;
  final String volume;
  final String pricePerLiter;
  final String odometer;
  final String? refillLocation;
  final Uint8List? receiptPhoto;
  final Uint8List? odometerPhoto;

  const _RefuelPreviewSheet({
    required this.dateTime,
    required this.taxId,
    required this.taxInvId,
    required this.amount,
    required this.volume,
    required this.pricePerLiter,
    required this.odometer,
    this.refillLocation,
    this.receiptPhoto,
    this.odometerPhoto,
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
                  'refuel_preview_title'.tr(),
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
                    ..._buildRows(),
                    const SizedBox(height: 12),
                    if (receiptPhoto != null && receiptPhoto!.isNotEmpty) ...[
                      Text(
                        'refuel_receipt_title'.tr(),
                        style: const TextStyle(fontWeight: FontWeight.w600),
                      ),
                      const SizedBox(height: 8),
                      _buildPreviewThumb(
                        context,
                        receiptPhoto!,
                        height: 100,
                        allImages: _allImages(),
                        initialIndex: 0,
                      ),
                      const SizedBox(height: 12),
                    ],
                    if (odometerPhoto != null && odometerPhoto!.isNotEmpty) ...[
                      Text(
                        'refuel_odometer_photo_title'.tr(),
                        style: const TextStyle(fontWeight: FontWeight.w600),
                      ),
                      const SizedBox(height: 8),
                      _buildPreviewThumb(
                        context,
                        odometerPhoto!,
                        height: 80,
                        allImages: _allImages(),
                        initialIndex:
                            receiptPhoto != null && receiptPhoto!.isNotEmpty
                            ? 1
                            : 0,
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
                        label: Text('refuel_preview_edit'.tr()),
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
                        label: Text('refuel_preview_confirm'.tr()),
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

  List<Widget> _buildRows() {
    return [
      _row('refuel_date_time'.tr(), dateTime),
      _row('refuel_station_tax_id'.tr(), taxId),
      _row('refuel_tax_inv_id'.tr(), taxInvId),
      _row('vehicle_expense_amount'.tr(), amount.isEmpty ? '' : '฿ $amount'),
      _row('vehicle_expense_volume'.tr(), volume),
      _row(
        'vehicle_expense_price_per_liter'.tr(),
        pricePerLiter.isEmpty ? '' : '฿ $pricePerLiter',
      ),
      _row('vehicle_expense_odometer'.tr(), odometer),
      _row('vehicle_expense_refill_location'.tr(), refillLocation ?? ''),
    ];
  }

  List<Uint8List> _allImages() {
    final list = <Uint8List>[];
    if (receiptPhoto != null && receiptPhoto!.isNotEmpty) {
      list.add(receiptPhoto!);
    }
    if (odometerPhoto != null && odometerPhoto!.isNotEmpty) {
      list.add(odometerPhoto!);
    }
    return list;
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

  void _showFullImage(
    BuildContext context,
    List<Uint8List> images, {
    int initialIndex = 0,
  }) {
    if (images.isEmpty) return;
    showDialog(
      context: context,
      builder: (ctx) => _RefuelFullScreenPreviewDialog(
        images: images,
        initialIndex: initialIndex.clamp(0, images.length - 1),
        onClose: () => Navigator.of(ctx).pop(),
      ),
    );
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
}

/// Dialog แสดงรูปเต็มจอ — เลื่อนซ้าย/ขวาดูรูปทั้งหมดได้, pinch-to-zoom (แบบเดียวกับ loading_phase)
class _RefuelFullScreenPreviewDialog extends StatefulWidget {
  final List<Uint8List> images;
  final int initialIndex;
  final VoidCallback onClose;

  const _RefuelFullScreenPreviewDialog({
    required this.images,
    required this.initialIndex,
    required this.onClose,
  });

  @override
  State<_RefuelFullScreenPreviewDialog> createState() =>
      _RefuelFullScreenPreviewDialogState();
}

class _RefuelFullScreenPreviewDialogState
    extends State<_RefuelFullScreenPreviewDialog> {
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
