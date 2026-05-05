import 'dart:typed_data';
import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/material.dart';
import 'package:firebase_functions/firebase_functions.dart';
import 'package:geolocator/geolocator.dart';
import 'package:image_picker/image_picker.dart';
import 'package:cloud_firestore/cloud_firestore.dart';
import '../../../home/data/repositories/trip_records_repository.dart';
import '../../../home/data/repositories/hubs_repository.dart';
import '../../data/repositories/delivery_trip_repository.dart';
import '../../../home/data/services/photo_overlay_service.dart';
import '../../../home/data/services/image_compression_service.dart';
import '../../../home/presentation/pages/main_layout_scope.dart';
import '../../../../core/utils/maps_navigation.dart';
import 'incident_report_page.dart';

/// Multi-delivery phase — driver delivers to multiple stops in one trip
class DeliveryPhasePageMulti extends StatefulWidget {
  const DeliveryPhasePageMulti({super.key, this.savedTripSummary});
  final SavedTripSummary? savedTripSummary;

  @override
  State<DeliveryPhasePageMulti> createState() => _DeliveryPhasePageMultiState();
}

class DeliveryStop {
  final int index;
  final String destination;
  bool isDelivered = false;
  final Map<String, Uint8List> photos = {}; // "stop_{index}_pre_open", etc.

  DeliveryStop({required this.index, required this.destination});
}

class _DeliveryPhasePageMultiState extends State<DeliveryPhasePageMulti> {
  List<DeliveryStop> _stops = [];
  int _currentStopIndex = -1;
  double? _lat;
  double? _lng;
  bool _locationLoading = true;
  bool _saving = false;
  List<HubDoc> _hubs = [];

  /// Current stop being captured
  DeliveryStop? get _currentStop =>
      _currentStopIndex >= 0 && _currentStopIndex < _stops.length
          ? _stops[_currentStopIndex]
          : null;

  /// Photo types for each stop (3 mandatory + optional runsheet)
  static const List<String> _stopPhotoTypes = [
    'pre_open',
    'opening',
    'empty_container',
  ];

  bool get _currentStopComplete =>
      _currentStop != null && _currentStop!.photos.length == _stopPhotoTypes.length;

  bool get _allStopsDelivered => _stops.every((s) => s.isDelivered);

  @override
  void initState() {
    super.initState();
    _loadLocation();
    _loadHubsForNavigation();
    _parseDeliveryStops();
  }

  /// Extract delivery stops from trip record
  void _parseDeliveryStops() {
    final stops = widget.savedTripSummary?.additionalData?['deliveryStops'] as List?;
    if (stops == null || stops.isEmpty) {
      Navigator.pop(context);
      return;
    }

    setState(() {
      _stops = [];
      for (int i = 0; i < stops.length; i++) {
        final stop = stops[i] as Map<String, dynamic>;
        _stops.add(DeliveryStop(
          index: i + 1,
          destination: stop['destination'] ?? 'Unknown',
        ));
      }
      // Start with first stop
      if (_stops.isNotEmpty) {
        _currentStopIndex = 0;
      }
    });
  }

