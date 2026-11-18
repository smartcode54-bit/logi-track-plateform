import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/material.dart';

import 'go_badge.dart';

/// What a feature not yet on the Go API shows in the gotest app (spec mobile-go-core-loop R16).
class GoUnavailablePage extends StatelessWidget {
  const GoUnavailablePage({super.key, required this.titleKey});

  /// i18n key of the feature's title.
  final String titleKey;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: goAppBar(titleKey.tr()),
      body: const GoUnavailableNotice(),
    );
  }
}

/// The notice itself, for a tab or a section.
class GoUnavailableNotice extends StatelessWidget {
  const GoUnavailableNotice({super.key});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(mainAxisSize: MainAxisSize.min, children: [
          Icon(Icons.construction, size: 56, color: theme.colorScheme.outline),
          const SizedBox(height: 16),
          Text('gotest_not_available_title'.tr(), style: theme.textTheme.titleMedium, textAlign: TextAlign.center),
          const SizedBox(height: 8),
          Text('gotest_not_available_body'.tr(), textAlign: TextAlign.center),
        ]),
      ),
    );
  }
}
