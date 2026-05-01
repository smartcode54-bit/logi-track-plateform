import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/material.dart';

import '../../../../core/utils/maps_navigation.dart';
import '../../data/repositories/checkin_repository.dart';
import '../../data/repositories/hubs_repository.dart';

enum _HubFilter { all, hub, soc }

/// รายการจุดรับ–จุดส่ง (HUB/SOC) จาก Firestore เหมือน Web; เปิด Google Maps ด้วย lat/lng
class HubsReferencePage extends StatefulWidget {
  const HubsReferencePage({super.key});

  @override
  State<HubsReferencePage> createState() => _HubsReferencePageState();
}

class _HubsReferencePageState extends State<HubsReferencePage> {
  List<HubDoc> _all = [];
  bool _loading = true;
  String? _error;
  final _searchController = TextEditingController();
  String _query = '';
  _HubFilter _filter = _HubFilter.all;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _searchController.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final list = await fetchAllHubs();
      list.sort((a, b) {
        final na = _displayName(a).toLowerCase();
        final nb = _displayName(b).toLowerCase();
        final c = na.compareTo(nb);
        if (c != 0) return c;
        return a.sourceId.compareTo(b.sourceId);
      });
      if (mounted) {
        setState(() {
          _all = list;
          _loading = false;
        });
      }
    } catch (e) {
      if (mounted) {
        setState(() {
          _error = e.toString();
          _loading = false;
        });
      }
    }
  }

  String _displayName(HubDoc h) {
    if (h.sourceNameTh.trim().isNotEmpty) return h.sourceNameTh.trim();
    return h.sourceNameEn.trim().isNotEmpty ? h.sourceNameEn.trim() : h.sourceId;
  }

  bool _matchesFilter(HubDoc h) {
    switch (_filter) {
      case _HubFilter.all:
        return true;
      case _HubFilter.hub:
        return h.stationType == stationTypeHub;
      case _HubFilter.soc:
        return h.stationType == stationTypeSoc;
    }
  }

  bool _matchesSearch(HubDoc h) {
    final q = _query.trim().toLowerCase();
    if (q.isEmpty) return true;
    return h.sourceId.toLowerCase().contains(q) ||
        h.sourceNameEn.toLowerCase().contains(q) ||
        h.sourceNameTh.toLowerCase().contains(q);
  }

  List<HubDoc> get _visible =>
      _all.where((h) => _matchesFilter(h) && _matchesSearch(h)).toList();

  Future<void> _openPlace(HubDoc h) async {
    if (!h.hasCoordinates) return;
    final ok = await openGoogleMapsPlace(
      lat: h.latitude!,
      lng: h.longitude!,
    );
    if (!mounted) return;
    if (!ok) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('hubs_reference_open_maps_failed'.tr())),
      );
    }
  }

  Future<void> _navigateHere(HubDoc h) async {
    if (!h.hasCoordinates) return;
    double? oLat;
    double? oLng;
    try {
      final pos = await getCurrentPosition();
      oLat = pos.latitude;
      oLng = pos.longitude;
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('hubs_reference_navigate_no_gps'.tr())),
      );
      await _openPlace(h);
      return;
    }
    final ok = await openGoogleMapsDrivingDirections(
      originLat: oLat,
      originLng: oLng,
      destLat: h.latitude,
      destLng: h.longitude,
    );
    if (!mounted) return;
    if (!ok) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('hubs_reference_open_maps_failed'.tr())),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text('hubs_reference_title'.tr()),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh),
            onPressed: _loading ? null : _load,
            tooltip: MaterialLocalizations.of(context).refreshIndicatorSemanticLabel,
          ),
        ],
      ),
      body: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 12, 16, 8),
            child: TextField(
              controller: _searchController,
              decoration: InputDecoration(
                hintText: 'hubs_reference_search_hint'.tr(),
                prefixIcon: const Icon(Icons.search),
                suffixIcon: _query.isNotEmpty
                    ? IconButton(
                        icon: const Icon(Icons.clear),
                        onPressed: () {
                          _searchController.clear();
                          setState(() => _query = '');
                        },
                      )
                    : null,
              ),
              onChanged: (v) => setState(() => _query = v),
            ),
          ),
          SingleChildScrollView(
            scrollDirection: Axis.horizontal,
            padding: const EdgeInsets.symmetric(horizontal: 12),
            child: Row(
              children: [
                ChoiceChip(
                  label: Text('hubs_reference_filter_all'.tr()),
                  selected: _filter == _HubFilter.all,
                  onSelected: (_) => setState(() => _filter = _HubFilter.all),
                ),
                const SizedBox(width: 8),
                ChoiceChip(
                  label: Text('hubs_reference_filter_hub'.tr()),
                  selected: _filter == _HubFilter.hub,
                  onSelected: (_) => setState(() => _filter = _HubFilter.hub),
                ),
                const SizedBox(width: 8),
                ChoiceChip(
                  label: Text('hubs_reference_filter_soc'.tr()),
                  selected: _filter == _HubFilter.soc,
                  onSelected: (_) => setState(() => _filter = _HubFilter.soc),
                ),
              ],
            ),
          ),
          const SizedBox(height: 8),
          Expanded(
            child: _loading
                ? const Center(child: CircularProgressIndicator())
                : _error != null
                    ? Center(
                        child: Padding(
                          padding: const EdgeInsets.all(24),
                          child: Text(
                            '${'hubs_reference_load_error'.tr()}\n$_error',
                            textAlign: TextAlign.center,
                          ),
                        ),
                      )
                    : _visible.isEmpty
                        ? Center(child: Text('hubs_reference_empty'.tr()))
                        : RefreshIndicator(
                            onRefresh: _load,
                            child: ListView.builder(
                              padding: const EdgeInsets.only(bottom: 24),
                              itemCount: _visible.length,
                              itemBuilder: (context, i) {
                                final h = _visible[i];
                                return _HubRow(
                                  hub: h,
                                  displayName: _displayName(h),
                                  onOpenMaps: () => _openPlace(h),
                                  onNavigate: () => _navigateHere(h),
                                );
                              },
                            ),
                          ),
          ),
        ],
      ),
    );
  }
}

