import 'package:flutter/material.dart';
import 'package:easy_localization/easy_localization.dart';
import '../../../features/home/data/repositories/hubs_repository.dart';

class SearchableHubPicker extends StatelessWidget {
  final String label;
  final String hintText;
  final String value;
  final List<HubDoc> hubs;
  final ValueChanged<HubDoc> onSelected;

  const SearchableHubPicker({
    super.key,
    required this.label,
    required this.hintText,
    required this.value,
    required this.hubs,
    required this.onSelected,
  });

  static bool _matchHub(HubDoc hub, String query) {
    if (query.isEmpty) return true;
    final q = query.trim().toLowerCase();
    return hub.sourceNameEn.toLowerCase().contains(q) ||
        hub.sourceNameTh.toLowerCase().contains(q) ||
        hub.sourceId.toLowerCase().contains(q);
  }

  Future<void> _openPicker(BuildContext context) async {
    final selected = await showModalBottomSheet<HubDoc>(
      context: context,
      isScrollControlled: true,
      useSafeArea: true,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (ctx) => SearchableHubSheet(
        hubs: hubs,
        initialValue: value,
        onSelected: (hub) => Navigator.of(ctx).pop(hub),
      ),
    );
    if (selected != null) onSelected(selected);
  }

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: () => _openPicker(context),
      borderRadius: BorderRadius.circular(4),
      child: InputDecorator(
        decoration: InputDecoration(
          labelText: label,
          hintText: hintText,
          border: const OutlineInputBorder(),
          suffixIcon: const Icon(Icons.arrow_drop_down),
        ),
        child: Text(
          value.isEmpty ? '' : value,
          style: TextStyle(
            color: value.isEmpty
                ? Theme.of(context).hintColor
                : Theme.of(context).textTheme.titleMedium?.color,
          ),
        ),
      ),
    );
  }
}

class SearchableHubSheet extends StatefulWidget {
  final List<HubDoc> hubs;
  final String initialValue;
  final ValueChanged<HubDoc> onSelected;

  const SearchableHubSheet({
    super.key,
    required this.hubs,
    required this.initialValue,
    required this.onSelected,
  });

  @override
  State<SearchableHubSheet> createState() => _SearchableHubSheetState();
}

class _SearchableHubSheetState extends State<SearchableHubSheet> {
  final TextEditingController _searchController = TextEditingController();
  final FocusNode _searchFocus = FocusNode();

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _searchFocus.requestFocus();
    });
  }

  @override
  void dispose() {
    _searchController.dispose();
    _searchFocus.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return DraggableScrollableSheet(
      expand: false,
      initialChildSize: 0.6,
      maxChildSize: 0.9,
      minChildSize: 0.4,
      builder: (context, scrollController) {
        return Column(
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 12, 16, 8),
              child: TextField(
                controller: _searchController,
                focusNode: _searchFocus,
                decoration: InputDecoration(
                  hintText: 'loading_phase_hub_search_hint'.tr(),
                  prefixIcon: const Icon(Icons.search),
                  border: const OutlineInputBorder(),
                  contentPadding: const EdgeInsets.symmetric(
                    horizontal: 16,
                    vertical: 12,
                  ),
                ),
                onChanged: (_) => setState(() {}),
              ),
            ),
            Expanded(
              child: ValueListenableBuilder<TextEditingValue>(
                valueListenable: _searchController,
                builder: (context, value, _) {
                  final query = value.text;
                  final filtered = widget.hubs
                      .where((h) => SearchableHubPicker._matchHub(h, query))
                      .toList();
                  if (filtered.isEmpty) {
                    return Center(
                      child: Text(
                        query.isEmpty
                            ? 'loading_phase_hub_no_list'.tr()
                            : 'loading_phase_hub_no_match'.tr(args: [query]),
                        style: TextStyle(color: Colors.grey[600]),
                      ),
                    );
                  }
                  return ListView.builder(
                    controller: scrollController,
                    itemCount: filtered.length,
                    itemBuilder: (context, index) {
                      final hub = filtered[index];
                      // Format: pickupID - hub name (TH)
                      final label =
                          '${hub.sourceId} - ${hub.sourceNameTh} (TH)';
                      final isSelected =
                          hub.sourceNameEn == widget.initialValue ||
                          hub.sourceId == widget.initialValue;
                      return ListTile(
                        title: Text(label),
                        trailing: isSelected
                            ? const Icon(Icons.check, color: Colors.green)
                            : null,
                        onTap: () => widget.onSelected(hub),
                      );
                    },
                  );
                },
              ),
            ),
          ],
        );
      },
    );
  }
}
