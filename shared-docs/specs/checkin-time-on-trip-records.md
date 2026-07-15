# Spec: Check-in time on `trip_records` (Driver Monitor)

> **Status:** ✅ Done (2026-07-15) — built via **live join**, not denormalization (see note below)
> **Owner:** Samart Kas
> **Created:** 2026-07-15
> **Domain:** drivers (Driver Monitor) — web only (mobile/functions descoped)
> **Related:** [ADR 0001](../adr/0001-checkin-time-on-trip-records.md) (§ Update), [glossary.md](../glossary.md)

---

> ## ⚠️ Implementation note (2026-07-15) — mechanism changed during build
>
> The denormalization mechanism (onCreate trigger + backfill + mobile write + query-axis switch) was
> found **infeasible**: Firestore is in `asia-southeast3`, which supports **no Firestore document
> triggers** (Gen1 or Gen2) — see `functions/src/chat.ts` and the ADR Update. Owner approved switching
> to a **live-join** approach, web-only:
>
> - Fixed the mis-keyed lookup: `checkInAtByTaskId` is rebuilt in `useDriverMonitor` from the
>   full-coverage `taskById` (realtime + older fetched tasks), keyed by both `task.id` and
>   `task.taskId`; the table/export look up by `trip.taskId` → `trip.id` → `trip.createdAt` fallback.
> - Query axis **stays `createdAt`** (no disappearing-docs risk).
> - **Not done (descoped):** `trip_records.checkInAt` field (R1), trigger (R2), backfill (R3), mobile
>   write + bump (R4), query switch (R5). The **Depart** column (R8), **revenue removal** (R9), header
>   rename (R7), and the check-in **display** (R6, via live join) are done.
> - Trade-off accepted: date-range **filter** stays on `createdAt` (check-in ≈ created, same day).

---

## 1. Problem & Goal

The Driver Monitor table column headed **"Created" / "สร้างเมื่อ"** is meant to show the driver's
**check-in time** but shows the trip's creation time instead. The value is produced by a mis-keyed
join (`checkInAtByTaskId[trip.id]`, keyed by task doc id but looked up by trip doc id), so it falls
back to `trip.createdAt` almost always. `createdAt` is stamped at **loading**, which is later than
check-in.

Goal: the column reliably reflects **check-in time**, and the table can **sort and date-filter** by
it — by denormalizing `tasks.checkInAt` onto `trip_records.checkInAt` (server-authoritative) so no
cross-document join is needed at read time.

## 2. Scope

**In scope:**
- Add `checkInAt` field to `trip_records` (mobile Dart model + web trip type).
- Cloud Function `onCreate` trigger on `trip_records` that stamps `checkInAt` from the task.
- One-time backfill callable for existing `trip_records`.
- Mobile: stamp `checkInAt` at trip creation (immediacy; trigger is the authority).
- Web Driver Monitor: switch query range + `orderBy` to `checkInAt`; display `trip.checkInAt` in
  the table cell and Excel export; rename the column header (en + th).
- Web Driver Monitor: add a **"Depart"** column showing the existing `trip.createdAt`, positioned
  between the Check-in column and the Driver column.
- Web Driver Monitor: **remove the "Estimated revenue" column from the main table** (keep it in the
  trip detail panel and the Excel export).

**Out of scope (later / not doing):**
- Changing the check-in write path itself (`tasks.checkInAt` stays the canonical source).
- A distinct `departAt` field/event — the Depart column reuses `createdAt` (creation = departure);
  no new schema, trigger, or backfill for departure.
- Removing estimated revenue from the trip detail panel or the Excel export (table column only).
- Reworking other columns, billing, or the incident/task sub-queries (they keep `createdAt`).
- Historical correction of true check-in times where none was ever recorded (fallback = `createdAt`).

## 3. Requirements

**Functional**
- **R1.** `trip_records` documents carry a `checkInAt` Firestore `Timestamp`.
- **R2.** A Cloud Function `onCreate` trigger on `trip_records/{tripId}` sets
  `checkInAt = tasks/{trip.taskId}.checkInAt ?? trip.createdAt`. It is the authoritative writer and is
  independent of the mobile app version. It must not overwrite a `checkInAt` already present on the
  created doc (idempotent / respects a mobile-provided value).
