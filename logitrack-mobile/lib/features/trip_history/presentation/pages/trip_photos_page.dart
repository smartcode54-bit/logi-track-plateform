import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/material.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../../home/data/models/trip_record.dart';
import '../../data/services/trip_photo_download_service.dart';

/// Per-trip photo viewer + bulk "download all" to the gallery (ADR 0018 / spec
/// `mobile-download-trip-photos`). Shows the trip's evidence photos and any
/// linked incident photos, ordered by workflow step; one button saves them all
/// to the `LogiTrack` album.
class TripPhotosPage extends StatefulWidget {
  const TripPhotosPage({super.key, required this.trip});

  final TripRecord trip;

  @override
  State<TripPhotosPage> createState() => _TripPhotosPageState();
}

class _TripPhotosPageState extends State<TripPhotosPage> {
  bool _loading = true;
  bool _saving = false;
  List<TripPhotoItem> _items = const [];

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() => _loading = true);
    final items = await loadOrderedTripPhotos(widget.trip);
    if (!mounted) return;
    setState(() {
      _items = items;
      _loading = false;
    });
  }

  Future<void> _downloadAll() async {
    if (_saving || _items.isEmpty) return;
    setState(() => _saving = true);
    try {
      final result = await saveTripPhotosToGallery(widget.trip, _items);
      if (!mounted) return;
      if (result.savedNone) {
        _snack('trip_photos_save_none'.tr());
      } else {
        _snack('trip_photos_saved'.tr(namedArgs: {
          'saved': '${result.saved}',
          'total': '${result.total}',
        }));
      }
    } on PhotoPermissionDeniedException {
      if (!mounted) return;
      _showPermissionDenied();
    } catch (_) {
      if (!mounted) return;
      _snack('trip_photos_save_none'.tr());
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  void _snack(String message) {
    ScaffoldMessenger.of(context)
      ..hideCurrentSnackBar()
      ..showSnackBar(SnackBar(content: Text(message)));
  }

  void _showPermissionDenied() {
    ScaffoldMessenger.of(context)
      ..hideCurrentSnackBar()
      ..showSnackBar(SnackBar(
        content: Text('trip_photos_permission_denied'.tr()),
        action: SnackBarAction(
          label: 'trip_photos_permission_settings'.tr(),
          onPressed: _openSettings,
        ),
      ));
  }

  Future<void> _openSettings() async {
    // Best-effort with existing deps only: re-request access, else open the OS
    // settings page (app-settings: works on iOS; harmless no-op elsewhere).
    final granted = await requestPhotoAccess();
    if (granted) {
      await _downloadAll();
      return;
    }
    try {
      await launchUrl(
        Uri.parse('app-settings:'),
        mode: LaunchMode.externalApplication,
      );
    } catch (_) {
      /* nothing else we can do without a permissions plugin */
    }
  }

  /// Open the full-screen, swipeable gallery starting at [initialIndex] within
  /// [items] (the photos of the grid that was tapped).
  void _openFullScreen(List<TripPhotoItem> items, int initialIndex) {
    if (items.isEmpty) return;
    Navigator.of(context).push(
      MaterialPageRoute<void>(
        fullscreenDialog: true,
        builder: (_) => _FullScreenGallery(
          items: items,
          initialIndex: initialIndex,
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final tripItems = _items.where((i) => !i.isIncident).toList();
    final incidentItems = _items.where((i) => i.isIncident).toList();

    return Scaffold(
      appBar: AppBar(title: Text('trip_photos_title'.tr())),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : ListView(
              padding: EdgeInsets.fromLTRB(
                12,
                12,
                12,
                12 + MediaQuery.of(context).viewPadding.bottom,
              ),
              children: [
                // Work-step photos (loading / delivery / multi-stop).
                if (tripItems.isNotEmpty)
                  _PhotoGrid(items: tripItems, onOpen: _openFullScreen)
                else
                  Padding(
                    padding: const EdgeInsets.symmetric(vertical: 24),
                    child: Center(
                      child: Text(
                        'trip_photos_empty'.tr(),
                        style: TextStyle(color: Colors.grey[600], fontSize: 15),
                        textAlign: TextAlign.center,
                      ),
                    ),
                  ),
                const SizedBox(height: 20),
                // Delivery-delay report — always shown. If there is no report,
                // show a green "delivered normally" banner instead of trying to
                // load images (which would 404 when nothing was uploaded).
                Row(
                  children: [
                    Icon(
                      incidentItems.isNotEmpty
                          ? Icons.warning_amber_rounded
                          : Icons.check_circle_outline,
                      size: 18,
                      color: incidentItems.isNotEmpty
                          ? Colors.orange[800]
                          : Colors.green[700],
                    ),
                    const SizedBox(width: 6),
                    Text(
                      'trip_photos_delay_section'.tr(),
                      style: Theme.of(context)
                          .textTheme
                          .titleSmall
                          ?.copyWith(fontWeight: FontWeight.w600),
                    ),
                  ],
                ),
                const SizedBox(height: 8),
                if (incidentItems.isNotEmpty)
                  _PhotoGrid(items: incidentItems, onOpen: _openFullScreen)
                else
                  Container(
                    width: double.infinity,
                    padding: const EdgeInsets.symmetric(
                      horizontal: 14,
                      vertical: 12,
                    ),
                    decoration: BoxDecoration(
                      color: Colors.green.withValues(alpha: 0.10),
                      borderRadius: BorderRadius.circular(8),
                      border: Border.all(
                        color: Colors.green.withValues(alpha: 0.40),
                      ),
                    ),
                    child: Row(
                      children: [
                        Icon(Icons.check_circle,
                            color: Colors.green[700], size: 20),
                        const SizedBox(width: 8),
                        Expanded(
                          child: Text(
                            'trip_photos_no_delay'.tr(),
                            style: TextStyle(
                              color: Colors.green[800],
                              fontWeight: FontWeight.w500,
                            ),
                          ),
                        ),
                      ],
                    ),
                  ),
              ],
            ),
      bottomNavigationBar: (_loading || _items.isEmpty)
          ? null
          : SafeArea(
              child: Padding(
                padding: const EdgeInsets.fromLTRB(16, 8, 16, 12),
                child: FilledButton.icon(
                  onPressed: _saving ? null : _downloadAll,
                  icon: _saving
                      ? const SizedBox(
                          width: 18,
                          height: 18,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        )
                      : const Icon(Icons.download),
                  label: Text(_saving
                      ? 'trip_photos_downloading'.tr()
                      : 'trip_photos_download_all'.tr()),
                ),
              ),
            ),
    );
  }
}

/// Human-readable, localized label for a photo's workflow step (spec R2 label
/// enhancement). Trip types map to loading/delivery step names; multi-stop shows
/// the stop number; incident photos get their own labels. Unknown/legacy types
/// fall back to the raw type string.
String tripPhotoStepLabel(TripPhotoItem item) {
  if (item.isIncident) {
    switch (item.type) {
      case 'map':
        return 'trip_photos_step_incident_map'.tr();
      case 'situation1':
        return 'trip_photos_step_incident_situation'.tr(namedArgs: {'n': '1'});
      case 'situation2':
        return 'trip_photos_step_incident_situation'.tr(namedArgs: {'n': '2'});
      default:
        return item.type;
    }
  }

  final stop = RegExp(r'^stop_(\d+)_(.+)$').firstMatch(item.type);
  if (stop != null) {
    final n = (int.tryParse(stop.group(1)!) ?? 0) + 1;
    return 'trip_photos_step_stop'.tr(
      namedArgs: {'n': '$n', 'step': _baseStepLabel(stop.group(2)!)},
    );
  }

  final extra = RegExp(r'^runsheet_extra_(\d+)$').firstMatch(item.type);
  if (extra != null) {
    return 'trip_photos_step_runsheet_extra'.tr(namedArgs: {'n': extra.group(1)!});
  }

  return _baseStepLabel(item.type);
}

String _baseStepLabel(String type) {
  switch (type) {
    case 'runsheet':
      return 'trip_photos_step_runsheet'.tr();
    case 'pre_close':
      return 'trip_photos_step_pre_close'.tr();
    case 'closing':
      return 'trip_photos_step_closing'.tr();
    case 'seal':
      return 'trip_photos_step_seal'.tr();
    case 'pre_open':
      return 'trip_photos_step_pre_open'.tr();
    case 'opening':
      return 'trip_photos_step_opening'.tr();
    case 'empty_container':
      return 'trip_photos_step_empty_container'.tr();
    case 'runsheet_received':
      return 'trip_photos_step_runsheet_received'.tr();
    // Customer-app screenshots (ADR 0019)
    case 'checkin_app':
      return 'trip_photos_step_checkin_app'.tr();
    case 'truck_release':
      return 'trip_photos_step_truck_release'.tr();
    case 'arrived':
      return 'trip_photos_step_arrived'.tr();
    default:
      return type; // unknown / legacy → raw type
  }
}

class _PhotoGrid extends StatelessWidget {
  const _PhotoGrid({required this.items, required this.onOpen});

  final List<TripPhotoItem> items;

  /// Opens the swipeable gallery at [index] within [items].
  final void Function(List<TripPhotoItem> items, int index) onOpen;

  @override
  Widget build(BuildContext context) {
    return GridView.builder(
      shrinkWrap: true,
      physics: const NeverScrollableScrollPhysics(),
      itemCount: items.length,
      gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
        crossAxisCount: 3,
        mainAxisSpacing: 10,
        crossAxisSpacing: 8,
        childAspectRatio: 0.72,
      ),
      itemBuilder: (context, i) {
        final item = items[i];
        return GestureDetector(
          onTap: () => onOpen(items, i),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Expanded(
                child: ClipRRect(
                  borderRadius: BorderRadius.circular(8),
                  child: Container(
                    color: Colors.grey[200],
                    child: Image.network(
                      item.url,
                      fit: BoxFit.cover,
                      // Decode to a thumbnail-sized bitmap so a grid of full-res
                      // evidence JPEGs doesn't blow up memory on low-end phones.
                      cacheWidth: 400,
                      loadingBuilder: (ctx, child, progress) => progress == null
                          ? child
                          : const Center(
                              child: SizedBox(
                                width: 20,
                                height: 20,
                                child: CircularProgressIndicator(strokeWidth: 2),
                              ),
                            ),
                      errorBuilder: (ctx, err, stack) => Center(
                        child: Icon(Icons.broken_image,
                            color: Colors.grey[400], size: 28),
                      ),
                    ),
                  ),
                ),
              ),
              const SizedBox(height: 4),
              Text(
                tripPhotoStepLabel(item),
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                textAlign: TextAlign.center,
                style: TextStyle(
                  fontSize: 11,
                  height: 1.15,
                  color: Colors.grey[700],
                ),
              ),
            ],
          ),
        );
      },
    );
  }
}

/// Full-screen, swipeable photo viewer. Swipe left/right to move between the
/// photos of the grid that was tapped; pinch to zoom each one. Shows a counter
/// and the current photo's step label.
class _FullScreenGallery extends StatefulWidget {
  const _FullScreenGallery({required this.items, required this.initialIndex});

  final List<TripPhotoItem> items;
  final int initialIndex;

  @override
  State<_FullScreenGallery> createState() => _FullScreenGalleryState();
}

class _FullScreenGalleryState extends State<_FullScreenGallery> {
  late final PageController _controller;
  late int _index;

  @override
  void initState() {
    super.initState();
    _index = widget.initialIndex;
    _controller = PageController(initialPage: widget.initialIndex);
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final media = MediaQuery.of(context);
    return Scaffold(
      backgroundColor: Colors.black,
      body: Stack(
        children: [
          Positioned.fill(
            child: PageView.builder(
              controller: _controller,
              itemCount: widget.items.length,
              onPageChanged: (i) => setState(() => _index = i),
              itemBuilder: (ctx, i) => InteractiveViewer(
                minScale: 0.8,
                maxScale: 4,
                child: Center(
                  child: Image.network(
                    widget.items[i].url,
                    fit: BoxFit.contain,
                    loadingBuilder: (ctx, child, progress) => progress == null
                        ? child
                        : const Center(
                            child: CircularProgressIndicator(),
                          ),
                    errorBuilder: (ctx, err, stack) => const Center(
                      child: Icon(Icons.broken_image,
                          color: Colors.white38, size: 48),
                    ),
                  ),
                ),
              ),
            ),
          ),
          // Top bar: photo counter + close button.
          Positioned(
            top: media.padding.top + 8,
            left: 16,
            right: 8,
            child: Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Text(
                  '${_index + 1} / ${widget.items.length}',
                  style: const TextStyle(color: Colors.white, fontSize: 16),
                ),
                IconButton(
                  icon: const Icon(Icons.close, color: Colors.white, size: 30),
                  onPressed: () => Navigator.of(context).pop(),
                ),
              ],
            ),
          ),
          // Bottom caption: current photo's step label.
          Positioned(
            left: 0,
            right: 0,
            bottom: 0,
            child: Container(
              color: Colors.black54,
              padding: EdgeInsets.fromLTRB(16, 10, 16, 10 + media.padding.bottom),
              child: Text(
                tripPhotoStepLabel(widget.items[_index]),
                textAlign: TextAlign.center,
                style: const TextStyle(color: Colors.white, fontSize: 14),
              ),
            ),
          ),
        ],
      ),
    );
  }
}
