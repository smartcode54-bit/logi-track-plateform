import 'dart:async';

import 'package:flutter_bloc/flutter_bloc.dart';

import '../../../../core/api/api_exception.dart';
import '../../trips/data/go_trip_repository.dart';
import '../../trips/domain/mobile_trip.dart';
import '../data/go_task_repository.dart';
import '../domain/driver_task.dart';

sealed class TaskListEvent {}

/// Load now (start, pull-to-refresh, resume, after a write).
class TaskListRefreshed extends TaskListEvent {}

/// Home is visible and the app in the foreground: poll every 60 s (R10, N5).
class TaskListPollingStarted extends TaskListEvent {}

/// Home hidden or the app in the background: stop polling.
class TaskListPollingStopped extends TaskListEvent {}

class TaskListState {
  const TaskListState({
    this.tasks = const [],
    this.pendingTrip,
    this.loading = false,
    this.error,
    this.updatedAt,
  });

  final List<DriverTask> tasks;

  /// The driver's in-transit trip whose task is not in the queue any more (resume, R14).
  final MobileTrip? pendingTrip;
  final bool loading;

  /// The last refresh's failure; the previous list stays shown (R10).
  final Object? error;
  final DateTime? updatedAt;

  TaskListState copyWith({
    List<DriverTask>? tasks,
    MobileTrip? pendingTrip,
    bool clearPending = false,
    bool? loading,
    Object? error,
    bool clearError = false,
    DateTime? updatedAt,
  }) =>
      TaskListState(
        tasks: tasks ?? this.tasks,
        pendingTrip: clearPending ? null : (pendingTrip ?? this.pendingTrip),
        loading: loading ?? this.loading,
        error: clearError ? null : (error ?? this.error),
        updatedAt: updatedAt ?? this.updatedAt,
      );
}

/// The driver's queue on Home (spec mobile-go-core-loop R10, R14): pulled, never pushed in this slice.
class TaskListBloc extends Bloc<TaskListEvent, TaskListState> {
  TaskListBloc({required this.tasks, required this.trips, this.interval = const Duration(seconds: 60)})
      : super(const TaskListState()) {
    on<TaskListRefreshed>(_refresh);
    on<TaskListPollingStarted>((e, emit) {
      _timer?.cancel();
      _timer = Timer.periodic(interval, (_) => add(TaskListRefreshed()));
    });
    on<TaskListPollingStopped>((e, emit) {
      _timer?.cancel();
      _timer = null;
    });
  }

  final GoTaskRepository tasks;
  final GoTripRepository trips;
  final Duration interval;
  Timer? _timer;

  Future<void> _refresh(TaskListRefreshed e, Emitter<TaskListState> emit) async {
    if (state.loading) return;
    emit(state.copyWith(loading: true));
    try {
      final list = await tasks.openQueue();
      final pending = await trips.pendingInTransit();
      final orphan = pending != null && !list.any((t) => t.trip?.id == pending.id || t.id == pending.taskId);
      emit(state.copyWith(
        tasks: list,
        pendingTrip: orphan ? pending : null,
        clearPending: !orphan,
        loading: false,
        clearError: true,
        updatedAt: DateTime.now(),
      ));
    } on ApiException catch (err) {
      emit(state.copyWith(loading: false, error: err));
    } on NetworkException catch (err) {
      emit(state.copyWith(loading: false, error: err));
    }
  }

  @override
  Future<void> close() {
    _timer?.cancel();
    return super.close();
  }
}
