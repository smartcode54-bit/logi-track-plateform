# Spec: Customer-app screenshots as mandatory evidence (check-in / loading / delivery)

> **Status:** ✅ Done (code complete 2026-08-23 — device-testing by owner pending, same as ADR 0018)
> **Owner:** Samart Kas
> **Created:** 2026-08-23
> **Domain:** drivers / tasks (mobile capture + web trip-details display)
> **Related:** [ADR 0019](../adr/0019-app-screenshots-as-mandatory-evidence.md) (the decision this builds),
> [ADR 0018](../adr/0018-driver-self-download-trip-photos.md) (workflow rank + photo viewer this extends),
> [ADR 0001](../adr/0001-checkin-time-on-trip-records.md) (check-in is a task-level event)

---

## 1. Problem & Goal

Drivers must file the **customer's own app screen** (SPX / J&T / any other customer or partner) as proof
at three workflow moments, on top of the existing evidence photos. Today none of these three attachments
exist. Goal: capture a **mandatory, un-overlaid** customer-app screenshot at **check-in** ("มาถึง / เข้า
stand"), at **loading save** ("ปล่อยรถ"), and at **delivery** ("มาถึง", before the first delivery photo),
store all three in the trip's photo set, and show them on the web trip-details view (preview **and** edit).

## 2. Scope

**In scope:**
- Mobile capture + mandatory gate for the three new screenshots (single **and** multi-delivery).
- Storing the check-in screenshot on the task and copying it forward into `trip_records.photos[]`.
- Web trip-details display of the new photo types in **both** the read-only preview and the edit dialog.
- Extending the ADR-0018 workflow-rank so the viewer/download order the new types correctly.
- i18n (en + th) for all new labels/prompts; `tasks.checkInAppScreenshotUrl` schema field.

**Out of scope (ทำทีหลัง / ไม่ทำ):**
- Any anti-reuse / authenticity enforcement (camera-only, capture-time check, perceptual de-dup) — the
  reuse gap is accepted (ADR 0019 §6); a hard boundary is a future ADR.
- Per-customer / per-partner configurability — **universal** for every trip/customer (owner decision).
- Billing changes (none — these photos never touch pricing).
- Standby flow (`standby_records`) and incident flow — unchanged.
- Adding the pre-existing check-in **camera** photo (`checkInPhotoUrl`) into `photos[]` — only the new
  `checkin_app` screenshot enters `photos[]`.

## 3. Requirements

**Functional**
- **R1 — Check-in screenshot (mandatory).** Both check-in entry points — `TaskCheckInPage` and
  `ManualCheckInPage` (`check_in_page.dart:709`, `:1450`) — require a second image, the customer-app
  screenshot, in addition to the existing check-in camera photo. Save is blocked until **both** are
  present (today the gate is only `if (_photoBytes == null) return;`, `check_in_page.dart:1019-1020`).
  Uploaded to Storage `checkin/{taskId}/app_screenshot_{ts}.jpg`; URL written to
  `tasks.checkInAppScreenshotUrl`.
- **R2 — Loading "ปล่อยรถ" screenshot (mandatory), last step.** A required `truck_release` screenshot
  captured as the final step before บันทึกรับงาน. Written into `trip_records.photos[]` as
  `{ url, type: 'truck_release' }`. Loading save is blocked until it is present (extends the existing
  `_stepPhotos.length != _cameraPhotoStepKeys.length` gate, `loading_phase_page.dart:832`).
- **R3 — Delivery "มาถึง" screenshot (mandatory), first step (single delivery).** A required `arrived`
  screenshot captured **before** the first existing delivery photo. Written to `photos[]` as
  `{ url, type: 'arrived' }`. Blocks the single-delivery save (extends `_canSubmit` /
  `_deliveryPhotoStepKeys`, `delivery_phase_page.dart:54,22`).
- **R4 — Delivery "มาถึง" screenshot per stop (multi-delivery).** In `delivery_phase_page_multi.dart`,
  every stop requires a `stop_{index}_arrived` screenshot, ranked first among that stop's photos.
  Blocks the stop's completion (extends `_photoTypesFor` / `_isStopComplete`,
  `delivery_phase_page_multi.dart:76-95`).
- **R5 — All three are un-overlaid customer-app screenshots.** Source = **camera OR gallery**; upload
  path uses **`skipOverlay: true`** (no GPS/time/compass burn-in — `checkin_repository.dart:29-35`), and
  the resulting `TripPhoto` has **`geocoding: null`** (do not stamp the driver's current location onto an
  image captured elsewhere — ADR 0019 §2).
