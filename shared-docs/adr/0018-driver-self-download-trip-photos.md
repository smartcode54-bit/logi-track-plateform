# ADR 0018 — Driver self-download of trip evidence photos to the phone gallery

- **Status:** Accepted (2026-08-22) — **shipped 2026-08-23** (device-tested by owner, committed + pushed to `main`). Built via spec `mobile-download-trip-photos`. Includes follow-ups: per-photo step labels, full-screen swipe gallery, delivery-delay section (green "no delay" banner + 404 drop), Trip History server-first refresh, and a bottom-bar safe-area sweep.
- **Deciders:** Samart Kas (product owner), Claude
- **Area:** logitrack-mobile (Trip History + a new per-trip photo viewer), Firebase Storage (read only). No web, no Cloud Functions, no schema change.

> **Amendment (2026-08-22).** The owner extended the scope during spec-writing: **incident photos linked
> to the trip are now included** in the viewer and the bulk download. This reverses the "incident out of
> scope" half of Decision 1 / the corresponding "Alternatives considered" rejection below — standby stays
> out. Incidents live in the **`incidentReport`** collection (camelCase; not `incident_reports`), linked
> by `incidentReport.tripId == trip.id`, storing photos as **three nullable URL fields**
> (`mapPhotoUrl` / `situation1PhotoUrl` / `situation2PhotoUrl`, `incident_report_repository.dart:81-96`),
> read-public at `incident_reports/**` (`storage.rules:41`). They sort as a trailing workflow group
> (after multi-stop). Specced in `shared-docs/specs/mobile-download-trip-photos.md` (R11).

## Context

The owner wants drivers (พขร.) to **download their own work-record photos from the mobile app** —
"ให้ พขร. สามารถ down รูปภาพจากการบันทึกการทำงานได้เองจาก mobile."

Facts established during grilling, grounded in code:

1. **These are proof-of-work [[Evidence photo|evidence photos]], not casual snapshots.** Every
   loading/delivery photo is passed through `photo_overlay_service.dart` before upload, which bakes a
   burned-in overlay into the JPEG: GPS coordinates, reverse-geocoded address, a Thai-Buddhist-era
   timestamp, `LogiTrack Pro` branding, and a Google-Maps QR code
   (`logitrack-mobile/lib/features/home/data/services/photo_overlay_service.dart:177`). The
   **pre-overlay original is never stored** — the overlay is baked before `uploadTripPhoto`.
2. **Storage read is already public.** `match /trip_records/{allPaths=**} { allow read: if true }`
   (`logitrack-web/storage.rules:35`). The `url` stored on each photo is a working download URL; the
   photos live at `trip_records/{tripId}/{photoType}.jpg`
   (`trip_records_repository.dart:10,217-228`) and are referenced in `trip_records.photos[]` as
   `{url, type, geocoding}` (`home/data/models/trip_record.dart:274`).
3. **🔴 There is no viewing surface today.** The Trip History page renders **text only** — trip id,
   route, times, status — and never touches `.photos`
   (`features/trip_history/presentation/pages/trip_history_page.dart:586`). `JobRecordPage` is a
   hardcoded mock ("BKK Hub → Chiang Mai", "#TRK-8821")
   (`features/job_record/presentation/pages/job_record_page.dart:83`). So this feature must **build
   the viewer**, not merely add a button.
4. **No save-to-gallery machinery exists.** `pubspec.yaml:31` has `image_picker`, `path_provider`,
   `firebase_storage`, `http`, `url_launcher` — but **no** gallery-saver plugin (`gal` /
   `saver_gallery`) and **no** `permission_handler`. Every "gallery" reference in the code is
   `ImageSource.gallery` (picking *from* the gallery), never saving *to* it.
