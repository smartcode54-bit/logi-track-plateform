import 'dart:ui';

import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/material.dart';
import 'package:package_info_plus/package_info_plus.dart';
import '../../../../core/theme/theme_controller.dart';
import '../../../../core/services/fcm_service.dart';
import '../../../../core/services/mobile_app_version_service.dart';
import '../../../home/data/services/draft_storage_service.dart';
import '../../data/repositories/auth_repository.dart';

// Read flavor from --dart-define=FLAVOR=dev|prod
const String _appFlavor = String.fromEnvironment('FLAVOR', defaultValue: 'dev');

class LoginPage extends StatefulWidget {
  const LoginPage({super.key});

  @override
  State<LoginPage> createState() => _LoginPageState();
}

class _LoginPageState extends State<LoginPage> {
  final _emailController = TextEditingController();
  final _passwordController = TextEditingController();
  final _formKey = GlobalKey<FormState>();
  bool _isPasswordVisible = false;
  final _authRepository = AuthRepository();
  bool _isLoading = false;
  String _versionNumber = '';
  String _versionDisplay = '';

  @override
  void initState() {
    super.initState();
    _loadVersion();
  }

  Future<void> _loadVersion() async {
    final info = await PackageInfo.fromPlatform();
    final envLabel = _appFlavor == 'prod' ? 'Release' : 'Dev';
    setState(() {
      _versionNumber = 'v${info.version}';
      _versionDisplay =
          'LOGI-TRACK $envLabel v${info.version} (DRIVER EDITION)';
    });
  }

  @override
  void dispose() {
    _emailController.dispose();
    _passwordController.dispose();
    super.dispose();
  }

