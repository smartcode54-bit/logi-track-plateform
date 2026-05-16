import 'package:flutter/material.dart';

/// LogiTrack Kinetic design system tokens (mobile).
///
/// Dark mode is the default for driver field use; light mode mirrors the
/// "Modern Industrial" complement for indoor/admin contexts.
class AppTheme {
  AppTheme._();

  // ---- Kinetic Dark palette (from Stitch design system V2.4.1) ----
  static const Color _darkBackground = Color(0xFF131313);
  static const Color _darkSurface = Color(0xFF131313);
  static const Color _darkSurfaceContainerLowest = Color(0xFF0E0E0E);
  static const Color _darkSurfaceContainerLow = Color(0xFF1C1B1B);
  static const Color _darkSurfaceContainer = Color(0xFF1E1E1E);
  static const Color _darkSurfaceContainerHigh = Color(0xFF2A2A2A);
  static const Color _darkSurfaceContainerHighest = Color(0xFF353534);
  static const Color _darkOnSurface = Color(0xFFF3F4F6);
  static const Color _darkOnSurfaceVariant = Color(0xFFC2C6D6);
  static const Color _darkOutline = Color(0xFF8C909F);
  static const Color _darkOutlineVariant = Color(0xFF424754);
  static const Color _darkPrimary = Color(0xFFADC6FF);
  static const Color _darkOnPrimary = Color(0xFF002E6A);
  static const Color _darkPrimaryContainer = Color(0xFF4D8EFF);
  static const Color _darkOnPrimaryContainer = Color(0xFF00285D);
  static const Color _darkSecondary = Color(0xFF45DFA4);
  static const Color _darkOnSecondary = Color(0xFF003825);
  static const Color _darkSecondaryContainer = Color(0xFF00BD85);
  static const Color _darkTertiary = Color(0xFFEAC33E);
  static const Color _darkOnTertiary = Color(0xFF3C2F00);
  static const Color _darkError = Color(0xFFFFB4AB);
  static const Color _darkOnError = Color(0xFF690005);
  static const Color _darkErrorContainer = Color(0xFF93000A);

  // ---- Light palette (complementary Industrial) ----
  static const Color _lightBackground = Color(0xFFFCF8FA);
  static const Color _lightSurface = Color(0xFFFCF8FA);
  static const Color _lightSurfaceContainerLowest = Color(0xFFFFFFFF);
  static const Color _lightSurfaceContainerLow = Color(0xFFF6F3F5);
  static const Color _lightSurfaceContainer = Color(0xFFF0EDEF);
  static const Color _lightSurfaceContainerHigh = Color(0xFFEAE7E9);
  static const Color _lightSurfaceContainerHighest = Color(0xFFE4E2E4);
  static const Color _lightOnSurface = Color(0xFF1B1B1D);
  static const Color _lightOnSurfaceVariant = Color(0xFF45464D);
  static const Color _lightOutline = Color(0xFF76777D);
  static const Color _lightOutlineVariant = Color(0xFFC6C6CD);
  static const Color _lightPrimary = Color(0xFF2563EB);
  static const Color _lightOnPrimary = Color(0xFFFFFFFF);
  static const Color _lightPrimaryContainer = Color(0xFFDAE2FD);
  static const Color _lightOnPrimaryContainer = Color(0xFF131B2E);
  static const Color _lightSecondary = Color(0xFF505F76);
  static const Color _lightOnSecondary = Color(0xFFFFFFFF);
  static const Color _lightTertiary = Color(0xFFEAC33E);
  static const Color _lightOnTertiary = Color(0xFF3C2F00);
  static const Color _lightError = Color(0xFFBA1A1A);
  static const Color _lightOnError = Color(0xFFFFFFFF);
  static const Color _lightErrorContainer = Color(0xFFFFDAD6);

  // Kinetic uses 4dp baseline; corner radii are intentionally small.
  static const double _radiusInput = 4;
  static const double _radiusButton = 4;
  static const double _radiusCard = 8;
  static const double _buttonHeight = 48;
  static const String _fontFamily = 'Inter';

  static ThemeData get dark => _buildTheme(
        brightness: Brightness.dark,
        scheme: const ColorScheme.dark(
          primary: _darkPrimary,
          onPrimary: _darkOnPrimary,
          primaryContainer: _darkPrimaryContainer,
          onPrimaryContainer: _darkOnPrimaryContainer,
          secondary: _darkSecondary,
          onSecondary: _darkOnSecondary,
          secondaryContainer: _darkSecondaryContainer,
          tertiary: _darkTertiary,
          onTertiary: _darkOnTertiary,
          error: _darkError,
          onError: _darkOnError,
          errorContainer: _darkErrorContainer,
          surface: _darkSurface,
          onSurface: _darkOnSurface,
          surfaceContainerLowest: _darkSurfaceContainerLowest,
          surfaceContainerLow: _darkSurfaceContainerLow,
          surfaceContainer: _darkSurfaceContainer,
          surfaceContainerHigh: _darkSurfaceContainerHigh,
          surfaceContainerHighest: _darkSurfaceContainerHighest,
          onSurfaceVariant: _darkOnSurfaceVariant,
          outline: _darkOutline,
          outlineVariant: _darkOutlineVariant,
        ),
        scaffoldBackground: _darkBackground,
        inputFill: _darkSurfaceContainer,
        inputBorder: _darkOutlineVariant,
        focusedBorder: _darkPrimary,
        hintColor: _darkOnSurfaceVariant,
      );

