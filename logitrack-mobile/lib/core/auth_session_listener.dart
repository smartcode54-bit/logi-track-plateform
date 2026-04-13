import 'dart:async';

import 'package:firebase_auth/firebase_auth.dart';
import 'package:flutter/material.dart';

import '../features/auth/presentation/pages/login_page.dart';

/// When [FirebaseAuth] drops from signed-in to signed-out (e.g. refresh token
/// revoked server-side), clear the stack and show [LoginPage]. Without this,
/// [MainLayout] stays visible until the user restarts the app.
class AuthSessionListener extends StatefulWidget {
  const AuthSessionListener({
    super.key,
    required this.navigatorKey,
    required this.child,
  });

  final GlobalKey<NavigatorState> navigatorKey;
  final Widget child;

  @override
  State<AuthSessionListener> createState() => _AuthSessionListenerState();
}

class _AuthSessionListenerState extends State<AuthSessionListener> {
  StreamSubscription<User?>? _sub;
  User? _previousUser;

  @override
  void initState() {
    super.initState();
    _sub = FirebaseAuth.instance.authStateChanges().listen((user) {
      final hadUser = _previousUser != null;
      _previousUser = user;
      if (!hadUser || user != null) return;

      WidgetsBinding.instance.addPostFrameCallback((_) {
        final nav = widget.navigatorKey.currentState;
        if (nav == null || !nav.mounted) return;
        nav.pushAndRemoveUntil<void>(
          MaterialPageRoute<void>(builder: (_) => const LoginPage()),
          (_) => false,
        );
      });
    });
  }

  @override
  void dispose() {
    _sub?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => widget.child;
}