- **R6 — Copy the check-in screenshot forward into the trip.** At the loading phase (which already reads
  the task), copy `task.checkInAppScreenshotUrl` into `trip_records.photos[]` as
  `{ url, type: 'checkin_app' }` (reference the same URL — **no re-upload**). Task is source of truth; the
  copy is best-effort. If the task has no such URL (legacy / pre-rollout), simply omit it.
- **R7 — Web trip-details shows the new photos in preview AND edit.** The three new types appear in the
  read-only trip detail (`DriverMonitorDashboard.tsx`, incl. the multi-stop per-stop view) **and** in the
  edit dialog (`EditTripDetailsDialog.tsx`) as first-class photo slots (view + Add/Replace).
- **R8 — Extend the ADR-0018 workflow rank** (`trip_photo_order.dart`) so `checkin_app` sorts **before**
  loading, `truck_release` at the **end** of loading, and `arrived` / `stop_{i}_arrived` **first** in each
  delivery / stop group. Without this they fall into the "unknown → last" bucket (rank 9000) and scramble.
- **R9 — Universal.** Applies to every FM/LH trip regardless of customer/partner. No config flag,
  hardcoded (owner decision).

**Non-functional**
- **N1.** i18n complete in `en` **and** `th`: web `context/locales/{en,th}/driverMonitor.ts` (labels +
  section titles) and the mobile translation files (`assets/translations/en.json` + `th.json`) for capture
  prompts and step labels. Key counts must stay equal across languages.
