/// A trip as the driver routes answer it (`MobileTripView`, T55).
class MobileTrip {
  MobileTrip({
    required this.id,
    required this.tripNo,
    required this.status,
    required this.photoTypes,
    this.taskId,
    this.origin,
    this.destination,
    this.licensePlate,
    this.isMultiDelivery = false,
  });

  final String id;
  final String tripNo;
  final String status;
  final String? taskId;
  final String? origin;
  final String? destination;
  final String? licensePlate;
  final bool isMultiDelivery;
  final Set<String> photoTypes;

  bool get inTransit => status == 'in_transit';
  bool get closed => status == 'delivered' || status == 'cancelled';

  factory MobileTrip.fromJson(Map<String, dynamic> j) => MobileTrip(
        id: j['id'] as String? ?? '',
        tripNo: j['tripNo'] as String? ?? '',
        status: j['status'] as String? ?? '',
        taskId: j['taskId'] as String?,
        origin: j['origin'] as String?,
        destination: j['destination'] as String?,
        licensePlate: j['licensePlate'] as String?,
        isMultiDelivery: j['isMultiDelivery'] == true,
        photoTypes: {
          for (final p in (j['photos'] as List? ?? const []))
            if (p is Map && p['type'] is String) p['type'] as String,
        },
      );
}

/// One uploaded evidence photo of a trip write: its type, object key and the overlay's geocoding.
class TripPhotoRef {
  TripPhotoRef({required this.type, required this.key, this.lat, this.lng, this.address, this.geocodedAt});
  final String type;
  final String key;
  final double? lat;
  final double? lng;
  final String? address;
  final DateTime? geocodedAt;

  Map<String, dynamic> toJson() => {
        'type': type,
        'key': key,
        if (lat != null) 'lat': lat,
        if (lng != null) 'lng': lng,
        if (address != null && address!.isNotEmpty) 'address': address,
        if (geocodedAt != null) 'geocodedAt': geocodedAt!.toUtc().toIso8601String(),
      };
}
