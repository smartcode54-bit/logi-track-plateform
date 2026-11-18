import 'package:flutter_bloc/flutter_bloc.dart';

import '../../../../core/api/api_exception.dart';
import '../../../core/session/session_bloc.dart';
import '../data/go_auth_repository.dart';

sealed class LoginEvent {}

class LoginSubmitted extends LoginEvent {
  LoginSubmitted(this.email, this.password);
  final String email;
  final String password;
}

class PasswordChangeSubmitted extends LoginEvent {
  PasswordChangeSubmitted({required this.email, required this.ticket, required this.newPassword});
  final String email;
  final String ticket;
  final String newPassword;
}

class LoginState {
  const LoginState({this.busy = false, this.error});
  final bool busy;

  /// The failure to show (an [ApiException] or [NetworkException]); null = none.
  final Object? error;
}

/// The login and change-password forms (R3, R5, R7): on success it hands the driver to [SessionBloc].
class LoginBloc extends Bloc<LoginEvent, LoginState> {
  LoginBloc({required this.auth, required this.session}) : super(const LoginState()) {
    on<LoginSubmitted>((e, emit) async {
      emit(const LoginState(busy: true));
      try {
        final me = await auth.login(e.email, e.password);
        session.add(SessionLoggedIn(me));
        emit(const LoginState());
      } on PasswordChangeRequired catch (p) {
        session.add(SessionPasswordChangeRequired(email: e.email, ticket: p.ticket));
        emit(const LoginState());
      } on ApiException catch (err) {
        emit(LoginState(error: err));
      } on NetworkException catch (err) {
        emit(LoginState(error: err));
      }
    });
    on<PasswordChangeSubmitted>((e, emit) async {
      emit(const LoginState(busy: true));
      try {
        final me = await auth.changePasswordAndLogin(e.email, e.ticket, e.newPassword);
        session.add(SessionLoggedIn(me));
        emit(const LoginState());
      } on PasswordChangeRequired catch (p) {
        session.add(SessionPasswordChangeRequired(email: e.email, ticket: p.ticket));
        emit(const LoginState());
      } on ApiException catch (err) {
        emit(LoginState(error: err));
      } on NetworkException catch (err) {
        emit(LoginState(error: err));
      }
    });
  }

  final GoAuthRepository auth;
  final SessionBloc session;
}