  static ThemeData get light => _buildTheme(
        brightness: Brightness.light,
        scheme: const ColorScheme.light(
          primary: _lightPrimary,
          onPrimary: _lightOnPrimary,
          primaryContainer: _lightPrimaryContainer,
          onPrimaryContainer: _lightOnPrimaryContainer,
          secondary: _lightSecondary,
          onSecondary: _lightOnSecondary,
          tertiary: _lightTertiary,
          onTertiary: _lightOnTertiary,
          error: _lightError,
          onError: _lightOnError,
          errorContainer: _lightErrorContainer,
          surface: _lightSurface,
          onSurface: _lightOnSurface,
          surfaceContainerLowest: _lightSurfaceContainerLowest,
          surfaceContainerLow: _lightSurfaceContainerLow,
          surfaceContainer: _lightSurfaceContainer,
          surfaceContainerHigh: _lightSurfaceContainerHigh,
          surfaceContainerHighest: _lightSurfaceContainerHighest,
          onSurfaceVariant: _lightOnSurfaceVariant,
          outline: _lightOutline,
          outlineVariant: _lightOutlineVariant,
        ),
        scaffoldBackground: _lightBackground,
        inputFill: _lightSurfaceContainerLowest,
        inputBorder: _lightOutlineVariant,
        focusedBorder: _lightPrimary,
        hintColor: _lightOnSurfaceVariant,
      );

