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

class _BroadcastListPageState extends State<BroadcastListPage> {
  bool _markedAsRead = false;

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
              final messageText = d['messageText'] as String? ?? d['body'] as String? ?? d['text'] as String? ?? '';
              final sentAt = d['sentAt'] as Timestamp? ?? d['createdAt'] as Timestamp?;
              final dateStr = sentAt != null
                  ? DateFormat('dd/MM/yyyy HH:mm').format(sentAt.toDate())
                  : '';
              final senderName = d['createdByName'] as String? ?? '';
              return Card(
                margin: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
                child: ListTile(
                  leading: CircleAvatar(
                    backgroundColor: Theme.of(context).colorScheme.primary,
                    child: const Icon(Icons.campaign, color: Colors.white),
                  ),
                  title: Text(
                    messageText.isNotEmpty
                        ? (messageText.length > 60 ? '${messageText.substring(0, 60)}…' : messageText)
                        : 'broadcast_title'.tr(),
                    style: const TextStyle(fontWeight: FontWeight.w600),
                  ),
                  subtitle: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      if (messageText.length > 60)
                        Padding(
                          padding: const EdgeInsets.only(top: 4),
                          child: Text(
                            messageText,
                            maxLines: 3,
                            overflow: TextOverflow.ellipsis,
                          ),
                        ),
                      Padding(
                        padding: const EdgeInsets.only(top: 6),
                        child: Row(
                          children: [
                            if (senderName.isNotEmpty)
                              Text(
                                senderName,
                                style: TextStyle(
                                  fontSize: 12,
                                  color: Theme.of(context).colorScheme.primary,
                                ),
                              ),
                            if (senderName.isNotEmpty && dateStr.isNotEmpty) const SizedBox(width: 8),
                            if (dateStr.isNotEmpty)
                              Text(
                                dateStr,
                                style: TextStyle(
                                  fontSize: 12,
                                  color: Theme.of(context).colorScheme.onSurfaceVariant,
                                ),
                              ),
                          ],
                        ),
                      ),
                    ],
                  ),
                  isThreeLine: true,
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
