import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/material.dart';
import '../../../home/data/repositories/hubs_repository.dart';

class AddDeliveryStopResult {
  final String destination;
  final String? sourceId;
  final bool isCustom;
  final String? destinationLinkedCustomerId;
  final String? destinationLinkedCustomerName;
  final String? destinationCustomerLinkKind;

  AddDeliveryStopResult({
    required this.destination,
    this.sourceId,
    required this.isCustom,
    this.destinationLinkedCustomerId,
    this.destinationLinkedCustomerName,
    this.destinationCustomerLinkKind,
  });
}

class AddDeliveryStopDialog extends StatefulWidget {
  final List<HubDoc> availableHubs;

  const AddDeliveryStopDialog({
    super.key,
    required this.availableHubs,
  });

  @override
  State<AddDeliveryStopDialog> createState() => _AddDeliveryStopDialogState();
}

class _AddDeliveryStopDialogState extends State<AddDeliveryStopDialog> {
  late List<HubDoc> _hubs;
  HubDoc? _selectedHub;
  bool _isCustomMode = false;
  String _searchQuery = '';
  final TextEditingController _customCodeController = TextEditingController();
  final TextEditingController _customNameController = TextEditingController();
  final TextEditingController _searchController = TextEditingController();

  @override
  void initState() {
    super.initState();
    _hubs = widget.availableHubs;
  }

  @override
  void dispose() {
    _customCodeController.dispose();
    _customNameController.dispose();
    _searchController.dispose();
    super.dispose();
  }

  bool get _canAddCustom =>
      _customCodeController.text.isNotEmpty &&
      _customNameController.text.isNotEmpty;

  bool get _canSubmit {
    if (_isCustomMode) {
      return _canAddCustom;
    } else {
      return _selectedHub != null;
    }
  }

  void _submitAddStop() {
    if (!_canSubmit) return;

    if (_isCustomMode) {
      final code = _customCodeController.text.trim().toUpperCase();
      final pointName = _customNameController.text.trim();
      Navigator.pop(
        context,
        AddDeliveryStopResult(
          destination: code,
          destinationLinkedCustomerName: pointName,
          sourceId: null,
          isCustom: true,
        ),
      );
    } else if (_selectedHub != null) {
      final h = _selectedHub!;
      // Same shape as seeded stop: destination = รหัสจุด (SPK…), linked name = ชื่อจุด — not logistics brand text.
      final code = h.sourceId.trim().toUpperCase();
      final th = h.sourceNameTh.trim();
      final en = h.sourceNameEn.trim();
      final pointName = th.isNotEmpty
          ? th
          : (en.isNotEmpty ? en : code);

      Navigator.pop(
        context,
        AddDeliveryStopResult(
          destination: code,
          sourceId: h.id,
          isCustom: false,
          destinationLinkedCustomerId: h.linkedCustomerId,
          destinationLinkedCustomerName: pointName,
          destinationCustomerLinkKind:
              (h.customerLinkKind != null &&
                      h.customerLinkKind!.trim().isNotEmpty)
                  ? h.customerLinkKind
                  : 'customer',
        ),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: Text('add_delivery_stop'.tr()),
      content: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // Mode Toggle
            SegmentedButton<bool>(
              segments: [
                ButtonSegment(
                  value: false,
                  label: Text('select_from_list'.tr()),
                ),
                ButtonSegment(
                  value: true,
                  label: Text('add_custom'.tr()),
                ),
              ],
              selected: {_isCustomMode},
              onSelectionChanged: (Set<bool> selected) {
                setState(() => _isCustomMode = selected.first);
              },
            ),
            const SizedBox(height: 16),

            // Mode Content
            if (!_isCustomMode) ...[
              Text(
                'select_destination'.tr(),
                style: Theme.of(context).textTheme.labelMedium,
              ),
              const SizedBox(height: 8),
              TextField(
                controller: _searchController,
                decoration: InputDecoration(
                  labelText: 'loading_phase_hub_search_hint'.tr(),
                  prefixIcon: const Icon(Icons.search),
                  border: const OutlineInputBorder(),
                  contentPadding: const EdgeInsets.symmetric(
                    vertical: 8,
                    horizontal: 12,
                  ),
                ),
                onChanged: (value) => setState(() => _searchQuery = value),
              ),
              const SizedBox(height: 8),
              ConstrainedBox(
                constraints: const BoxConstraints(maxHeight: 300),
                child: Builder(
                  builder: (context) {
                    final filtered = _hubs
                        .where((hub) =>
                            hub.sourceNameEn
                                .toLowerCase()
                                .contains(_searchQuery.toLowerCase()) ||
                            hub.sourceId
                                .toLowerCase()
                                .contains(_searchQuery.toLowerCase()))
                        .toList();

                    if (filtered.isEmpty) {
                      return Center(
                        child: Padding(
                          padding: const EdgeInsets.all(16.0),
                          child: Text(
                            'No results found',
                            style: Theme.of(context).textTheme.bodySmall,
                          ),
                        ),
                      );
                    }

                    return ListView.builder(
                      shrinkWrap: true,
                      itemCount: filtered.length,
                      itemBuilder: (context, index) {
                        final hub = filtered[index];
                        final isSelected = _selectedHub?.id == hub.id;

                        return Card(
                          margin: const EdgeInsets.only(bottom: 8),
                          child: ListTile(
                            selected: isSelected,
                            selectedTileColor: Colors.blue.shade50,
                            title: Text(
                              hub.codeWithName,
                              style: TextStyle(
                                fontWeight: isSelected ? FontWeight.w600 : FontWeight.w500,
                              ),
                            ),
                            trailing: isSelected
                                ? const Icon(Icons.check_circle,
                                    color: Colors.blue)
                                : null,
                            onTap: () => setState(() => _selectedHub = hub),
                          ),
                        );
                      },
                    );
                  },
                ),
              ),
            ] else ...[
              TextField(
                controller: _customCodeController,
                decoration: InputDecoration(
                  labelText: 'destination_code'.tr(),
                  hintText: 'e.g. SPK001',
                  border: const OutlineInputBorder(),
                ),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _customNameController,
                decoration: InputDecoration(
                  labelText: 'destination_name'.tr(),
                  hintText: 'e.g. Bangkok Hub',
                  border: const OutlineInputBorder(),
                ),
              ),
              const SizedBox(height: 8),
              Container(
                padding: const EdgeInsets.all(12),
                decoration: BoxDecoration(
                  color: Colors.orange.shade50,
                  border: Border.all(color: Colors.orange.shade300),
                  borderRadius: BorderRadius.circular(8),
                ),
                child: Text(
                  'custom_stop_note'.tr(),
                  style: TextStyle(
                    fontSize: 12,
                    color: Colors.orange.shade700,
                  ),
                ),
              ),
            ],
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: Text('cancel'.tr()),
        ),
        ElevatedButton(
          onPressed: _canSubmit ? _submitAddStop : null,
          child: Text('add'.tr()),
        ),
      ],
    );
  }
}
