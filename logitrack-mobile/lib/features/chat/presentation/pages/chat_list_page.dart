import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:easy_localization/easy_localization.dart';
import 'package:firebase_auth/firebase_auth.dart';
import 'package:flutter/material.dart';
import '../../data/repositories/chat_repository.dart';

class ChatListPage extends StatelessWidget {
  const ChatListPage({super.key});

  Future<void> _startNewChat(BuildContext context, String driverUid) async {
    final repo = ChatRepository();
    try {
      final chatId = await repo.createChatForDriver(driverUid);
      if (!context.mounted) return;
      Navigator.of(context).pushNamed('/chat-room', arguments: chatId);
    } catch (e) {
      if (!context.mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('${'chat_failed_send'.tr()}: $e')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final user = FirebaseAuth.instance.currentUser;
    if (user == null) {
      return Scaffold(
        body: Center(child: Text('chat_please_sign_in'.tr())),
      );
    }

    final repo = ChatRepository();
    return Scaffold(
      appBar: AppBar(
        title: Text('chat_title'.tr()),
      ),
      body: StreamBuilder<QuerySnapshot<Map<String, dynamic>>>(
        stream: repo.watchChatsForDriver(user.uid),
        builder: (context, snapshot) {
          if (snapshot.hasError) {
            return Center(child: Text('Error: ${snapshot.error}'));
          }
          if (!snapshot.hasData) {
            return const Center(child: CircularProgressIndicator());
          }
          final docs = snapshot.data!.docs;
          final openChats = docs.where((d) => d.data()['status'] != 'closed').toList();
          if (openChats.isEmpty) {
            return Center(
              child: Padding(
                padding: const EdgeInsets.all(24.0),
                child: Column(
                  mainAxisAlignment: MainAxisAlignment.center,
                  children: [
                    Text('chat_no_chats'.tr(), textAlign: TextAlign.center),
                    const SizedBox(height: 16),
                    FilledButton.icon(
                      onPressed: () => _startNewChat(context, user.uid),
                      icon: const Icon(Icons.chat_bubble_outline),
                      label: Text('chat_start_conversation'.tr()),
                    ),
                  ],
                ),
              ),
            );
          }
          return ListView.builder(
            itemCount: openChats.length,
            itemBuilder: (context, index) {
              final doc = openChats[index];
              final d = doc.data();
              final lastMessage = d['lastMessage'] as String? ?? '';
              final lastAt = d['lastMessageAt'] as Timestamp?;
              final priority = d['priority'] as String? ?? 'normal';
              return ListTile(
                leading: const CircleAvatar(
                  child: Icon(Icons.support_agent),
                ),
                title: Text(
                  'chat_support_label'.tr(),
                  style: const TextStyle(fontWeight: FontWeight.w600),
                ),
                subtitle: Text(
                  lastMessage.isEmpty ? 'chat_no_messages'.tr() : lastMessage,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
                trailing: priority == 'urgent'
                    ? Chip(
                        label: Text('chat_urgent'.tr(), style: const TextStyle(fontSize: 10)),
                        backgroundColor: Colors.red,
                        padding: EdgeInsets.symmetric(horizontal: 6, vertical: 0),
                      )
                    : null,
                onTap: () {
                  Navigator.of(context).pushNamed(
                    '/chat-room',
                    arguments: doc.id,
                  );
                },
              );
            },
          );
        },
      ),
    );
  }
}
