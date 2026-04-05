import 'dart:async';
import 'dart:typed_data';
import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:easy_localization/easy_localization.dart';
import 'package:firebase_auth/firebase_auth.dart';
import 'package:flutter/material.dart';
import 'package:intl/intl.dart';
import 'package:image_picker/image_picker.dart';
import '../../data/repositories/chat_repository.dart';
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
  StreamSubscription<QuerySnapshot<Map<String, dynamic>>>?
  _messagesSubscription;
  /// เวลา `lastReadByDriver` ก่อนเปิดห้อง (ms) — ใช้เส้นคั่นข้อความจากแอดมินที่ยังไม่ได้อ่าน
  int? _readBaselineMs;

  Future<void> _bootstrapReadBaseline() async {
    try {
      final snap = await _repo.getChatDoc(widget.chatId);
      if (!mounted) return;
      final raw = snap.data()?['lastReadByDriver'];
      var ms = 0;
      if (raw is Timestamp) ms = raw.millisecondsSinceEpoch;
      setState(() => _readBaselineMs = ms);
    } catch (_) {
      if (mounted) setState(() => _readBaselineMs = 0);
    }
    await _repo.markChatAsReadByDriver(widget.chatId);
  }

  @override
  void initState() {
    super.initState();
    _bootstrapReadBaseline();
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
      return Scaffold(body: Center(child: Text('chat_please_sign_in'.tr())));
    }

    return Scaffold(
      appBar: AppBar(title: Text('chat_room_title'.tr())),
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
                final lastReadByAdminMap =
                    chatData?['lastReadByAdmin'] as Map<String, dynamic>?;

                int? readTimeToMs(dynamic v) {
                  if (v == null) return null;
                  if (v is Timestamp) return v.millisecondsSinceEpoch;
                  if (v is Map) {
                    final sec = v['_seconds'] as int? ?? v['seconds'] as int?;
                    final nanosec =
                        v['_nanoseconds'] as int? ?? v['nanoseconds'] as int?;
                    if (sec != null)
                      return sec * 1000 + ((nanosec ?? 0) ~/ 1000000);
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

                    bool isMessageRead(
                      Map<String, dynamic> d,
                      Timestamp? createdAt,
                    ) {
                      if (createdAt == null) return false;
                      final msgMs = createdAt.millisecondsSinceEpoch;
                      final senderRole = d['senderRole'] as String? ?? '';
                      if (senderRole == 'driver') {
                        if (lastReadByAdminMap == null ||
                            lastReadByAdminMap.isEmpty)
                          return false;
                        for (final v in lastReadByAdminMap.values) {
                          final readMs = readTimeToMs(v);
                          if (readMs != null && readMs >= msgMs) return true;
                        }
                        return false;
                      }
                      if (senderRole == 'admin') {
                        return lastReadByDriverMs != null &&
                            lastReadByDriverMs >= msgMs;
                      }
                      return false;
                    }

                    int? firstUnreadAdminIndex;
                    final baseline = _readBaselineMs;
                    if (baseline != null) {
                      for (var i = 0; i < docs.length; i++) {
                        final dd = docs[i].data();
                        final role = dd['senderRole'] as String? ?? '';
                        final ts = dd['createdAt'] as Timestamp?;
                        if (role == 'admin' &&
                            ts != null &&
                            ts.millisecondsSinceEpoch > baseline) {
                          firstUnreadAdminIndex = i;
                          break;
                        }
                      }
                    }

                    Widget messageTile(int index) {
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
                        final initial = senderName.isNotEmpty
                            ? senderName[0].toUpperCase()
                            : '?';

                        if (isMe) {
                          return Align(
                            alignment: Alignment.centerRight,
                            child: Padding(
                              padding: const EdgeInsets.only(bottom: 8),
                              child: Container(
                                padding: const EdgeInsets.symmetric(
                                  horizontal: 12,
                                  vertical: 8,
                                ),
                                constraints: BoxConstraints(
                                  maxWidth:
                                      MediaQuery.of(context).size.width * 0.8,
                                ),
                                decoration: BoxDecoration(
                                  color: Theme.of(
                                    context,
                                  ).colorScheme.primaryContainer,
                                  borderRadius: BorderRadius.circular(12),
                                ),
                                child: Column(
                                  crossAxisAlignment: CrossAxisAlignment.start,
                                  children: [
                                    if (imageUrl != null)
                                      Padding(
                                        padding: const EdgeInsets.only(
                                          bottom: 4,
                                        ),
                                        child: ClipRRect(
                                          borderRadius: BorderRadius.circular(
                                            8,
                                          ),
                                          child: Image.network(
                                            imageUrl,
                                            width: 200,
                                            fit: BoxFit.cover,
                                            errorBuilder: (_, __, ___) =>
                                                const Icon(Icons.broken_image),
                                          ),
                                        ),
                                      ),
                                    if (text.isNotEmpty)
                                      Text(
                                        (isBroadcast ? '📢 ' : '') + text,
                                        style: TextStyle(
                                          color: Theme.of(
                                            context,
                                          ).colorScheme.onPrimaryContainer,
                                        ),
                                      ),
                                    Row(
                                      mainAxisSize: MainAxisSize.min,
                                      children: [
                                        Text(
                                          time,
                                          style: TextStyle(
                                            fontSize: 10,
                                            color: Theme.of(context)
                                                .colorScheme
                                                .onPrimaryContainer
                                                .withValues(alpha: 0.7),
                                          ),
                                        ),
                                        if (isMessageRead(d, createdAt))
                                          Text(
                                            ' · ${'chat_read'.tr()}',
                                            style: TextStyle(
                                              fontSize: 10,
                                              color: Theme.of(context)
                                                  .colorScheme
                                                  .onPrimaryContainer
                                                  .withValues(alpha: 0.7),
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
                                    crossAxisAlignment:
                                        CrossAxisAlignment.start,
                                    children: [
                                      Text(
                                        senderName,
                                        style: TextStyle(
                                          fontSize: 11,
                                          color: Theme.of(context)
                                              .colorScheme
                                              .onSurface
                                              .withValues(alpha: 0.7),
                                        ),
                                      ),
                                      const SizedBox(height: 2),
                                      Container(
                                        padding: const EdgeInsets.symmetric(
                                          horizontal: 12,
                                          vertical: 8,
                                        ),
                                        constraints: BoxConstraints(
                                          maxWidth:
                                              MediaQuery.of(
                                                context,
                                              ).size.width *
                                              0.8,
                                        ),
                                        decoration: BoxDecoration(
                                          color: Colors.grey.shade200,
                                          borderRadius: BorderRadius.circular(
                                            12,
                                          ),
                                        ),
                                        child: Column(
                                          crossAxisAlignment:
                                              CrossAxisAlignment.start,
                                          children: [
                                            if (imageUrl != null)
                                              Padding(
                                                padding: const EdgeInsets.only(
                                                  bottom: 4,
                                                ),
                                                child: ClipRRect(
                                                  borderRadius:
                                                      BorderRadius.circular(8),
                                                  child: Image.network(
                                                    imageUrl,
                                                    width: 200,
                                                    fit: BoxFit.cover,
                                                    errorBuilder:
                                                        (
                                                          _,
                                                          __,
                                                          ___,
                                                        ) => const Icon(
                                                          Icons.broken_image,
                                                        ),
                                                  ),
                                                ),
                                              ),
                                            if (text.isNotEmpty)
                                              Text(
                                                (isBroadcast ? '📢 ' : '') +
                                                    text,
                                                style: const TextStyle(
                                                  color: Colors.black87,
                                                ),
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
                    }

                    final messageChildren = <Widget>[];
                    DateTime? prevDayCal;
                    for (var index = 0; index < docs.length; index++) {
                      final d0 = docs[index].data();
                      final createdAt0 = d0['createdAt'] as Timestamp?;
                      final day = createdAt0?.toDate();
                      if (day != null) {
                        final cal = DateTime(day.year, day.month, day.day);
                        if (prevDayCal == null || cal != prevDayCal) {
                          messageChildren.add(_buildDateSeparator(context, day));
                          prevDayCal = cal;
                        }
                      }
                      if (firstUnreadAdminIndex == index && baseline != null) {
                        messageChildren.add(_buildUnreadDivider(context));
                      }
                      messageChildren.add(messageTile(index));
                    }

                    return ListView(
                      controller: _scrollController,
                      padding: const EdgeInsets.symmetric(
                        horizontal: 12,
                        vertical: 8,
                      ),
                      children: messageChildren,
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
                        contentPadding: const EdgeInsets.symmetric(
                          horizontal: 12,
                          vertical: 10,
                        ),
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

  String _labelForChatDate(DateTime d, BuildContext context) {
    final now = DateTime.now();
    final today = DateTime(now.year, now.month, now.day);
    final d0 = DateTime(d.year, d.month, d.day);
    if (d0 == today) return 'chat_date_today'.tr();
    final yesterday = today.subtract(const Duration(days: 1));
    if (d0 == yesterday) return 'chat_date_yesterday'.tr();
    return DateFormat.yMMMEd(context.locale.toString()).format(d);
  }

  Widget _buildDateSeparator(BuildContext context, DateTime dateTime) {
    final label = _labelForChatDate(dateTime, context);
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 8),
      child: Center(
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
          decoration: BoxDecoration(
            color: Theme.of(context)
                .colorScheme
                .surfaceContainerHighest
                .withValues(alpha: 0.85),
            borderRadius: BorderRadius.circular(999),
          ),
          child: Text(
            label,
            style: TextStyle(
              fontSize: 12,
              color: Theme.of(context).colorScheme.onSurface.withValues(alpha: 0.7),
            ),
          ),
        ),
      ),
    );
  }

  Widget _buildUnreadDivider(BuildContext context) {
    final cs = Theme.of(context).colorScheme;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 8),
      child: Row(
        children: [
          Expanded(
            child: Divider(
              color: cs.primary.withValues(alpha: 0.4),
              height: 1,
              thickness: 1,
            ),
          ),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 8),
            child: Text(
              'chat_unread_divider'.tr(),
              style: TextStyle(
                fontSize: 12,
                fontWeight: FontWeight.w600,
                color: cs.primary,
              ),
            ),
          ),
          Expanded(
            child: Divider(
              color: cs.primary.withValues(alpha: 0.4),
              height: 1,
              thickness: 1,
            ),
          ),
        ],
      ),
    );
  }
}
