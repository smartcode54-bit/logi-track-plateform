import 'package:easy_localization/easy_localization.dart';
import 'package:firebase_app_check/firebase_app_check.dart';
import 'package:firebase_core/firebase_core.dart';
import 'package:firebase_messaging/firebase_messaging.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_dotenv/flutter_dotenv.dart';
// import 'package:device_preview/device_preview.dart';
import 'core/theme/theme_controller.dart';
import 'core/services/fcm_service.dart';
import 'features/auth/presentation/pages/login_page.dart';
import 'features/home/presentation/pages/main_layout.dart';
import 'features/home/presentation/pages/main_layout_scope.dart';
import 'features/home/presentation/pages/check_in_page.dart';
import 'features/loading_phase/presentation/pages/loading_phase_page.dart';
import 'features/profile/presentation/pages/profile_page.dart';
import 'features/job_record/presentation/pages/job_record_page.dart';
import 'features/trip_history/presentation/pages/trip_history_page.dart';
import 'firebase_options.dart';

void main() async {
  WidgetsFlutterBinding.ensureInitialized();
  await dotenv.load(fileName: ".env");
  await EasyLocalization.ensureInitialized();
  try {
    await Firebase.initializeApp(
      options: DefaultFirebaseOptions.currentPlatform,
    );
  } catch (e) {
    // If Firebase is already initialized by google-services.json (Android),
    // just use the existing instance.
    if (e.toString().contains('duplicate-app')) {
      Firebase.app(); // Use the already-initialized default app
    } else {
      rethrow;
    }
  }

  // App Check: ใช้ Debug provider ตอนพัฒนา เพื่อไม่ให้เกิด "No AppCheckProvider installed"
  if (kDebugMode) {
    await FirebaseAppCheck.instance.activate(
      androidProvider: AndroidProvider.debug,
      appleProvider: AppleProvider.debug,
    );
  }

  FirebaseMessaging.onBackgroundMessage(firebaseMessagingBackgroundHandler);

  runApp(
    EasyLocalization(
      supportedLocales: const [Locale('en'), Locale('th')],
      path: 'assets/translations',
      fallbackLocale: const Locale('en'),
      // child: DevicePreview(
      //   enabled: !kReleaseMode,
      //   builder: (context) => const MyApp(),
      // ),
      child: const MyApp(),
    ),
  );
}

class MyApp extends StatelessWidget {
  const MyApp({super.key});

  @override
  Widget build(BuildContext context) {
    final themeController = ThemeController();

    return ValueListenableBuilder<ThemeMode>(
      valueListenable: themeController.themeMode,
      builder: (context, themeMode, _) {
        return MaterialApp(
          locale: context.locale,
          // builder: DevicePreview.appBuilder,
          localizationsDelegates: context.localizationDelegates,
          supportedLocales: context.supportedLocales,
          title: 'LogiTrack Driver',
          themeMode: themeMode,
          theme: ThemeData(
            brightness: Brightness.light,
            scaffoldBackgroundColor: Colors.white,
            colorScheme: const ColorScheme.light(
              primary: Color(0xFF2563EB),
              surface: Colors.white,
              onPrimary: Colors.white,
              onSurface: Colors.black,
            ),
            useMaterial3: true,
            fontFamily: 'Inter',
            inputDecorationTheme: InputDecorationTheme(
              filled: true,
              fillColor: Colors.grey[100],
              border: OutlineInputBorder(
                borderRadius: BorderRadius.circular(8),
                borderSide: BorderSide(color: Colors.grey[300]!),
              ),
              enabledBorder: OutlineInputBorder(
                borderRadius: BorderRadius.circular(8),
                borderSide: BorderSide(color: Colors.grey[300]!),
              ),
              focusedBorder: OutlineInputBorder(
                borderRadius: BorderRadius.circular(8),
                borderSide: const BorderSide(color: Color(0xFF2563EB)),
              ),
              labelStyle: TextStyle(color: Colors.grey[600]),
              hintStyle: TextStyle(color: Colors.grey[400]),
            ),
            elevatedButtonTheme: ElevatedButtonThemeData(
              style: ElevatedButton.styleFrom(
                backgroundColor: const Color(0xFF2563EB),
                foregroundColor: Colors.white,
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(8),
                ),
                padding: const EdgeInsets.symmetric(vertical: 16),
                textStyle: const TextStyle(
                  fontSize: 16,
                  fontWeight: FontWeight.bold,
                ),
              ),
            ),
          ),
          darkTheme: ThemeData(
            brightness: Brightness.dark,
            scaffoldBackgroundColor: const Color(0xFF111827),
            colorScheme: const ColorScheme.dark(
              primary: Color(0xFF2563EB),
              surface: Color(0xFF1F2937),
              onPrimary: Colors.white,
              onSurface: Colors.white,
            ),
            useMaterial3: true,
            fontFamily: 'Inter',
            inputDecorationTheme: InputDecorationTheme(
              filled: true,
              fillColor: const Color(0xFF1F2937),
              border: OutlineInputBorder(
                borderRadius: BorderRadius.circular(8),
                borderSide: const BorderSide(color: Color(0xFF374151)),
              ),
              enabledBorder: OutlineInputBorder(
                borderRadius: BorderRadius.circular(8),
                borderSide: const BorderSide(color: Color(0xFF374151)),
              ),
              focusedBorder: OutlineInputBorder(
                borderRadius: BorderRadius.circular(8),
                borderSide: const BorderSide(color: Color(0xFF2563EB)),
              ),
              labelStyle: const TextStyle(color: Color(0xFF9CA3AF)),
              hintStyle: const TextStyle(color: Color(0xFF6B7280)),
            ),
            elevatedButtonTheme: ElevatedButtonThemeData(
              style: ElevatedButton.styleFrom(
                backgroundColor: const Color(0xFF2563EB),
                foregroundColor: Colors.white,
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(8),
                ),
                padding: const EdgeInsets.symmetric(vertical: 16),
                textStyle: const TextStyle(
                  fontSize: 16,
                  fontWeight: FontWeight.bold,
                ),
              ),
            ),
          ),
          home: const LoginPage(),
          routes: {
            '/home': (context) {
              final args = ModalRoute.of(context)?.settings.arguments;
              int? tab;
              SavedTripSummary? summary;
              if (args is Map) {
                tab = args['tab'] as int?;
                final tripId = args['tripId'] as String?;
                if (tripId != null) {
                  summary = SavedTripSummary(
                    tripId: tripId,
                    origin: args['origin'] as String?,
                    destination: args['destination'] as String?,
                    sealCode: args['sealCode'] as String?,
                    jobType: args['jobType'] as String?,
                  );
                }
              }
              return MainLayout(
                initialTabIndex: tab,
                initialTripSummary: summary,
              );
            },
            '/check-in': (context) {
              final driverId =
                  ModalRoute.of(context)?.settings.arguments as String?;
              return CheckInPage(driverId: driverId ?? '');
            },
            '/loading-phase': (context) => const LoadingPhasePage(),
            '/profile': (context) => const ProfilePage(),
            '/job-record': (context) => const JobRecordPage(),
            '/trip-history': (context) => const TripHistoryPage(),
          },
        );
      },
    );
  }
}
