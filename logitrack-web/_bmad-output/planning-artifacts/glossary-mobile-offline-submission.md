# Mobile Offline Submission — Glossary

Captured during the field-test grilling session, 2026-07-06 ([ADR-0007](adr/ADR-0007-mobile-loading-submission-retry-and-seal-dedup.md)). Definitions here are authoritative for the queue-block / seal-dedup fix work.

- **Loading phase (รับสินค้า)** — the pickup-side form the driver fills after Check-In: scans the run sheet (OCR), photographs the seal/parcels, enters seal code and parcel count, then submits to create the `trip_records` document. Distinct from **Delivery phase** (ส่งสินค้า), which happens later and is what actually marks a trip `Delivered`.

- **tripId (run-sheet ID)** — the physical run-sheet identifier printed/scanned on the job's paper document (e.g. `ZX...` for J&T sheets), captured via OCR or typed manually. Doubles as the **Firestore document ID** for `trip_records/{tripId}` — writes use `.doc(tripId).set(..., merge: true)`, so a clean resubmission with the *exact same* tripId string is idempotent and self-excludes from the seal-duplicate check. Any character-level difference (whitespace, case, OCR misread) produces a **different** document.

- **sealCode** — the physical security seal number recorded on the shipment. Checked for uniqueness across *all* `trip_records` (not scoped to a single driver) via `checkDuplicateTripIdAndSeal`, excluding the current tripId's own document. A genuine collision means some other document — under a different tripId — already carries this seal.

- **checkDuplicateTripIdAndSeal** — the client-side (no server-side/Cloud Function equivalent exists) duplicate guard in `trip_records_repository.dart`. Runs as a single point-in-time read; `tripIdExists` blocks unconditionally if *any* document exists at that ID (even the driver's own earlier successful write), `sealCodeExists` blocks only if a *different* document ID shares the seal.

- **Check-in queue gate** — `isQueueEligibleForCheckIn` / `taskBlocksSuccessorInQueue` (`task_repository.dart`). Enforces "one active job at a time" per driver: a task cannot be checked into if an earlier task in the driver's sorted queue is not `Completed` / `Cancelled` / `Delivered` (or `Checked in` with a matching delivered trip). Has no visibility into *why* a predecessor is stuck — a Loading form that silently failed to submit looks identical, from this gate's point of view, to a job the driver simply hasn't started yet.

- **"Checked in" limbo** — the state a task is left in when Check-In succeeds (`tasks.status = "Checked in"`) but the subsequent Loading-phase submission never completes. Nothing else advances the task's status, so it blocks the Check-in queue gate indefinitely until the driver manually resubmits.

- **LoadingDraft** — local (SharedPreferences + disk) autosave of the in-progress Loading form, restored on next page open (`draft_storage_service.dart`). Purely a UI convenience — it does **not** retry the Firestore submission itself; connectivity recovery only re-validates trip existence, never re-POSTs a queued write.

- **SavedTripSummary / pending delivery summary** — the app's local record that a trip is "in flight" for this driver (`main_layout.dart`, key `prefKeyPendingDeliverySummary`), used to block starting a new pickup while one is outstanding. Cleared only when the linked trip is confirmed `delivered` (or missing/cancelled) from the server — never cleared just because a Loading submission attempt was made.

- **Check-then-act gap (submission race)** — the async distance in `_doSubmit()` between the one-shot duplicate check (top of the function) and the actual Firestore write (after GPS lookup + multi-photo upload). Under flaky connectivity this gap is wide enough for the underlying write to eventually land while the driver, seeing no confirmation, believes the attempt failed and retries — the suspected trigger for the seal-duplicate false positive (ADR-0007, root cause #4).

- **Cache-fallback read (offline persistence)** — `cloud_firestore`'s default `.get()` behavior: try the server, fall back to the on-device cache if unreachable. Firestore's local cache is updated **immediately and synchronously** the instant `.set()`/`.update()` is called, before the server has acknowledged anything — so a plain `.get()` while offline can return **this device's own not-yet-synced pending write**, indistinguishable from genuinely server-committed data. This is the confirmed mechanism behind the seal-duplicate false positive (ADR-0007, root cause #4): the duplicate check never distinguishes "committed on the server" from "only pending in my own write queue."
