import 'dart:async';
import 'dart:typed_data';
import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:easy_localization/easy_localization.dart';
import 'package:firebase_auth/firebase_auth.dart';
import 'package:flutter/material.dart';
import 'package:image_picker/image_picker.dart';
import '../../data/repositories/chat_repository.dart';
import '../../../../features/home/data/services/image_compression_service.dart';
import '../../../../main.dart' show onChatRoomExited;

class ChatRoomPage extends StatefulWidget {
  const ChatRoomPage({super.key, required this.chatId});

  final String chatId;

  @override
  State<ChatRoomPage> createState() => _ChatRoomPageState();
}

class _ChatRoomPageState extends State<ChatRoomPage> {
  final _repo = ChatRepository();
  final _textController = TextEditingController();
  final _scrollController = ScrollController();
  bool _sending = false;
  bool _sendingImage = false;
  StreamSubscription<QuerySnapshot<Map<String, dynamic>>>? _messagesSubscription;

  @override
  void initState() {
    super.initState();
    _repo.markChatAsReadByDriver(widget.chatId);
    _messagesSubscription = _repo.watchMessages(widget.chatId).listen((snap) {
      final docs = snap.docs;
      if (docs.isNotEmpty) {
        final last = docs.last.data();
        if ((last['senderRole'] as String? ?? '') == 'admin') {
          _repo.markChatAsReadByDriver(widget.chatId);
        }
      }
    });
  }

  @override
  void dispose() {
    _messagesSubscription?.cancel();
    onChatRoomExited?.call();
    _textController.dispose();
    _scrollController.dispose();
    super.dispose();
  }

