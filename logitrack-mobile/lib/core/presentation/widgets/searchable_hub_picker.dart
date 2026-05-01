import 'package:flutter/material.dart';
import 'package:easy_localization/easy_localization.dart';
import '../../../features/home/data/repositories/hubs_repository.dart';

class SearchableHubPicker extends StatelessWidget {
  final String label;
  final String hintText;
  final String value;
  final List<HubDoc> hubs;
  final ValueChanged<HubDoc> onSelected;
  /// ถ้า true แสดงปุ่ม "เพิ่มจุดรับส่งใหม่" — driver กรอกรหัส+ชื่อแล้วบันทึก Firestore ได้
  final bool allowAddNew;
  /// HUB หรือ SOC — ใช้เมื่อ allowAddNew ในการสร้างจุดใหม่
  final String stationTypeForNew;
  /// เรียกเมื่อเพิ่ม Hub/SOC ใหม่สำเร็จ — ใช้ใส่ hub เข้า list (เช่น _allHubs) เพื่อให้ save ได้
  final ValueChanged<HubDoc>? onHubAdded;

  const SearchableHubPicker({
    super.key,
    required this.label,
    required this.hintText,
    required this.value,
    required this.hubs,
    required this.onSelected,
    this.allowAddNew = false,
    this.stationTypeForNew = stationTypeHub,
    this.onHubAdded,
  });

  static bool _matchHub(HubDoc hub, String query) {
    if (query.isEmpty) return true;
    final q = query.trim().toLowerCase();
    return hub.sourceNameEn.toLowerCase().contains(q) ||
        hub.sourceNameTh.toLowerCase().contains(q) ||
        hub.sourceId.toLowerCase().contains(q);
  }

