import 'dart:typed_data';
import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/material.dart';
import 'package:geolocator/geolocator.dart';
import 'package:image_picker/image_picker.dart';
import 'package:cloud_firestore/cloud_firestore.dart'
    show FirebaseFirestore, GetOptions, Source;
import '../../../home/data/repositories/checkin_repository.dart';
import '../../../home/data/repositories/trip_records_repository.dart';
import '../../../home/data/repositories/hubs_repository.dart';
import '../../../home/data/services/photo_overlay_service.dart';
import '../../data/repositories/delivery_trip_repository.dart';
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
  Position? _currentPosition;
  OverlayContext? _cachedOverlayContext;
  bool _locationLoading = true;
  bool _saving = false;
  List<HubDoc> _hubs = [];

  List<DeliveryStop> get _undeliveredStops =>
      _stops.where((s) => !s.isDelivered).toList()
        ..sort((a, b) => a.sequence.compareTo(b.sequence));

  List<DeliveryStop> get _deliveredStops =>
      _stops.where((s) => s.isDelivered).toList()
        ..sort((a, b) => a.sequence.compareTo(b.sequence));

  bool get _allStopsDelivered => _stops.every((s) => s.isDelivered);

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

  List<String> _photoTypesFor(DeliveryStop stop) =>
      stop.isLastStop(_stops.length) ? _lastStopPhotoTypes : _nonLastStopPhotoTypes;

  bool _isStopComplete(DeliveryStop stop) {
    final required = _photoTypesFor(stop);
    return required.every((t) => stop.photos.containsKey('stop_${stop.index}_$t'));
  }

  int _capturedPhotoCount(DeliveryStop stop) {
    final required = _photoTypesFor(stop);
    return required.where((t) => stop.photos.containsKey('stop_${stop.index}_$t')).length;
  }

  String _photoLabel(String type) {
    const labels = {
      'before_open': 'delivery_photo_before_opening',
      'during_open': 'delivery_photo_during_opening',
      'close_container': 'delivery_photo_closing_cabinet',
      'empty_container': 'delivery_photo_cabinet_empty',
    };
    return labels[type]?.tr() ?? type;
  }

  /// Get numbered circle emoji for stop index (1-9)
  String _getNumberedCircle(int index) {
    const circles = ['①', '②', '③', '④', '⑤', '⑥', '⑦', '⑧', '⑨'];
    if (index > 0 && index <= circles.length) {
      return circles[index - 1];
    }
    return '$index';
  }

  void _reorderStop(int oldIndex, int newIndex) {
    if (newIndex > oldIndex) newIndex -= 1;
    final undelivered = _undeliveredStops;
    if (oldIndex < 0 || oldIndex >= undelivered.length) return;
    if (newIndex < 0 || newIndex >= undelivered.length) return;

    setState(() {
      final moved = undelivered.removeAt(oldIndex);
      undelivered.insert(newIndex, moved);
      for (int i = 0; i < undelivered.length; i++) {
        undelivered[i].sequence = i;
      }
      int seq = undelivered.length;
      for (final s in _deliveredStops) {
        s.sequence = seq++;
      }
    });
  }

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
      final pos = await getCurrentPosition();
      if (mounted) {
        setState(() {
          _currentPosition = pos;
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
        (hub) {
          final code = (hub.sourceId ?? '').trim().toUpperCase();
          return code == d;
        },
        orElse: () => HubDoc(sourceId: d, sourceNameTh: d, sourceNameEn: d),
      );
    } catch (_) {
      return HubDoc(sourceId: d, sourceNameTh: d, sourceNameEn: d);
    }
  }

  String _stopDisplayLabel(DeliveryStop stop) {
    final hub = _getStopHub(stop.destination);
    if (hub == null) return stop.destination;
    return hub.sourceNameTh ?? hub.sourceNameEn ?? hub.sourceId ?? stop.destination;
  }

  Future<void> _capturePhotoForStop(DeliveryStop stop, String photoType) async {
    // Show camera/gallery picker
    final source = await showModalBottomSheet<ImageSource>(
      context: context,
      builder: (ctx) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              leading: const Icon(Icons.camera_alt),
              title: Text('delivery_photo_source_camera'.tr()),
              onTap: () => Navigator.pop(ctx, ImageSource.camera),
            ),
            ListTile(
              leading: const Icon(Icons.photo_library),
              title: Text('delivery_photo_source_gallery'.tr()),
              onTap: () => Navigator.pop(ctx, ImageSource.gallery),
            ),
          ],
        ),
      ),
    );
    if (source == null || !mounted) return;

    try {
      final picker = ImagePicker();
      final xfile = await picker.pickImage(source: source, imageQuality: 85);
      if (xfile == null || !mounted) return;
      final bytes = await xfile.readAsBytes();

      // Fetch/reuse overlay context for GPS stamp
      if (_cachedOverlayContext == null && _currentPosition != null) {
        try {
          _cachedOverlayContext = await fetchOverlayContext(
            _currentPosition!.latitude,
            _currentPosition!.longitude,
          );
        } catch (_) {}
      }

      final stamped = await stampOverlayAndCompressForEvidence(
        bytes.toList(),
        position: _currentPosition,
        overlayContext: _cachedOverlayContext,
      );

      if (mounted) {
        setState(() {
          stop.photos['stop_${stop.index}_$photoType'] = stamped;
        });
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('delivery_photo_error'.tr())),
        );
      }
    }
  }

  Future<void> _openStopBottomSheet(DeliveryStop stop) async {
    await showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      useSafeArea: true,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (sheetCtx) {
        return DraggableScrollableSheet(
          expand: false,
          initialChildSize: 0.9,
          maxChildSize: 0.95,
          minChildSize: 0.5,
          builder: (_, scrollController) {
            return StatefulBuilder(
              builder: (bsCtx, setSheetState) {
                final photoTypes = _photoTypesFor(stop);
                final allCaptured = _isStopComplete(stop);

                return Column(
                  children: [
                    // Drag handle
                    Center(
                      child: Container(
                        margin: const EdgeInsets.only(top: 10, bottom: 4),
                        width: 40,
                        height: 4,
                        decoration: BoxDecoration(
                          color: Colors.grey.shade300,
                          borderRadius: BorderRadius.circular(2),
                        ),
                      ),
                    ),

                    // Sheet header
                    Padding(
                      padding: const EdgeInsets.fromLTRB(20, 8, 20, 0),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Row(
                            children: [
                              Container(
                                width: 32, height: 32,
                                decoration: const BoxDecoration(
                                  color: Color(0xFF2563EB),
                                  shape: BoxShape.circle,
                                ),
                                child: Center(
                                  child: Text('${stop.index}',
                                    style: const TextStyle(
                                      color: Colors.white,
                                      fontWeight: FontWeight.bold,
                                    ),
                                  ),
                                ),
                              ),
                              const SizedBox(width: 12),
                              Expanded(
                                child: Text(
                                  _stopDisplayLabel(stop),
                                  style: Theme.of(bsCtx).textTheme.titleMedium
                                      ?.copyWith(fontWeight: FontWeight.bold),
                                  maxLines: 2,
                                  overflow: TextOverflow.ellipsis,
                                ),
                              ),
                            ],
                          ),
                          const SizedBox(height: 4),
                          Padding(
                            padding: const EdgeInsets.only(left: 44),
                            child: Text(
                              'delivery_stop_progress'.tr(args: [
                                '${_capturedPhotoCount(stop)}',
                                '${photoTypes.length}',
                              ]),
                              style: TextStyle(
                                color: Colors.grey.shade600, fontSize: 13),
                            ),
                          ),
                          const Divider(height: 24),
                        ],
                      ),
                    ),

                    // Scrollable photo tile list
                    Expanded(
                      child: ListView.separated(
                        controller: scrollController,
                        padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
                        itemCount: photoTypes.length,
                        separatorBuilder: (_, __) => const SizedBox(height: 12),
                        itemBuilder: (_, i) {
                          final type = photoTypes[i];
                          final photoKey = 'stop_${stop.index}_$type';
                          final bytes = stop.photos[photoKey];
                          final captured = bytes != null;
                          final label = _photoLabel(type);

                          return InkWell(
                            onTap: () async {
                              await _capturePhotoForStop(stop, type);
                              setSheetState(() {});
                              if (mounted) setState(() {});
                            },
                            borderRadius: BorderRadius.circular(12),
                            child: Container(
                              padding: const EdgeInsets.all(16),
                              decoration: BoxDecoration(
                                color: captured
                                    ? Colors.green.withOpacity(0.05)
                                    : Theme.of(bsCtx).cardColor,
                                borderRadius: BorderRadius.circular(12),
                                border: Border.all(
                                  color: captured
                                      ? Colors.green
                                      : Colors.grey.shade300,
                                  width: 1.5,
                                ),
                              ),
                              child: Row(
                                children: [
                                  // Thumbnail or placeholder
                                  if (captured)
                                    ClipRRect(
                                      borderRadius: BorderRadius.circular(8),
                                      child: Image.memory(
                                        bytes,
                                        height: 56, width: 56,
                                        fit: BoxFit.cover,
                                      ),
                                    )
                                  else
                                    Container(
                                      padding: const EdgeInsets.all(12),
                                      decoration: BoxDecoration(
                                        color: Colors.blueAccent.withOpacity(0.1),
                                        shape: BoxShape.circle,
                                      ),
                                      child: const Icon(Icons.camera_alt,
                                          color: Colors.blueAccent),
                                    ),
                                  const SizedBox(width: 16),

                                  // Label column
                                  Expanded(
                                    child: Column(
                                      crossAxisAlignment: CrossAxisAlignment.start,
                                      children: [
                                        Text(label,
                                          style: const TextStyle(
                                            fontWeight: FontWeight.bold,
                                            fontSize: 16,
                                          ),
                                        ),
                                        const SizedBox(height: 4),
                                        Text(
                                          captured
                                              ? 'photo_captured'.tr()
                                              : 'photo_required'.tr(),
                                          style: TextStyle(
                                            color: captured
                                                ? Colors.green.shade600
                                                : Colors.grey.shade500,
                                            fontSize: 13,
                                          ),
                                        ),
                                      ],
                                    ),
                                  ),

                                  // Status icon
                                  if (captured)
                                    const Icon(Icons.check_circle,
                                        color: Colors.green, size: 26)
                                  else
                                    Icon(Icons.camera_alt,
                                        color: Colors.grey.shade400, size: 24),
                                ],
                              ),
                            ),
                          );
                        },
                      ),
                    ),

                    // Pinned footer: Confirm button
                    SafeArea(
                      child: Padding(
                        padding: const EdgeInsets.fromLTRB(16, 8, 16, 16),
                        child: SizedBox(
                          width: double.infinity,
                          child: ElevatedButton(
                            onPressed: (allCaptured && !_saving)
                                ? () async {
                                    Navigator.of(sheetCtx).pop();
                                    await _confirmStopDelivery(stop);
                                  }
                                : null,
                            style: ElevatedButton.styleFrom(
                              backgroundColor: Colors.green.shade600,
                              foregroundColor: Colors.white,
                              padding: const EdgeInsets.symmetric(vertical: 14),
                              shape: RoundedRectangleBorder(
                                borderRadius: BorderRadius.circular(12),
                              ),
                              disabledBackgroundColor: Colors.grey.shade300,
                            ),
                            child: _saving
                                ? const SizedBox(
                                    height: 20, width: 20,
                                    child: CircularProgressIndicator(
                                      strokeWidth: 2,
                                      color: Colors.white,
                                    ),
                                  )
                                : Text(
                                    stop.isLastStop(_stops.length)
                                        ? 'delivery_confirm_last'
                                            .tr(args: ['${stop.index}'])
                                        : 'delivery_confirm_stop'
                                            .tr(args: ['${stop.index}']),
                                    style: const TextStyle(
                                      fontSize: 16,
                                      fontWeight: FontWeight.bold,
                                    ),
                                  ),
                          ),
                        ),
                      ),
                    ),
                  ],
                );
              },
            );
          },
        );
      },
    );
  }

  Future<void> _confirmStopDelivery(DeliveryStop stop) async {
    if (!_isStopComplete(stop)) return;

    try {
      setState(() => _saving = true);

      final tripId = widget.savedTripSummary?.tripId;
      if (tripId == null) throw Exception('No trip ID');

      // Upload photos to Firebase Storage
      final uploadedPhotos = <Map<String, dynamic>>[];
      for (final entry in stop.photos.entries) {
        final url = await uploadTripPhoto(
          tripId: tripId,
          photoType: entry.key,
          imageBytes: entry.value.toList(),
        );
        uploadedPhotos.add({
          'url': url,
          'type': entry.key,
          'geocoding': {
            'lat': _currentPosition?.latitude,
            'lng': _currentPosition?.longitude,
            'timestamp': DateTime.now().toIso8601String(),
          },
        });
      }

      // Write to Firestore (returns true if all stops delivered)
      final allDone = await submitDeliveryStopRecord(
        tripId: tripId,
        taskId: widget.savedTripSummary?.taskId,
        stopIndex: stop.index,
        destination: stop.destination,
        photos: uploadedPhotos,
        deliveredLat: _currentPosition?.latitude ?? 0,
        deliveredLng: _currentPosition?.longitude ?? 0,
      );

      if (mounted) {
        setState(() {
          stop.isDelivered = true;
          _saving = false;
        });

        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('delivery_stop_confirmed'.tr())),
        );

        if (allDone) {
          ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(content: Text('delivery_all_stops_done'.tr())),
          );
          _completeMultiTripSuccess();
        }
      }
    } catch (e) {
      if (mounted) {
        setState(() => _saving = false);
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(
              'delivery_submit_error'.tr(args: [e.toString()]))),
        );
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    const darkNavy = Color(0xFF0F172A);
    final undelivered = _undeliveredStops;
    final delivered = _deliveredStops;

    return Scaffold(
      appBar: AppBar(
        title: Text('delivery_multi_title'.tr()),
        backgroundColor: darkNavy,
        foregroundColor: Colors.white,
        elevation: 0,
        actions: [
          TextButton.icon(
            onPressed: _saving ? null : () => Navigator.of(context).push(
              MaterialPageRoute<bool>(
                builder: (context) => IncidentReportPage(
                  savedTripSummary: widget.savedTripSummary,
                ),
              ),
            ),
            icon: const Icon(Icons.warning_amber_rounded, color: Colors.orange),
            label: Text('report_incident'.tr(),
                style: const TextStyle(color: Colors.white)),
          ),
        ],
      ),
      body: Column(
        children: [
          _buildProgressBar(),
          Expanded(
            child: _stops.isEmpty
                ? const Center(child: CircularProgressIndicator())
                : _buildStopsList(undelivered, delivered),
          ),
        ],
      ),
    );
  }

  Widget _buildProgressBar() {
    const darkNavy = Color(0xFF0F172A);
    final delivered = _deliveredStops.length;
    final total = _stops.length;
    final fraction = total == 0 ? 0.0 : delivered / total;
    final pct = (fraction * 100).round();

    return Container(
      color: darkNavy,
      padding: const EdgeInsets.fromLTRB(16, 0, 16, 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              Text(
                'delivery_progress'.tr(args: ['$delivered', '$total']),
                style: const TextStyle(color: Colors.white70, fontSize: 13),
              ),
              Text(
                '$pct%',
                style: TextStyle(
                  color: fraction == 1.0 ? Colors.greenAccent : Colors.white,
                  fontWeight: FontWeight.bold,
                  fontSize: 13,
                ),
              ),
            ],
          ),
          const SizedBox(height: 6),
          ClipRRect(
            borderRadius: BorderRadius.circular(4),
            child: LinearProgressIndicator(
              value: fraction,
              minHeight: 8,
              backgroundColor: Colors.white24,
              valueColor: AlwaysStoppedAnimation<Color>(
                fraction == 1.0 ? Colors.greenAccent : const Color(0xFF2563EB),
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildStopsList(
      List<DeliveryStop> undelivered, List<DeliveryStop> delivered) {
    final showDragHint = undelivered.length >= 2;

    return ReorderableListView(
      padding: const EdgeInsets.only(top: 8, bottom: 16),
      onReorder: _reorderStop,
      footer: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (showDragHint)
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
              child: Text(
                'delivery_reorder_hint'.tr(),
                style: TextStyle(color: Colors.grey.shade500, fontSize: 12),
              ),
            ),
          if (delivered.isNotEmpty) ...[
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
              child: Text(
                'delivery_stop_done'.tr().toUpperCase(),
                style: TextStyle(
                  color: Colors.grey.shade500,
                  fontSize: 12,
                  fontWeight: FontWeight.bold,
                  letterSpacing: 0.8,
                ),
              ),
            ),
            ...delivered.map((s) => _buildStopCard(s, reorderable: false)),
          ],
          if (_allStopsDelivered) _buildCompleteTripBanner(),
        ],
      ),
      children: [
        for (final stop in undelivered)
          _buildStopCard(stop, reorderable: true,
              key: ValueKey('stop_${stop.index}')),
      ],
    );
  }

  Widget _buildStopCard(DeliveryStop stop,
      {bool reorderable = true, Key? key}) {
    final hub = _getStopHub(stop.destination);
    final label = _stopDisplayLabel(stop);
    final captured = _capturedPhotoCount(stop);
    final required = _photoTypesFor(stop).length;
    final complete = captured == required;

    // Photo progress badge color
    final badgeColor = stop.isDelivered
        ? Colors.green.shade600
        : complete
            ? Colors.green.shade600
            : captured > 0
                ? Colors.orange.shade600
                : Colors.red.shade400;

    final badgeText = stop.isDelivered
        ? '$required/$required ✓'
        : '$captured/$required';

    return Card(
      key: key,
      margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 5),
      elevation: stop.isDelivered ? 0 : 2,
      color: stop.isDelivered
          ? Colors.grey.shade100
          : Theme.of(context).cardColor,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                // Numbered chip
                Container(
                  width: 32, height: 32,
                  decoration: BoxDecoration(
                    color: stop.isDelivered
                        ? Colors.grey.shade400
                        : const Color(0xFF2563EB),
                    shape: BoxShape.circle,
                  ),
                  child: Center(
                    child: Text(
                      '${stop.index}',
                      style: const TextStyle(
                        color: Colors.white,
                        fontWeight: FontWeight.bold,
                        fontSize: 14,
                      ),
                    ),
                  ),
                ),
                const SizedBox(width: 10),

                // Destination label
                Expanded(
                  child: Text(
                    label,
                    style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                      fontWeight: FontWeight.w600,
                      color: stop.isDelivered ? Colors.grey : Colors.black87,
                    ),
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                const SizedBox(width: 8),

                // Photo progress badge
                Container(
                  padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
                  decoration: BoxDecoration(
                    color: badgeColor.withOpacity(0.12),
                    borderRadius: BorderRadius.circular(20),
                    border: Border.all(color: badgeColor, width: 1),
                  ),
                  child: Text(
                    badgeText,
                    style: TextStyle(
                      color: badgeColor,
                      fontWeight: FontWeight.bold,
                      fontSize: 12,
                    ),
                  ),
                ),

                // Drag handle (reorderable stops only)
                if (reorderable)
                  Padding(
                    padding: const EdgeInsets.only(left: 4),
                    child: Icon(Icons.drag_handle,
                        color: Colors.grey.shade400, size: 22),
                  ),
              ],
            ),
            const SizedBox(height: 10),

            // Action buttons row — 2 buttons, no up/down
            Row(
              children: [
                // Navigate button
                Expanded(
                  child: OutlinedButton.icon(
                    onPressed: () => _openNavigationToStop(hub, stop),
                    icon: const Icon(Icons.navigation_outlined, size: 18),
                    label: Text('delivery_navigate'.tr()),
                    style: OutlinedButton.styleFrom(
                      padding: const EdgeInsets.symmetric(vertical: 10),
                    ),
                  ),
                ),
                const SizedBox(width: 8),

                // Select / Done button
                Expanded(
                  child: ElevatedButton.icon(
                    onPressed: stop.isDelivered
                        ? null
                        : () => _openStopBottomSheet(stop),
                    icon: Icon(
                      stop.isDelivered
                          ? Icons.check_circle
                          : Icons.camera_alt_outlined,
                      size: 18,
                    ),
                    label: Text(
                      stop.isDelivered
                          ? 'delivery_stop_done'.tr()
                          : 'delivery_stop_select_photos'.tr(),
                    ),
                    style: ElevatedButton.styleFrom(
                      backgroundColor: stop.isDelivered
                          ? Colors.grey.shade400
                          : const Color(0xFF2563EB),
                      foregroundColor: Colors.white,
                      padding: const EdgeInsets.symmetric(vertical: 10),
                    ),
                  ),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildCompleteTripBanner() {
    return Padding(
      padding: const EdgeInsets.all(16),
      child: Card(
        color: Colors.green.shade50,
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            children: [
              const Icon(Icons.check_circle, color: Colors.green, size: 48),
              const SizedBox(height: 12),
              Text(
                'delivery_all_stops_done'.tr(),
                textAlign: TextAlign.center,
                style: const TextStyle(
                  fontSize: 16,
                  fontWeight: FontWeight.bold,
                  color: Colors.green,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Future<void> _openNavigationToStop(HubDoc? hub, [DeliveryStop? stop]) async {
    if (_currentPosition == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('delivery_location_error'.tr())),
      );
      return;
    }

    try {
      final hasCoords = hub?.latitude != null && hub?.longitude != null;
      await openGoogleMapsDrivingDirections(
        originLat: _currentPosition!.latitude,
        originLng: _currentPosition!.longitude,
        destLat: hasCoords ? hub!.latitude : null,
        destLng: hasCoords ? hub!.longitude : null,
        destinationPlaceName: stop?.destination,
      );
    } catch (e) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('delivery_submit_error'.tr(args: [e.toString()]))),
      );
    }
  }
}
