/// โมเดลตาม shared-docs/schemas/trip-record.ts (TripRecords collection)
/// ใช้สำหรับบันทึกรับงาน (Loading Phase) และสถานะ trip อื่นๆ
class TripRecord {
  final String? id;
  final String status;
  final String jobType;
  final List<TripPhoto> photos;
  final TripOcrData? ocrData;
  final String? spxTripId;
  final String? taskId;
  final String? origin;
  final String? destination;
  final String? sealCode;
  final String? distance;
  final int? parcelCount;
  final String? sealTime;
  final String? totalWeight;
  final double? lat;
  final double? lng;
  final DateTime? deliveredTimestamp;
  final double? deliveredLat;
  final double? deliveredLng;
  final DateTime? createdAt;
  final DateTime? updatedAt;

  const TripRecord({
    this.id,
    required this.status,
    required this.jobType,
    this.photos = const [],
    this.ocrData,
    this.spxTripId,
    this.taskId,
    this.origin,
    this.destination,
    this.sealCode,
    this.distance,
    this.parcelCount,
    this.sealTime,
    this.totalWeight,
    this.lat,
    this.lng,
    this.deliveredTimestamp,
    this.deliveredLat,
    this.deliveredLng,
    this.createdAt,
    this.updatedAt,
  });

  Map<String, dynamic> toFirestore() {
    final now = DateTime.now();
    return {
      'status': status,
      'jobType': jobType,
      'photos': photos.map((p) => p.toMap()).toList(),
      if (ocrData != null) 'ocrData': ocrData!.toMap(),
      if (spxTripId != null) 'spxTripId': spxTripId,
      if (taskId != null) 'taskId': taskId,
      if (origin != null) 'origin': origin,
      if (destination != null) 'destination': destination,
      if (sealCode != null) 'sealCode': sealCode,
      if (distance != null) 'distance': distance,
      if (parcelCount != null) 'parcelCount': parcelCount,
      if (sealTime != null) 'sealTime': sealTime,
      if (totalWeight != null) 'totalWeight': totalWeight,
      if (lat != null) 'lat': lat,
      if (lng != null) 'lng': lng,
      if (deliveredTimestamp != null) 'deliveredTimestamp': deliveredTimestamp,
      if (deliveredLat != null) 'deliveredLat': deliveredLat,
      if (deliveredLng != null) 'deliveredLng': deliveredLng,
      'createdAt': createdAt ?? now,
      'updatedAt': updatedAt ?? now,
    };
  }
}

class TripPhoto {
  final String url;
  final String type;
  final TripPhotoGeocoding? geocoding;

  const TripPhoto({required this.url, required this.type, this.geocoding});

  Map<String, dynamic> toMap() => {
    'url': url,
    'type': type,
    if (geocoding != null) 'geocoding': geocoding!.toMap(),
  };
}

class TripPhotoGeocoding {
  final double? lat;
  final double? lng;
  final String? address;
  final DateTime? timestamp;

  const TripPhotoGeocoding({this.lat, this.lng, this.address, this.timestamp});

  Map<String, dynamic> toMap() => {
    if (lat != null) 'lat': lat,
    if (lng != null) 'lng': lng,
    if (address != null) 'address': address,
    if (timestamp != null) 'timestamp': timestamp?.toIso8601String(),
  };
}

class TripOcrData {
  final String? tripId;
  final String? sealCode;
  final String? routeInfo;

  const TripOcrData({this.tripId, this.sealCode, this.routeInfo});

  Map<String, dynamic> toMap() => {
    if (tripId != null) 'tripId': tripId,
    if (sealCode != null) 'sealCode': sealCode,
    if (routeInfo != null) 'routeInfo': routeInfo,
  };
}