- **N2.** No billing change (do **not** touch `lib/billingCompute.ts` / `functions/src/core/billingCompute.ts`).
  No Firestore-rule and no Storage-rule change — the driver already writes their own task and the
  `checkin/**` path; download URLs are tokenized (verify during build, don't assume).
- **N3.** Legacy / in-flight safety: the gates are per-phase, so no backfill. A trip checked-in before
  rollout then delivered after only faces the delivery gate; `truck_release` is never demanded on an
  already-loaded trip; `checkin_app` is simply absent. Every reader (mobile viewer, web slots) must treat
  the three as **optional-on-legacy** (null-safe), even though they are mandatory-on-new.
- **N4.** Mobile is the primary change → bump `pubspec.yaml` (web + mobile move on one number per
  `CLAUDE.md`; a feature → next minor).

## 4. Design

**New [[Photo type]]s** (all un-overlaid [[Customer-app screenshot]]s):

| Type | Where written | Rank position |
|------|---------------|---------------|
| `checkin_app` | task → copied into `photos[]` at loading (R6) | before loading (new group, e.g. 500) |
| `truck_release` | `photos[]` at loading save | end of loading group |
| `arrived` | `photos[]` at single delivery save | first in delivery group |
| `stop_{i}_arrived` | `photos[]` at each stop save (multi) | first within each stop |

**Data model (Firestore)**
- **`tasks.checkInAppScreenshotUrl`** — new nullable string (mirrors `checkInPhotoUrl`). Add to
  `logitrack-web/validate/taskSchema.ts` **and** `shared-docs/schemas/taskSchema.ts` (keep in sync).
- `trip_records.photos[]` — no schema change (already `{url, type, geocoding?}`); the new entries carry
  `geocoding: null`. Note in the [[Evidence photo]] glossary term (already amended by ADR 0019) that
  `photos[]` is now a mixed overlaid/un-overlaid set.

**Cloud Functions / billing**
- None. No callable, no trigger (this project has none), no billing sync.

**Mobile (Flutter)**
- **Check-in** (`check_in_page.dart`): add a required second image slot in both `TaskCheckInPage` and
  `ManualCheckInPage` (state e.g. `_appScreenshotBytes`), camera-or-gallery, compressed **without**
  overlay (`stampOverlayAndCompressForEvidence(bytes, skipOverlay: true)` or `compressImageForUpload`
  directly). Upload to `checkin/{taskId}/app_screenshot_{ts}.jpg` and write
  `tasks.checkInAppScreenshotUrl` — extend `submitCheckIn` in `checkin_repository.dart` (or add a sibling
  writer) to persist it. Gate both images before enabling Save.
- **Loading** (`loading_phase_page.dart` + `loading_trip_repository.dart`): add a required `truck_release`
  screenshot. **Decided: approach (b)** — mirror the delivery `runsheet_received` precedent, i.e. add
  `truck_release` to the loading step-key handling and **branch its capture to skip overlay** (loading has
  no such branch today — the delivery `isRunsheetReceived` branch at `delivery_phase_page.dart:487,492,504`
  is the shape to copy; loading's `_cameraPhotoStepKeys` otherwise overlays every entry at `:1245,:2075`).
  Because the capture flows through the existing `_stepPhotos` → `StampedPhotoInput` machinery (which
  requires lat/lng/timestamp), `/spec-build` must **special-case `truck_release` in
  `submitLoadingPhaseRecord` to write `geocoding: null`** (R5 — no overlay AND no location metadata on a
  screenshot). Also pass `checkInAppScreenshotUrl` (read from the task) into `submitLoadingPhaseRecord` and
  append `TripPhoto(url, type:'checkin_app', geocoding:null)` (R6).
- **Delivery single** (`delivery_phase_page.dart`): add `arrived` first in `_deliveryPhotoStepKeys` and
  branch its capture in `_takeDeliveryPhoto` to **skip overlay** — the same shape as the existing
  `isRunsheetReceived` branch (`:487,:492,:504`). In `_submitDelivery`'s photo loop (`:599-607`) set
  `geocoding` to null for `arrived` (don't stamp the submit location on a screenshot).
- **Delivery multi** (`delivery_phase_page_multi.dart`): add `'arrived'` to the front of
  `_lastStopPhotoTypes` and `_nonLastStopPhotoTypes` (`:76-90`); branch `_capturePhotoForStop` (`:314`)
  to skip overlay for `arrived`; key it `stop_{index}_arrived` (`:362`); `_isStopComplete` (`:92-95`)
  then enforces it per stop.
- **Rank** (`trip_history/data/trip_photo_order.dart`): add a `_checkinOrder = ['checkin_app']` group
  ranked < 1000; append `'truck_release'` to `_loadingOrder`; insert `'arrived'` at index 0 of
  `_deliveryOrder` (covers both single `2000+i` and multi `3000+idx*10+si`).

**Web (Next.js)**
- **`EditTripDetailsDialog.tsx`** (edit): add labels for `checkin_app`, `truck_release`, `arrived`
  (`:70-80`); append `truck_release` to `LOADING_PHASE_TYPES` (`:84-90`); insert `arrived` at the front
  of `DELIVERY_PHASE_TYPES` (`:92`); add a **Check-in** section for `checkin_app` in the sections list
  (`:1181-1187`). This gives view + Add/Replace for the new slots.
- **`DriverMonitorDashboard.tsx`** (preview / read-only detail, incl. the multi-stop per-stop photo
  view): render the new types so the added photos show in the trip preview (R7 / owner's explicit
  requirement: preview **and** edit). Multi-stop must show `stop_{i}_arrived` in each stop group.
- **i18n:** `context/locales/{en,th}/driverMonitor.ts` — new label keys (`checkin_app`, `truck_release`,
  `arrived`) + any section title (e.g. `driverMonitor.editTrip.checkinPhase`).

**Firestore / Storage Rules**
- No change expected (verify during build): driver already updates own task; `checkin/**` write already
  used by the existing check-in photo; `trip_records/**` read already public (ADR 0018 fact 2).

## 5. Affected files

Mobile:
- `logitrack-mobile/lib/features/home/presentation/pages/check_in_page.dart`
- `logitrack-mobile/lib/features/home/data/repositories/checkin_repository.dart`
- `logitrack-mobile/lib/features/loading_phase/presentation/pages/loading_phase_page.dart`
- `logitrack-mobile/lib/features/loading_phase/data/repositories/loading_trip_repository.dart`
- `logitrack-mobile/lib/features/delivery_phase/presentation/pages/delivery_phase_page.dart`
- `logitrack-mobile/lib/features/delivery_phase/presentation/pages/delivery_phase_page_multi.dart`
- `logitrack-mobile/lib/features/delivery_phase/data/repositories/delivery_trip_repository.dart` (if the multi/stop TripPhoto geocoding needs null)
- `logitrack-mobile/lib/features/trip_history/data/trip_photo_order.dart`
- `logitrack-mobile/assets/translations/en.json` + `th.json`
- `logitrack-mobile/pubspec.yaml` (version bump)

Web:
- `logitrack-web/features/drivers/components/EditTripDetailsDialog.tsx`
- `logitrack-web/features/drivers/components/DriverMonitorDashboard.tsx`
- `logitrack-web/context/locales/en/driverMonitor.ts` + `th/driverMonitor.ts`
- `logitrack-web/validate/taskSchema.ts`

Shared:
- `shared-docs/schemas/taskSchema.ts`
- `shared-docs/.vibe-rules.md` (Change Log)

## 6. Task breakdown
- [x] T1. Schema: add `tasks.checkInAppScreenshotUrl` in `validate/taskSchema.ts` + `shared-docs/schemas/taskSchema.ts` (sync).
- [x] T2. Check-in (R1/R5): second required screenshot slot in `TaskCheckInPage` + `ManualCheckInPage`; write `checkInAppScreenshotUrl`; gate both images.
- [x] T3. Loading (R2/R5/R6): required `truck_release` screenshot (no overlay, geocoding null) + copy `checkin_app` forward; extend the save gate.
- [x] T4. Delivery single (R3/R5): `arrived` first, no-overlay capture branch, geocoding null, gate.
- [x] T5. Delivery multi (R4/R5): per-stop `stop_{i}_arrived`, no-overlay branch, per-stop gate.
- [x] T6. Rank (R8): extend `trip_photo_order.dart` (checkin group, truck_release, arrived) + viewer labels in `trip_photos_page.dart`.
- [x] T7. Web (R7): `EditTripDetailsDialog` slots + labels (checkin_app/truck_release/arrived) + check-in section; `TRIP_PHOTO_TYPE_ENUM` extended; `DriverMonitorDashboard` preview already renders all photos generically (no change needed).
- [x] T8. i18n en + th (web driverMonitor + mobile translations), equal key counts.
- [x] T9. Update `.vibe-rules.md` Change Log.
- [x] T10. Bump `pubspec.yaml` version (3.2.0+2 → 3.3.0+3).

## 7. Acceptance criteria

- [ ] **AC1 (R1).** Check-in cannot be saved without both the camera photo and the app screenshot; on
  save, `tasks.checkInAppScreenshotUrl` is set — verified from both `TaskCheckInPage` and `ManualCheckInPage`.
- [ ] **AC2 (R2).** Loading cannot be saved without the `truck_release` screenshot; the saved
  `trip_records.photos[]` contains `{type:'truck_release', geocoding:null}` (no burned-in overlay).
- [ ] **AC3 (R3).** Single delivery cannot be saved without `arrived`; `photos[]` gains
  `{type:'arrived', geocoding:null}`; the on-image overlay is absent.
- [ ] **AC4 (R4).** In a multi-drop trip, each stop cannot be completed without its `stop_{i}_arrived`
  screenshot; each is present in `photos[]`.
- [ ] **AC5 (R6).** After loading, `photos[]` contains `{type:'checkin_app'}` referencing the same URL as
  `tasks.checkInAppScreenshotUrl` (no duplicate upload).
- [ ] **AC6 (R7).** On web Driver Monitor, opening a trip shows the three new photos in the **preview**;
  the **edit** dialog shows them as slots with Add/Replace. Multi-stop shows per-stop `arrived`.
- [ ] **AC7 (R8).** The mobile trip photo viewer / download orders photos as: checkin_app → loading
  (…, truck_release) → delivery (arrived, …) → each stop (arrived first) — not scrambled to the tail.
- [ ] **AC8 (N3).** A pre-rollout trip with no `checkin_app` / `truck_release` renders without error and
  is still downloadable.
- [x] **AC9.** `tsc --noEmit` (0 errors), web ESLint (0 errors), Vitest (263/263), and `dart analyze` on all
  changed files (0 errors) are green; en/th key counts match. AC1–AC8 are runtime/device checks — pending
  owner device-test (same flow as ADR 0018).

## 8. Risks & rollback

| Risk | Mitigation / rollback |
|------|----------------------|
| "Mandatory" only proves an image is attached, not authenticity (reuse of a stale/other screenshot) | Accepted (ADR 0019 §6). A hard anti-reuse boundary is a separate future ADR. |
| Appending screenshot types to the overlaid step-key lists routes them through overlay capture / stamps a false location | Chosen approach (b) for all three: add a **no-overlay capture branch** mirroring `runsheet_received` (`delivery_phase_page.dart:487,492,504`) — loading gains an equivalent branch — and special-case each screenshot type to write `geocoding: null` at save. Covered in Design. |
| New types omitted from `trip_photo_order.dart` → out-of-order in viewer/download | R8 / T6 explicitly extend the rank; AC7 verifies order. |
| Legacy/in-flight trips lack the fields → null crashes or false "incomplete" | Per-phase gates (no backfill); readers null-safe (N3 / AC8). |
| Blocking a driver mid-shift if the customer app is unavailable | Camera-or-gallery keeps capture flexible; owner chose mandatory knowingly. Rollback = revert the gate change per phase (each is an isolated required-check). |
| i18n key drift between en/th | T8 + AC9 enforce equal counts. |

## 9. Open questions / follow-ups
- Exact `pubspec.yaml` version number (decide at build time; web+mobile share the number).
- Whether the read-only preview lives only in `DriverMonitorDashboard` or also elsewhere (confirm during
  build that both the trip card preview and the edit dialog are covered — owner requires both).
- After merge: update [ADR 0019](../adr/0019-app-screenshots-as-mandatory-evidence.md) status from
  "implementation pending" to shipped, and the `project_app_screenshot_evidence` memory.

> ⚠️ This spec file (and the ADR/glossary) are ignored by the repo's broad `*.md` gitignore rule — commit
> with `git add -f shared-docs/specs/app-screenshots-mandatory-evidence.md`.
