import 'package:flutter/foundation.dart' show debugPrint;

import 'cloud_vision_ocr_service.dart';
import 'ocr_digit_normalizer.dart';

class FuelReceiptOcrResult {
  final String? tid;
  final String? taxId;
  final String? taxInvNo;
  final String? date;
  final String? time;
  final double? liter;
  final double? totalAmount;
  final String? odometer;
  // Included for validation purposes
  final double? pricePerLiter;
  final bool? isValidated;

  FuelReceiptOcrResult({
    this.tid,
    this.taxId,
    this.taxInvNo,
    this.date,
    this.time,
    this.liter,
    this.totalAmount,
    this.odometer,
    this.pricePerLiter,
    this.isValidated,
  });

  Map<String, dynamic> toJson() => {
    'tid': tid,
    'tax_id': taxId,
    'tax_inv_no': taxInvNo,
    'date': date,
    'time': time,
    'liter': liter,
    'total_amount': totalAmount,
    'odometer': odometer,
  };

  @override
  String toString() {
    return 'FuelReceiptOcrResult(tid: $tid, taxId: $taxId, taxInvNo: $taxInvNo, date: $date, time: $time, liter: $liter, totalAmount: $totalAmount, odometer: $odometer, isValidated: $isValidated)';
  }
}

/// Runs OCR on raw image bytes for a Fuel Receipt (Vision API).
Future<FuelReceiptOcrResult> runFuelReceiptOcrOnImageBytes(
  List<int> imageBytes,
) async {
  try {
    final fullText = await runCloudVisionOcrOnImageBytes(imageBytes);

    debugPrint(
      '=== OCR Fuel Receipt Full Text ===\n${fullText ?? "(empty)"}\n===================================',
    );

    if (fullText == null || fullText.isEmpty) {
      return FuelReceiptOcrResult();
    }

    final tid = _clean(_extractTid(fullText));
    final taxId = _clean(_extractTaxId(fullText));
    final taxInvNo = _clean(_extractTaxInvNo(fullText));
    final date = _clean(_extractDate(fullText));
    final time = _clean(_extractTime(fullText));

    final literRaw = _extractLiter(fullText);
    final priceRaw = _extractPricePerLiter(fullText);
    final totalRaw = _extractTotalAmount(fullText);

    double? liter = _parseDouble(normalizeOcrDigits(literRaw));
    double? pricePerLiter = _parseDouble(normalizeOcrDigits(priceRaw));
    double? totalAmount = _parseDouble(normalizeOcrDigits(totalRaw));

    // Advanced Fallback: If OCR returns values separated from labels by newlines
    if (totalAmount == null || liter == null || pricePerLiter == null) {
      final allFloats = _extractAllFloats(fullText);
      if (allFloats.length >= 3) {
        bool mathMatched = false;
        // Try to find L * P = T combination in all floats
        for (int i = 0; i < allFloats.length && !mathMatched; i++) {
          for (int j = 0; j < allFloats.length && !mathMatched; j++) {
            if (i == j) continue;
            for (int k = 0; k < allFloats.length && !mathMatched; k++) {
              if (k == i || k == j) continue;
              final L = allFloats[i];
              final P = allFloats[j];
              final T = allFloats[k];
              // Math logic validation with float error margin
              if ((L * P - T).abs() <= 1.0) {
                // Ensure sensible bounds: Price usually 20-55, Liter 5-1000, Total >= Liter
                if (P > 10 && P < 60 && L > 5 && T >= L) {
                  liter ??= L;
                  pricePerLiter ??= P;
                  totalAmount ??= T;
                  mathMatched = true;
                }
              }
            }
          }
        }
      }
    }

    // Data Validation Logic: Double check totalAmount = liter * pricePerLiter
    bool isValidated = false;

    if (liter != null && pricePerLiter != null) {
      final double calculatedTotal = double.parse(
        (liter * pricePerLiter).toStringAsFixed(2),
      );

      if (totalAmount != null) {
        // Allow a small margin of error for rounding (e.g., 1,990.00 vs 1990.09)
        final double diff = (totalAmount - calculatedTotal).abs();
        if (diff <= 1.0) {
          isValidated = true;
        } else {
          debugPrint(
            'OCR Validation Warning: totalAmount ($totalAmount) != liter ($liter) * price ($pricePerLiter) [$calculatedTotal]',
          );
        }
      } else {
        totalAmount = calculatedTotal;
        isValidated = true;
      }
    } else if (totalAmount != null && liter != null) {
      isValidated = true;
    }

    final odometer = _clean(_extractOdometer(fullText));

    debugPrint('''
=== OCR Extracted Data ===
TID: $tid
Tax ID: $taxId
Tax Inv No: $taxInvNo
Date: $date
Time: $time
Liter: $liter
Price/Liter: $pricePerLiter
Total Amount: $totalAmount
Odometer: $odometer
Is Validated (Math Check): $isValidated
==========================
''');

    return FuelReceiptOcrResult(
      tid: tid,
      taxId: taxId,
      taxInvNo: taxInvNo,
      date: date,
      time: time,
      liter: liter,
      totalAmount: totalAmount,
      odometer: odometer,
      pricePerLiter: pricePerLiter,
      isValidated: isValidated,
    );
  } catch (e, st) {
    debugPrint('Fuel Receipt OCR error: $e\n$st');
    return FuelReceiptOcrResult();
  }
}