  Future<void> _sendText() async {
    final text = _textController.text.trim();
    if (text.isEmpty || _sending) return;
    setState(() => _sending = true);
    try {
      await _repo.sendTextMessage(widget.chatId, text);
      await _repo.markChatAsReadByDriver(widget.chatId);
      _textController.clear();
      if (_scrollController.hasClients) {
        _scrollController.animateTo(
          _scrollController.position.maxScrollExtent,
          duration: const Duration(milliseconds: 200),
          curve: Curves.easeOut,
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('${'chat_failed_send'.tr()}: $e')),
        );
      }
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  Future<void> _pickAndSendImage() async {
    if (_sendingImage) return;
    final source = await showModalBottomSheet<ImageSource>(
      context: context,
      builder: (ctx) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              leading: const Icon(Icons.camera_alt),
              title: Text('refuel_receipt_take_photo'.tr()),
              onTap: () => Navigator.pop(ctx, ImageSource.camera),
            ),
            ListTile(
              leading: const Icon(Icons.photo_library),
              title: Text('refuel_receipt_from_gallery'.tr()),
              onTap: () => Navigator.pop(ctx, ImageSource.gallery),
            ),
          ],
        ),
      ),
    );
    if (source == null || !mounted) return;

    final picker = ImagePicker();
    final xFile = await picker.pickImage(source: source);
    if (xFile == null || !mounted) return;
    setState(() => _sendingImage = true);
    try {
      final bytes = await xFile.readAsBytes();
      final imageBytes = Uint8List.fromList(bytes);
      await _repo.sendImageMessage(widget.chatId, imageBytes);
      await _repo.markChatAsReadByDriver(widget.chatId);
      if (_scrollController.hasClients) {
        _scrollController.animateTo(
          _scrollController.position.maxScrollExtent,
          duration: const Duration(milliseconds: 200),
          curve: Curves.easeOut,
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('${'chat_failed_send_image'.tr()}: $e')),
        );
      }
    } finally {
      if (mounted) setState(() => _sendingImage = false);
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

    return Scaffold(
      appBar: AppBar(
        title: Text('chat_room_title'.tr()),
      ),
      body: Column(
        children: [
          Expanded(
            child: StreamBuilder<DocumentSnapshot<Map<String, dynamic>>>(
              stream: _repo.watchChatDoc(widget.chatId),
              builder: (context, chatSnap) {
                if (!chatSnap.hasData) {
                  return const Center(child: CircularProgressIndicator());
                }
                final chatData = chatSnap.data?.data();
                final lastReadByDriverRaw = chatData?['lastReadByDriver'];
                final lastReadByAdminMap = chatData?['lastReadByAdmin'] as Map<String, dynamic>?;

                int? readTimeToMs(dynamic v) {
                  if (v == null) return null;
                  if (v is Timestamp) return v.millisecondsSinceEpoch;
                  if (v is Map) {
                    final sec = v['_seconds'] as int? ?? v['seconds'] as int?;
                    final nanosec = v['_nanoseconds'] as int? ?? v['nanoseconds'] as int?;
                    if (sec != null) return sec * 1000 + ((nanosec ?? 0) ~/ 1000000);
                  }
                  return null;
                }

                final lastReadByDriverMs = readTimeToMs(lastReadByDriverRaw);

                return StreamBuilder<QuerySnapshot<Map<String, dynamic>>>(
                  stream: _repo.watchMessages(widget.chatId),
                  builder: (context, snapshot) {
                    if (snapshot.hasError) {
                      return Center(child: Text('Error: ${snapshot.error}'));
                    }
                    if (!snapshot.hasData) {
                      return const Center(child: CircularProgressIndicator());
                    }
                    final docs = snapshot.data!.docs;
                    if (docs.isEmpty) {
                      return Center(
                        child: Text('chat_no_messages_say_hello'.tr()),
                      );
                    }

                    bool isMessageRead(Map<String, dynamic> d, Timestamp? createdAt) {
                      if (createdAt == null) return false;
                      final msgMs = createdAt.millisecondsSinceEpoch;
                      final senderRole = d['senderRole'] as String? ?? '';
                      if (senderRole == 'driver') {
                        if (lastReadByAdminMap == null || lastReadByAdminMap.isEmpty) return false;
                        for (final v in lastReadByAdminMap.values) {
                          final readMs = readTimeToMs(v);
                          if (readMs != null && readMs >= msgMs) return true;
                        }
                        return false;
                      }
                      if (senderRole == 'admin') {
                        return lastReadByDriverMs != null && lastReadByDriverMs >= msgMs;
                      }
                      return false;
                    }

                    return ListView.builder(
                  controller: _scrollController,
                  padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
                  itemCount: docs.length,
                  itemBuilder: (context, index) {
                    final doc = docs[index];
                    final d = doc.data();
                    final senderRole = d['senderRole'] as String? ?? '';
                    final isMe = senderRole == 'driver';
                    final text = d['text'] as String? ?? '';
                    final type = d['type'] as String? ?? 'normal';
                    final isBroadcast = type == 'broadcast';
                    final imageUrl = d['imageUrl'] as String?;
                    final createdAt = d['createdAt'] as Timestamp?;
                    final time = createdAt != null
                        ? '${createdAt.toDate().hour.toString().padLeft(2, '0')}:${createdAt.toDate().minute.toString().padLeft(2, '0')}'
                        : '';

                    // ฝั่งผู้ส่ง (ฉัน) ไม่แสดงอวาตาร์และชื่อ แค่บับเบิ้ล
                    final senderName = 'chat_support'.tr();
                    final initial = senderName.isNotEmpty ? senderName[0].toUpperCase() : '?';

                    if (isMe) {
                      return Align(
                        alignment: Alignment.centerRight,
                        child: Padding(
                          padding: const EdgeInsets.only(bottom: 8),
                          child: Container(
                            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
                            constraints: BoxConstraints(
                              maxWidth: MediaQuery.of(context).size.width * 0.8,
                            ),
                            decoration: BoxDecoration(
                              color: Theme.of(context).colorScheme.primaryContainer,
                              borderRadius: BorderRadius.circular(12),
                            ),
                            child: Column(
                              crossAxisAlignment: CrossAxisAlignment.start,
                              children: [
                                if (imageUrl != null)
                                  Padding(
                                    padding: const EdgeInsets.only(bottom: 4),
                                    child: ClipRRect(
                                      borderRadius: BorderRadius.circular(8),
                                      child: Image.network(
                                        imageUrl,
                                        width: 200,
                                        fit: BoxFit.cover,
                                        errorBuilder: (_, __, ___) => const Icon(Icons.broken_image),
                                      ),
                                    ),
                                  ),
                                if (text.isNotEmpty)
                                  Text(
                                    (isBroadcast ? '📢 ' : '') + text,
                                    style: TextStyle(
                                      color: Theme.of(context).colorScheme.onPrimaryContainer,
                                    ),
                                  ),
                                Row(
                                  mainAxisSize: MainAxisSize.min,
                                  children: [
                                    Text(
                                      time,
                                      style: TextStyle(
                                        fontSize: 10,
                                        color: Theme.of(context).colorScheme.onPrimaryContainer.withValues(alpha: 0.7),
                                      ),
                                    ),
                                    if (isMessageRead(d, createdAt))
                                      Text(
                                        ' · ${'chat_read'.tr()}',
                                        style: TextStyle(
                                          fontSize: 10,
                                          color: Theme.of(context).colorScheme.onPrimaryContainer.withValues(alpha: 0.7),
                                        ),
                                      ),
                                  ],
                                ),
                              ],
                            ),
                          ),
                        ),
                      );
                    }

                    return Align(
                      alignment: Alignment.centerLeft,
                      child: Padding(
                        padding: const EdgeInsets.only(bottom: 8),
                        child: Row(
                          mainAxisSize: MainAxisSize.min,
                          mainAxisAlignment: MainAxisAlignment.start,
                          crossAxisAlignment: CrossAxisAlignment.end,
                          children: [
                            CircleAvatar(
                              radius: 16,
                              backgroundColor: Colors.grey.shade400,
                              child: Text(
                                initial,
                                style: const TextStyle(
                                  color: Colors.white,
                                  fontSize: 14,
                                  fontWeight: FontWeight.w600,
                                ),
                              ),
                            ),
                            const SizedBox(width: 8),
                            Flexible(
                              child: Column(
                                crossAxisAlignment: CrossAxisAlignment.start,
                                children: [
                                  Text(
                                    senderName,
                                    style: TextStyle(
                                      fontSize: 11,
                                      color: Theme.of(context).colorScheme.onSurface.withValues(alpha: 0.7),
                                    ),
                                  ),
                                  const SizedBox(height: 2),
                                  Container(
                                    padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
                                    constraints: BoxConstraints(
                                      maxWidth: MediaQuery.of(context).size.width * 0.8,
                                    ),
                                    decoration: BoxDecoration(
                                      color: Colors.grey.shade200,
                                      borderRadius: BorderRadius.circular(12),
                                    ),
                                    child: Column(
                                      crossAxisAlignment: CrossAxisAlignment.start,
                                      children: [
                                        if (imageUrl != null)
                                          Padding(
                                            padding: const EdgeInsets.only(bottom: 4),
                                            child: ClipRRect(
                                              borderRadius: BorderRadius.circular(8),
                                              child: Image.network(
                                                imageUrl,
                                                width: 200,
                                                fit: BoxFit.cover,
                                                errorBuilder: (_, __, ___) => const Icon(Icons.broken_image),
                                              ),
                                            ),
                                          ),
                                        if (text.isNotEmpty)
                                          Text(
                                            (isBroadcast ? '📢 ' : '') + text,
                                            style: const TextStyle(color: Colors.black87),
                                          ),
                                        Text(
                                          time,
                                          style: const TextStyle(
                                            fontSize: 10,
                                            color: Colors.black54,
                                          ),
                                        ),
                                      ],
                                    ),
                                  ),
                                ],
                              ),
                            ),
                          ],
                        ),
                      ),
                    );
                  },
                );
              },
            );
          },
        ),
      ),
          SafeArea(
            child: Padding(
              padding: const EdgeInsets.all(8.0),
              child: Row(
                children: [
                  IconButton(
                    icon: _sendingImage
                        ? const SizedBox(
                            width: 24,
                            height: 24,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          )
                        : const Icon(Icons.image_outlined),
                    onPressed: _sendingImage ? null : _pickAndSendImage,
                  ),
                  Expanded(
                    child: TextField(
                      controller: _textController,
                      decoration: InputDecoration(
                        hintText: 'chat_type_message'.tr(),
                        border: const OutlineInputBorder(),
                        contentPadding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
                      ),
                      textCapitalization: TextCapitalization.sentences,
                      onSubmitted: (_) => _sendText(),
                    ),
                  ),
                  const SizedBox(width: 8),
                  IconButton(
                    icon: _sending
                        ? const SizedBox(
                            width: 24,
                            height: 24,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          )
                        : const Icon(Icons.send),
                    onPressed: _sending ? null : _sendText,
                  ),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }
}
