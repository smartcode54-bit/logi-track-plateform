import 'package:flutter_test/flutter_test.dart';
import 'package:logi_track_driver_app/features/home/data/services/ocr_screenshot_service.dart';

void main() {
  group('parseOcrScreenshotFromRawText', () {
    test('keeps SPX behavior and parses partner from LH/FM tag', () {
      const text = '''
เลขทริป : LT0Q2E2467U61
Seal Code: SPX 2723354
ชื่อคนขับ LH-TTP
สถานีเริ่มต้น: HUB-A
สถานีถัดไป: SOCN
''';

      final result = parseOcrScreenshotFromRawText(
        text,
        kind: OcrImageKind.runsheetSpx,
      );

      expect(result.tripId, 'LT0Q2E2467U61');
      expect(result.sealCode, 'SPX2723354');
      expect(result.partnerCode, 'TTP');
      expect(result.ocrProfile, 'spx_runsheet');
    });

    test('maps ZX profile to tripId and fixed partner code', () {
      const text = '''
วันที่ปล่อยรถ 2026-04-26 18:30
สถานีต้นทาง 29Nong Ruea06
สถานีปลายทาง KKN_GW
ซัพพลายเออร์ CJSF
ZXZB26008589091
''';

      final result = parseOcrScreenshotFromRawText(
        text,
        kind: OcrImageKind.waybillZx,
      );

      expect(result.tripId, 'ZXZB26008589091');
      expect(result.partnerCode, zxPartnerCode);
      expect(result.supplierCode, 'CJSF');
      expect(result.releaseTime, isNotNull);
      expect(result.ocrProfile, 'zx_waybill');
    });

    test('preserves scanned seal source when barcode values provided', () {
      const text = '''
สถานีต้นทาง AAA
สถานีปลายทาง BBB
''';

      final result = parseOcrScreenshotFromRawText(
        text,
        kind: OcrImageKind.waybillZx,
        barcodeTripId: 'ZXZB1234567890',
        barcodeSealCode: '32742262',
        barcodeSecondarySealCode: '33381437',
      );

      expect(result.tripId, 'ZXZB1234567890');
      expect(result.sealCode, '32742262');
      expect(result.secondarySealCode, '33381437');
      expect(result.sealSource, 'scanned');
      expect(result.partnerCode, zxPartnerCode);
    });
  });

  group('raw barcode helpers', () {
    test('extracts ZX tripId from noisy raw value', () {
      final raw = 'Trip Ref : ZXZB26008589091 (รอบที่1)';
      final trip = extractTripIdFromRawValue(raw);
      expect(trip, 'ZXZB26008589091');
    });

    test('extracts numeric seal in seal mode', () {
      final raw = 'Seal# 32742262';
      final seal = extractPrimarySealFromRawValue(raw, kind: OcrImageKind.seal);
      expect(seal, '32742262');
    });
  });
}
