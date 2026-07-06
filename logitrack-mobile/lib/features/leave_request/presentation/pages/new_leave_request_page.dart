import 'dart:typed_data';

import 'package:easy_localization/easy_localization.dart';
import 'package:firebase_auth/firebase_auth.dart';
import 'package:flutter/material.dart';
import 'package:image_picker/image_picker.dart';
import '../../data/repositories/leave_request_repository.dart';
import 'leave_request_page.dart';

/// Number of leave days to offer in the dropdown (1 to 7).
const _leaveDaysOptions = [1, 2, 3, 4, 5, 6, 7];

class NewLeaveRequestPage extends StatefulWidget {
  const NewLeaveRequestPage({
    super.key,
    required this.driverId,
    this.driverName,
  });

  final String driverId;
  final String? driverName;

  @override
  State<NewLeaveRequestPage> createState() => _NewLeaveRequestPageState();
}

class _NewLeaveRequestPageState extends State<NewLeaveRequestPage> {
  final _repo = LeaveRequestRepository();
  final _reasonController = TextEditingController();

  late String _type;
  late int _leaveDays;
  late DateTime _startDate;
  late DateTime _endDate;
  bool _showReview = false;
  final List<Uint8List> _attachmentBytes = [];
  bool _submitting = false;
  late final PageController _evidencePageController;
  int _reviewEvidencePageIndex = 0;

  @override
  void initState() {
    super.initState();
    _type = 'SICK';
    _leaveDays = 1;
    _startDate = DateTime.now();
    _recalcEndDate();
    _evidencePageController = PageController(initialPage: 0);
  }

  @override
  void dispose() {
    _reasonController.dispose();
    _evidencePageController.dispose();
    super.dispose();
  }

  void _recalcEndDate() {
    _endDate = DateTime(_startDate.year, _startDate.month, _startDate.day)
        .add(Duration(days: _leaveDays - 1));
  }

