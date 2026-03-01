/// Normalize OCR confusables to digits for numeric-only fields (0–9).
/// Use only on strings that are expected to be numbers (liter, amount, odometer, tax id, etc.).
/// Do NOT use on alphanumeric IDs (e.g. tripId "LT0...", sealCode "SPX...").
String normalizeOcrDigits(String? text) {
  if (text == null || text.isEmpty) return text ?? '';
  String s = text;
  // 0: O, o
  s = s.replaceAll('O', '0').replaceAll('o', '0');
  // 1: l, I, |
  s = s.replaceAll('l', '1').replaceAll('I', '1').replaceAll('|', '1');
  // 2: Z
  s = s.replaceAll('Z', '2');
  // 3: E
  s = s.replaceAll('E', '3');
  // 4: A, H
  s = s.replaceAll('A', '4').replaceAll('H', '4');
  // 5: S, s
  s = s.replaceAll('S', '5').replaceAll('s', '5');
  // 6: G, b
  s = s.replaceAll('G', '6').replaceAll('b', '6');
  // 7: T, L, /
  s = s.replaceAll('T', '7').replaceAll('L', '7').replaceAll('/', '7');
  // 8: B, &
  s = s.replaceAll('B', '8').replaceAll('&', '8');
  // 9: g, q
  s = s.replaceAll('g', '9').replaceAll('q', '9');
  return s;
}

/// Character class for regex: digit or common OCR confusable.
/// Use in extractors so we still match when OCR reads 0 as O, 1 as l, etc.
const String kDigitOrConfusable = r'[0-9OolI|ZESsAbBgqTL/&]';
