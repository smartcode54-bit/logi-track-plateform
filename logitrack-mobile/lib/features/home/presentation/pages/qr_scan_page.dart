import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/material.dart';
import 'package:image_picker/image_picker.dart';
import 'package:mobile_scanner/mobile_scanner.dart';
import 'package:easy_localization/easy_localization.dart';
import '../../data/utils/image_path_io.dart' if (dart.library.html) '../../data/utils/image_path_stub.dart' as path_utils;

/// หน้าสแกน QR/Barcode คืนค่า rawValue กลับทาง Navigator.pop(context, value).
/// รองรับสแกนจากกล้อง หรือเลือกรูปจากแกลเลอรี่ (อ่านจากรูป).
class QrScanPage extends StatefulWidget {
  final String title;

  const QrScanPage({super.key, this.title = 'loading_phase_scan_qr'});

  @override
  State<QrScanPage> createState() => _QrScanPageState();
}

class _QrScanPageState extends State<QrScanPage> {
  final MobileScannerController _controller = MobileScannerController(
    detectionSpeed: DetectionSpeed.normal,
    facing: CameraFacing.back,
    torchEnabled: false,
  );
  bool _scanned = false;

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  void _onDetect(BarcodeCapture capture) {
    if (_scanned) return;
    final barcodes = capture.barcodes;
    if (barcodes.isEmpty) return;
    final raw = barcodes.first.rawValue?.trim();
    if (raw == null || raw.isEmpty) return;
    _scanned = true;
    if (mounted) Navigator.of(context).pop(raw);
  }

  Future<void> _pickImageAndDecode() async {
    if (kIsWeb) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('loading_phase_scan_web_hint'.tr())),
        );
      }
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
    final xfile = await picker.pickImage(source: source, imageQuality: 90);
    if (xfile == null || !mounted) return;

    final path = await path_utils.getImagePathForDecode(xfile);
    if (path == null || path.isEmpty) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('loading_phase_scan_web_hint'.tr())),
        );
      }
      return;
    }
    if (!mounted) return;
    try {
      final capture = await _controller.analyzeImage(path);
      if (!mounted) return;
      final list = capture?.barcodes ?? [];
      final raw = list.isNotEmpty ? list.first.rawValue?.trim() : null;
      if (raw != null && raw.isNotEmpty) {
        Navigator.of(context).pop(raw);
      } else {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('loading_phase_scan_no_code'.tr()), backgroundColor: Colors.orange),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('${'loading_phase_scan_failed'.tr()} $e'), backgroundColor: Colors.red),
        );
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    if (kIsWeb) {
      return Scaffold(
        appBar: AppBar(title: Text(widget.title.tr())),
        body: Center(
          child: Column(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              Text('loading_phase_scan_web_hint'.tr(), textAlign: TextAlign.center),
              const SizedBox(height: 16),
              OutlinedButton.icon(
                onPressed: () => Navigator.of(context).pop(),
                icon: const Icon(Icons.close),
                label: Text('ok'.tr()),
              ),
            ],
          ),
        ),
      );
    }
    return Scaffold(
      appBar: AppBar(
        title: Text(widget.title.tr()),
        actions: [
          TextButton.icon(
            onPressed: _pickImageAndDecode,
            icon: const Icon(Icons.photo_library),
            label: Text('loading_phase_scan_from_image'.tr()),
          ),
        ],
      ),
      body: Stack(
        fit: StackFit.expand,
        children: [
          MobileScanner(
            controller: _controller,
            onDetect: _onDetect,
          ),
          Positioned(
            left: 0,
            right: 0,
            bottom: 24,
            child: Center(
              child: Text(
                'loading_phase_scan_hint'.tr(),
                style: TextStyle(color: Colors.white, shadows: [Shadow(color: Colors.black.withValues(alpha: 0.8), blurRadius: 4)]),
              ),
            ),
          ),
        ],
      ),
    );
  }
}