  void _loginWithGoogle() async {
    setState(() => _isLoading = true);
    try {
      final user = await _authRepository.signInWithGoogle();
      if (mounted && user != null) {
        saveFcmTokenToUser(user.uid);
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('login_success'.tr())));
        await _navigateToHome();
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(
              '${'error'.tr()}: ${e.toString().replaceAll("Exception: ", "")}',
            ),
            backgroundColor: Theme.of(context).colorScheme.error,
          ),
        );
      }
    } finally {
      if (mounted) setState(() => _isLoading = false);
    }
  }

  Future<void> _navigateToHome() async {
    if (!mounted) return;
    final allowed =
        await MobileAppVersionService.instance.ensureAllowedToRun(context);
    if (!allowed || !mounted) return;

    final draft = await DraftStorageService.instance.loadDeliveryDraft();
    final args = draft != null
        ? {
            'tab': 2,
            'tripId': draft.tripId,
            'origin': draft.origin,
            'destination': draft.destination,
            'sealCode': draft.sealCode,
            'jobType': draft.jobType,
          }
        : null;
    if (mounted) {
      Navigator.pushReplacementNamed(context, '/home', arguments: args);
    }
  }

  void _login() async {
    if (_formKey.currentState!.validate()) {
      setState(() => _isLoading = true);
      try {
        final user = await _authRepository.signInWithEmail(
          _emailController.text.trim(),
          _passwordController.text,
        );
        if (mounted && user != null) {
          saveFcmTokenToUser(user.uid);
          ScaffoldMessenger.of(context)
              .showSnackBar(SnackBar(content: Text('login_success'.tr())));
          await _navigateToHome();
        }
      } catch (e) {
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(
              content: Text(
                '${'error'.tr()}: ${e.toString().replaceAll("Exception: ", "")}',
              ),
              backgroundColor: Theme.of(context).colorScheme.error,
            ),
          );
        }
      } finally {
        if (mounted) setState(() => _isLoading = false);
      }
    }
  }

  void _onForgotPassword() {
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text('${'need_help'.tr()} ${'contact_dispatch'.tr()}'),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final isDark = theme.brightness == Brightness.dark;

    return Scaffold(
      backgroundColor: scheme.surface,
      body: Stack(
        children: [
          // Subtle blurred gradient orbs
          const _BackgroundOrbs(),

          // Main content
          SafeArea(
            child: Column(
              children: [
                Expanded(
                  child: SingleChildScrollView(
                    padding: const EdgeInsets.fromLTRB(20, 32, 20, 24),
                    child: ConstrainedBox(
                      constraints: const BoxConstraints(maxWidth: 400),
                      child: Form(
                        key: _formKey,
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.stretch,
                          children: [
                            const _BrandHeader(),
                            const SizedBox(height: 24),

                            // Email
                            _FieldLabel(text: 'email_label'.tr()),
                            const SizedBox(height: 6),
                            _StitchTextField(
                              controller: _emailController,
                              hintText: 'email_hint'.tr(),
                              icon: Icons.person_outline,
                              keyboardType: TextInputType.emailAddress,
                              textInputAction: TextInputAction.next,
                              validator: (value) {
                                if (value == null || value.isEmpty) {
                                  return 'email_required'.tr();
                                }
                                if (!RegExp(r'^[\w-\.]+@([\w-]+\.)+[\w-]{2,4}$')
                                    .hasMatch(value)) {
                                  return 'email_invalid'.tr();
                                }
                                return null;
                              },
                            ),
                            const SizedBox(height: 16),

                            // Password
                            Row(
                              children: [
                                Expanded(
                                  child:
                                      _FieldLabel(text: 'password_label'.tr()),
                                ),
                                _ForgotPasswordLink(onTap: _onForgotPassword),
                              ],
                            ),
                            const SizedBox(height: 6),
                            _StitchTextField(
                              controller: _passwordController,
                              hintText: 'password_hint'.tr(),
                              icon: Icons.lock_outline,
                              obscureText: !_isPasswordVisible,
                              textInputAction: TextInputAction.done,
                              onFieldSubmitted: (_) =>
                                  _isLoading ? null : _login(),
                              suffixIcon: IconButton(
                                splashRadius: 20,
                                icon: Icon(
                                  _isPasswordVisible
                                      ? Icons.visibility_off_outlined
                                      : Icons.visibility_outlined,
                                  size: 20,
                                  color: scheme.outline,
                                ),
                                onPressed: () => setState(() =>
                                    _isPasswordVisible = !_isPasswordVisible),
                              ),
                              validator: (value) {
                                if (value == null || value.isEmpty) {
                                  return 'password_required'.tr();
                                }
                                return null;
                              },
                            ),
                            const SizedBox(height: 20),

                            // Login button
                            _PrimaryLoginButton(
                              isLoading: _isLoading,
                              onPressed: _isLoading ? null : _login,
                            ),
                            const SizedBox(height: 16),

                            const _OrDivider(),
                            const SizedBox(height: 16),

                            // Google sign-in
                            _GoogleSignInButton(
                              onPressed: _isLoading ? null : _loginWithGoogle,
                            ),
                            const SizedBox(height: 28),

                            // Contact dispatch pill
                            Center(
                              child: _ContactDispatchPill(
                                onTap: _onForgotPassword,
                              ),
                            ),
                            const SizedBox(height: 24),
                          ],
                        ),
                      ),
                    ),
                  ),
                ),

                // Footer
                _FooterControls(
                  versionNumber: _versionNumber,
                  versionDisplay: _versionDisplay,
                ),
              ],
            ),
          ),

          // Theme switcher pinned top-right
          Positioned(
            top: 12,
            right: 12,
            child: SafeArea(
              child: _ThemeToggleButton(isDark: isDark),
            ),
          ),
        ],
      ),
    );
  }
}

// =================== sub-widgets ===================