  void _showAddEvidenceOptions(BuildContext context) {
    showModalBottomSheet<void>(
      context: context,
      builder: (ctx) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              leading: const Icon(Icons.camera_alt),
              title: Text('leave_request_take_photo'.tr()),
              onTap: () {
                Navigator.pop(ctx);
                _pickEvidenceImage(context, true);
              },
            ),
            ListTile(
              leading: const Icon(Icons.attach_file),
              title: Text('leave_request_attach_file'.tr()),
              subtitle: Text('leave_request_choose_gallery'.tr()),
              onTap: () {
                Navigator.pop(ctx);
                _pickEvidenceImage(context, false);
              },
            ),
          ],
        ),
      ),
    );
  }

  Future<void> _pickEvidenceImage(BuildContext context, bool fromCamera) async {
    final source = fromCamera ? ImageSource.camera : ImageSource.gallery;
    final picker = ImagePicker();
    final xfile = await picker.pickImage(source: source, imageQuality: 85);
    if (xfile == null || !mounted) return;
    final bytes = await xfile.readAsBytes();
    if (!mounted) return;
    setState(() => _attachmentBytes.add(bytes));
  }

  Future<void> _submitFromReview() async {
    final reason = _reasonController.text.trim();
    setState(() => _submitting = true);
    List<String> urls = [];
    if (_attachmentBytes.isNotEmpty) {
      final uid = FirebaseAuth.instance.currentUser?.uid;
      if (uid == null) {
        if (mounted) {
          setState(() => _submitting = false);
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(
              content: Text('Please sign in to attach evidence'),
            ),
          );
        }
        return;
      }
      final ts = DateTime.now().millisecondsSinceEpoch;
      try {
        for (var i = 0; i < _attachmentBytes.length; i++) {
          final url = await uploadLeaveEvidence(
            userId: uid,
            index: i,
            timestampMs: ts,
            imageBytes: _attachmentBytes[i],
          );
          urls.add(url);
        }
      } catch (e) {
        if (mounted) {
          setState(() => _submitting = false);
          ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(content: Text('Upload failed: $e')),
          );
        }
        return;
      }
    }
    try {
      await _repo.createLeaveRequest(
        driverId: widget.driverId,
        driverName: widget.driverName,
        type: _type,
        startDate: _startDate,
        endDate: _endDate,
        reason: reason,
        attachmentUrls: urls,
      );
      if (mounted) {
        setState(() => _submitting = false);
        // Blocking confirmation — must be acknowledged before we pop, so a successful
        // submit can never "disappear silently" (this flow previously had no
        // confirmation at all).
        await showDialog<void>(
          context: context,
          barrierDismissible: false,
          builder: (ctx) => AlertDialog(
            icon: const Icon(Icons.check_circle, color: Colors.green, size: 48),
            title: Text('leave_request_submit_success_title'.tr()),
            content: Text('leave_request_submit_success_body'.tr()),
            actions: [
              FilledButton(
                onPressed: () => Navigator.of(ctx).pop(),
                child: Text('loading_phase_submit_success_ok'.tr()),
              ),
            ],
          ),
        );
        if (!mounted) return;
        Navigator.pop(context, true);
      }
    } catch (e) {
      if (mounted) {
        setState(() => _submitting = false);
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Error: $e')),
        );
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text('leave_request_new'.tr()),
      ),
      body: SingleChildScrollView(
        padding: const EdgeInsets.all(24),
        child: _showReview ? _buildReview() : _buildForm(),
      ),
    );
  }

  Widget _buildForm() {
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        DropdownButtonFormField<String>(
          value: _type,
          decoration: InputDecoration(
            labelText: 'leave_request_type'.tr(),
            border: const OutlineInputBorder(),
          ),
          items: leaveTypes
              .map((t) => DropdownMenuItem(
                    value: t,
                    child: Text(LeaveRequestPage.typeLabel(t)),
                  ))
              .toList(),
          onChanged: (v) {
            if (v == null) return;
            setState(() {
              _type = v;
              if (v == 'BUSINESS') {
                final today = DateTime(
                  DateTime.now().year,
                  DateTime.now().month,
                  DateTime.now().day,
                );
                final startDay = DateTime(
                  _startDate.year,
                  _startDate.month,
                  _startDate.day,
                );
                if (startDay.isBefore(today)) {
                  _startDate = today;
                  _recalcEndDate();
                }
              }
            });
          },
        ),
        const SizedBox(height: 12),
        DropdownButtonFormField<int>(
          value: _leaveDays,
          decoration: InputDecoration(
            labelText: 'leave_request_days'.tr(),
            border: const OutlineInputBorder(),
          ),
          items: _leaveDaysOptions
              .map((d) => DropdownMenuItem(
                    value: d,
                    child: Text(
                      d == 1
                          ? 'leave_request_days_count'.tr(args: ['1'])
                          : 'leave_request_days_count_plural'.tr(args: ['$d']),
                    ),
                  ))
              .toList(),
          onChanged: (v) {
            if (v != null) {
              setState(() {
                _leaveDays = v;
                _recalcEndDate();
              });
            }
          },
        ),
        const SizedBox(height: 12),
        ListTile(
          title: Text('leave_request_start_date'.tr()),
          subtitle: Text(
            DateFormat.yMMMd(context.locale.languageCode).format(_startDate),
          ),
          trailing: const Icon(Icons.calendar_today),
          onTap: () async {
            final now = DateTime.now();
            final today = DateTime(now.year, now.month, now.day);
            final firstDate = _type == 'SICK'
                ? today.subtract(const Duration(days: 30))
                : today;
            final lastDate = today.add(const Duration(days: 365));
            final picked = await showDatePicker(
              context: context,
              initialDate: _startDate,
              firstDate: firstDate,
              lastDate: lastDate,
            );
            if (picked != null) {
              setState(() {
                _startDate = picked;
                _recalcEndDate();
              });
            }
          },
        ),
        ListTile(
          title: Text('leave_request_end_date'.tr()),
          subtitle: Text(
            DateFormat.yMMMd(context.locale.languageCode).format(_endDate),
          ),
          trailing: Icon(
            Icons.event_available,
            color: Theme.of(context).colorScheme.primary,
          ),
        ),
        const SizedBox(height: 8),
        TextField(
          controller: _reasonController,
          decoration: InputDecoration(
            labelText: 'leave_request_reason_optional'.tr(),
            border: const OutlineInputBorder(),
            alignLabelWithHint: true,
          ),
          maxLines: 3,
        ),
        const SizedBox(height: 16),
        Text(
          'leave_request_evidence_optional'.tr(),
          style: Theme.of(context).textTheme.titleSmall,
        ),
        const SizedBox(height: 8),
        OutlinedButton.icon(
          onPressed: () => _showAddEvidenceOptions(context),
          icon: const Icon(Icons.add_photo_alternate),
          label: Text('leave_request_add_evidence'.tr()),
        ),
        if (_attachmentBytes.isNotEmpty) ...[
          const SizedBox(height: 8),
          SizedBox(
            height: 72,
            child: ListView.separated(
              scrollDirection: Axis.horizontal,
              itemCount: _attachmentBytes.length,
              separatorBuilder: (_, __) => const SizedBox(width: 8),
              itemBuilder: (context, index) {
                return Stack(
                  alignment: Alignment.topRight,
                  children: [
                    ClipRRect(
                      borderRadius: BorderRadius.circular(8),
                      child: Image.memory(
                        _attachmentBytes[index],
                        width: 72,
                        height: 72,
                        fit: BoxFit.cover,
                      ),
                    ),
                    IconButton(
                      icon: const Icon(Icons.close, size: 20),
                      style: IconButton.styleFrom(
                        backgroundColor: Colors.black54,
                        foregroundColor: Colors.white,
                        padding: const EdgeInsets.all(4),
                        minimumSize: const Size(28, 28),
                      ),
                      onPressed: () {
                        setState(() => _attachmentBytes.removeAt(index));
                      },
                    ),
                  ],
                );
              },
            ),
          ),
        ],
        const SizedBox(height: 24),
        FilledButton(
          onPressed: () {
            setState(() {
              _showReview = true;
              _reviewEvidencePageIndex = 0;
            });
            if (_attachmentBytes.isNotEmpty) {
              _evidencePageController.jumpToPage(0);
            }
          },
          child: Text('leave_request_review'.tr()),
        ),
      ],
    );
  }

  Widget _buildReview() {
    final reason = _reasonController.text.trim();
    final locale = context.locale.languageCode;

    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Card(
          child: Padding(
            padding: const EdgeInsets.all(16),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                _reviewRow(
                  'leave_request_type'.tr(),
                  LeaveRequestPage.typeLabel(_type),
                ),
                _reviewRow(
                  'leave_request_days'.tr(),
                  _leaveDays == 1
                      ? 'leave_request_days_count'.tr(args: ['1'])
                      : 'leave_request_days_count_plural'.tr(args: ['$_leaveDays']),
                ),
                _reviewRow(
                  'leave_request_start_date'.tr(),
                  DateFormat.yMMMd(locale).format(_startDate),
                ),
                _reviewRow(
                  'leave_request_end_date'.tr(),
                  DateFormat.yMMMd(locale).format(_endDate),
                ),
                _reviewRow(
                  'leave_request_reason'.tr(),
                  reason.isEmpty ? '—' : reason,
                ),
                _buildReviewEvidenceSection(),
              ],
            ),
          ),
        ),
        const SizedBox(height: 24),
        OutlinedButton.icon(
          onPressed: _submitting
              ? null
              : () => setState(() => _showReview = false),
          icon: const Icon(Icons.edit),
          label: Text('leave_request_back_to_edit'.tr()),
        ),
        const SizedBox(height: 12),
        FilledButton(
          onPressed: _submitting ? null : _submitFromReview,
          child: _submitting
              ? const SizedBox(
                  height: 20,
                  width: 20,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : Text('leave_request_confirm_submit'.tr()),
        ),
      ],
    );
  }

  Widget _buildReviewEvidenceSection() {
    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(
            'leave_request_evidence_optional'.tr(),
            style: Theme.of(context).textTheme.labelMedium?.copyWith(
                  color: Theme.of(context).colorScheme.onSurfaceVariant,
                ),
          ),
          const SizedBox(height: 8),
          if (_attachmentBytes.isEmpty)
            Text(
              '—',
              style: Theme.of(context).textTheme.bodyLarge,
            )
          else
            Row(
              children: [
                IconButton(
                  onPressed: _reviewEvidencePageIndex > 0
                      ? () {
                          setState(() => _reviewEvidencePageIndex--);
                          _evidencePageController.previousPage(
                            duration: const Duration(milliseconds: 250),
                            curve: Curves.easeInOut,
                          );
                        }
                      : null,
                  icon: const Icon(Icons.chevron_left),
                ),
                Expanded(
                  child: SizedBox(
                    height: 100,
                    child: PageView.builder(
                      controller: _evidencePageController,
                      itemCount: _attachmentBytes.length,
                      onPageChanged: (i) =>
                          setState(() => _reviewEvidencePageIndex = i),
                      itemBuilder: (context, index) {
                        return GestureDetector(
                          onTap: () => _showEvidenceViewer(initialIndex: index),
                          child: Padding(
                            padding: const EdgeInsets.symmetric(horizontal: 4),
                            child: ClipRRect(
                              borderRadius: BorderRadius.circular(8),
                              child: Image.memory(
                                _attachmentBytes[index],
                                fit: BoxFit.cover,
                              ),
                            ),
                          ),
                        );
                      },
                    ),
                  ),
                ),
                IconButton(
                  onPressed:
                      _reviewEvidencePageIndex < _attachmentBytes.length - 1
                          ? () {
                              setState(() => _reviewEvidencePageIndex++);
                              _evidencePageController.nextPage(
                                duration: const Duration(milliseconds: 250),
                                curve: Curves.easeInOut,
                              );
                            }
                          : null,
                  icon: const Icon(Icons.chevron_right),
                ),
              ],
            ),
        ],
      ),
    );
  }

  void _showEvidenceViewer({required int initialIndex}) {
    showDialog<void>(
      context: context,
      builder: (context) => _EvidenceFullScreenViewer(
        images: _attachmentBytes,
        initialIndex: initialIndex,
        onClose: () => Navigator.of(context).pop(),
      ),
    );
  }

  Widget _reviewRow(String label, String value) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(
            label,
            style: Theme.of(context).textTheme.labelMedium?.copyWith(
                  color: Theme.of(context).colorScheme.onSurfaceVariant,
                ),
          ),
          const SizedBox(height: 2),
          Text(
            value,
            style: Theme.of(context).textTheme.bodyLarge,
          ),
        ],
      ),
    );
  }
}

