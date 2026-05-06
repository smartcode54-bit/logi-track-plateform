import 'dart:typed_data';
import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/material.dart';
import 'package:geolocator/geolocator.dart';
import 'package:image_picker/image_picker.dart';
import 'package:cloud_firestore/cloud_firestore.dart'
    show FirebaseFirestore, GetOptions, Source;
import '../../../home/data/repositories/trip_records_repository.dart';
import '../../../home/data/repositories/hubs_repository.dart';
import '../../data/repositories/delivery_trip_repository.dart';
import '../../../home/data/services/image_compression_service.dart';
import '../../../home/presentation/pages/main_layout_scope.dart';
import '../../../../core/utils/maps_navigation.dart';
import 'incident_report_page.dart';

/// Multi-delivery phase — driver delivers to multiple stops in one trip
class DeliveryPhasePageMulti extends StatefulWidget {
  const DeliveryPhasePageMulti({
    super.key,
    this.savedTripSummary,
    /// When true (inside [MainLayout] [IndexedStack]) never call [Navigator.pop] to leave—
    /// it removes the root route and shows a blank black screen.
    this.embeddedInBottomNav = false,
  });

  final SavedTripSummary? savedTripSummary;

  /// True when mounted as tab content under [MainLayout]; false when opened via [Navigator.push].
  final bool embeddedInBottomNav;

  @override
  State<DeliveryPhasePageMulti> createState() => _DeliveryPhasePageMultiState();
}

class DeliveryStop {
  final int index;
  final String destination;
  final String? sourceId; // hub ID if from predefined list
  final bool isCustom; // true if user-added
  int sequence; // order in deliveryStops array (0-based) — mutable for reorder
  bool isDelivered = false;
  final Map<String, Uint8List> photos = {}; // "stop_{index}_pre_open", etc.

  DeliveryStop({
    required this.index,
    required this.destination,
    this.sourceId,
    this.isCustom = false,
    required this.sequence,
  });

  /// Check if this is the last stop (sequence == total - 1)
  bool isLastStop(int totalStops) => sequence == totalStops - 1;
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

  /// Photo types for last stop: before opening, during opening, cabinet empty
  static const List<String> _lastStopPhotoTypes = [
    'before_open',
    'during_open',
    'empty_container',
  ];

  /// Photo types for non-last stops: before opening, during opening, closing cabinet
  static const List<String> _nonLastStopPhotoTypes = [
    'before_open',
    'during_open',
    'close_container',
  ];

  /// Get numbered circle emoji for stop index (1-9)
  String _getNumberedCircle(int index) {
    const circles = ['①', '②', '③', '④', '⑤', '⑥', '⑦', '⑧', '⑨'];
    if (index > 0 && index <= circles.length) {
      return circles[index - 1];
    }
    return '$index';
  }

  /// Move stop up (-1) or down (+1) in the list
  void _moveStop(int listIndex, int direction) {
    final targetIndex = listIndex + direction;

    // Boundary check
    if (targetIndex < 0 || targetIndex >= _stops.length) return;

    // Don't move if stop or target is delivered
    if (_stops[listIndex].isDelivered || _stops[targetIndex].isDelivered) return;

    setState(() {
      // Swap stops
      final temp = _stops[listIndex];
      _stops[listIndex] = _stops[targetIndex];
      _stops[targetIndex] = temp;

      // Reassign sequence based on new position
      for (int i = 0; i < _stops.length; i++) {
        _stops[i].sequence = i;
      }

      // Update active stop index if needed
      if (_currentStopIndex == listIndex) {
        _currentStopIndex = targetIndex;
      } else if (_currentStopIndex == targetIndex) {
        _currentStopIndex = listIndex;
      }
    });
  }

  List<String> _getPhotoTypesForStop() {
    if (_currentStop == null) return [];
    final totalStops = _stops.length;
    final isLast = _currentStop!.isLastStop(totalStops);
    return isLast ? _lastStopPhotoTypes : _nonLastStopPhotoTypes;
  }

  bool get _currentStopComplete {
    if (_currentStop == null) return false;
    final requiredPhotos = _getPhotoTypesForStop();
    return _currentStop!.photos.length == requiredPhotos.length;
  }

  bool get _allStopsDelivered => _stops.every((s) => s.isDelivered);

