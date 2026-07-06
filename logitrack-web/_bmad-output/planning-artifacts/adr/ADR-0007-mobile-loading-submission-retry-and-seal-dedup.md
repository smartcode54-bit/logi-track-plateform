# ADR-0007: Mobile offline Loading-phase submission — Check-in queue block + seal-duplicate false positive

- **Status:** Proposed (2026-07-06) — root cause confirmed, fix not yet implemented
- **Context owners:** Mobile / Driver Operations
- **Related:** `task_repository.dart`, `trip_records_repository.dart`, `loading_phase_page.dart`, `loading_trip_repository.dart`, `draft_storage_service.dart`, `main_layout.dart`, `check_in_page.dart`

## Context

Field test (2026-07-06, admin driving-test session) surfaced two related mobile bugs:

1. **"ที่จังหวะอินเตอร์เน็ตกลับมา ระบบไม่รับข้อมูลใบงานนั้น"** — reconfirmed via interview: the failure is specifically at **Check-In** ("ผมทำการ Check In แต่ระบบไม่รับ"), for a **new** task, while the **previous** task's Loading phase (รับสินค้า) form was filled but never successfully submitted.
2. **"เลขซีลแจ้งว่ามีการใช้ซ้ำทั้งที่ใช้ครั้งแรก"** — the seal-duplicate error fires against **the same job's own** run sheet ("ชนกับใบงานเดียวกัน (ตัวมันเอง)"), not a different driver's job. Suspected downstream of bug #1.

## Root cause chain (evidence-graded)

**[Verified] 1. The Check-In queue gate has no concept of "stuck, unsubmitted Loading form."**
`isQueueEligibleForCheckIn` / `taskBlocksSuccessorInQueue` (`lib/features/home/data/repositories/task_repository.dart:63-98`) block Check-In on any task if an *earlier* task for the same driver is not `Completed` / `Cancelled` / `Delivered` (or `Checked in` **and** already has a delivered trip, per `getDeliveredTaskIdsForDriver`). A task that reached `Checked in` and then had its Loading-phase submission fail silently under bad connectivity has no other status transition available — it never becomes `Delivered`, so it **permanently blocks every later Check-In** for that driver. The only feedback shown is a generic `please_finish_ongoing_task_first` snackbar (`check_in_page.dart:632-641`) — it never tells the driver *which* task is stuck or that a Loading form is still sitting in a local draft.

**[Verified] 2. There is no connectivity-triggered retry.** `draft_storage_service.dart` (LoadingDraft) and `main_layout.dart:232-284` (`SavedTripSummary` "pending delivery") only ever check whether a trip **still exists / is not yet delivered** when the app resumes or reconnects — nothing re-attempts the actual Firestore write. Recovery is 100% manual: the driver must notice, reopen the exact Loading form, and press Submit again.

