import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/material.dart';
import 'package:flutter_bloc/flutter_bloc.dart';

import '../../../app/go_badge.dart';
import '../../../core/error_text.dart';
import '../../../core/session/session_bloc.dart';
import 'login_bloc.dart';

/// The forced password change after a temporary password (spec mobile-go-core-loop R5): redeems the ticket, then signs
/// in with the new password.
class GoChangePasswordPage extends StatefulWidget {
  const GoChangePasswordPage({super.key, required this.email, required this.ticket});
  final String email;
  final String ticket;

  @override
  State<GoChangePasswordPage> createState() => _GoChangePasswordPageState();
}

class _GoChangePasswordPageState extends State<GoChangePasswordPage> {
  final _form = GlobalKey<FormState>();
  final _pw = TextEditingController();
  final _confirm = TextEditingController();

  @override
  void dispose() {
    _pw.dispose();
    _confirm.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: goAppBar('password_change_title'.tr(), actions: [
        IconButton(
          tooltip: 'login'.tr(),
          icon: const Icon(Icons.close),
          onPressed: () => context.read<SessionBloc>().add(SessionLogoutRequested()),
        ),
      ]),
      body: BlocBuilder<LoginBloc, LoginState>(builder: (context, state) {
        final error = state.error;
        return ListView(padding: const EdgeInsets.all(24), children: [
          Text('password_change_body'.tr()),
          const SizedBox(height: 16),
          if (error != null)
            Padding(
              padding: const EdgeInsets.only(bottom: 12),
              child: Text(
                apiErrorText(error, overrides: const {
                  'invalid_argument': 'password_change_invalid',
                  'invalid_token': 'password_change_expired',
                }),
                style: TextStyle(color: Theme.of(context).colorScheme.error),
              ),
            ),
          Form(
            key: _form,
            child: Column(children: [
              TextFormField(
                controller: _pw,
                obscureText: true,
                decoration: InputDecoration(labelText: 'password_new_label'.tr()),
                validator: (v) => (v ?? '').isEmpty ? 'password_required'.tr() : null,
              ),
              const SizedBox(height: 12),
              TextFormField(
                controller: _confirm,
                obscureText: true,
                decoration: InputDecoration(labelText: 'password_confirm_label'.tr()),
                validator: (v) => v != _pw.text ? 'password_mismatch'.tr() : null,
              ),
            ]),
          ),
          const SizedBox(height: 24),
          FilledButton(
            onPressed: state.busy
                ? null
                : () {
                    if (!(_form.currentState?.validate() ?? false)) return;
                    context.read<LoginBloc>().add(PasswordChangeSubmitted(
                        email: widget.email, ticket: widget.ticket, newPassword: _pw.text));
                  },
            child: Text('password_change_submit'.tr()),
          ),
        ]);
      }),
    );
  }
}
