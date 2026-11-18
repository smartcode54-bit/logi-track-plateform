import 'dart:convert';

import 'package:flutter_bloc/flutter_bloc.dart';

import '../../core/api/api_exception.dart';
import '../../core/api/op_id.dart';
import '../../core/api/upload_client.dart';
import 'photo_capture.dart';

/// One photo slot of an evidence form.
class EvidenceSlot {
  const EvidenceSlot(this.id, {required this.labelKey, this.screenshot = false, this.required = true});

  /// The photo type (trip photo type, or checkin_photo / checkin_app).
  final String id;
  final String labelKey;

  /// A customer-app screenshot (gallery, no overlay) rather than a camera photo.
  final bool screenshot;
  final bool required;
}

sealed class EvidenceEvent {}

class EvidencePhotoSet extends EvidenceEvent {
  EvidencePhotoSet(this.slot, this.photo);
  final String slot;
  final CapturedPhoto photo;
}

/// The form's non-photo fields changed (they are part of the submit's fingerprint, R15).
class EvidenceFieldsChanged extends EvidenceEvent {
  EvidenceFieldsChanged(this.fields);
  final Map<String, Object?> fields;
}

class EvidenceSubmitted extends EvidenceEvent {}

class EvidenceState {
  const EvidenceState({
    this.photos = const {},
    this.fields = const {},
    this.busy = false,
    this.done = 0,
    this.total = 0,
    this.error,
    this.missingKey,
    this.result,
  });

  final Map<String, CapturedPhoto> photos;
  final Map<String, Object?> fields;
  final bool busy;

  /// Upload progress: photos uploaded / to upload in this submit.
  final int done;
  final int total;

  /// The submit's failure ([ApiException] / [NetworkException]); the form and the photos stay (R15).
  final Object? error;

  /// An i18n key when the form is incomplete (a required photo or field is missing).
  final String? missingKey;

  /// What the submit answered; non-null = finished.
  final Object? result;

  EvidenceState copyWith({
    Map<String, CapturedPhoto>? photos,
    Map<String, Object?>? fields,
    bool? busy,
    int? done,
    int? total,
    Object? error,
    String? missingKey,
    Object? result,
    bool clearError = false,
  }) =>
      EvidenceState(
        photos: photos ?? this.photos,
        fields: fields ?? this.fields,
        busy: busy ?? this.busy,
        done: done ?? this.done,
        total: total ?? this.total,
        error: clearError ? null : (error ?? this.error),
        missingKey: clearError ? null : (missingKey ?? this.missingKey),
        result: result ?? this.result,
      );
}

/// The shared engine of the check-in, loading and delivery forms (spec mobile-go-core-loop R12-R15): it keeps the
/// photos, uploads each one once (a retry reuses the keys already uploaded), and runs [submit] with the submit's
/// `Idempotency-Key`. The key is made when the driver first presses submit and reused for every retry of the same
/// form; a change to the fields or the photos makes a new one (a different request must not replay the old answer).
abstract class EvidenceFormBloc extends Bloc<EvidenceEvent, EvidenceState> {
  EvidenceFormBloc({required this.uploads, required this.slots}) : super(const EvidenceState()) {
    on<EvidencePhotoSet>((e, emit) {
      _keys.remove(e.slot);
      emit(state.copyWith(photos: {...state.photos, e.slot: e.photo}, clearError: true));
    });
    on<EvidenceFieldsChanged>((e, emit) => emit(state.copyWith(fields: e.fields, clearError: true)));
    on<EvidenceSubmitted>(_submit);
  }

  final UploadClient uploads;
  final List<EvidenceSlot> slots;

  final Map<String, String> _keys = {};
  String? _opId;
  String? _opFingerprint;

  /// The upload purpose of [slot] (checkin_photo, checkin_app_screenshot, trip_photo).
  String purposeOf(EvidenceSlot slot);

  /// The entity the uploads of this submit belong to ([opId] for a trip that does not exist yet, D9).
  String entityIdFor(String opId);

  /// An i18n key when a non-photo field is missing or invalid; null = complete.
  String? validateFields(Map<String, Object?> fields) => null;

  /// The write itself. [keys] maps each slot to its uploaded object key.
  Future<Object> submit(String opId, Map<String, String> keys, EvidenceState state);

  Future<void> _submit(EvidenceSubmitted e, Emitter<EvidenceState> emit) async {
    if (state.busy) return;
    for (final s in slots) {
      if (s.required && !state.photos.containsKey(s.id)) {
        emit(state.copyWith(missingKey: s.labelKey, clearError: false));
        return;
      }
    }
    final fieldError = validateFields(state.fields);
    if (fieldError != null) {
      emit(state.copyWith(missingKey: fieldError));
      return;
    }
    final fingerprint = _fingerprint();
    if (_opId == null || _opFingerprint != fingerprint) {
      _opId = newOpId();
      _opFingerprint = fingerprint;
    }
    final opId = _opId!;
    final todo = [for (final s in slots) if (state.photos.containsKey(s.id) && !_keys.containsKey(s.id)) s];
    emit(state.copyWith(busy: true, done: 0, total: todo.length, clearError: true));
    try {
      var done = 0;
      for (final s in todo) {
        _keys[s.id] = await uploads.uploadJpeg(
          purpose: purposeOf(s),
          entityId: entityIdFor(opId),
          bytes: state.photos[s.id]!.bytes,
          variant: purposeOf(s) == 'trip_photo' ? s.id : null,
        );
        done++;
        emit(state.copyWith(done: done));
      }
      final result = await submit(opId, Map.unmodifiable(_keys), state);
      emit(state.copyWith(busy: false, result: result));
    } on ApiException catch (err) {
      emit(state.copyWith(busy: false, error: err));
    } on NetworkException catch (err) {
      emit(state.copyWith(busy: false, error: err));
    }
  }

  /// The identity of the request a submit sends: the fields and which photo fills each slot.
  String _fingerprint() => jsonEncode({
        'fields': state.fields.map((k, v) => MapEntry(k, v?.toString())),
        'photos': {for (final e in state.photos.entries) e.key: identityHashCode(e.value)},
      });
}