String? _clean(String? v) => (v != null && v.isNotEmpty) ? v.trim() : null;

double? _parseDouble(String? v) {
  if (v == null || v.isEmpty) return null;
  // Replace comma with empty if it's not the decimal separator
  // We want to translate 1.680.00 -> 1680.00 and 1,578.89 -> 1578.89
  var cleanStr = v.replaceAll(RegExp(r'[^\d\.,]'), '');

  final lastDot = cleanStr.lastIndexOf('.');
  final lastComma = cleanStr.lastIndexOf(',');
  final separatorIndex = lastDot > lastComma ? lastDot : lastComma;

  if (separatorIndex != -1 && cleanStr.length - separatorIndex == 3) {
    // it's a decimal separator (since it's exactly 2 chars from the end)
    final wholePart = cleanStr
        .substring(0, separatorIndex)
        .replaceAll(RegExp(r'[\.,]'), '');
    final decimalPart = cleanStr.substring(separatorIndex + 1);
    cleanStr = '$wholePart.$decimalPart';
  } else {
    // No valid decimal separator at the end, just remove all dots and commas
    cleanStr = cleanStr.replaceAll(RegExp(r'[\.,]'), '');
  }

  if (cleanStr.isEmpty) return null;
  return double.tryParse(cleanStr);
}

// Extract all valid floats on the document to run advanced Math-Matching check
List<double> _extractAllFloats(String text) {
  final matches = RegExp(
    '\\b($kDigitOrConfusable{1,3}(?:[.,]$kDigitOrConfusable{3})*[.,]$kDigitOrConfusable{2}|$kDigitOrConfusable+[.,]$kDigitOrConfusable{2})\\b',
  ).allMatches(text);
  final floats = <double>[];
  for (final m in matches) {
    final normalized = normalizeOcrDigits(m.group(1));
    final parsed = _parseDouble(normalized);
    if (parsed != null) floats.add(parsed);
  }
  return floats;
}

// ==== Extraction Regexes ====

String? _extractTime(String text) {
  final match = RegExp(r'\b(\d{2}:\d{2}(?::\d{2})?)\b').firstMatch(text);
  return match?.group(1);
}

String? _extractTid(String text) {
  final match = RegExp(
    '(?:TID|tid)[:\\s\\-]*($kDigitOrConfusable{5,12})',
  ).firstMatch(text);
  final raw = match?.group(1);
  return raw != null ? normalizeOcrDigits(raw) : null;
}

String? _extractTaxId(String text) {
  // Pattern: 13 หลัก (อนุญาตตัวสับสนกับตัวเลข เช่น O,l)
  final labelMatch = RegExp(
    r'(?:TAX\s*ID|เลขประจำตัว(?:ผู้เสียภาษี)?อากร)',
    caseSensitive: false,
  ).firstMatch(text);

  if (labelMatch != null) {
    final textAfterLabel = text.substring(labelMatch.end);
    final valueMatch = RegExp(
      '\\b($kDigitOrConfusable{13})\\b',
    ).firstMatch(textAfterLabel);
    if (valueMatch != null) {
      return normalizeOcrDigits(valueMatch.group(1));
    }
  }

  final fallback = RegExp('\\b($kDigitOrConfusable{13})\\b').firstMatch(text);
  final raw = fallback?.group(1);
  return raw != null ? normalizeOcrDigits(raw) : null;
}

