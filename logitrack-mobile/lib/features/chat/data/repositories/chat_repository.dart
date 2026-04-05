import 'dart:typed_data';
import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:firebase_auth/firebase_auth.dart';
import 'package:firebase_storage/firebase_storage.dart';
import '../../../../features/home/data/services/image_compression_service.dart';

class ChatRepository {
  final FirebaseFirestore _firestore = FirebaseFirestore.instance;
  final FirebaseStorage _storage = FirebaseStorage.instance;

  /// Create a new chat for the current driver (so admin can see it in Queued). Call when driver has no chat yet.
  Future<String> createChatForDriver(String driverUid) async {
    String driverDisplayName = 'Driver';
    try {
      final driversSnap = await _firestore
          .collection('drivers')
          .where('authId', isEqualTo: driverUid)
          .limit(1)
          .get();
      if (driversSnap.docs.isNotEmpty) {
        final d = driversSnap.docs.first.data();
        final first = d['firstName'] as String? ?? '';
        final last = d['lastName'] as String? ?? '';
        driverDisplayName = [first, last].where((s) => s.isNotEmpty).join(' ').trim();
        if (driverDisplayName.isEmpty) driverDisplayName = 'Driver';
      }
    } catch (_) {}
    final ref = _firestore.collection('chats').doc();
    await ref.set({
      'driverId': driverUid,
      'driverDisplayName': driverDisplayName,
      'status': 'open',
      'assignedAdminId': null,
      'assignedAt': null,
      'closedAt': null,
      'priority': 'normal',
      'lastReadByAdmin': <String, dynamic>{},
      'lastMessage': '',
      'lastMessageAt': FieldValue.serverTimestamp(),
      'lastMessageBy': '',
      'createdAt': FieldValue.serverTimestamp(),
      'updatedAt': FieldValue.serverTimestamp(),
    });
    return ref.id;
  }

  /// Stream chats where driverId == current user uid (Auth UID). Filter closed in UI if needed.
  Stream<QuerySnapshot<Map<String, dynamic>>> watchChatsForDriver(String driverUid) {
    return _firestore
        .collection('chats')
        .where('driverId', isEqualTo: driverUid)
        .orderBy('lastMessageAt', descending: true)
        .snapshots();
  }

  /// ดึงแชทจาก server ครั้งเดียว (ใช้ตอนกลับจากห้องแชท เพื่อให้ badge อัปเดตตามข้อมูลล่าสุด).
  Future<QuerySnapshot<Map<String, dynamic>>> getChatsForDriverFromServer(String driverUid) {
    return _firestore
        .collection('chats')
        .where('driverId', isEqualTo: driverUid)
        .orderBy('lastMessageAt', descending: true)
        .get(const GetOptions(source: Source.server));
  }

  /// Mark chat as read by driver (เมื่อเปิดห้องแชทหรือตอบแล้ว ใช้เอา badge ออก).
  Future<void> markChatAsReadByDriver(String chatId) async {
    final user = FirebaseAuth.instance.currentUser;
    if (user == null) return;
    final chatRef = _firestore.collection('chats').doc(chatId);
    await chatRef.update({
      'lastReadByDriver': FieldValue.serverTimestamp(),
      'updatedAt': FieldValue.serverTimestamp(),
    });
  }

  /// Stream chat document (for lastReadByDriver, lastReadByAdmin — แสดงสถานะ อ่านแล้ว).
  Stream<DocumentSnapshot<Map<String, dynamic>>> watchChatDoc(String chatId) {
    return _firestore.collection('chats').doc(chatId).snapshots();
  }

  /// ดึงเอกสารแชทครั้งเดียว (ใช้เก็บเวลาอ่านก่อน mark-as-read ตอนเปิดห้อง).
  Future<DocumentSnapshot<Map<String, dynamic>>> getChatDoc(String chatId) {
    return _firestore.collection('chats').doc(chatId).get();
  }

  /// Stream messages for a chat room.
  Stream<QuerySnapshot<Map<String, dynamic>>> watchMessages(String chatId) {
    return _firestore
        .collection('chats')
        .doc(chatId)
        .collection('messages')
        .orderBy('createdAt', descending: false)
        .snapshots();
  }

  /// Send a text message and update chat lastMessage.
  Future<void> sendTextMessage(String chatId, String text) async {
    final user = FirebaseAuth.instance.currentUser;
    if (user == null) throw Exception('Not authenticated');
    final messagesRef = _firestore
        .collection('chats')
        .doc(chatId)
        .collection('messages');
    final chatRef = _firestore.collection('chats').doc(chatId);
    await _firestore.runTransaction((tx) async {
      final docRef = messagesRef.doc();
      tx.set(docRef, {
        'senderId': user.uid,
        'senderRole': 'driver',
        'text': text,
        'createdAt': FieldValue.serverTimestamp(),
      });
      tx.update(chatRef, {
        'lastMessage': text,
        'lastMessageAt': FieldValue.serverTimestamp(),
        'lastMessageBy': user.uid,
        'lastReadByDriver': FieldValue.serverTimestamp(),
        'updatedAt': FieldValue.serverTimestamp(),
      });
    });
  }

  /// Compress image bytes then upload to Storage and send a message with imageUrl.
  Future<void> sendImageMessage(String chatId, Uint8List imageBytes, {String? caption}) async {
    final user = FirebaseAuth.instance.currentUser;
    if (user == null) throw Exception('Not authenticated');
    if (imageBytes.isEmpty) throw Exception('Image is empty');
    final compressed = await compressImageForUpload(imageBytes.toList());
    if (compressed.isEmpty) throw Exception('Compressed image is empty');
    final path = 'chat_media/$chatId/${DateTime.now().millisecondsSinceEpoch}.jpg';
    final ref = _storage.ref().child(path);
    await ref.putData(
      compressed,
      SettableMetadata(contentType: 'image/jpeg'),
    );
    final imageUrl = await ref.getDownloadURL();
    final messagesRef = _firestore
        .collection('chats')
        .doc(chatId)
        .collection('messages');
    final chatRef = _firestore.collection('chats').doc(chatId);
    final text = caption ?? '';
    await _firestore.runTransaction((tx) async {
      final docRef = messagesRef.doc();
      tx.set(docRef, {
        'senderId': user.uid,
        'senderRole': 'driver',
        'text': text,
        'imageUrl': imageUrl,
        'createdAt': FieldValue.serverTimestamp(),
      });
      tx.update(chatRef, {
        'lastMessage': text.isEmpty ? '[Image]' : text,
        'lastMessageAt': FieldValue.serverTimestamp(),
        'lastMessageBy': user.uid,
        'lastReadByDriver': FieldValue.serverTimestamp(),
        'updatedAt': FieldValue.serverTimestamp(),
      });
    });
  }
}