  /// When embedded in [MainLayout]'s [IndexedStack], never [Navigator.pop] — it pops the shell route → blank screen.
  void _abortOpeningMultiDelivery(String messageKey) {
    if (!mounted) return;
    final msg = messageKey.tr();
    if (widget.embeddedInBottomNav) {
      MainLayoutScope.maybeOf(context)?.onEmbeddedMultiDeliveryAbort?.call(msg);
    } else {
      ScaffoldMessenger.maybeOf(context)?.showSnackBar(
        SnackBar(content: Text(msg)),
      );
      if (Navigator.of(context).canPop()) {
        Navigator.pop(context);
      }
    }
  }

  void _completeMultiTripSuccess() {
    if (!mounted) return;
    MainLayoutScope.maybeOf(context)?.onDeliveryCompleted?.call();
    if (!widget.embeddedInBottomNav && Navigator.of(context).canPop()) {
      Navigator.pop(context, true);
    }
  }

  @override
  void initState() {
    super.initState();
    _loadLocation();
    _loadHubsForNavigation();
    _loadDeliveryStopsFromFirestore();
  }

  /// Load delivery stops from tasks/{taskId}
  Future<void> _loadDeliveryStopsFromFirestore({int retryCount = 0}) async {
    final taskId = widget.savedTripSummary?.taskId;
    if (taskId == null || taskId.isEmpty) {
      _abortOpeningMultiDelivery('delivery_multi_abort_no_task');
      return;
    }

    try {
      // Force read from server (not cache) to get latest data after add/update
      final snapshot = await FirebaseFirestore.instance
          .collection('tasks')
          .doc(taskId)
          .get(const GetOptions(source: Source.server));

      if (!mounted) return;

      if (!snapshot.exists) {
        if (retryCount < 3) {
          // Retry up to 3 times with slight delay to wait for server update
          await Future.delayed(const Duration(milliseconds: 500));
          return _loadDeliveryStopsFromFirestore(retryCount: retryCount + 1);
        }
        _abortOpeningMultiDelivery('delivery_multi_abort_task_missing');
        return;
      }

      final data = snapshot.data() as Map<String, dynamic>?;
      final stops = data?['deliveryStops'] as List?;

      if (stops == null || stops.isEmpty) {
        if (retryCount < 3) {
          // Retry up to 3 times with slight delay to wait for deliveryStops to be populated
          await Future.delayed(const Duration(milliseconds: 500));
          return _loadDeliveryStopsFromFirestore(retryCount: retryCount + 1);
        }
        _abortOpeningMultiDelivery('delivery_multi_abort_stops_missing');
        return;
      }

      // Cross-reference with trip_records to restore already-delivered stops
      final deliveredIndices = <int>{};
      final tripId = widget.savedTripSummary?.tripId;
      if (tripId != null && tripId.isNotEmpty) {
        try {
          final tripSnap = await FirebaseFirestore.instance
              .collection('trip_records')
              .doc(tripId)
              .get();
          if (tripSnap.exists) {
            final progress =
                tripSnap.data()?['deliveryStopsProgress'] as List?;
            if (progress != null) {
              for (final p in progress) {
                if (p is Map<String, dynamic> &&
                    p['status'] == 'delivered') {
                  final idx = p['index'];
                  if (idx is int) deliveredIndices.add(idx);
                }
              }
            }
          }
        } catch (_) {}
      }

      setState(() {
        _stops = [];
        for (int i = 0; i < stops.length; i++) {
          final stop = stops[i] as Map<String, dynamic>;
          final ds = DeliveryStop(
            index: i + 1,
            destination: stop['destination'] ?? 'Unknown',
            sourceId: stop['sourceId'] as String?,
            isCustom: stop['isCustom'] as bool? ?? false,
            sequence: i,
          );
          if (deliveredIndices.contains(ds.index)) {
            ds.isDelivered = true;
          }
          _stops.add(ds);
        }
        _currentStopIndex = -1;
      });
    } catch (e) {
      if (mounted) {
        debugPrint('[DeliveryPhasePageMulti] Error loading delivery stops: $e');
        if (retryCount < 3) {
          // Retry on network error
          await Future.delayed(const Duration(milliseconds: 500));
          return _loadDeliveryStopsFromFirestore(retryCount: retryCount + 1);
        }
        _abortOpeningMultiDelivery('delivery_multi_abort_network');
      }
    }
  }

