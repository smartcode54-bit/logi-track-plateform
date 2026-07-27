import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/material.dart';

import '../../../../core/services/cloud_functions_service.dart';

/// Full broadcast message after tapping read on [BroadcastListPage].
class BroadcastDetailPage extends StatefulWidget {
  const BroadcastDetailPage({
    super.key,
    this.broadcastId,
    this.headline,
    required this.messageText,
    this.senderName,
    this.dateStr,
  });

  /// Firestore document id under `broadcasts/{id}`.
  final String? broadcastId;
  final String? headline;
  final String messageText;
  final String? senderName;
  final String? dateStr;

  @override
  State<BroadcastDetailPage> createState() => _BroadcastDetailPageState();
}

class _BroadcastDetailPageState extends State<BroadcastDetailPage> {
  @override
  void initState() {
    super.initState();
    final id = widget.broadcastId?.trim();
    if (id == null || id.isEmpty) return;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _reportRead(id);
    });
  }

  Future<void> _reportRead(String id) async {
    try {
      await CloudFunctionsService.instance.call<Map<String, dynamic>>(
        'markBroadcastRead',
        data: {'broadcastId': id},
      );
    } catch (e, st) {
      debugPrint('markBroadcastRead failed: $e\n$st');
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final messageText = widget.messageText;
    final body = messageText.trim().isEmpty
        ? Text(
            'broadcast_empty_body'.tr(),
            style: theme.textTheme.bodyLarge?.copyWith(
              color: theme.colorScheme.onSurfaceVariant,
            ),
          )
        : SelectableText(
            messageText,
            style: theme.textTheme.bodyLarge,
          );

    return Scaffold(
      appBar: AppBar(
        title: Text('broadcast_detail_title'.tr()),
      ),
      body: SingleChildScrollView(
        padding: EdgeInsets.fromLTRB(
          20,
          20,
          20,
          20 + MediaQuery.of(context).viewPadding.bottom,
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            if (widget.senderName != null && widget.senderName!.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(bottom: 8),
                child: Row(
                  children: [
                    Icon(
                      Icons.person_outline,
                      size: 18,
                      color: theme.colorScheme.primary,
                    ),
                    const SizedBox(width: 8),
                    Expanded(
                      child: Text(
                        widget.senderName!,
                        style: theme.textTheme.titleSmall?.copyWith(
                          color: theme.colorScheme.primary,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                    ),
                  ],
                ),
              ),
            if (widget.dateStr != null && widget.dateStr!.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(bottom: 16),
                child: Row(
                  children: [
                    Icon(
                      Icons.schedule,
                      size: 18,
                      color: theme.colorScheme.onSurfaceVariant,
                    ),
                    const SizedBox(width: 8),
                    Text(
                      widget.dateStr!,
                      style: theme.textTheme.bodySmall?.copyWith(
                        color: theme.colorScheme.onSurfaceVariant,
                      ),
                    ),
                  ],
                ),
              ),
            if (widget.headline != null && widget.headline!.trim().isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(bottom: 12),
                child: Text(
                  widget.headline!.trim(),
                  style: theme.textTheme.titleLarge?.copyWith(
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ),
            body,
          ],
        ),
      ),
    );
  }
}