5. **The flat `photos[]` array is the complete set.** Multi-drop delivery writes each stop's photos to
   **both** `deliveryStopsProgress[].photos` **and** the merged flat `photos[]`
   (`delivery_phase/data/repositories/delivery_trip_repository.dart:187-195`), and the mobile model
   reads the flat array (`trip_record.dart:101`). So `TripRecord.photos` already carries loading +
   single-delivery + per-stop photos. **No model or query change is needed to enumerate a trip's
   photos.** (`DeliveryStopProgress` has no photos field — the per-stop copy is redundant here.)
6. **[[Photo type]] taxonomy:** loading = `runsheet`, `runsheet_extra_1..3`
   (`loading_phase_page.dart:36`), `pre_close`, `closing`, `seal` (`loading_phase_page.dart:32`);
   single delivery = `pre_open`, `opening`, `empty_container`, `runsheet_received`
   (`delivery_phase_page.dart:22`); multi-stop = `stop_{index}_{type}`.
7. **Stored array order ≠ workflow order.** The flat array is built by `mergeTripPhotosReplacingTypes`
   (append/replace by type — `delivery_trip_repository.dart:187`), so its order is insertion/replace
   order. "Ordered by workflow step" therefore needs an **explicit sort by a canonical type-rank**.
8. **The [[Assigned round]] is not on the trip.** "รอบเวลาของงานตาม assign" is the task's `time`
   (HH:MM) + `date` (`validate/taskSchema.ts:61-63`). `trip_records` carries `taskId` but **not** that
   time; its `createdAt`/`std` is when the *driver saved loading*, not the assigned slot
   (`trip_record.dart:73-84`). Driver-created manual trips and legacy trips have **no** `taskId`.

## Decision

1. **Build a per-trip photo viewer, reachable from Trip History.** Tapping a **First Mile** or
   **Line Haul** trip row on `trip_history_page.dart` opens a screen showing that trip's
   [[Evidence photo|evidence photos]] as thumbnails. **Scope is `trip_records` only** — standby and
   incident photos are explicitly out of scope for this ADR.
