import 'package:flutter/foundation.dart' show debugPrint;

import 'cloud_vision_ocr_service.dart';

/// ผล OCR จากบิลน้ำมัน (ปั๊ม เช่น Bangchak)
class FuelReceiptOcrResult {
  final double? liters;
  final double? amountThb;
  /// เลขประจำตัวผู้เสียภาษีสถานี (TAX INU NO. จากบิล)
  final String? stationTaxId;
  /// เลขที่ใบกำกับภาษี (TAX INV NO. จากบิล เช่น 333160013327)
  final String? taxInvId;
  final int? odometerKm;

  FuelReceiptOcrResult({
    this.liters,
    this.amountThb,
    this.stationTaxId,
    this.taxInvId,
    this.odometerKm,
  });
}

/// รัน OCR บนรูปบิลน้ำมัน ด้วย Google Vision API (key เดียวกับแผนที่) — อ่านไม่ได้ให้ใส่มือได้
Future<FuelReceiptOcrResult> runFuelReceiptOcrOnImageBytes(List<int> imageBytes) async {
  try {
    final fullText = await runCloudVisionOcrOnImageBytes(imageBytes);

    // Log ผลอ่านไว้ดู
    debugPrint('=== Fuel Receipt OCR Full Text (Vision API) ===\n${fullText == null || fullText.isEmpty ? "(ว่าง)" : fullText}\n===============================');

    if (fullText == null || fullText.isEmpty) return FuelReceiptOcrResult();

    return FuelReceiptOcrResult(
      liters: _extractLiters(fullText),
      amountThb: _extractAmountThb(fullText),
      stationTaxId: _extractStationTaxId(fullText),
      taxInvId: _extractTaxInvId(fullText),
      odometerKm: _extractOdometerKm(fullText),
    );
  } catch (e, st) {
    debugPrint('Fuel Receipt OCR error: $e\n$st');
    return FuelReceiptOcrResult();
  }
}

/// ลองแก้ 8 → 0 (OCR บิลมักอ่าน 0 เป็น 8) แล้ว parse; ถ้าอยู่ใน range คืนค่าที่แก้แล้ว
double? _parseDoubleWith08Fix(String raw, {required double min, required double max}) {
  final cleaned = raw.replaceAll(',', '');
  final v = double.tryParse(cleaned);
  if (v != null && v >= min && v <= max) return v;
  if (!cleaned.contains('8')) return null;
  final fixed = cleaned.replaceAll('8', '0');
  final v2 = double.tryParse(fixed);
  if (v2 != null && v2 >= min && v2 <= max) return v2;
  return null;
}

/// LITER / LITTER 56.68 — บิลบางใบพิมพ์ LITTER (สอง T) หรือ LITER (ต้องได้ค่าลิตรใส่ฟิลด์)
double? _extractLiters(String text) {
  const minL = 0.01;
  const maxL = 1999.99;

  double? tryLiters(String numStr) =>
      _parseDoubleWith08Fix(numStr.replaceAll(RegExp(r'\s'), ''), min: minL, max: maxL);

  // 1) LITER หรือ LITTER ตามด้วยตัวเลข (อนุญาต space/ขึ้นบรรทัดใหม่ระหว่างคำกับเลข)
  var match = RegExp(
    r'LIT\s*(?:ER|TER)\s*[:\s]*([\d,]+(?:\s*\.\s*[\d]+)?)',
    caseSensitive: false,
  ).firstMatch(text);
  if (match != null) {
    final v = tryLiters(match.group(1)!);
    if (v != null) return v;
  }

  // 2) LITER/LITTER ชิดเลข (ไม่มี space ระหว่างคำกับเลข)
  match = RegExp(r'LIT(?:ER|TER)([\d,]+(?:\.[\d]+)?)', caseSensitive: false).firstMatch(text);
  if (match != null) {
    final v = tryLiters(match.group(1)!);
    if (v != null) return v;
  }

  // 3) ตัวเลขมาก่อน แล้วตามด้วย L หรือ LITER/LITTER (เช่น 56.68 L หรือ 56.68 LITER)
  match = RegExp(
    r'([\d,]+(?:\s*\.\s*[\d]+)?)\s*L(?:IT(?:ER|TER)?)?\b',
    caseSensitive: false,
  ).firstMatch(text);
  if (match != null) {
    final v = tryLiters(match.group(1)!);
    if (v != null) return v;
  }

  // 4) คำว่า "ลิตร" (ไทย) ตามด้วยตัวเลข
  match = RegExp(
    r'ลิตร\s*[:\s]*([\d,]+(?:\s*\.\s*[\d]+)?)',
    caseSensitive: false,
  ).firstMatch(text);
  if (match != null) {
    final v = tryLiters(match.group(1)!);
    if (v != null) return v;
  }

  // 5) Fallback: LIT ตามด้วยอะไรก็ได้สั้นๆ แล้ว ER/TER แล้วตามด้วยตัวเลข (ภายใน ~50 ตัวอักษร)
  match = RegExp(
    r'LIT[\s\S]{0,25}?(?:ER|TER)[\s\S]{0,40}?([\d,]+(?:\s*\.\s*[\d]+)?)',
    caseSensitive: false,
  ).firstMatch(text);
  if (match != null) {
    final v = tryLiters(match.group(1)!);
    if (v != null) return v;
  }

  return null;
}

