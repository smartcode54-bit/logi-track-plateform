import 'dart:math' as math;

import 'package:firebase_auth/firebase_auth.dart';
import 'package:flutter/material.dart';

const _kBalloonSize = 56.0;
const _kDefaultMargin = 16.0;
const _kTapThreshold = 12.0;
/// Space above bottom nav so balloon sits above "vehicle" button
const _kBottomNavHeight = 56.0;
const _kMarginAboveNav = 12.0;

/// Floating chat balloon that appears on every page when user is logged in.
/// Tap to open chat; long-press and drag to move.
class ChatBalloonOverlay extends StatefulWidget {
  const ChatBalloonOverlay({super.key});

  @override
  State<ChatBalloonOverlay> createState() => _ChatBalloonOverlayState();
}

class _ChatBalloonOverlayState extends State<ChatBalloonOverlay> {
  Offset? _position;
  Offset? _dragStartPosition;

  Offset _defaultPosition(Size size) {
    final bottomOffset = _kBottomNavHeight + _kMarginAboveNav;
    return Offset(
      size.width - _kBalloonSize - _kDefaultMargin,
      size.height - _kBalloonSize - bottomOffset,
    );
  }

  Offset _clamp(Offset p, Size size) {
    final maxX = math.max(0.0, size.width - _kBalloonSize).toDouble();
    final maxY = math.max(0.0, size.height - _kBalloonSize - _kBottomNavHeight).toDouble();
    return Offset(
      p.dx.clamp(0.0, maxX),
      p.dy.clamp(0.0, maxY),
    );
  }

  void _openChat() {
    final routeName = ModalRoute.of(context)?.settings.name;
    if (routeName != '/chat' && routeName != '/chat-room') {
      Navigator.of(context).pushNamed('/chat');
    }
  }

  @override
  Widget build(BuildContext context) {
    return StreamBuilder<User?>(
      stream: FirebaseAuth.instance.authStateChanges(),
      builder: (context, snapshot) {
        final user = snapshot.data;
        if (user == null) return const SizedBox.shrink();

        return LayoutBuilder(
          builder: (context, constraints) {
            final size = Size(constraints.maxWidth, constraints.maxHeight);
            final defaultPos = _defaultPosition(size);
            final left = (_position ?? defaultPos).dx;
            final top = (_position ?? defaultPos).dy;
            final clamped = _clamp(Offset(left, top), size);

            return Positioned(
              left: clamped.dx,
              top: clamped.dy,
              child: GestureDetector(
                onPanStart: (details) {
                  final pos = _position ?? defaultPos;
                  setState(() {
                    _dragStartPosition = pos;
                    _position = _clamp(pos, size);
                  });
                },
                onPanUpdate: (details) {
                  setState(() {
                    final current = _position ?? defaultPos;
                    _position = _clamp(current + details.delta, size);
                  });
                },
                onPanEnd: (details) {
                  final start = _dragStartPosition;
                  final end = _position;
                  _dragStartPosition = null;
                  if (start != null && end != null &&
                      (end - start).distance < _kTapThreshold) {
                    _openChat();
                  }
                },
                child: Material(
                  color: Colors.transparent,
                  child: Container(
                    width: _kBalloonSize,
                    height: _kBalloonSize,
                    decoration: BoxDecoration(
                      color: Theme.of(context).colorScheme.primary,
                      shape: BoxShape.circle,
                      boxShadow: [
                        BoxShadow(
                          color: Colors.black.withOpacity(0.2),
                          blurRadius: 8,
                          offset: const Offset(0, 2),
                        ),
                      ],
                    ),
                    child: const Icon(
                      Icons.chat_bubble_outline,
                      color: Colors.white,
                      size: 28,
                    ),
                  ),
                ),
              ),
            );
          },
        );
      },
    );
  }
}