  static ThemeData _buildTheme({
    required Brightness brightness,
    required ColorScheme scheme,
    required Color scaffoldBackground,
    required Color inputFill,
    required Color inputBorder,
    required Color focusedBorder,
    required Color hintColor,
  }) {
    return ThemeData(
      useMaterial3: true,
      brightness: brightness,
      colorScheme: scheme,
      scaffoldBackgroundColor: scaffoldBackground,
      fontFamily: _fontFamily,
      dividerTheme: DividerThemeData(
        color: scheme.outlineVariant,
        thickness: 1,
        space: 1,
      ),
      appBarTheme: AppBarTheme(
        backgroundColor: scaffoldBackground,
        surfaceTintColor: Colors.transparent,
        elevation: 0,
        scrolledUnderElevation: 0,
        centerTitle: false,
        titleTextStyle: TextStyle(
          fontFamily: _fontFamily,
          fontSize: 20,
          fontWeight: FontWeight.w600,
          color: scheme.onSurface,
        ),
        iconTheme: IconThemeData(color: scheme.onSurface),
      ),
      cardTheme: CardThemeData(
        color: scheme.surfaceContainer,
        surfaceTintColor: Colors.transparent,
        elevation: 0,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(_radiusCard),
          side: BorderSide(color: scheme.outlineVariant),
        ),
      ),
      inputDecorationTheme: InputDecorationTheme(
        filled: true,
        fillColor: inputFill,
        contentPadding:
            const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(_radiusInput),
          borderSide: BorderSide(color: inputBorder),
        ),
        enabledBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(_radiusInput),
          borderSide: BorderSide(color: inputBorder),
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(_radiusInput),
          borderSide: BorderSide(color: focusedBorder, width: 2),
        ),
        errorBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(_radiusInput),
          borderSide: BorderSide(color: scheme.error),
        ),
        focusedErrorBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(_radiusInput),
          borderSide: BorderSide(color: scheme.error, width: 2),
        ),
        hintStyle: TextStyle(
          color: hintColor.withValues(alpha: 0.6),
          fontFamily: _fontFamily,
        ),
        labelStyle: TextStyle(color: hintColor, fontFamily: _fontFamily),
        prefixIconColor: hintColor,
        suffixIconColor: hintColor,
      ),
      elevatedButtonTheme: ElevatedButtonThemeData(
        style: ElevatedButton.styleFrom(
          backgroundColor: scheme.primary,
          foregroundColor: scheme.onPrimary,
          disabledBackgroundColor: scheme.primary.withValues(alpha: 0.4),
          disabledForegroundColor: scheme.onPrimary.withValues(alpha: 0.6),
          minimumSize: const Size.fromHeight(_buttonHeight),
          padding: const EdgeInsets.symmetric(vertical: 14, horizontal: 16),
          elevation: 0,
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(_radiusButton),
          ),
          textStyle: const TextStyle(
            fontFamily: _fontFamily,
            fontSize: 16,
            fontWeight: FontWeight.w600,
            letterSpacing: 0.15,
          ),
        ),
      ),
      filledButtonTheme: FilledButtonThemeData(
        style: FilledButton.styleFrom(
          minimumSize: const Size.fromHeight(_buttonHeight),
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(_radiusButton),
          ),
        ),
      ),
      outlinedButtonTheme: OutlinedButtonThemeData(
        style: OutlinedButton.styleFrom(
          backgroundColor: inputFill,
          foregroundColor: scheme.onSurface,
          side: BorderSide(color: scheme.outlineVariant),
          minimumSize: const Size.fromHeight(_buttonHeight),
          padding: const EdgeInsets.symmetric(vertical: 14, horizontal: 16),
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(_radiusButton),
          ),
          textStyle: const TextStyle(
            fontFamily: _fontFamily,
            fontSize: 16,
            fontWeight: FontWeight.w600,
            letterSpacing: 0.15,
          ),
        ),
      ),
      textButtonTheme: TextButtonThemeData(
        style: TextButton.styleFrom(
          foregroundColor: scheme.primary,
          textStyle: const TextStyle(
            fontFamily: _fontFamily,
            fontWeight: FontWeight.w600,
          ),
        ),
      ),
      iconButtonTheme: IconButtonThemeData(
        style: IconButton.styleFrom(foregroundColor: scheme.onSurface),
      ),
      snackBarTheme: SnackBarThemeData(
        backgroundColor: scheme.surfaceContainerHigh,
        contentTextStyle: TextStyle(
          color: scheme.onSurface,
          fontFamily: _fontFamily,
        ),
        behavior: SnackBarBehavior.floating,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(_radiusCard),
        ),
      ),
      drawerTheme: DrawerThemeData(
        backgroundColor: scheme.surfaceContainer,
        surfaceTintColor: Colors.transparent,
        shape: const RoundedRectangleBorder(),
      ),
      listTileTheme: ListTileThemeData(
        iconColor: scheme.onSurfaceVariant,
        textColor: scheme.onSurface,
        minLeadingWidth: 24,
        contentPadding: const EdgeInsets.symmetric(horizontal: 20),
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(_radiusCard),
        ),
      ),
      textTheme: _textTheme(scheme.onSurface),
    );
  }

  static TextTheme _textTheme(Color onSurface) {
    return TextTheme(
      displayLarge: TextStyle(
        fontFamily: _fontFamily,
        fontSize: 32,
        fontWeight: FontWeight.w800,
        color: onSurface,
        letterSpacing: -0.5,
      ),
      headlineLarge: TextStyle(
        fontFamily: _fontFamily,
        fontSize: 24,
        fontWeight: FontWeight.w700,
        color: onSurface,
      ),
      headlineMedium: TextStyle(
        fontFamily: _fontFamily,
        fontSize: 22,
        fontWeight: FontWeight.w700,
        color: onSurface,
      ),
      headlineSmall: TextStyle(
        fontFamily: _fontFamily,
        fontSize: 20,
        fontWeight: FontWeight.w600,
        color: onSurface,
      ),
      titleLarge: TextStyle(
        fontFamily: _fontFamily,
        fontSize: 18,
        fontWeight: FontWeight.w600,
        color: onSurface,
      ),
      titleMedium: TextStyle(
        fontFamily: _fontFamily,
        fontSize: 16,
        fontWeight: FontWeight.w600,
        color: onSurface,
        letterSpacing: 0.15,
      ),
      titleSmall: TextStyle(
        fontFamily: _fontFamily,
        fontSize: 14,
        fontWeight: FontWeight.w600,
        color: onSurface,
      ),
      bodyLarge: TextStyle(
        fontFamily: _fontFamily,
        fontSize: 16,
        fontWeight: FontWeight.w400,
        color: onSurface,
        height: 1.5,
      ),
      bodyMedium: TextStyle(
        fontFamily: _fontFamily,
        fontSize: 14,
        fontWeight: FontWeight.w400,
        color: onSurface,
        height: 1.43,
      ),
      bodySmall: TextStyle(
        fontFamily: _fontFamily,
        fontSize: 12,
        fontWeight: FontWeight.w400,
        color: onSurface,
        height: 1.33,
      ),
      labelLarge: TextStyle(
        fontFamily: _fontFamily,
        fontSize: 14,
        fontWeight: FontWeight.w600,
        color: onSurface,
      ),
      labelMedium: TextStyle(
        fontFamily: _fontFamily,
        fontSize: 12,
        fontWeight: FontWeight.w500,
        color: onSurface,
        letterSpacing: 0.5,
      ),
      labelSmall: TextStyle(
        fontFamily: _fontFamily,
        fontSize: 11,
        fontWeight: FontWeight.w500,
        color: onSurface,
        letterSpacing: 0.5,
      ),
    );
  }
}