  Future<void> _loadLocation() async {
    try {
      final pos = await getCurrentPosition();
      if (mounted) {
        setState(() {
          _lat = pos.latitude;
          _lng = pos.longitude;
          _locationLoading = false;
        });
      }
    } catch (e) {
      if (mounted) {
        setState(() => _locationLoading = false);
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('delivery_location_error'.tr())),
        );
      }
    }
  }

  Future<void> _loadHubsForNavigation() async {
    try {
      final list = await fetchAllHubs();
      if (mounted) setState(() => _hubs = list);
    } catch (_) {}
  }

  HubDoc? _getStopHub(String destination) {
    return _hubs.firstWhere(
      (h) => h.name?.toUpperCase() == destination.toUpperCase(),
      orElse: () => HubDoc(),
    );
  }

  Future<void> _capturePhoto(String photoType) async {
    if (_currentStop == null) return;

    try {
      final picker = ImagePicker();
      final file = await picker.pickImage(source: ImageSource.camera);
      if (file == null) return;

      final bytes = await file.readAsBytes();
      final compressed = await compressImageForUpload(bytes);

      if (mounted) {
        setState(() {
          final photoKey = 'stop_${_currentStop!.index}_$photoType';
          _currentStop!.photos[photoKey] = compressed;
        });
      }
    } catch (e) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('delivery_photo_error'.tr())),
      );
    }
  }

  Future<void> _confirmStopDelivery() async {
    if (_currentStop == null || !_currentStopComplete) return;

    try {
      setState(() => _saving = true);

      final tripId = widget.savedTripSummary?.tripId;
      if (tripId == null) throw Exception('No trip ID');

      // Upload photos to Firebase Storage
      final uploadedPhotos = <Map<String, dynamic>>[];
      for (final entry in _currentStop!.photos.entries) {
        final url = await uploadTripPhoto(
          tripId: tripId,
          photoType: entry.key, // e.g., "stop_1_pre_open"
          imageBytes: entry.value.toList(),
        );
        uploadedPhotos.add({
          'url': url,
          'type': entry.key,
        });
      }

      // Submit delivery stop progress via Cloud Function
      final callable = FirebaseFunctions.instance.httpsCallable(
        'submitDeliveryStopProgress',
      );

      await callable.call({
        'tripId': tripId,
        'stopIndex': _currentStop!.index,
        'destination': _currentStop!.destination,
        'photos': uploadedPhotos.asMap().entries.map((e) {
          final key = _currentStop!.photos.keys.toList()[e.key];
          return {
            'url': e.value,
            'type': key, // e.g., "stop_1_pre_open"
            'geocoding': {
              'lat': _lat,
              'lng': _lng,
              'timestamp': DateTime.now().toIso8601String(),
            }
          };
        }).toList(),
        'deliveredLat': _lat,
        'deliveredLng': _lng,
      });

      if (mounted) {
        setState(() {
          _currentStop!.isDelivered = true;
          _saving = false;

          // Move to next undelivered stop
          final nextIndex = _stops.indexWhere((s) => !s.isDelivered);
          if (nextIndex >= 0) {
            _currentStopIndex = nextIndex;
          }
        });

        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('delivery_stop_confirmed'.tr())),
        );
      }
    } catch (e) {
      if (mounted) setState(() => _saving = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('delivery_submit_error'.tr(args: [e.toString()]))),
      );
    }
  }

  Future<void> _completeAllDeliveries() async {
    if (!_allStopsDelivered) return;

    // At this point, Cloud Function automatically:
    // 1. Checked all stops are delivered
    // 2. Marked task as "Completed"
    // 3. Set trip status to "delivered"

    if (mounted) {
      Navigator.pop(context, true);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('delivery_all_stops_complete'.tr())),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_stops.isEmpty) {
      return Scaffold(
        appBar: AppBar(title: Text('delivery_phase'.tr())),
        body: Center(child: Text('No delivery stops found'.tr())),
      );
    }

    return Scaffold(
      appBar: AppBar(
        title: Text('delivery_multi_title'.tr()),
        elevation: 0,
        backgroundColor: Colors.blue.shade600,
        foregroundColor: Colors.white,
      ),
      body: SingleChildScrollView(
        child: Padding(
          padding: const EdgeInsets.all(16.0),
          child: Column(
            children: [
              // Stop Progress Indicator
              _buildStopProgressCard(),
              const SizedBox(height: 16),

              // Current Stop Details
              if (_currentStop != null) ...[
                _buildStopDetailsCard(),
                const SizedBox(height: 16),

                // Photo Capture Section
                _buildPhotoSection(),
                const SizedBox(height: 16),

                // Action Buttons
                _buildActionButtons(),
              ] else
                Center(
                  child: Text('All stops delivered!'.tr(),
                      style: Theme.of(context).textTheme.titleLarge),
                ),

              const SizedBox(height: 24),

              // Stop List
              _buildStopsList(),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildStopProgressCard() {
    final delivered = _stops.where((s) => s.isDelivered).length;
    final total = _stops.length;

    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16.0),
        child: Column(
          children: [
            Text(
              'delivery_progress'.tr(args: ['$delivered', '$total']),
              style: Theme.of(context).textTheme.titleMedium,
            ),
            const SizedBox(height: 8),
            LinearProgressIndicator(
              value: total > 0 ? delivered / total : 0,
              minHeight: 8,
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildStopDetailsCard() {
    final stop = _currentStop!;
    final hub = _getStopHub(stop.destination);

    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16.0),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Text(
                  'Stop ${stop.index}',
                  style: Theme.of(context).textTheme.titleLarge,
                ),
                if (stop.isDelivered)
                  Chip(
                    label: Text('Delivered'),
                    backgroundColor: Colors.green.shade100,
                  ),
              ],
            ),
            const SizedBox(height: 8),
            Text(
              'Destination: ${stop.destination}',
              style: Theme.of(context).textTheme.bodyMedium,
            ),
            const SizedBox(height: 8),
            ElevatedButton.icon(
              onPressed: () => _openNavigationToStop(hub),
              icon: const Icon(Icons.navigation),
              label: Text('Navigate'),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildPhotoSection() {
    final stop = _currentStop!;
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16.0),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              'Capture Photos (${stop.photos.length}/${_stopPhotoTypes.length})',
              style: Theme.of(context).textTheme.titleMedium,
            ),
            const SizedBox(height: 12),
            GridView.count(
              crossAxisCount: 2,
              shrinkWrap: true,
              physics: const NeverScrollableScrollPhysics(),
              children: _stopPhotoTypes.map((type) {
                final photoKey = 'stop_${stop.index}_$type';
                final hasPhoto = stop.photos.containsKey(photoKey);

                return GestureDetector(
                  onTap: () => _capturePhoto(type),
                  child: Container(
                    margin: const EdgeInsets.all(4),
                    decoration: BoxDecoration(
                      border: Border.all(
                        color: hasPhoto ? Colors.green : Colors.grey,
                        width: 2,
                      ),
                      borderRadius: BorderRadius.circular(8),
                      color: hasPhoto ? Colors.green.shade50 : Colors.grey.shade100,
                    ),
                    child: Center(
                      child: hasPhoto
                          ? const Icon(Icons.check_circle, color: Colors.green, size: 32)
                          : Column(
                              mainAxisAlignment: MainAxisAlignment.center,
                              children: [
                                const Icon(Icons.camera_alt, size: 28),
                                const SizedBox(height: 4),
                                Text(
                                  type.replaceAll('_', '\n'),
                                  textAlign: TextAlign.center,
                                  style: const TextStyle(fontSize: 10),
                                ),
                              ],
                            ),
                    ),
                  ),
                );
              }).toList(),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildActionButtons() {
    return Row(
      children: [
        Expanded(
          child: ElevatedButton(
            onPressed: _currentStopComplete && !_saving ? _confirmStopDelivery : null,
            style: ElevatedButton.styleFrom(
              backgroundColor: Colors.green.shade600,
              padding: const EdgeInsets.symmetric(vertical: 12),
            ),
            child: _saving
                ? const SizedBox(
                    height: 20,
                    width: 20,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                : Text('Confirm Stop ${_currentStop!.index}'),
          ),
        ),
        if (_allStopsDelivered) ...[
          const SizedBox(width: 8),
          Expanded(
            child: ElevatedButton(
              onPressed: _completeAllDeliveries,
              style: ElevatedButton.styleFrom(
                backgroundColor: Colors.blue.shade600,
              ),
              child: Text('Complete Trip'),
            ),
          ),
        ],
      ],
    );
  }

  Widget _buildStopsList() {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          'All Stops',
          style: Theme.of(context).textTheme.titleMedium,
        ),
        const SizedBox(height: 8),
        ..._stops.asMap().entries.map((e) {
          final stop = e.value;
          final isActive = _currentStopIndex == e.key;

          return Card(
            color: isActive ? Colors.blue.shade50 : null,
            child: ListTile(
              title: Text('Stop ${stop.index}: ${stop.destination}'),
              trailing: stop.isDelivered
                  ? const Icon(Icons.check_circle, color: Colors.green)
                  : const Icon(Icons.radio_button_unchecked),
              onTap: stop.isDelivered
                  ? null
                  : () {
                      setState(() => _currentStopIndex = e.key);
                    },
            ),
          );
        }).toList(),
      ],
    );
  }

  Future<void> _openNavigationToStop(HubDoc hub) async {
    if (_lat == null || _lng == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Location not available'.tr())),
      );
      return;
    }

    try {
      final useCoords = hub.hasCoordinates;
      await openGoogleMapsDrivingDirections(
        originLat: _lat!,
        originLng: _lng!,
        destLat: useCoords ? hub.latitude : null,
        destLng: useCoords ? hub.longitude : null,
        destName: _currentStop!.destination,
      );
    } catch (e) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Navigation error'.tr(args: [e.toString()]))),
      );
    }
  }
}