**[Verified] 3. Duplicate-check and the actual write are split by a long async gap — a check-then-act race.** In `_doSubmit()` (`loading_phase_page.dart:1184-1220`), `checkDuplicateTripIdAndSeal` runs **once**, at the very top, as a plain point-in-time Firestore read. Only *after* it reports "no duplicate" does the code proceed through GPS lookup → photo overlay/stamping → compressing → uploading up to 4 images to Storage → the final `trip_records` Firestore write (comment at line 1183 calls this out as the slow part). Under exactly the flaky-connectivity conditions being field-tested, this gap is wide enough for the underlying write to land (immediately, or later via Firestore's local write queue) while the driver — seeing no confirmation, a stalled spinner, or a timeout — believes the submission failed and is still working with the original draft.

**[Confirmed by design/SDK behavior] 4. The duplicate check reads Firestore's on-device cache, which includes this device's own unsynced pending writes — and treats that identically to "confirmed on the server."** `checkDuplicateTripIdAndSeal` (`trip_records_repository.dart:42-79`) calls plain `.get()` / `.where(...).get()` with no `GetOptions` override. The app has no custom local seal cache anywhere (checked — none exists); "the database" *is* Firestore. But `cloud_firestore: ^6.4.1` (`pubspec.yaml:35`) has offline persistence on by default on mobile, and default `.get()` semantics are "try server, fall back to on-device cache if unreachable." Critically, the moment `_doSubmit()`'s final `.set(doc(tripId), {...sealCode...}, merge:true)` call executes, Firestore writes it into the **local cache immediately and synchronously** — before the server has ever acknowledged it, and likely before that `.set()` Future even resolves (the Future only completes on server ack, which is why the "saving..." spinner can hang indefinitely offline). From that instant, *this device's own local cache* reports that document/seal as existing, indistinguishable from a genuinely server-committed one.
   - A retry with the **exact same** tripId is still self-safe (`doc.id == tripId` exclusion holds), so it would surface as `tripIdExists` ("duplicate trip ID"), not specifically a seal error.
   - The **seal-specific** false positive the driver saw requires the retry to look up a **different** document ID than the first (pending, cached) attempt — most plausible trigger: re-running OCR on the same physical run sheet captures a tripId string differing by whitespace/casing/one misread character. The seal query then finds the *first attempt's own still-unsynced write*, sitting under that different ID, and reports it as someone else's seal.
   - **Directly answers the "is blocking on this even necessary?" question: no, not with the current unconditional logic.** The check should distinguish "committed on the server — a real conflict" from "only exists in my own device's pending write queue — not yet a real conflict." Today it cannot tell the difference, because both look identical from a cache-fallback `.get()`.

**Also verified, compounding the confusion:** `tripIdExists` in `checkDuplicateTripIdAndSeal` (`trip_records_repository.dart:39-53`) blocks unconditionally the moment *any* document exists at that tripId — including the driver's own earlier, successfully-landed-but-unconfirmed write. So a same-tripId retry after a "silent success" is correctly rejected as a trip-ID duplicate, but with no way for the driver to tell "this is your own already-submitted job" apart from "someone else already used this run sheet."

## Decision (proposed fix direction — implementation not yet started)

1. **Give the queue-block error a cause.** When a task blocks Check-In because a predecessor task is stuck `Checked in` with an undelivered trip, tell the driver *which* task and offer a "resume Loading form" action instead of a generic snackbar.
2. **Add reconnect-triggered auto-flush.** When the app regains connectivity and finds a persisted `LoadingDraft` / pending `SavedTripSummary`, attempt a silent resubmission before requiring driver action; fall back to the manual UI only if that attempt fails.
3. **Close the check-then-act gap.** Re-check duplicate tripId/seal immediately before the Firestore write (or fold both into one transaction/guard on the `trip_records/{tripId}` document), not as a disconnected pre-flight read minutes before the write actually happens.
4. **Normalize tripId before it becomes a lookup/document key** (trim, case-fold, collapse whitespace) so OCR noise cannot mint a second document for the same physical run sheet. If a normalized-match document already exists for the *same driver*, offer "resume/view existing submission" instead of a hard "duplicate" block.
5. **Give the driver positive confirmation.** A clear "submitted" receipt/state so there is never ambiguity about whether the previous attempt actually landed — removing the incentive to blindly retry.
6. **(Highest-confidence, most surgical) Force the duplicate-check reads to the server, not cache.** Pass `GetOptions(source: Source.server)` to both the `tripId` doc `.get()` and the `sealCode` `.where(...).get()` in `checkDuplicateTripIdAndSeal`. Online behavior is unchanged (zero regression). Offline, the calls now correctly *fail* (server unreachable) instead of silently answering from a possibly-pending, unconfirmed local cache; the caller should catch that specific failure and tell the driver "ตรวจสอบซีลซ้ำไม่ได้ตอนออฟไลน์ จะตรวจสอบอีกครั้งเมื่อเน็ตกลับมา" rather than either falsely blocking (root cause #4) or falsely passing.

## Consequences

- Fixes require touching `task_repository.dart` (blocking message + resume affordance), `main_layout.dart` / `draft_storage_service.dart` (reconnect flush), `loading_phase_page.dart` / `loading_trip_repository.dart` (atomic duplicate guard, tripId normalization), and probably new i18n strings (en/th).
- No schema migration needed — this is app-logic only; `trip_records` document shape is unchanged.
- Until implemented, drivers hitting poor connectivity during Loading-phase submission remain at risk of a permanently stuck queue and confusing seal-duplicate errors; the only current mitigation is admin intervention (manually clearing/resolving the stuck task via web admin).

## Alternatives considered

- **Only improve the error message, no auto-retry** — rejected: does not fix the underlying stuck-job state; drivers would still be fully blocked until they manually notice and resubmit.
- **Fully automatic silent retry with no tripId normalization** — rejected: does not address the OCR-variance seal-collision path (hypothesis #4), which is the more field-relevant failure given OCR is used on every submission.

## Open questions

- Whether two `trip_records` documents with near-identical tripIds and the same `sealCode` actually exist for the reported incident (would fully confirm the OCR-variance trigger for decision #4) — not required to justify decision #6, which is correct regardless.
- Fix implementation is out of scope for this ADR per explicit instruction — track as a follow-up task once this document is reviewed.