  Future<void> _loadLocation() async {
    try {
      final pos = await Geolocator.getCurrentPosition(
        locationSettings: const LocationSettings(
          accuracy: LocationAccuracy.high,
        ),
      );
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
    final d = destination.trim().toUpperCase();
    if (d.isEmpty) return null;
    try {
      return _hubs.firstWhere(
        (h) =>
            h.sourceId.trim().toUpperCase() == d ||
            h.sourceNameEn.trim().toUpperCase() == d ||
            h.sourceNameTh.trim().toUpperCase() == d,
      );
    } catch (_) {
      return null;
    }
  }

  /// "รหัส - ชื่อ" ถ้า resolve ได้ ถ้าไม่ได้แสดง destination ตรง ๆ
  String _stopDisplayLabel(DeliveryStop stop) {
    final hub = _getStopHub(stop.destination);
    if (hub != null) {
      final code = hub.sourceId;
      final name = hub.sourceNameTh.isNotEmpty ? hub.sourceNameTh : hub.sourceNameEn;
      if (code.isNotEmpty && name.isNotEmpty && code.toUpperCase() != name.toUpperCase()) {
        return '$code - $name';
      }
      return name.isNotEmpty ? name : code;
    }
    return stop.destination;
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
          photoType: entry.key,
          imageBytes: entry.value.toList(),
        );
        uploadedPhotos.add({
          'url': url,
          'type': entry.key,
          'geocoding': {
            'lat': _lat,
            'lng': _lng,
            'timestamp': DateTime.now().toIso8601String(),
          },
        });
      }

      // Write directly to Firestore (no Cloud Function needed)
      final allDone = await submitDeliveryStopRecord(
        tripId: tripId,
        taskId: widget.savedTripSummary?.taskId,
        stopIndex: _currentStop!.index,
        destination: _currentStop!.destination,
        photos: uploadedPhotos,
        deliveredLat: _lat ?? 0,
        deliveredLng: _lng ?? 0,
      );

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