- **R3.** A one-time backfill callable (admin-only) populates `checkInAt` on all existing
  `trip_records` using the same rule (`task.checkInAt ?? trip.createdAt`), batched, returning stats
  (scanned / updated / alreadySet / missingTask / errors).
- **R4.** Mobile stamps `checkInAt` on the `trip_record` at creation from the already-loaded
  `_taskCheckedInAt` (`loading_phase_page.dart`), for immediacy. Absence of this value must never
  block trip creation (trigger backfills it).
- **R5.** The Driver Monitor trip_records query switches its range + `orderBy` from `createdAt` to
  `checkInAt`. **Gated:** only after R3 backfill reports 100% coverage.
- **R6.** The table cell and Excel export read `trip.checkInAt` directly; the mis-keyed
  `checkInAtByTaskId[trip.id]` join is removed (the `checkInAtByTaskId` map and its 500-task coverage
  gap become unnecessary for this column).
- **R7.** The check-in column header is renamed to **"Check-in" / "เช็คอิน"** in `en` + `th`.
- **R8.** A **"Depart"** column is added to the main table, showing `trip.createdAt`, positioned
  **between** the Check-in column and the Driver column. Header **"Depart" / "ออกเดินทาง"** (en + th).
  Display-only (no sorting/filtering by it; the table still sorts/filters by `checkInAt` per R5).
- **R9.** The **"Estimated revenue" column is removed from the main table** (header + cell). It
  remains in the trip detail panel and the Excel export unchanged.

**Non-functional**
- **N1.** i18n complete in `en` and `th` for any changed/added label.
- **N2.** After R5, **no** `trip_record` disappears from the monitor — guaranteed because R2/R3 make
  `checkInAt` present on 100% of docs (Firestore range/order queries exclude docs missing the field).
- **N3.** The trigger works for trips created by **any** mobile build (version-independent).
- **N4.** Backfill is batched (≤500/commit) and safe to re-run; trigger cost is one lightweight
  read+write per trip_record create.
- **N5.** Functions use region `asia-southeast1`, consistent with existing functions.

## 4. Design

**Data model (Firestore)**
- `trip_records.checkInAt` — new `Timestamp`. Denormalized copy of `tasks.checkInAt`, with
  `trip.createdAt` fallback so the field is always present.
- Source of truth stays `tasks.checkInAt` (already in `taskSchema` as `z.any().optional()`; written by
  `checkin_repository.dart:102-110`).
- Join key from trip to task is **`trip.taskId`** (used as the task doc id, per glossary) — the
  trigger and backfill resolve the task via `tasks/{trip.taskId}`.

**Cloud Functions**
- **Trigger:** `onTripRecordCreated` — `functions.region("asia-southeast1").firestore
  .document("trip_records/{tripId}").onCreate(...)`. Reads `trip.taskId` → `tasks/{taskId}.checkInAt`;
  writes `checkInAt = task.checkInAt ?? snap.createdAt`. Skip if `checkInAt` already set on the new doc
  (R2 idempotency). No-op if `taskId` missing → fall back to `createdAt`.
- **Backfill:** `backfillTripRecordCheckIn` (onCall, admin-only) — scan `trip_records`, for each
  missing `checkInAt` resolve the task and set `task.checkInAt ?? trip.createdAt`; batch commit; return
  stats. Mirror existing backfill callables (`backfillTripTruckData.ts`) for auth, batching, and stats
  shape.
- Not billing-related → no `lib/billingCompute.ts` ↔ `functions/src/core/billingCompute.ts` sync
  needed.

**Web (Next.js)**
- `features/drivers/hooks/useDriverMonitor.ts`
  - `mapTripDoc`: add `checkInAt: toDate(data.checkInAt)` to the trip mapping (trip type gains
    `checkInAt: Date | null`).
  - trip_records query (lines ~310-314): change the two `where` bounds and `orderBy` from `createdAt`
    to `checkInAt`. (Tasks query line ~450 and incident query stay on `createdAt`.)
  - `checkInAtByTaskId` (lines ~692-699) may be removed if unused elsewhere after R6, or left if other
    consumers rely on it — verify at build time.
