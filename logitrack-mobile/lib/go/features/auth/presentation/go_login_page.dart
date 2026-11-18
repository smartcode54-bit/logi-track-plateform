import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/material.dart';
import 'package:flutter_bloc/flutter_bloc.dart';

import '../../../app/go_badge.dart';
import '../../../core/error_text.dart';
import 'login_bloc.dart';

/// The gotest login (spec mobile-go-core-loop R3, R7, R16, R17): email and password against the Go API. Google
/// sign-in and "forgot password" are not in this slice (shown disabled).
class GoLoginPage extends StatefulWidget {
  const GoLoginPage({super.key, this.messageKey, required this.versionLabel});

  /// An i18n key to show above the form (session_expired_relogin, ...).
  final String? messageKey;
  final String versionLabel;

  @override
  State<GoLoginPage> createState() => _GoLoginPageState();
}

class _GoLoginPageState extends State<GoLoginPage> {
  final _form = GlobalKey<FormState>();
  final _email = TextEditingController();
  final _password = TextEditingController();

  @override
  void dispose() {
    _email.dispose();
    _password.dispose();
    super.dispose();
  }

  void _submit(BuildContext context) {
    if (!(_form.currentState?.validate() ?? false)) return;
    context.read<LoginBloc>().add(LoginSubmitted(_email.text, _password.text));
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: SafeArea(
        child: BlocBuilder<LoginBloc, LoginState>(builder: (context, state) {
          return ListView(padding: const EdgeInsets.all(24), children: [
            const SizedBox(height: 32),
            Row(mainAxisAlignment: MainAxisAlignment.center, children: [
              Text('LogiTrack', style: Theme.of(context).textTheme.headlineMedium),
              const GoBadge(),
            ]),
            const SizedBox(height: 8),
            Text(widget.versionLabel, textAlign: TextAlign.center, style: Theme.of(context).textTheme.bodySmall),
            const SizedBox(height: 24),
            if (widget.messageKey != null) _Notice(text: widget.messageKey!.tr()),
            if (state.error != null)
              _Notice(
                text: apiErrorText(state.error!, overrides: const {
                  'invalid_credentials': 'login_error_invalid_credentials',
                  'account_disabled': 'login_error_account_disabled',
                  'driver_profile_required': 'login_error_driver_profile_required',
                  'no_account': 'login_error_invalid_credentials',
                }),
                error: true,
              ),
            Form(
              key: _form,
              child: Column(children: [
                TextFormField(
                  controller: _email,
                  keyboardType: TextInputType.emailAddress,
                  autofillHints: const [AutofillHints.email],
                  decoration: InputDecoration(labelText: 'email'.tr()),
                  validator: (v) => (v ?? '').trim().isEmpty ? 'email_required'.tr() : null,
                ),
                const SizedBox(height: 12),
                TextFormField(
                  controller: _password,
                  obscureText: true,
                  autofillHints: const [AutofillHints.password],
                  decoration: InputDecoration(labelText: 'password'.tr()),
                  validator: (v) => (v ?? '').isEmpty ? 'password_required'.tr() : null,
                  onFieldSubmitted: (_) => _submit(context),
                ),
              ]),
            ),
            const SizedBox(height: 24),
            FilledButton(
              onPressed: state.busy ? null : () => _submit(context),
              child: state.busy
                  ? const SizedBox(height: 20, width: 20, child: CircularProgressIndicator(strokeWidth: 2))
                  : Text('login'.tr()),
            ),
            const SizedBox(height: 12),
            OutlinedButton.icon(
              onPressed: null,
              icon: const Icon(Icons.g_mobiledata),
              label: Text('gotest_not_available_title'.tr()),
            ),
          ]);
        }),
      ),
    );
  }
}

class _Notice extends StatelessWidget {
  const _Notice({required this.text, this.error = false});
  final String text;
  final bool error;

  @override
  Widget build(BuildContext context) {
    final cs = Theme.of(context).colorScheme;
    return Container(
      margin: const EdgeInsets.only(bottom: 16),
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: error ? cs.errorContainer : cs.secondaryContainer,
        borderRadius: BorderRadius.circular(8),
      ),
      child: Text(text, style: TextStyle(color: error ? cs.onErrorContainer : cs.onSecondaryContainer)),
    );
  }
}