/// SALE THB 1,870.00 or TOTAL THB 1,870.00
double? _extractAmountThb(String text) {
  var match = RegExp(r'(?:SALE|TOTAL)\s*THB\s*([\d,]+(?:\.[\d]+)?)', caseSensitive: false).firstMatch(text);
  if (match != null) {
    final v = _parseDoubleWith08Fix(match.group(1)!, min: 0.01, max: 999999.99);
    if (v != null) return v;
  }
  match = RegExp(r'THB\s*([\d,]+(?:\.[\d]+)?)', caseSensitive: false).firstMatch(text);
  if (match != null) {
    final v = _parseDoubleWith08Fix(match.group(1)!, min: 0.01, max: 999999.99);
    if (v != null) return v;
  }
  return null;
}

/// TAX ID สถานีจากบิล — ลงในช่อง TAX ID input (แก้ 0/8: OCR มักอ่าน 0 เป็น 8)
String? _extractStationTaxId(String text) {
  var match = RegExp(
    r'TAX\s+I\s*D\s*[:\s]*(\d[\d\s]{9,25})',
    caseSensitive: false,
  ).firstMatch(text);
  if (match == null) {
    match = RegExp(
      r'TAXID\s*[:\s]*(\d{10,15})',
      caseSensitive: false,
    ).firstMatch(text);
  }
  if (match == null) {
    match = RegExp(
      r'TAX\s*ID\s*[:\s]*(\d[\d\s]{9,25})',
      caseSensitive: false,
    ).firstMatch(text);
  }
  if (match != null) {
    final value = match.group(1)!.replaceAll(RegExp(r'\s'), '').trim();
    if (value.length >= 10 && value.length <= 15) {
      // TAX ID สถานี: แก้ 8→0 เสมอ (บิลมักพิมพ์ 0 เป็นวงสองชั้น แล้ว OCR อ่านเป็น 8)
      final fixed = value.replaceAll('8', '0');
      return fixed;
    }
  }
  return null;
}

/// TAX INV ID — เลขที่ใบเสร็จที่ใช้ในทางบัญชี (TAX INV NO. 333160013327) บนบิล
String? _extractTaxInvId(String text) {
  var match = RegExp(
    r'TAX\s+INV\s*NO\.?\s*[:\s]*(\d{10,15})',
    caseSensitive: false,
  ).firstMatch(text);
  if (match != null) {
    final value = match.group(1)!.trim();
    if (value.contains('8')) {
      final fixed = value.replaceAll('8', '0');
      if (fixed.length >= 10 && fixed.length <= 15) return fixed;
    }
    return value;
  }
  // บางบิลสะกดเป็น TAX INU NO. (ตัว U) — ใช้เป็น fallback
  match = RegExp(
    r'TAX\s+INU\s*NO\.?\s*[:\s]*(\d{10,15})',
    caseSensitive: false,
  ).firstMatch(text);
  if (match != null) {
    final value = match.group(1)!.trim();
    if (value.contains('8')) {
      final fixed = value.replaceAll('8', '0');
      if (fixed.length >= 10 && fixed.length <= 15) return fixed;
    }
    return value;
  }
  return null;
}

/// เลขไมล์ ขณะเติมน้ำมัน — จากบิลคือ "เลขระยะทาง (KM) 00050716" เท่านั้น (ไม่เอาเลขจาก TAX INV 333...)
/// หมายเหตุ 0 vs 8: บนบิล 0 บางแบบเป็นวงสองชั้น — OCR อาจสลับได้
int? _extractOdometerKm(String text) {
  int? tryParseOdometer(String? s) {
    if (s == null || s.isEmpty) return null;
    final v = int.tryParse(s);
    if (v == null || v < 1000 || v > 9999999) return null;
    if (v >= 200000 && v <= 400000) return null;
    return v;
  }

  String _stripDigits(String? s) => s?.replaceAll(RegExp(r'\s'), '') ?? '';

  int? tryOdometerWith08Fix(String? s) {
    final cleaned = _stripDigits(s);
    if (cleaned.isEmpty) return null;
    final v = tryParseOdometer(cleaned);
    if (v != null) return v;
    if (!cleaned.contains('8')) return null;
    final fixed = cleaned.replaceAll('8', '0');
    return tryParseOdometer(fixed);
  }

  // เลขระยะทาง(KM) หรือ ผลขระยะทาง(KM) — OCR มักอ่าน "เลข" เป็น "ผลข" (อนุญาตเลขมี space ปน)
  var match = RegExp(
    r'(?:เลข|ผลข)ระยะทาง\s*\(?\s*KM\s*\)?\s*[:\s]*\s*([\d\s]{4,20})',
    caseSensitive: false,
  ).firstMatch(text);
  var v = tryOdometerWith08Fix(match?.group(1));
  if (v != null) return v;

  match = RegExp(
    r'(?:เลขระยะทาง|ผลขระยะทาง|ODOMETER)\s*[:\s]*([\d\s]{4,20})',
    caseSensitive: false,
  ).firstMatch(text);
  v = tryOdometerWith08Fix(match?.group(1));
  if (v != null) return v;

  // ระยะทาง(KM) ตามด้วยเลข 4–8 หลัก (เช่น 00050716) อาจมี space คั่น
  match = RegExp(
    r'ระยะทาง\s*\(?\s*KM\s*\)?\s*[:\s]*\s*([\d\s]{4,20})',
    caseSensitive: false,
  ).firstMatch(text);
  v = tryOdometerWith08Fix(match?.group(1));
  if (v != null) return v;

  // KM) 00050716 หรือ KM 00050716
  match = RegExp(
    r'KM\s*\)?\s*[:\s]*\s*([\d\s]{4,20})',
    caseSensitive: false,
  ).firstMatch(text);
  v = tryOdometerWith08Fix(match?.group(1));
  if (v != null) return v;

  return null;
}