        if (allDone) {
          ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(content: Text('delivery_all_stops_complete'.tr())),
          );
          _completeMultiTripSuccess();
        }
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
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('delivery_all_stops_complete'.tr())),
      );
      _completeMultiTripSuccess();
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

    final delivered = _stops.where((s) => s.isDelivered).length;
    final total = _stops.length;

    return Scaffold(
      appBar: AppBar(
        title: Text('delivery_multi_title'.tr()),
        elevation: 0,
        backgroundColor: Colors.blue.shade600,
        foregroundColor: Colors.white,
        actions: [
          TextButton.icon(
            onPressed: _saving
                ? null
                : () async {
                    await Navigator.of(context).push<bool>(
                      MaterialPageRoute<bool>(
                        builder: (context) => IncidentReportPage(
                          savedTripSummary: widget.savedTripSummary,
                        ),
                      ),
                    );
                  },
            icon: const Icon(
              Icons.warning_amber_rounded,
              color: Colors.orange,
            ),
            label: Text(
              'report_incident'.tr(),
              style: const TextStyle(color: Colors.white),
            ),
          ),
        ],
      ),
      body: SingleChildScrollView(
        child: Padding(
          padding: const EdgeInsets.all(16.0),
          child: Column(
            children: [
              // Progress header: "Sent X / Y"
              _buildProgressHeader(delivered, total),
              const SizedBox(height: 16),

              // Stop list with navigate buttons
              _buildStopsListWithNavigation(),
              const SizedBox(height: 24),

              // Divider
              const Divider(thickness: 2),
              const SizedBox(height: 16),

              // Evidence section for selected stop
              if (_currentStop != null) ...[
                Text(
                  'delivery_evidence'.tr(),
                  style: Theme.of(context).textTheme.titleMedium?.copyWith(
                        fontWeight: FontWeight.bold,
                      ),
                ),
                const SizedBox(height: 12),
                _buildPhotoSection(),
                const SizedBox(height: 16),
                _buildConfirmButton(),
              ] else if (_allStopsDelivered)
                Center(
                  child: Text('All stops delivered!'.tr(),
                      style: Theme.of(context).textTheme.titleLarge),
                )
              else
                Center(
                  child: Text('delivery_select_stop'.tr(),
                      style: Theme.of(context).textTheme.titleMedium),
                ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildProgressHeader(int delivered, int total) {
    return Card(
      color: Colors.blue.shade50,
      child: Padding(
        padding: const EdgeInsets.all(16.0),
        child: Center(
          child: Text(
            'delivery_sent'.tr(args: ['$delivered', '$total']),
            style: Theme.of(context).textTheme.headlineSmall?.copyWith(
                  fontWeight: FontWeight.bold,
                  color: Colors.blue.shade900,
                ),
          ),
        ),
      ),
    );
  }


  Widget _buildPhotoSection() {
    final stop = _currentStop!;
    final requiredPhotos = _getPhotoTypesForStop();
    final totalStops = _stops.length;
    final isLastStop = stop.isLastStop(totalStops);

    String _getPhotoLabel(String type) {
      final labels = <String, String>{
        'before_open': 'delivery_photo_before_opening',
        'during_open': 'delivery_photo_during_opening',
        'close_container': 'delivery_photo_closing_cabinet',
        'empty_container': 'delivery_photo_cabinet_empty',
      };
      return labels[type]?.tr() ?? type;
    }

    return Column(
      children: requiredPhotos.map((type) {
        final photoKey = 'stop_${stop.index}_$type';
        final hasPhoto = stop.photos.containsKey(photoKey);
        final label = _getPhotoLabel(type);

        return Padding(
          padding: const EdgeInsets.only(bottom: 12.0),
          child: GestureDetector(
            onTap: () => _capturePhoto(type),
            child: Container(
              padding: const EdgeInsets.all(12),
              decoration: BoxDecoration(
                border: Border.all(
                  color: hasPhoto ? Colors.green.shade400 : Colors.red.shade300,
                  width: 2,
                ),
                borderRadius: BorderRadius.circular(8),
                color: hasPhoto ? Colors.green.shade50 : Colors.red.shade50,
              ),
              child: Row(
                children: [
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          label,
                          style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                                fontWeight: FontWeight.w700,
                                color: Colors.black87,
                              ),
                        ),
                        const SizedBox(height: 4),
                        Row(
                          children: [
                            if (!hasPhoto)
                              Container(
                                width: 20,
                                height: 20,
                                decoration: BoxDecoration(
                                  shape: BoxShape.circle,
                                  color: Colors.red.shade400,
                                ),
                                child: const Center(
                                  child: Text(
                                    '!',
                                    style: TextStyle(
                                      color: Colors.white,
                                      fontWeight: FontWeight.bold,
                                      fontSize: 12,
                                    ),
                                  ),
                                ),
                              ),
                            if (!hasPhoto) const SizedBox(width: 6),
                            Text(
                              hasPhoto ? 'photo_captured'.tr() : 'photo_required'.tr(),
                              style: Theme.of(context).textTheme.bodySmall?.copyWith(
                                    color: hasPhoto ? Colors.green.shade700 : Colors.red.shade700,
                                    fontWeight: FontWeight.w700,
                                  ),
                            ),
                          ],
                        ),
                      ],
                    ),
                  ),
                  const SizedBox(width: 12),
                  Container(
                    width: 48,
                    height: 48,
                    decoration: BoxDecoration(
                      color: hasPhoto ? Colors.green.shade100 : Colors.red.shade100,
                      borderRadius: BorderRadius.circular(8),
                      border: Border.all(
                        color: hasPhoto ? Colors.green.shade300 : Colors.red.shade300,
                        width: 1,
                      ),
                    ),
                    child: Center(
                      child: hasPhoto
                          ? const Icon(Icons.check_circle, color: Colors.green, size: 28)
                          : Icon(Icons.camera_alt, color: Colors.red.shade500, size: 24),
                    ),
                  ),
                ],
              ),
            ),
          ),
        );
      }).toList(),
    );
  }

  Widget _buildConfirmButton() {
    final stop = _currentStop!;
    final totalStops = _stops.length;
    final isLastStop = stop.isLastStop(totalStops);
    final confirmText = isLastStop
        ? 'delivery_confirm_last'.tr(args: ['${stop.index}'])
        : 'delivery_confirm_stop'.tr(args: ['${stop.index}']);

    return Column(
      children: [
        SizedBox(
          width: double.infinity,
          child: ElevatedButton(
            onPressed: _currentStopComplete && !_saving ? _confirmStopDelivery : null,
            style: ElevatedButton.styleFrom(
              backgroundColor: Colors.green.shade600,
              padding: const EdgeInsets.symmetric(vertical: 14),
            ),
            child: _saving
                ? const SizedBox(
                    height: 20,
                    width: 20,
                    child: CircularProgressIndicator(
                      strokeWidth: 2,
                      valueColor: AlwaysStoppedAnimation<Color>(Colors.white),
                    ),
                  )
                : Text(confirmText),
          ),
        ),
        if (_allStopsDelivered) ...[
          const SizedBox(height: 8),
          SizedBox(
            width: double.infinity,
            child: ElevatedButton(
              onPressed: _completeAllDeliveries,
              style: ElevatedButton.styleFrom(
                backgroundColor: Colors.blue.shade600,
                padding: const EdgeInsets.symmetric(vertical: 14),
              ),
              child: Text('delivery_complete_trip'.tr()),
            ),
          ),
        ],
      ],
    );
  }

  Widget _buildStopsListWithNavigation() {
    // Sort stops by sequence (ascending: 1, 2, 3...)
    final sortedStopsWithIndex = _stops.asMap().entries.toList()
      ..sort((a, b) => a.value.sequence.compareTo(b.value.sequence));

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: sortedStopsWithIndex.map((e) {
        final originalIndex = e.key;
        final stop = e.value;
        final isActive = _currentStopIndex == originalIndex;
        final isLastStop = stop.isLastStop(_stops.length);
        final hub = _getStopHub(stop.destination);

        return Padding(
          padding: const EdgeInsets.only(bottom: 12.0),
          child: Card(
            color: isActive ? Colors.blue.shade100 : Colors.white,
            child: Padding(
              padding: const EdgeInsets.all(12.0),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    mainAxisAlignment: MainAxisAlignment.spaceBetween,
                    children: [
                      Expanded(
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Row(
                              children: [
                                Text(
                                  _getNumberedCircle(stop.index),
                                  style: TextStyle(
                                    fontSize: 28,
                                    color: !isActive ? Colors.black54 : Colors.black87,
                                  ),
                                ),
                                const SizedBox(width: 8),
                                Expanded(
                                  child: Text(
                                    _stopDisplayLabel(stop),
                                    style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                                          color: Colors.black87,
                                          fontWeight: FontWeight.w600,
                                        ),
                                    maxLines: 2,
                                    overflow: TextOverflow.ellipsis,
                                  ),
                                ),
                              ],
                            ),
                          ],
                        ),
                      ),
                      if (stop.isDelivered)
                        const Icon(Icons.check_circle, color: Colors.green, size: 24),
                    ],
                  ),
                  const SizedBox(height: 12),
                  Row(
                    children: [
                      // Up button
                      SizedBox(
                        width: 50,
                        child: IconButton(
                          onPressed: (originalIndex > 0 && !stop.isDelivered)
                              ? () => _moveStop(originalIndex, -1)
                              : null,
                          icon: const Icon(Icons.arrow_upward),
                          tooltip: 'delivery_move_up'.tr(),
                          style: IconButton.styleFrom(
                            side: BorderSide(
                              color: (originalIndex > 0 && !stop.isDelivered)
                                  ? Colors.grey.shade400
                                  : Colors.grey.shade300,
                            ),
                          ),
                        ),
                      ),
                      const SizedBox(width: 4),
                      // Down button
                      SizedBox(
                        width: 50,
                        child: IconButton(
                          onPressed: (originalIndex < _stops.length - 1 && !stop.isDelivered)
                              ? () => _moveStop(originalIndex, 1)
                              : null,
                          icon: const Icon(Icons.arrow_downward),
                          tooltip: 'delivery_move_down'.tr(),
                          style: IconButton.styleFrom(
                            side: BorderSide(
                              color: (originalIndex < _stops.length - 1 && !stop.isDelivered)
                                  ? Colors.grey.shade400
                                  : Colors.grey.shade300,
                            ),
                          ),
                        ),
                      ),
                      const SizedBox(width: 4),
                      Expanded(
                        child: OutlinedButton.icon(
                          onPressed: () => _openNavigationToStop(hub),
                          icon: const Icon(Icons.navigation, size: 18),
                          label: Text('delivery_navigate'.tr()),
                        ),
                      ),
                      const SizedBox(width: 6),
                      Expanded(
                        child: ElevatedButton(
                          onPressed: stop.isDelivered
                              ? null
                              : () {
                                  setState(() => _currentStopIndex = originalIndex);
                                },
                          style: ElevatedButton.styleFrom(
                            backgroundColor: isActive ? Colors.blue.shade600 : Colors.grey.shade400,
                          ),
                          child: Text(
                            stop.isDelivered ? 'delivery_sent'.tr() : 'delivery_select'.tr(),
                          ),
                        ),
                      ),
                    ],
                  ),
                ],
              ),
            ),
          ),
        );
      }).toList(),
    );
  }

  Future<void> _openNavigationToStop(HubDoc? hub) async {
    if (_lat == null || _lng == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('delivery_location_error'.tr())),
      );
      return;
    }

    try {
      final hasCoords = hub?.latitude != null && hub?.longitude != null;
      await openGoogleMapsDrivingDirections(
        originLat: _lat!,
        originLng: _lng!,
        destLat: hasCoords ? hub!.latitude : null,
        destLng: hasCoords ? hub!.longitude : null,
        destinationPlaceName: _currentStop!.destination,
      );
    } catch (e) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('delivery_submit_error'.tr(args: [e.toString()]))),
      );
    }
  }
}
