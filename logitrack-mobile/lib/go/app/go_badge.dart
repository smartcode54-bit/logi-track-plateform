import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/material.dart';

/// The GO-TEST tag (spec mobile-go-core-loop R17): on the login page and every app bar, so a test build is never
/// mistaken for the fleet's app.
class GoBadge extends StatelessWidget {
  const GoBadge({super.key});

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: const EdgeInsets.symmetric(horizontal: 8),
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(color: Colors.deepOrange, borderRadius: BorderRadius.circular(6)),
      child: Text(
        'gotest_badge'.tr(),
        style: const TextStyle(color: Colors.white, fontWeight: FontWeight.bold, fontSize: 12),
      ),
    );
  }
}

/// An app bar with the GO-TEST tag.
AppBar goAppBar(String title, {List<Widget> actions = const []}) => AppBar(
      title: Row(children: [Flexible(child: Text(title, overflow: TextOverflow.ellipsis)), const GoBadge()]),
      actions: actions,
    );
