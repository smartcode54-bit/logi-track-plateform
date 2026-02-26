void main() {
  var text = 'LTE, LT0Q2Q24F0TT1';
  var tripId = RegExp(r'LT[A-Za-z0-9\-]{8,}').firstMatch(text)?.group(0);
  print('Trip ID: $tripId');

  var text2 = 'SPX, SPX3783396';
  var sealCode = RegExp(r'SPX[A-Za-z0-9\-]{5,}').firstMatch(text2)?.group(0);
  print('Seal Code: $sealCode');
}
