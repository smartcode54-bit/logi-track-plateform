import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/material.dart';
import 'package:flutter_bloc/flutter_bloc.dart';
import 'package:flutter_dotenv/flutter_dotenv.dart';

import '../../core/route_observer.dart';
import '../../core/theme/app_theme.dart';
import '../core/session/session_bloc.dart';
import '../features/auth/presentation/go_change_password_page.dart';
import '../features/auth/presentation/go_login_page.dart';
import '../features/auth/presentation/login_bloc.dart';
import '../features/tasks/presentation/go_home_page.dart';
import 'go_badge.dart';
import 'go_services.dart';

/// The gotest app (spec mobile-go-core-loop R1, D3): reached from `main()` only when `FLAVOR=gotest`, before any
/// Firebase initialisation, so an accidental Firebase call fails at once ([core/no-app]) instead of reaching a
/// project. It loads `.env.dev` for the photo overlay keys only (gotest runs on the dev project's keys).
Future<void> runGoTestApp() async {
  await dotenv.load(fileName: '.env.dev');
  await EasyLocalization.ensureInitialized();
  final navigatorKey = GlobalKey<NavigatorState>();
  late final SessionBloc session;
  final services = await GoServices.create(
    onSessionEnded: () => session.add(SessionEnded()),
    languageCode: () => navigatorKey.currentContext?.locale.languageCode ?? 'th',
  );
  session = SessionBloc(services.auth)..add(SessionStarted());
  runApp(
    EasyLocalization(
      supportedLocales: const [Locale('en'), Locale('th')],
      path: 'assets/translations',
      fallbackLocale: const Locale('en'),
      child: BlocProvider.value(
        value: session,
        child: _GoTestApp(services: services, navigatorKey: navigatorKey),
      ),
    ),
  );
}

class _GoTestApp extends StatelessWidget {
  const _GoTestApp({required this.services, required this.navigatorKey});
  final GoServices services;
  final GlobalKey<NavigatorState> navigatorKey;

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'LogiTrack GO-TEST',
      navigatorKey: navigatorKey,
      navigatorObservers: [routeObserver],
      localizationsDelegates: context.localizationDelegates,
      supportedLocales: context.supportedLocales,
      locale: context.locale,
      theme: AppTheme.light,
      debugShowCheckedModeBanner: false,
      home: BlocProvider(
        create: (_) => LoginBloc(auth: services.auth, session: context.read<SessionBloc>()),
        child: BlocConsumer<SessionBloc, SessionState>(
          listenWhen: (a, b) => b is SessionAuthenticated && a is! SessionAuthenticated,
          listener: (context, _) => services.checkVersion(context),
          builder: (context, state) => switch (state) {
            SessionUnknown() => const Scaffold(body: Center(child: CircularProgressIndicator())),
            SessionOffline() => _OfflineStart(),
            SessionUnauthenticated(:final messageKey) =>
              GoLoginPage(messageKey: messageKey, versionLabel: services.versionLabel),
            SessionNeedsPassword(:final email, :final ticket) => GoChangePasswordPage(email: email, ticket: ticket),
            SessionAuthenticated(:final me) => GoHomePage(services: services, driverName: me.displayName),
          },
        ),
      ),
    );
  }
}

/// A cold start without the server: the session is kept, the driver retries (R4).
class _OfflineStart extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Row(children: [Text('LogiTrack'), GoBadge()])),
      body: Center(
        child: Column(mainAxisSize: MainAxisSize.min, children: [
          const Icon(Icons.wifi_off, size: 48),
          const SizedBox(height: 12),
          Padding(padding: const EdgeInsets.all(24), child: Text('api_error_network'.tr(), textAlign: TextAlign.center)),
          FilledButton(
            onPressed: () => context.read<SessionBloc>().add(SessionStarted()),
            child: Text('retry'.tr()),
          ),
        ]),
      ),
    );
  }
}