  static String _displayName(HubDoc hub) {
    final th = hub.sourceNameTh.trim();
    if (th.isNotEmpty) return th;
    final en = hub.sourceNameEn.trim();
    if (en.isNotEmpty) return en;
    return hub.sourceId;
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
        allowAddNew: allowAddNew,
        stationTypeForNew: stationTypeForNew,
        onHubAdded: onHubAdded,
      ),
    );
    if (selected != null) onSelected(selected);
  }

  @override
  Widget build(BuildContext context) {
    HubDoc? selectedHub;
    if (value.isNotEmpty) {
      for (final hub in hubs) {
        if (hub.sourceId == value ||
            hub.sourceNameEn == value ||
            hub.sourceNameTh == value) {
          selectedHub = hub;
          break;
        }
      }
    }
    final displayValue = value.isEmpty
        ? ''
        : (selectedHub != null ? _displayName(selectedHub) : value);

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
          displayValue,
          style: TextStyle(
            color: displayValue.isEmpty
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
  final bool allowAddNew;
  final String stationTypeForNew;
  final ValueChanged<HubDoc>? onHubAdded;

  const SearchableHubSheet({
    super.key,
    required this.hubs,
    required this.initialValue,
    required this.onSelected,
    this.allowAddNew = false,
    this.stationTypeForNew = stationTypeHub,
    this.onHubAdded,
  });

  @override
  State<SearchableHubSheet> createState() => _SearchableHubSheetState();
}

class AddHubBottomSheet extends StatefulWidget {
  final String stationType;

  const AddHubBottomSheet({super.key, required this.stationType});

  @override
  State<AddHubBottomSheet> createState() => _AddHubBottomSheetState();
}

class _AddHubBottomSheetState extends State<AddHubBottomSheet> {
  final _formKey = GlobalKey<FormState>();
  final _codeController = TextEditingController();
  final _nameController = TextEditingController();
  bool _saving = false;

  @override
  void dispose() {
    _codeController.dispose();
    _nameController.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    if (!_formKey.currentState!.validate()) return;
    setState(() => _saving = true);
    try {
      final hub = await addHubByDriver(
        sourceId: _codeController.text.trim(),
        sourceName: _nameController.text.trim(),
        stationType: widget.stationType,
      );
      if (mounted) Navigator.of(context).pop(hub);
    } catch (e) {
      if (mounted) {
        final msg = e.toString().contains('HUB_DUPLICATE_CODE')
            ? 'hub_add_duplicate_code'.tr()
            : 'hub_add_failed'.tr(args: [e.toString()]);
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(msg), backgroundColor: Colors.red),
        );
      }
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final isHub = widget.stationType == stationTypeHub;
    return Padding(
      padding: EdgeInsets.only(bottom: MediaQuery.of(context).viewInsets.bottom),
      child: SafeArea(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Form(
            key: _formKey,
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Text(
                  isHub ? 'hub_add_new_hub'.tr() : 'hub_add_new_soc'.tr(),
                  style: Theme.of(context).textTheme.titleLarge,
                ),
                const SizedBox(height: 16),
                TextFormField(
                  controller: _codeController,
                  decoration: InputDecoration(
                    labelText: 'hub_add_code'.tr(),
                    hintText: 'hub_add_code_hint'.tr(),
                    border: const OutlineInputBorder(),
                  ),
                  textCapitalization: TextCapitalization.characters,
                  validator: (v) => (v ?? '').trim().isEmpty ? 'hub_add_code_required'.tr() : null,
                ),
                const SizedBox(height: 12),
                TextFormField(
                  controller: _nameController,
                  decoration: InputDecoration(
                    labelText: 'hub_add_name'.tr(),
                    hintText: 'hub_add_name_hint'.tr(),
                    border: const OutlineInputBorder(),
                  ),
                  validator: (v) => (v ?? '').trim().isEmpty ? 'hub_add_name_required'.tr() : null,
                ),
                const SizedBox(height: 24),
                Row(
                  children: [
                    Expanded(
                      child: OutlinedButton(
                        onPressed: _saving ? null : () => Navigator.of(context).pop(),
                        child: Text('manual_checkin_cancel'.tr()),
                      ),
                    ),
                    const SizedBox(width: 12),
                    Expanded(
                      child: ElevatedButton(
                        onPressed: _saving ? null : _save,
                        child: _saving
                            ? const SizedBox(
                                width: 20,
                                height: 20,
                                child: CircularProgressIndicator(strokeWidth: 2, color: Colors.white),
                              )
                            : Text('checkin_save'.tr()),
                      ),
                    ),
                  ],
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
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

  Future<void> _openAddHub(BuildContext context) async {
    final hub = await showModalBottomSheet<HubDoc>(
      context: context,
      isScrollControlled: true,
      useSafeArea: true,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (ctx) => AddHubBottomSheet(stationType: widget.stationTypeForNew),
    );
    if (hub != null && mounted) {
      widget.onHubAdded?.call(hub);
      // Defer pop เพื่อหลีกเลี่ยง _debugLocked (Navigator ไม่ให้เรียก pop ระหว่าง build/setState)
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (!mounted) return;
        Navigator.of(context).pop(hub);
      });
    }
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
                  final showAddNew = widget.allowAddNew;
                  final itemCount = filtered.length + (showAddNew ? 1 : 0);

                  if (filtered.isEmpty && !showAddNew) {
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
                    itemCount: itemCount,
                    itemBuilder: (context, index) {
                      if (showAddNew && index == filtered.length) {
                        return ListTile(
                          leading: Icon(Icons.add_circle_outline, color: Theme.of(context).colorScheme.primary),
                          title: Text('hub_add_new'.tr()),
                          subtitle: Text('hub_add_new_subtitle'.tr()),
                          onTap: () => _openAddHub(context),
                        );
                      }
                      final hub = filtered[index];
                      final label = SearchableHubPicker._displayName(hub);
                      final isSelected = hub.sourceId == widget.initialValue ||
                          hub.sourceNameEn == widget.initialValue ||
                          hub.sourceNameTh == widget.initialValue;
                      return ListTile(
                        title: Text(label),
                        subtitle: Text(hub.sourceId),
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
