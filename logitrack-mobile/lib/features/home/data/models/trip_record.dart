import 'package:cloud_firestore/cloud_firestore.dart';

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
  final String? partnerCode;
  final String? driverId;
  final String? distance;
  final int? parcelCount;
  final String? sealTime;
  final String? totalWeight;
  final double? lat;
  final double? lng;
  final DateTime? deliveredTimestamp;
  final double? deliveredLat;
  final double? deliveredLng;

  /// STD = Scheduled Time of Departure (CreateAt / บันทึกเมื่อ)
  final DateTime? std;

  /// STA = Scheduled Time of Arrival (เวลา Seal รถ + durationMinutes จาก hub_soc_distances)
  final DateTime? sta;

  /// ATA = Actual Time of Arrival (จะอัปเดตเมื่อถึง Hub/SOC ด้วย Geofencing)
  final DateTime? ata;

  /// เวลาเดินทาง (นาที) จาก hub_soc_distances
  final double? durationMinutes;
  final DateTime? createdAt;
  final DateTime? updatedAt;
  final bool needsAdminReview;
  final String? reviewStatus;
  final String? reviewReason;
  final DateTime? resubmittedAt;

  static DateTime? _parseDate(dynamic v) {
    if (v == null) return null;
    if (v is DateTime) return v;
    if (v is Timestamp) return v.toDate();
    if (v is String) return DateTime.tryParse(v);
    return null;
  }

  /// สร้างจาก Firestore doc (หรือ map จาก API)
  factory TripRecord.fromMap(Map<String, dynamic> map, {String? id}) {
    final photosRaw = map['photos'];
    List<TripPhoto> photos = const [];
    if (photosRaw is List) {
      photos = photosRaw.map((e) {
        if (e is Map<String, dynamic>) {
          TripPhotoGeocoding? geo;
          final g = e['geocoding'];
          if (g is Map<String, dynamic>) {
            geo = TripPhotoGeocoding(
              lat: (g['lat'] as num?)?.toDouble(),
              lng: (g['lng'] as num?)?.toDouble(),
              address: g['address'] as String?,
              timestamp: _parseDate(g['timestamp']),
            );
          }
          return TripPhoto(
            url: (e['url'] as String?) ?? '',
            type: (e['type'] as String?) ?? '',
            geocoding: geo,
          );
        }
        return const TripPhoto(url: '', type: '');
      }).toList();
    }
    TripOcrData? ocrData;
    final ocr = map['ocrData'];
    if (ocr is Map<String, dynamic>) {
      ocrData = TripOcrData(
        tripId: ocr['tripId'] as String?,
        sealCode: ocr['sealCode'] as String?,
        secondarySealCode: ocr['secondarySealCode'] as String?,
        routeInfo: ocr['routeInfo'] as String?,
        partnerCode: ocr['partnerCode'] as String?,
        supplierCode: ocr['supplierCode'] as String?,
        releaseTime: ocr['releaseTime'] as String?,
        sealSource: ocr['sealSource'] as String?,
        ocrProfile: ocr['ocrProfile'] as String?,
      );
    }
    return TripRecord(
      id: id ?? map['id'] as String?,
      status: (map['status'] as String?) ?? 'loading',
      jobType: (map['jobType'] as String?) ?? 'first_mile',
      photos: photos,
      ocrData: ocrData,
      spxTripId: map['spxTripId'] as String?,
      taskId: map['taskId'] as String?,
      origin: map['origin'] as String?,
      destination: map['destination'] as String?,
      sealCode: map['sealCode'] as String?,
      partnerCode: map['partnerCode'] as String?,
      driverId: map['driverId'] as String?,
      distance: map['distance'] as String?,
      parcelCount: (map['parcelCount'] as num?)?.toInt(),
      sealTime: map['sealTime'] as String?,
      totalWeight: map['totalWeight'] as String?,
      lat: (map['lat'] as num?)?.toDouble(),
      lng: (map['lng'] as num?)?.toDouble(),
      deliveredTimestamp: _parseDate(map['deliveredTimestamp']),
      deliveredLat: (map['deliveredLat'] as num?)?.toDouble(),
      deliveredLng: (map['deliveredLng'] as num?)?.toDouble(),
      std: _parseDate(map['std']),
      sta: _parseDate(map['sta']),
      ata: _parseDate(map['ata']),
      durationMinutes: (map['durationMinutes'] as num?)?.toDouble(),
      createdAt: _parseDate(map['createdAt']),
      updatedAt: _parseDate(map['updatedAt']),
      needsAdminReview: map['needsAdminReview'] == true,
      reviewStatus: map['reviewStatus'] as String?,
      reviewReason: map['reviewReason'] as String?,
      resubmittedAt: _parseDate(map['resubmittedAt']),
    );
  }

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
    this.partnerCode,
    this.driverId,
    this.distance,
    this.parcelCount,
    this.sealTime,
    this.totalWeight,
    this.lat,
    this.lng,
    this.deliveredTimestamp,
    this.deliveredLat,
    this.deliveredLng,
    this.std,
    this.sta,
    this.ata,
    this.durationMinutes,
    this.createdAt,
    this.updatedAt,
    this.needsAdminReview = false,
    this.reviewStatus,
    this.reviewReason,
    this.resubmittedAt,
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
      if (partnerCode != null) 'partnerCode': partnerCode,
      if (driverId != null) 'driverId': driverId,
      if (distance != null) 'distance': distance,
      if (parcelCount != null) 'parcelCount': parcelCount,
      if (sealTime != null) 'sealTime': sealTime,
      if (totalWeight != null) 'totalWeight': totalWeight,
      if (lat != null) 'lat': lat,
      if (lng != null) 'lng': lng,
      if (deliveredTimestamp != null) 'deliveredTimestamp': deliveredTimestamp,
      if (deliveredLat != null) 'deliveredLat': deliveredLat,
      if (deliveredLng != null) 'deliveredLng': deliveredLng,
      if (std != null) 'std': std,
      if (sta != null) 'sta': sta,
      if (ata != null) 'ata': ata,
      if (durationMinutes != null) 'durationMinutes': durationMinutes,
      'createdAt': createdAt ?? now,
      'updatedAt': updatedAt ?? now,
      'needsAdminReview': needsAdminReview,
      if (reviewStatus != null) 'reviewStatus': reviewStatus,
      if (reviewReason != null) 'reviewReason': reviewReason,
      if (resubmittedAt != null) 'resubmittedAt': resubmittedAt,
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
  final String? secondarySealCode;
  final String? routeInfo;
  final String? partnerCode;
  final String? supplierCode;
  final String? releaseTime;
  final String? sealSource;
  final String? ocrProfile;

  const TripOcrData({
    this.tripId,
    this.sealCode,
    this.secondarySealCode,
    this.routeInfo,
    this.partnerCode,
    this.supplierCode,
    this.releaseTime,
    this.sealSource,
    this.ocrProfile,
  });

  Map<String, dynamic> toMap() => {
    if (tripId != null) 'tripId': tripId,
    if (sealCode != null) 'sealCode': sealCode,
    if (secondarySealCode != null) 'secondarySealCode': secondarySealCode,
    if (routeInfo != null) 'routeInfo': routeInfo,
    if (partnerCode != null) 'partnerCode': partnerCode,
    if (supplierCode != null) 'supplierCode': supplierCode,
    if (releaseTime != null) 'releaseTime': releaseTime,
    if (sealSource != null) 'sealSource': sealSource,
    if (ocrProfile != null) 'ocrProfile': ocrProfile,
  };
}