String? _extractTaxInvNo(String text) {
  final match = RegExp(
    '(?:TAX\\s*IN[VU]\\s*NO\\.?|เลขที่ใบกำกับภาษี?|ใบกำกับภาษี)[:\\s\\-]*($kDigitOrConfusable{12})\\b',
    caseSensitive: false,
  ).firstMatch(text);
  final raw = match?.group(1);
  return raw != null ? normalizeOcrDigits(raw) : null;
}

String? _extractDate(String text) {
  // Common format: 08/09/25, 08-09-2025
  final match = RegExp(
    r'\b(\d{2})[/\-](\d{2})[/\-](\d{2,4})\b',
  ).firstMatch(text);
  if (match != null) {
    final d = match.group(1)!;
    final m = match.group(2)!;
    int y = int.parse(match.group(3)!);

    // ปรับปรุง: รองรับปี พ.ศ. (Thai Buddhist Era)
    if (y < 100) {
      if (y > 20 && y < 40) {
        y += 2500; // สมมติช่วงปี พ.ศ. ปัจจุบัน
      } else {
        y += 2000;
      }
    }
    if (y > 2500) y -= 543; // แปลง พ.ศ. เป็น ค.ศ.

    // แปลง y ให้เป็น String แบบ 4 หลัก เช่น 2025
    final yStr = y.toString().padLeft(4, '0');
    return '$yStr-$m-$d';
  }
  return null;
}

String? _extractLiter(String text) {
  final match = RegExp(
    '(?:LITER|ปริมาณ|ลิตร)[:\\s]*($kDigitOrConfusable+\\.$kDigitOrConfusable{2})\\b',
    caseSensitive: false,
  ).firstMatch(text);
  final raw = match?.group(1);
  return raw != null ? normalizeOcrDigits(raw) : null;
}

String? _extractPricePerLiter(String text) {
  final match = RegExp(
    '(?:BHT/LTR\\.?|ราคา/ลิตร|PRICE/L)[:\\s]*([$kDigitOrConfusable,]+\\.$kDigitOrConfusable+)',
    caseSensitive: false,
  ).firstMatch(text);
  final raw = match?.group(1);
  return raw != null ? normalizeOcrDigits(raw) : null;
}

String? _extractTotalAmount(String text) {
  final match = RegExp(
    '(?:TOTAL\\s*THB|TOTAL|จำนวนเงินรวม|ยอดรวม(?:ทั้งสิ้น)?|รวมเงิน)[:\\s]*([$kDigitOrConfusable,]+(?:\\.$kDigitOrConfusable+)?)',
    caseSensitive: false,
  ).firstMatch(text);

  if (match != null) {
    final raw = match.group(1)?.replaceAll(',', '');
    return raw != null ? normalizeOcrDigits(raw) : null;
  }

  return null;
}

String? _extractOdometer(String text) {
  String? fixOdometerPadding(String? raw) {
    if (raw == null || raw.isEmpty) return null;

    // OCR often misreads padding '0's as '8's (e.g., '88898898' instead of '00098898').
    // To safely fix this without corrupting real '8's in the actual mileage:
    // We replace '8's with '0's ONLY in the padding section (everything before the last 5 digits).
    if (raw.length > 5) {
      final paddingLength = raw.length - 5;
      final padding = raw.substring(0, paddingLength);
      final mileage = raw.substring(paddingLength);

      final fixedPadding = padding.replaceAll('8', '0');
      return fixedPadding + mileage;
    }

    return raw;
  }

  final odoMatch = RegExp(
    '(?:ระยะทาง|เลขไมล์|ODOMETER|MILEAGE|สะสมระยะทาง).*(?:KM|กม\\.?)?\\s*[:\\s]*($kDigitOrConfusable{4,9})',
    caseSensitive: false,
  ).firstMatch(text);

  if (odoMatch != null) {
    return fixOdometerPadding(normalizeOcrDigits(odoMatch.group(1)));
  }

  final fallbackKMMatch = RegExp(
    '\\b(?:KM|กม\\.?)\\s*[:\\s]*($kDigitOrConfusable{4,9})\\b',
    caseSensitive: false,
  ).firstMatch(text);

  if (fallbackKMMatch != null) {
    return fixOdometerPadding(normalizeOcrDigits(fallbackKMMatch.group(1)));
  }

  return null;
}
