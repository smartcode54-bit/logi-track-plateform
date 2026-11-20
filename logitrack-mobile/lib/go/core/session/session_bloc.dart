import 'package:flutter_bloc/flutter_bloc.dart';

import '../../../core/api/api_exception.dart';
import '../../features/auth/data/go_auth_repository.dart';
import '../../features/auth/domain/mobile_me.dart';

// --- events ------------------------------------------------------------------------------------------------------

sealed class SessionEvent {}

/// The app starts: resume a stored session if there is one (R4).
class SessionStarted extends SessionEvent {}

/// A login (or a password change) succeeded.
class SessionLoggedIn extends SessionEvent {
  SessionLoggedIn(this.me);
  final MobileMe me;
}

/// A login answered password_change_required (R5).
class SessionPasswordChangeRequired extends SessionEvent {
  SessionPasswordChangeRequired({required this.email, required this.ticket});
  final String email;
  final String ticket;
}

/// The driver logs out (R6).
class SessionLogoutRequested extends SessionEvent {}

/// The server ended the session (revoked, expired refresh): back to login with a message (R3).
class SessionEnded extends SessionEvent {}

// --- states ------------------------------------------------------------------------------------------------------

sealed class SessionState {}

class SessionUnknown extends SessionState {}

/// No session. [messageKey] is an i18n key to show on the login page (session_expired_relogin, ...).
class SessionUnauthenticated extends SessionState {
  SessionUnauthenticated({this.messageKey});
  final String? messageKey;
}

class SessionNeedsPassword extends SessionState {
  SessionNeedsPassword({required this.email, required this.ticket});
  final String email;
  final String ticket;
}

/// A cold start that could not reach the server: the stored session is kept; the driver retries (R4).
class SessionOffline extends SessionState {}

class SessionAuthenticated extends SessionState {
  SessionAuthenticated(this.me);
  final MobileMe me;
}

/// The gotest app's session (spec mobile-go-core-loop R3-R7): one source of truth for which top-level screen shows.
class SessionBloc extends Bloc<SessionEvent, SessionState> {
  SessionBloc(this.auth) : super(SessionUnknown()) {
    on<SessionStarted>(_onStarted);
    on<SessionLoggedIn>((e, emit) => emit(SessionAuthenticated(e.me)));
    on<SessionPasswordChangeRequired>((e, emit) => emit(SessionNeedsPassword(email: e.email, ticket: e.ticket)));
    on<SessionLogoutRequested>((e, emit) async {
      await auth.logout();
      emit(SessionUnauthenticated());
    });
    on<SessionEnded>((e, emit) {
      if (state is SessionUnauthenticated) return;
      emit(SessionUnauthenticated(messageKey: 'session_expired_relogin'));
    });
  }

  final GoAuthRepository auth;

  Future<void> _onStarted(SessionStarted e, Emitter<SessionState> emit) async {
    emit(SessionUnknown());
    try {
      final me = await auth.resume();
      emit(me == null ? SessionUnauthenticated() : SessionAuthenticated(me));
    } on NetworkException {
      emit(SessionOffline());
    } on ApiException catch (e) {
      emit(SessionUnauthenticated(
          messageKey: e.code == 'driver_profile_required' ? 'login_error_driver_profile_required' : null));
    }
  }
}