class _BrandHeader extends StatelessWidget {
  const _BrandHeader();

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    return Column(
      children: [
        Container(
          padding: const EdgeInsets.all(14),
          decoration: BoxDecoration(
            color: scheme.primaryContainer,
            borderRadius: BorderRadius.circular(12),
            boxShadow: [
              BoxShadow(
                color: scheme.primaryContainer.withValues(alpha: 0.35),
                blurRadius: 20,
                offset: const Offset(0, 6),
              ),
            ],
          ),
          child: Icon(
            Icons.local_shipping,
            size: 36,
            color: scheme.onPrimaryContainer,
          ),
        ),
        const SizedBox(height: 14),
        Text(
          'LogiTrack',
          style: theme.textTheme.headlineSmall?.copyWith(
            color: scheme.primary,
            fontWeight: FontWeight.w800,
            letterSpacing: -0.3,
          ),
        ),
        const SizedBox(height: 10),
        Text(
          'welcome_back'.tr(),
          style: theme.textTheme.titleMedium?.copyWith(
            color: scheme.onSurface,
            fontWeight: FontWeight.w600,
          ),
        ),
        const SizedBox(height: 2),
        Text(
          'welcome_subtitle'.tr(),
          textAlign: TextAlign.center,
          style: theme.textTheme.bodyMedium?.copyWith(
            color: scheme.onSurfaceVariant,
          ),
        ),
      ],
    );
  }
}

class _FieldLabel extends StatelessWidget {
  const _FieldLabel({required this.text});
  final String text;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Padding(
      padding: const EdgeInsets.only(left: 4),
      child: Text(
        text,
        style: Theme.of(context).textTheme.labelMedium?.copyWith(
              color: scheme.onSurfaceVariant,
              fontWeight: FontWeight.w500,
              letterSpacing: 0.5,
            ),
      ),
    );
  }
}

class _ForgotPasswordLink extends StatelessWidget {
  const _ForgotPasswordLink({required this.onTap});
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return GestureDetector(
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 4),
        child: Text(
          'forgot_password'.tr(),
          style: TextStyle(
            color: scheme.primary,
            fontWeight: FontWeight.w500,
            fontSize: 12,
            letterSpacing: 0.5,
          ),
        ),
      ),
    );
  }
}

class _StitchTextField extends StatelessWidget {
  const _StitchTextField({
    required this.controller,
    required this.hintText,
    required this.icon,
    this.obscureText = false,
    this.keyboardType,
    this.textInputAction,
    this.onFieldSubmitted,
    this.validator,
    this.suffixIcon,
  });

  final TextEditingController controller;
  final String hintText;
  final IconData icon;
  final bool obscureText;
  final TextInputType? keyboardType;
  final TextInputAction? textInputAction;
  final ValueChanged<String>? onFieldSubmitted;
  final FormFieldValidator<String>? validator;
  final Widget? suffixIcon;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return TextFormField(
      controller: controller,
      obscureText: obscureText,
      keyboardType: keyboardType,
      textInputAction: textInputAction,
      onFieldSubmitted: onFieldSubmitted,
      autocorrect: false,
      style: TextStyle(
        color: scheme.onSurface,
        fontSize: 16,
        letterSpacing: 0.5,
      ),
      decoration: InputDecoration(
        filled: true,
        fillColor: scheme.surfaceContainer,
        prefixIcon: Padding(
          padding: const EdgeInsets.only(left: 14, right: 8),
          child: Icon(icon, size: 20, color: scheme.outline),
        ),
        prefixIconConstraints: const BoxConstraints(minWidth: 42),
        suffixIcon: suffixIcon,
        hintText: hintText,
        hintStyle: TextStyle(
          color: scheme.outline,
          fontSize: 16,
          letterSpacing: 0.5,
        ),
        contentPadding:
            const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(8),
          borderSide: BorderSide(color: scheme.outlineVariant),
        ),
        enabledBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(8),
          borderSide: BorderSide(color: scheme.outlineVariant),
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(8),
          borderSide: BorderSide(color: scheme.primary, width: 1.5),
        ),
        errorBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(8),
          borderSide: BorderSide(color: scheme.error),
        ),
        focusedErrorBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(8),
          borderSide: BorderSide(color: scheme.error, width: 1.5),
        ),
      ),
      validator: validator,
    );
  }
}

class _PrimaryLoginButton extends StatelessWidget {
  const _PrimaryLoginButton({required this.isLoading, required this.onPressed});