class _HubRow extends StatelessWidget {
  const _HubRow({
    required this.hub,
    required this.displayName,
    required this.onOpenMaps,
    required this.onNavigate,
  });

  final HubDoc hub;
  final String displayName;
  final VoidCallback onOpenMaps;
  final VoidCallback onNavigate;

  @override
  Widget build(BuildContext context) {
    final isHub = hub.stationType == stationTypeHub;
    final typeLabel = isHub
        ? 'hubs_reference_station_type_hub'.tr()
        : 'hubs_reference_station_type_soc'.tr();
    final hasCoords = hub.hasCoordinates;

    return Card(
      margin: const EdgeInsets.symmetric(horizontal: 16, vertical: 6),
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Expanded(
                  child: Text(
                    displayName,
                    style: Theme.of(context).textTheme.titleSmall?.copyWith(
                          fontWeight: FontWeight.w600,
                        ),
                  ),
                ),
                Chip(
                  label: Text(
                    typeLabel,
                    style: const TextStyle(fontSize: 12),
                  ),
                  visualDensity: VisualDensity.compact,
                  materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
                ),
              ],
            ),
            const SizedBox(height: 4),
            Text(
              hub.sourceId,
              style: Theme.of(context).textTheme.bodySmall?.copyWith(
                    color: Theme.of(context).colorScheme.onSurfaceVariant,
                  ),
            ),
            if (!hasCoords) ...[
              const SizedBox(height: 8),
              Text(
                'hubs_reference_no_coordinates'.tr(),
                style: Theme.of(context).textTheme.bodySmall?.copyWith(
                      color: Theme.of(context).colorScheme.error,
                    ),
              ),
            ],
            if (hasCoords) ...[
              const SizedBox(height: 8),
              Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [
                  OutlinedButton.icon(
                    onPressed: onOpenMaps,
                    icon: const Icon(Icons.map_outlined, size: 18),
                    label: Text('hubs_reference_open_maps'.tr()),
                  ),
                  OutlinedButton.icon(
                    onPressed: onNavigate,
                    icon: const Icon(Icons.directions, size: 18),
                    label: Text('hubs_reference_navigate'.tr()),
                  ),
                ],
              ),
            ],
          ],
        ),
      ),
    );
  }
}
