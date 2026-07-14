import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/material.dart';

import '../features/home/data/repositories/trucks_repository.dart';

/// Lets the driver confirm — or correct — the truck they are taking for this job.
///
/// The vehicle belongs to the task, not to the driver: an admin picks it at assign time and the
/// driver is responsible for that truck for that job only. Whatever is confirmed here is what
/// lands on the trip record and on the driver's fuel/expense entries.
class TruckPickerField extends StatelessWidget {
  const TruckPickerField({
    super.key,
    required this.trucks,
    required this.selected,
    required this.onChanged,
    this.loading = false,
    this.enabled = true,
  });

  final List<SelectableTruck> trucks;
  final SelectableTruck? selected;
  final ValueChanged<SelectableTruck> onChanged;
  final bool loading;
  final bool enabled;

  Future<void> _openPicker(BuildContext context) async {
    final picked = await showModalBottomSheet<SelectableTruck>(
      context: context,
      isScrollControlled: true,
      builder: (_) => _TruckPickerSheet(trucks: trucks, selected: selected),
    );
    if (picked != null) onChanged(picked);
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final missing = selected == null;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          'truck_confirm_label'.tr(),
          style: theme.textTheme.labelLarge,
        ),
        const SizedBox(height: 6),
        InkWell(
          onTap: (enabled && !loading && trucks.isNotEmpty)
              ? () => _openPicker(context)
              : null,
          borderRadius: BorderRadius.circular(8),
          child: InputDecorator(
            decoration: InputDecoration(
              border: const OutlineInputBorder(),
              enabled: enabled,
              errorText: missing && !loading ? 'truck_required'.tr() : null,
              prefixIcon: const Icon(Icons.local_shipping_outlined),
              suffixIcon: loading
                  ? const Padding(
                      padding: EdgeInsets.all(12),
                      child: SizedBox(
                        width: 16,
                        height: 16,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      ),
                    )
                  : const Icon(Icons.arrow_drop_down),
            ),
            child: Text(
              selected == null
                  ? (trucks.isEmpty && !loading
                      ? 'truck_none_available'.tr()
                      : 'truck_select'.tr())
                  : '${selected!.licensePlate}  ·  ${selected!.taskTruckType ?? selected!.docType}',
              style: selected == null
                  ? theme.textTheme.bodyMedium
                      ?.copyWith(color: theme.hintColor)
                  : theme.textTheme.bodyMedium
                      ?.copyWith(fontWeight: FontWeight.w600),
            ),
          ),
        ),
      ],
    );
  }
}

class _TruckPickerSheet extends StatefulWidget {
  const _TruckPickerSheet({required this.trucks, this.selected});

  final List<SelectableTruck> trucks;
  final SelectableTruck? selected;

  @override
  State<_TruckPickerSheet> createState() => _TruckPickerSheetState();
}

class _TruckPickerSheetState extends State<_TruckPickerSheet> {
  String _query = '';

  @override
  Widget build(BuildContext context) {
    final term = _query.trim().toLowerCase();
    final visible = widget.trucks.where((truck) {
      if (term.isEmpty) return true;
      final haystack =
          '${truck.licensePlate} ${truck.docType} ${truck.model ?? ''}'
              .toLowerCase();
      return haystack.contains(term);
    }).toList();

    return Padding(
      padding: EdgeInsets.only(
        bottom: MediaQuery.of(context).viewInsets.bottom,
      ),
      child: SafeArea(
        child: SizedBox(
          height: MediaQuery.of(context).size.height * 0.7,
          child: Column(
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
                child: TextField(
                  autofocus: true,
                  decoration: InputDecoration(
                    prefixIcon: const Icon(Icons.search),
                    hintText: 'truck_search_hint'.tr(),
                    border: const OutlineInputBorder(),
                  ),
                  onChanged: (value) => setState(() => _query = value),
                ),
              ),
              Expanded(
                child: visible.isEmpty
                    ? Center(child: Text('truck_none_found'.tr()))
                    : ListView.builder(
                        itemCount: visible.length,
                        itemBuilder: (_, index) {
                          final truck = visible[index];
                          final isSelected = truck.id == widget.selected?.id;
                          return ListTile(
                            leading: Icon(
                              isSelected
                                  ? Icons.check_circle
                                  : Icons.local_shipping_outlined,
                              color: isSelected
                                  ? Theme.of(context).colorScheme.primary
                                  : null,
                            ),
                            title: Text(
                              truck.licensePlate,
                              style: const TextStyle(
                                fontWeight: FontWeight.w600,
                              ),
                            ),
                            subtitle: Text(
                              [
                                truck.taskTruckType ?? truck.docType,
                                truck.model,
                              ].where((v) => v != null && v.isNotEmpty).join(' · '),
                            ),
                            onTap: () => Navigator.pop(context, truck),
                          );
                        },
                      ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