class _EvidenceFullScreenViewer extends StatefulWidget {
  const _EvidenceFullScreenViewer({
    required this.images,
    required this.initialIndex,
    required this.onClose,
  });

  final List<Uint8List> images;
  final int initialIndex;
  final VoidCallback onClose;

  @override
  State<_EvidenceFullScreenViewer> createState() =>
      _EvidenceFullScreenViewerState();
}

class _EvidenceFullScreenViewerState extends State<_EvidenceFullScreenViewer> {
  late PageController _pageController;
  late int _currentIndex;

  @override
  void initState() {
    super.initState();
    _pageController = PageController(initialPage: widget.initialIndex);
    _currentIndex = widget.initialIndex;
  }

  @override
  void dispose() {
    _pageController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final hasMultiple = widget.images.length > 1;
    return Dialog(
      backgroundColor: Colors.black,
      insetPadding: EdgeInsets.zero,
      child: Stack(
        fit: StackFit.expand,
        children: [
          PageView.builder(
            controller: _pageController,
            itemCount: widget.images.length,
            onPageChanged: (i) => setState(() => _currentIndex = i),
            itemBuilder: (context, index) => InteractiveViewer(
              minScale: 0.5,
              maxScale: 4,
              child: Image.memory(
                widget.images[index],
                fit: BoxFit.contain,
              ),
            ),
          ),
          Positioned(
            top: MediaQuery.of(context).padding.top + 8,
            right: 8,
            child: IconButton(
              onPressed: widget.onClose,
              icon: const Icon(Icons.close, color: Colors.white, size: 28),
              style: IconButton.styleFrom(
                backgroundColor: Colors.black54,
              ),
            ),
          ),
          if (hasMultiple) ...[
            Positioned(
              left: 8,
              top: 0,
              bottom: 0,
              child: Center(
                child: IconButton(
                  onPressed: _currentIndex > 0
                      ? () {
                          setState(() => _currentIndex--);
                          _pageController.previousPage(
                            duration: const Duration(milliseconds: 250),
                            curve: Curves.easeInOut,
                          );
                        }
                      : null,
                  icon: Icon(
                    Icons.chevron_left,
                    color: _currentIndex > 0 ? Colors.white : Colors.white38,
                    size: 40,
                  ),
                ),
              ),
            ),
            Positioned(
              right: 48,
              top: 0,
              bottom: 0,
              child: Center(
                child: IconButton(
                  onPressed: _currentIndex < widget.images.length - 1
                      ? () {
                          setState(() => _currentIndex++);
                          _pageController.nextPage(
                            duration: const Duration(milliseconds: 250),
                            curve: Curves.easeInOut,
                          );
                        }
                      : null,
                  icon: Icon(
                    Icons.chevron_right,
                    color: _currentIndex < widget.images.length - 1
                        ? Colors.white
                        : Colors.white38,
                    size: 40,
                  ),
                ),
              ),
            ),
            Positioned(
              bottom: MediaQuery.of(context).padding.bottom + 16,
              left: 0,
              right: 0,
              child: Center(
                child: Text(
                  '${_currentIndex + 1} / ${widget.images.length}',
                  style: const TextStyle(
                    color: Colors.white70,
                    fontSize: 14,
                  ),
                ),
              ),
            ),
          ],
        ],
      ),
    );
  }
}