  final bool isLoading;
  final VoidCallback? onPressed;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return SizedBox(
      height: 48,
      child: ElevatedButton(
        onPressed: onPressed,
        style: ElevatedButton.styleFrom(
          backgroundColor: scheme.primaryContainer,
          foregroundColor: scheme.onPrimaryContainer,
          disabledBackgroundColor:
              scheme.primaryContainer.withValues(alpha: 0.4),
          disabledForegroundColor:
              scheme.onPrimaryContainer.withValues(alpha: 0.6),
          elevation: 4,
          shadowColor: scheme.primaryContainer.withValues(alpha: 0.5),
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(8),
          ),
          textStyle: const TextStyle(
            fontSize: 16,
            fontWeight: FontWeight.w600,
            letterSpacing: 0.15,
          ),
        ),
        child: isLoading
            ? SizedBox(
                height: 20,
                width: 20,
                child: CircularProgressIndicator(
                  strokeWidth: 2.2,
                  color: scheme.onPrimaryContainer,
                ),
              )
            : Row(
                mainAxisSize: MainAxisSize.min,
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  Text('login'.tr()),
                  const SizedBox(width: 8),
                  const Icon(Icons.login, size: 20),
                ],
              ),
      ),
    );
  }
}

class _OrDivider extends StatelessWidget {
  const _OrDivider();

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Row(
      children: [
        Expanded(child: Divider(color: scheme.outlineVariant, height: 1)),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 14),
          child: Text(
            'or'.tr(),
            style: TextStyle(
              color: scheme.outline,
              fontSize: 12,
              fontWeight: FontWeight.w500,
              letterSpacing: 0.5,
            ),
          ),
        ),
        Expanded(child: Divider(color: scheme.outlineVariant, height: 1)),
      ],
    );
  }
}

class _GoogleSignInButton extends StatelessWidget {
  const _GoogleSignInButton({required this.onPressed});
  final VoidCallback? onPressed;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return SizedBox(
      height: 48,
      child: OutlinedButton(
        onPressed: onPressed,
        style: OutlinedButton.styleFrom(
          backgroundColor: scheme.surfaceContainerHigh,
          foregroundColor: scheme.onSurface,
          side: BorderSide(color: scheme.outlineVariant),
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(8),
          ),
          textStyle: const TextStyle(
            fontSize: 16,
            fontWeight: FontWeight.w600,
            letterSpacing: 0.15,
          ),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            const _GoogleLogo(),
            const SizedBox(width: 12),
            Text('sign_in_google'.tr()),
          ],
        ),
      ),
    );
  }
}

class _GoogleLogo extends StatelessWidget {
  const _GoogleLogo();

  @override
  Widget build(BuildContext context) {
    return Container(
      width: 22,
      height: 22,
      decoration: const BoxDecoration(
        color: Colors.white,
        shape: BoxShape.circle,
      ),
      alignment: Alignment.center,
      child: const Text(
        'G',
        style: TextStyle(
          color: Color(0xFF4285F4),
          fontWeight: FontWeight.w700,
          fontSize: 14,
          height: 1.0,
        ),
      ),
    );
  }
}