- `features/drivers/components/DriverMonitorDashboard.tsx`
  - **Check-in column** — Cell (~854) and Excel `created` value (~417): replace
    `(trip.id && checkInAtByTaskId[trip.id]) || trip.createdAt` with `trip.checkInAt ?? trip.createdAt`
    (defensive fallback; field should always be present post-backfill). Header (~816) uses the new
    `driverMonitor.table.checkInAt` key.
  - **Depart column (new, R8)** — insert a `<TableHead>` after the Check-in header (~816) and a
    `<TableCell>` after the Check-in cell (~854), showing `formatTimestamp(trip.createdAt)`. Header
    `driverMonitor.table.depart`. Column order becomes: `tripId | Check-in | Depart | Driver | …`.
  - **Estimated revenue removal (R9)** — delete the table header (~826) and the cell (~916,
    `formatMoney(getBillingForTrip(...)`). Do **not** touch the detail panel (~1272-1275) or the Excel
    export revenue column (header ~402 `nav.income`, values row[10] at ~435/456/471).
- Column order reference (table headers, current lines 815-826):
  `tripId(815) | createdAt→Check-in(816) | +Depart | driver(817) | licensePlate | jobType | origin |
  destination | sealCode | partnerCode | status | deliveredTime | ~~estimatedRevenue(826, removed)~~`.
  Keep the header row and body row cell counts in sync.
- i18n keys (`context/locales/en/driverMonitor.ts` + `th/driverMonitor.ts`):
  - `driverMonitor.table.checkInAt` = `"Check-in"` / `"เช็คอิน"` (new), used for the Check-in header.
    `driverMonitor.table.createdAt` ("Created") may become unused by the table after the header switch
    (the detail panel uses the separate `driverMonitor.detail.createdAt`); the Depart column uses the
    new `.table.depart` key rather than reusing `.table.createdAt`. Remove `.table.createdAt` only if a
    usage check confirms nothing else references it.
  - `driverMonitor.table.depart` = `"Depart"` / `"ออกเดินทาง"` (new), for the Depart header.
  - `driverMonitor.table.estimatedRevenue` stays defined (still used by the detail panel ~1272).

**Mobile (Flutter)**
- `logitrack-mobile/lib/features/home/data/models/trip_record.dart` — add `checkInAt` field to the
  model, `toJson` (`'checkInAt': ...`) and `fromJson`/`_parseDate`.
- `logitrack-mobile/lib/features/loading_phase/presentation/pages/loading_phase_page.dart` — pass
  `_taskCheckedInAt` into the trip_record write (already read at line ~199).
- Bump `pubspec.yaml` version (mobile model change).

**Firestore indexes / rules**
- The trip_records query is a single-field range + `orderBy` on the **same** field, no equality
  filters → uses the **automatic single-field index** on `checkInAt`. Confirm `checkInAt` is **not**
  single-field-exempted in `firestore.indexes.json`; add an index only if a query with an added
  equality filter is introduced (none planned). No new composite index expected.
- Firestore rules: `trip_records` write permissions unchanged for the trigger (Admin SDK bypasses
  rules). If client/mobile writes `checkInAt`, ensure existing `trip_records` write rules allow the
  field (it is part of the create payload the driver already makes).

## 5. Affected files

- `logitrack-web/functions/src/triggers.ts` (or a new `functions/src/tripRecordCheckIn.ts`) — trigger.
- `logitrack-web/functions/src/index.ts` — export trigger + backfill callable.
- `logitrack-web/functions/src/backfillTripRecordCheckIn.ts` — new backfill callable.
- `logitrack-web/features/drivers/hooks/useDriverMonitor.ts` — trip type, mapTripDoc, query axis.
- `logitrack-web/features/drivers/components/DriverMonitorDashboard.tsx` — cell, export, header.
- `logitrack-web/context/locales/en/driverMonitor.ts` + `th/driverMonitor.ts` — header i18n.
- `logitrack-web/firestore.indexes.json` — only if an index change proves necessary.
- `logitrack-mobile/lib/features/home/data/models/trip_record.dart` — model field.
- `logitrack-mobile/lib/features/loading_phase/presentation/pages/loading_phase_page.dart` — write.
- `logitrack-mobile/pubspec.yaml` — version bump.
- (optional UI) a Utilities/backfill entry to run `backfillTripRecordCheckIn`, mirroring existing
  backfill UIs.

## 6. Task breakdown

- [~] **T1–T5. DESCOPED** — trigger, backfill, mobile write, trip type field, query-axis switch.
      Not built: Firestore triggers unavailable in `asia-southeast3`; owner chose the live-join
      approach with the `createdAt` query axis retained. (Superseded — see ADR Update.)
