import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/material.dart';
import '../../data/repositories/broadcast_repository.dart';
import '../../data/services/broadcast_read_tracker.dart';

class BroadcastListPage extends StatefulWidget {
  const BroadcastListPage({super.key});

  @override
  State<BroadcastListPage> createState() => _BroadcastListPageState();
}

String _displayBroadcastSubject(String storedTitle) {
  final t = storedTitle.trim();
  if (t.isNotEmpty) return t;
  return 'broadcast_no_subject'.tr();
}

class _BroadcastListPageState extends State<BroadcastListPage> {
  bool _markedAsRead = false;

  void _openDetail(
    BuildContext context, {
    required String broadcastId,
    required String headline,
    required String messageText,
    required String senderName,
    required String dateStr,
  }) {
    Navigator.pushNamed(
      context,
      '/broadcast-detail',
      arguments: <String, dynamic>{
        'broadcastId': broadcastId,
        if (headline.isNotEmpty) 'headline': headline,
        'messageText': messageText,
        if (senderName.isNotEmpty) 'senderName': senderName,
        if (dateStr.isNotEmpty) 'dateStr': dateStr,
      },
    );
  }

  void _markAsReadIfNeeded(QuerySnapshot<Map<String, dynamic>>? snap) {
    if (_markedAsRead || snap == null || snap.docs.isEmpty) return;
    _markedAsRead = true;
    final sentAt = snap.docs.first.data()['sentAt'] as Timestamp?;
    saveLastReadBroadcast(sentAt);
  }

  @override
  Widget build(BuildContext context) {
    final repo = BroadcastRepository();
    return Scaffold(
      appBar: AppBar(
        title: Text('broadcast_title'.tr()),
      ),
      body: RefreshIndicator(
        onRefresh: () async {
          await Future.delayed(const Duration(milliseconds: 400));
        },
        child: StreamBuilder<QuerySnapshot<Map<String, dynamic>>>(
          stream: repo.watchBroadcasts(),
          builder: (context, snapshot) {
            if (snapshot.hasError) {
              return ListView(
                physics: const AlwaysScrollableScrollPhysics(),
                children: [
                  SizedBox(
                    height: MediaQuery.of(context).size.height * 0.6,
                    child: Center(child: Text('Error: ${snapshot.error}')),
                  ),
                ],
              );
            }
            if (!snapshot.hasData) {
              return const Center(child: CircularProgressIndicator());
            }
            final docs = snapshot.data!.docs;
            _markAsReadIfNeeded(snapshot.data);
            if (docs.isEmpty) {
              return ListView(
                physics: const AlwaysScrollableScrollPhysics(),
                children: [
                  SizedBox(
                    height: MediaQuery.of(context).size.height * 0.6,
                    child: Center(
                      child: Padding(
                        padding: const EdgeInsets.all(24.0),
                        child: Column(
                          mainAxisAlignment: MainAxisAlignment.center,
                          children: [
                            Icon(
                              Icons.campaign_outlined,
                              size: 64,
                              color: Theme.of(context).colorScheme.primary.withOpacity(0.5),
                            ),
                            const SizedBox(height: 16),
                            Text(
                              'broadcast_no_items'.tr(),
                              textAlign: TextAlign.center,
                              style: Theme.of(context).textTheme.bodyLarge,
                            ),
                          ],
                        ),
                      ),
                    ),
                  ),
                ],
              );
            }
            return ListView.builder(
              physics: const AlwaysScrollableScrollPhysics(),
            padding: const EdgeInsets.symmetric(vertical: 8),
            itemCount: docs.length,
            itemBuilder: (context, index) {
              final doc = docs[index];
              final d = doc.data();
              final headline = (d['title'] as String?)?.trim() ?? '';
              final messageText = d['messageText'] as String? ?? d['body'] as String? ?? d['text'] as String? ?? '';
              final sentAt = d['sentAt'] as Timestamp? ?? d['createdAt'] as Timestamp?;
              final dateStr = sentAt != null
                  ? DateFormat('dd/MM/yyyy HH:mm').format(sentAt.toDate())
                  : '';
              final senderName = d['createdByName'] as String? ?? '';
              final subjectLine = _displayBroadcastSubject(headline);
              return Card(
                margin: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
                child: Padding(
                  padding: const EdgeInsets.all(12),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      Row(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          CircleAvatar(
                            backgroundColor:
                                Theme.of(context).colorScheme.primary,
                            child: const Icon(Icons.campaign, color: Colors.white),
                          ),
                          const SizedBox(width: 12),
                          Expanded(
                            child: Text(
                              subjectLine,
                              maxLines: 4,
                              overflow: TextOverflow.ellipsis,
                              style: const TextStyle(
                                fontWeight: FontWeight.w600,
                                fontSize: 16,
                              ),
                            ),
                          ),
                        ],
                      ),
                      if (senderName.isNotEmpty || dateStr.isNotEmpty)
                        Padding(
                          padding: const EdgeInsets.only(left: 52, top: 8),
                          child: Row(
                            children: [
                              if (senderName.isNotEmpty)
                                Flexible(
                                  child: Text(
                                    senderName,
                                    maxLines: 1,
                                    overflow: TextOverflow.ellipsis,
                                    style: TextStyle(
                                      fontSize: 12,
                                      color:
                                          Theme.of(context).colorScheme.primary,
                                    ),
                                  ),
                                ),
                              if (senderName.isNotEmpty && dateStr.isNotEmpty)
                                const SizedBox(width: 8),
                              if (dateStr.isNotEmpty)
                                Text(
                                  dateStr,
                                  style: TextStyle(
                                    fontSize: 12,
                                    color: Theme.of(context)
                                        .colorScheme
                                        .onSurfaceVariant,
                                  ),
                                ),
                            ],
                          ),
                        ),
                      const SizedBox(height: 10),
                      OutlinedButton(
                        onPressed: () => _openDetail(
                          context,
                          broadcastId: doc.id,
                          headline: headline,
                          messageText: messageText,
                          senderName: senderName,
                          dateStr: dateStr,
                        ),
                        child: Text('broadcast_read_button'.tr()),
                      ),
                    ],
                  ),
                ),
              );
            },
          );
        },
      ),
    ),
    );
  }
}