2. **One bulk "download all" action per trip.** No per-photo download. The button is shown only when
   the trip has ≥ 1 photo (loading-only trips that aren't delivered yet still qualify).
3. **Order photos by an explicit workflow rank**, never by the stored array order (fact 7). The rank:
   loading (`runsheet`, `runsheet_extra_1..3`, `pre_close`, `closing`, `seal`) → single delivery
   (`pre_open`, `opening`, `empty_container`, `runsheet_received`) → multi-stop grouped by **ascending
   stop index**, each stop in the same step order. Unknown/legacy types sort **last**, stably.
4. **"Download" = save the (already overlaid) image bytes to the device photo gallery**, into an album
   named **`LogiTrack`**. Each saved file's name encodes the **Trip ID** and the job's
   **[[Assigned round]]** so the driver can identify it later.
5. **Resolve the assigned round/time from the linked task at download time.** Fetch `task.date` +
   `task.time` by `trip.taskId` (one Firestore read per download tap). If the trip has **no `taskId`**
   or the task is missing, **fall back to `trip.createdAt`**. A missing time never blocks the download.
6. **Best-effort bulk with an X/Y report.** Fetch and save each photo independently; a failure on one
   (404, network) does not abort the rest. Report the outcome as "saved X/Y".
7. **Authorization is by in-app scope, not by a Storage rule.** The viewer only ever lists the
   signed-in driver's **own** trips — Trip History already queries `trip_records where driverId == uid`
   (or the driver's doc id) and merges (`trip_history_page.dart:66-78`,
   `trip_records_repository.dart:83-107`). **No Storage rule change** (read stays public), **no server
   proxy, no audit log.**

## Consequences

**Positive**

- Drivers get a self-serve copy of their proof photos with no admin round-trip.
- **Zero schema/migration work:** `TripRecord.photos` already carries every photo (fact 5); the
  round/time is fetched on demand (decision 5).
- No security-surface change — Storage `trip_records` read was already public.

**Negative / risks**

- **New runtime dependency + OS permission.** A gallery-saver plugin is required (recommend `gal` —
  it does album placement and `requestAccess()` in one API). Permissions: **iOS** needs add-only Photo
  Library access (`Info.plist` `NSPhotoLibraryAddUsageDescription`); **Android 13+** needs none for a
  MediaStore write, **older Android** needs `WRITE_EXTERNAL_STORAGE` (the plugin handles the split).
  The permission-denied path must explain + offer settings, never crash.
- **Data leaves the platform onto a personal device.** Accepted because the photos are the driver's
  own captures and Storage read is already public — the in-app scope (decision 7) is the only
  boundary. If governance ever needs a hard boundary, that is a **separate ADR**: lock `trip_records`
  read, serve bytes through an authenticated callable, and/or add a download audit log — all
  **explicitly declined here**.
- **Album/file naming is best-effort across platforms.** `gal`/`saver_gallery` set the album reliably
  on Android; iOS honors the album but Photos organizes by capture date and may not surface our
  filename in its UI — so Trip ID + round is a convenience label, **not** a guaranteed searchable
  field.
- **Overlaid image only.** The pre-overlay original is never stored (fact 1), so the download
  necessarily carries the GPS/branding/QR overlay. Acceptable — it strengthens the proof — but not
  optional.
- **One extra Firestore read per download tap** (the task fetch, decision 5). Negligible: it is
  user-initiated, not on list render. Duplicate taps make duplicate gallery copies (savers don't
  dedupe) — acceptable.

**Follow-ups**

- Spec this via `/spec-new`: pick the plugin (`gal`), the exact filename format
  (e.g. `LogiTrack_{tripId}_{yyyyMMdd-HHmm}_{step}.jpg`), the workflow-rank table as a shared constant,
  i18n keys (en + th), the iOS `Info.plist` string, the Android manifest entry, and the empty /
  permission-denied / partial-failure copy.
- Consider later extending the viewer/download to `standby_records` and `incident_reports` (out of
  scope now), and reusing the per-trip viewer as the driver's general read-only proof view.

## Alternatives considered

- **Share sheet (`share_plus`) instead of a gallery save.** Rejected: the owner wants a **permanent
  copy filed in the gallery**, not an ephemeral share.
- **Open each `url` in the browser via `url_launcher`.** Rejected: one-at-a-time, no album, poor UX.
- **Per-photo download button.** Rejected: the owner wants **whole-trip bulk** in workflow order.
- **Include `standby_records` / `incident_reports` photos.** Rejected for v1 — scope kept to
  `trip_records`.
- **Lock Storage read + serve via an authenticated callable + audit log.** Rejected now: real
  complexity for no current governance requirement; the owner chose in-app scope only and declined the
  audit log.
- **Denormalize `task.time`/`task.date` onto `trip_records` at creation** to avoid the task fetch.
  Rejected: needs a schema change **plus a backfill** of historical trips; the on-demand fetch is one
  cheap read on a rare, user-initiated action — the same reasoning as ADR 0001 (resolve task fields by
  join, don't denormalize) and ADR 0010 (trip→task display fallback).
- **Rely on the stored `photos[]` order.** Rejected: it is insertion/replace order
  (`mergeTripPhotosReplacingTypes`), not workflow order, so it would scramble the sequence the driver
  expects (fact 7).

## Related

- Glossary: [[Evidence photo]], [[Photo type]], [[Assigned round]]; and existing [[Delivery stop]],
  [[`taskId` vs `id`]], [[Denormalization (in this codebase)]].
- [ADR 0001](0001-checkin-time-on-trip-records.md) — task fields resolved by live join rather than
  denormalized; same reason the round/time is fetched, not stored.
- [ADR 0010](0010-job-category-carried-on-trip-independent-of-billing.md) — trip → task display
  fallback pattern reused by decision 5.
- Conventions: [0000-adr-conventions.md](0000-adr-conventions.md).