- [x] **T6.** Web: table cell + Excel export show check-in via the **corrected full-coverage live
      join** (`checkInAtByTaskId[trip.taskId] ?? [trip.id] ?? trip.createdAt`); mis-keyed `trip.id`
      lookup fixed; map now rebuilt from `taskById` covering older tasks too. (R6)
- [x] **T7.** i18n: added `driverMonitor.table.checkInAt` = "Check-in"/"เช็คอิน" and
      `driverMonitor.table.depart` = "Depart"/"ออกเดินทาง" (en + th). (R7, R8, N1)
- [x] **T8.** Web: added the **Depart** column (shows `trip.createdAt`) between Check-in and Driver;
      header/body cell counts aligned. (R8)
- [x] **T9.** Web: removed the **Estimated revenue** column from the table (header + cell); detail
      panel and Excel export left intact. (R9)
- [~] **T10. N/A** — query axis unchanged (`createdAt`), so no `checkInAt` index needed.
- [ ] **T11.** Update `.vibe-rules.md` Change Log.

## 7. Acceptance criteria

- [ ] **AC1. (R1, R4)** A newly created trip_record has `checkInAt` equal to its task's `checkInAt`.
- [ ] **AC2. (R2)** Creating a trip_record whose task has `checkInAt` results — via the trigger — in
      `trip.checkInAt == task.checkInAt`, even when the doc was written by an old app build with no
      `checkInAt`. If the task lacks `checkInAt`, `trip.checkInAt == trip.createdAt`.
- [ ] **AC3. (R3, N2)** After backfill, **0** trip_records in the monitor's date range lack
      `checkInAt`; the backfill report shows updated + alreadySet = scanned (minus explicit errors).
- [ ] **AC4. (R5)** The Driver Monitor loads and sorts by check-in time; changing the date filter
      selects trips by `checkInAt`; no trip that existed before the switch disappears.
- [ ] **AC5. (R6)** The table cell and exported "Check-in" column show the check-in time (equal to the
      task's `checkInAt`), not the loading/creation time; the `checkInAtByTaskId[trip.id]` join is gone.
- [ ] **AC6. (R7, N1)** The check-in column header reads "Check-in" (en) / "เช็คอิน" (th).
- [ ] **AC7. (R8)** A "Depart" / "ออกเดินทาง" column appears between Check-in and Driver, showing the
      trip's `createdAt`; header and body cell counts match (no column misalignment).
- [ ] **AC8. (R9)** The "Estimated revenue" column is gone from the main table, but still shows in the
      trip detail panel and in the Excel export.
- [ ] **AC9.** `tsc --noEmit` (web + functions) and `dart analyze` (mobile) pass; CI green.

## 8. Risks & rollback

| Risk | Mitigation / rollback |
|------|----------------------|
| Query-axis switch (R5) ships before backfill completes → trips missing `checkInAt` vanish from monitor | Hard gate: T4 backfill to 100% before T5. Fallback in trigger/backfill guarantees the field is always set. Rollback: revert the query `orderBy`/range to `createdAt` (one-line) — display still works via `trip.checkInAt ?? trip.createdAt`. |
| Manual/self-created check-in flow doesn't write `task.checkInAt` → trigger has no source | Trigger falls back to `trip.createdAt` (field still present). Follow-up: confirm/patch the self-check-in path to write `task.checkInAt` (see §9). |
| Old mobile builds never write `trip.checkInAt` | Trigger is authoritative and version-independent (N3); mobile write is only for immediacy. |
| `checkInAt` single-field index exempted → query fails | T8 verify; add index if needed. Query is single-field range+order, so an automatic index normally suffices. |
| Trigger overwrites a correct mobile-written value | R2 idempotency: skip if `checkInAt` already present on the created doc. |

## 9. Open questions / follow-ups

- **Confirm the self-created/manual check-in flow writes `task.checkInAt`** (driver creates own task in
  `check_in_page.dart`). If not, the trigger will fall back to `createdAt` for those trips; decide
  whether to patch that write path. (Verify during `/spec-build`.)
- Decide header key strategy: reuse `driverMonitor.table.createdAt` value vs add
  `driverMonitor.table.checkInAt` (spec recommends a **new** key; confirm no other surface needs the
  old "Created" label).
- Optional: expose `backfillTripRecordCheckIn` in the Utilities backfill page, or run once via console.