class _ContactDispatchPill extends StatelessWidget {
  const _ContactDispatchPill({required this.onTap});
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Material(
      color: scheme.surfaceContainerLow,
      borderRadius: BorderRadius.circular(999),
      child: InkWell(
        borderRadius: BorderRadius.circular(999),
        onTap: onTap,
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
          decoration: BoxDecoration(
            borderRadius: BorderRadius.circular(999),
            border: Border.all(color: scheme.outlineVariant),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(
                Icons.headset_mic_outlined,
                size: 18,
                color: scheme.onSurfaceVariant,
              ),
              const SizedBox(width: 6),
              Text(
                '${'need_help'.tr()} ${'contact_dispatch'.tr()}',
                style: TextStyle(
                  color: scheme.onSurfaceVariant,
                  fontSize: 12,
                  fontWeight: FontWeight.w500,
                  letterSpacing: 0.5,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _ThemeToggleButton extends StatelessWidget {
  const _ThemeToggleButton({required this.isDark});
  final bool isDark;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Material(
      color: scheme.surfaceContainerHigh,
      shape: const CircleBorder(),
      elevation: 4,
      shadowColor: Colors.black.withValues(alpha: 0.3),
      child: InkWell(
        customBorder: const CircleBorder(),
        onTap: () => ThemeController().toggleTheme(),
        child: Container(
          width: 48,
          height: 48,
          decoration: BoxDecoration(
            shape: BoxShape.circle,
            border: Border.all(color: scheme.outlineVariant),
          ),
          child: Icon(
            isDark ? Icons.light_mode_outlined : Icons.dark_mode_outlined,
            size: 22,
            color: scheme.onSurface,
            semanticLabel: 'theme_toggle_tooltip'.tr(),
          ),
        ),
      ),
    );
  }
}

class _FooterControls extends StatelessWidget {
  const _FooterControls({
    required this.versionNumber,
    required this.versionDisplay,
  });

  final String versionNumber;
  final String versionDisplay;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Container(
      width: double.infinity,
      color: scheme.surfaceContainerLowest,
      padding: const EdgeInsets.fromLTRB(16, 14, 16, 16),
      child: Column(
        children: [
          // Language pill
          Container(
            padding: const EdgeInsets.all(4),
            decoration: BoxDecoration(
              color: scheme.surfaceContainer,
              borderRadius: BorderRadius.circular(999),
              border: Border.all(color: scheme.outlineVariant),
            ),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: const [
                _LanguageOption(code: 'en', label: 'EN'),
                SizedBox(width: 4),
                _LanguageOption(code: 'th', label: 'TH'),
              ],
            ),
          ),
          const SizedBox(height: 12),
          if (versionNumber.isNotEmpty)
            Text(
              '$versionNumber  •  LogiTrack Kinetic Systems',
              textAlign: TextAlign.center,
              style: TextStyle(
                color: scheme.outline,
                fontSize: 12,
                letterSpacing: 0.4,
              ),
            ),
          const SizedBox(height: 2),
          Text(
            versionDisplay,
            textAlign: TextAlign.center,
            style: TextStyle(
              color: scheme.outline.withValues(alpha: 0.6),
              fontSize: 10,
              letterSpacing: 0.3,
            ),
          ),
        ],
      ),
    );
  }
}

class _LanguageOption extends StatelessWidget {
  const _LanguageOption({required this.code, required this.label});
  final String code;
  final String label;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final isSelected = context.locale.languageCode == code;
    return GestureDetector(
      behavior: HitTestBehavior.opaque,
      onTap: () async {
        await context.setLocale(Locale(code));
      },
      child: AnimatedContainer(
        duration: const Duration(milliseconds: 150),
        padding: const EdgeInsets.symmetric(horizontal: 18, vertical: 6),
        decoration: BoxDecoration(
          color: isSelected ? scheme.secondaryContainer : Colors.transparent,
          borderRadius: BorderRadius.circular(999),
        ),
        child: Text(
          label,
          style: TextStyle(
            color: isSelected
                ? scheme.onSecondary
                : scheme.onSurfaceVariant.withValues(alpha: 0.85),
            fontWeight: FontWeight.w600,
            fontSize: 12,
            letterSpacing: 0.8,
          ),
        ),
      ),
    );
  }
}

class _BackgroundOrbs extends StatelessWidget {
  const _BackgroundOrbs();

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return IgnorePointer(
      child: Stack(
        children: [
          Positioned(
            top: -100,
            left: -120,
            child: _Orb(color: scheme.secondary, size: 300, sigma: 100),
          ),
          Positioned(
            bottom: -180,
            right: -160,
            child: _Orb(color: scheme.primary, size: 500, sigma: 120),
          ),
        ],
      ),
    );
  }
}

class _Orb extends StatelessWidget {
  const _Orb({
    required this.color,
    required this.size,
    required this.sigma,
  });

  final Color color;
  final double size;
  final double sigma;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: size,
      height: size,
      child: ImageFiltered(
        imageFilter: ImageFilter.blur(sigmaX: sigma, sigmaY: sigma),
        child: Container(
          decoration: BoxDecoration(
            shape: BoxShape.circle,
            color: color.withValues(alpha: 0.10),
          ),
        ),
      ),
    );
  }
}
