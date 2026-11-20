import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/material.dart';
import 'package:flutter_bloc/flutter_bloc.dart';

import '../core/error_text.dart';
import 'evidence_form_bloc.dart';
import 'photo_capture.dart';

/// The photo slots of an evidence form: tap to take (or pick, for a screenshot) a photo; a filled slot shows it.
class EvidenceSlots extends StatelessWidget {
  const EvidenceSlots({super.key, required this.capture});
  final PhotoCapture capture;

  @override
  Widget build(BuildContext context) {
    final bloc = context.read<EvidenceFormBloc>();
    return BlocBuilder<EvidenceFormBloc, EvidenceState>(
      buildWhen: (a, b) => a.photos != b.photos || a.busy != b.busy,
      builder: (context, state) => Wrap(
        spacing: 12,
        runSpacing: 12,
        children: [
          for (final s in bloc.slots)
            _SlotTile(
              slot: s,
              photo: state.photos[s.id],
              enabled: !state.busy,
              onTap: () async {
                final p = s.screenshot ? await capture.screenshot() : await capture.camera();
                if (p != null) bloc.add(EvidencePhotoSet(s.id, p));
              },
            ),
        ],
      ),
    );
  }
}

class _SlotTile extends StatelessWidget {
  const _SlotTile({required this.slot, required this.photo, required this.enabled, required this.onTap});
  final EvidenceSlot slot;
  final CapturedPhoto? photo;
  final bool enabled;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final cs = Theme.of(context).colorScheme;
    final label = slot.labelKey.tr() + (slot.required ? ' *' : '');
    return InkWell(
      onTap: enabled ? onTap : null,
      borderRadius: BorderRadius.circular(8),
      child: Container(
        width: 150,
        height: 150,
        decoration: BoxDecoration(
          border: Border.all(color: photo == null ? cs.outline : cs.primary, width: photo == null ? 1 : 2),
          borderRadius: BorderRadius.circular(8),
        ),
        clipBehavior: Clip.antiAlias,
        child: Stack(fit: StackFit.expand, children: [
          if (photo != null) Image.memory(photo!.bytes, fit: BoxFit.cover),
          if (photo == null)
            Center(child: Icon(slot.screenshot ? Icons.screenshot_outlined : Icons.photo_camera_outlined, size: 36)),
          Align(
            alignment: Alignment.bottomCenter,
            child: Container(
              width: double.infinity,
              color: Colors.black54,
              padding: const EdgeInsets.all(4),
              child: Text(label,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                  textAlign: TextAlign.center,
                  style: const TextStyle(color: Colors.white, fontSize: 12)),
            ),
          ),
        ]),
      ),
    );
  }
}

/// The submit button with the upload progress, the incomplete-form hint and the failure with its retry (R15).
class EvidenceSubmitBar extends StatelessWidget {
  const EvidenceSubmitBar({super.key, required this.labelKey, this.errorOverrides = const {}});
  final String labelKey;
  final Map<String, String> errorOverrides;

  @override
  Widget build(BuildContext context) {
    return BlocBuilder<EvidenceFormBloc, EvidenceState>(builder: (context, state) {
      final cs = Theme.of(context).colorScheme;
      return Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
        if (state.missingKey != null)
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: Text('go_required_missing'.tr(namedArgs: {'item': state.missingKey!.tr()}),
                style: TextStyle(color: cs.error)),
          ),
        if (state.error != null)
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: Text(apiErrorText(state.error!, overrides: errorOverrides), style: TextStyle(color: cs.error)),
          ),
        if (state.busy && state.total > 0)
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: Text('upload_progress'.tr(namedArgs: {'done': '${state.done}', 'total': '${state.total}'})),
          ),
        FilledButton(
          onPressed: state.busy ? null : () => context.read<EvidenceFormBloc>().add(EvidenceSubmitted()),
          child: state.busy
              ? const SizedBox(height: 20, width: 20, child: CircularProgressIndicator(strokeWidth: 2))
              : Text((state.error != null ? 'retry' : labelKey).tr()),
        ),
      ]);
    });
  }
}
