# ADR 0001 — Denormalize check-in time onto `trip_records` and drive the Driver Monitor "Check-in" column by it

- **Status:** ⚠️ Superseded in part (2026-07-15) — the denormalization **mechanism** below is
  **infeasible** and was replaced during build. See **§ Update** immediately below. The problem
  analysis and glossary remain valid.
- **Deciders:** Samart Kas (product owner), Claude (grilling session)
- **Area:** logitrack-web (Driver Monitor). (Mobile / Cloud Functions / schema are no longer touched —
  see Update.)

## Update (2026-07-15) — mechanism revised: no denormalization, fix the live join instead

**Why:** the chosen mechanism (Decision #1, an `onCreate` Firestore trigger on `trip_records`) cannot
be built. The Firestore database is in **`asia-southeast3`** (`firebase.json` `firestore.location`),
which supports **neither Gen1 nor Gen2 Firestore document triggers** — documented in the codebase at
`functions/src/chat.ts` (*"Firestore is in asia-southeast3, which is not supported for Gen2 triggers"*)
and `functions/src/triggers.ts`. Every reactive write here is an app-invoked **callable**, never a
Firestore trigger. The "version-independent server trigger" — the whole reason denormalization was
preferred over a mobile write — therefore does not exist on this platform.

**Revised decision (owner-approved 2026-07-15):** do **not** denormalize. Instead **fix the existing
mis-keyed live join** in the web:
- `checkInAtByTaskId` is rebuilt from the full-coverage `taskById` the billing effect already
  assembles (realtime tasks **+** the older tasks fetched for trips beyond the 500-task window), keyed
  by both `task.id` and `task.taskId`.
- The Driver Monitor cell/export look up by **`trip.taskId`** (→ `trip.id` → `trip.createdAt`
  fallback), so real check-in shows for **every loaded trip**, recent or old.
- The Firestore query axis **stays `createdAt`** (no disappearing-docs risk; no query change).
- **Dropped entirely:** `trip_records.checkInAt` field, the trigger, the backfill callable, the mobile
  write + version bump, and the Phase C query-axis switch.

**Consequences of the revision:** no schema/backfill/mobile release; works for all trips via a live
join (no version dependency). Trade-off accepted: the date-range **filter** stays on `createdAt`
(cannot Firestore-range-filter by check-in without the field); check-in ≈ created (same day) so the
practical difference is negligible. Superset of "Alternatives considered → Fix the mis-keyed lookup",
which is now the chosen path.

The original analysis, invariant, and decisions below are retained as the record of what was decided
before the platform constraint was discovered.

---


## Context

The Driver Monitor table (`features/drivers/components/DriverMonitorDashboard.tsx`) has a
column whose header reads **"Created" / "สร้างเมื่อ"** (`driverMonitor.table.createdAt`). The
request was to make that column show the driver's **check-in time** instead of the record's
creation time.

Investigation showed the request rests on a false premise. The column already *attempts* to show
check-in time, with a fallback:

```ts
// DriverMonitorDashboard.tsx line 854 (cell) and ~417 (Excel export)
(trip.id && checkInAtByTaskId[trip.id]) || trip.createdAt
```

But the lookup is **mis-keyed** and therefore almost always falls back to `createdAt`:

- `checkInAtByTaskId` is keyed by the **task document id** (`useDriverMonitor.ts:696`, `map[t.id] = at`).
- The lookup uses `trip.id` — the **trip_record** document id.
- The trip→task link everywhere else in the hook is `trip.taskId`, used as a task doc id
  (`useDriverMonitor.ts:492, 516, 530`). `trip.id` is only a coincidental secondary fallback.
- So `checkInAtByTaskId[trip.id]` misses unless a trip's doc id happens to equal its task's doc id,
  which is not the data model. Result: the column shows `createdAt`.

### Facts established during grilling

- **Canonical check-in timestamp:** `tasks.checkInAt`, written by mobile at check-in
  (`checkin_repository.dart:102-110`, alongside `status:'Checked in'`, `checkInPhotoUrl`,
  `checkInLat/Lng`). Present in the task Zod schema as `checkInAt: z.any().optional()`
  (`validate/taskSchema.ts:100`, `shared-docs/schemas/taskSchema.ts:100`).
- **trip_records do not carry check-in time.** The Dart model (`trip_record.dart`) has only
  `createdAt` — no `checkInAt`, no `startedAt`. (`startedAt` seen in code is a `StandbyPage`
  constructor argument, unrelated to trip records.)
- **The value is in hand at trip creation.** Mobile reads `task.checkInAt` into `_taskCheckedInAt`
  during the loading phase (`loading_phase_page.dart:199-205`), which is exactly when the
  trip_record is created. Denormalizing it costs one field on the write.
- **Domain invariant (owner-asserted):** a driver must check in before loading, and loading is when
  the trip_record is created. Therefore **every trip_record has a preceding check-in**, and
  `checkInAt` is always available at creation time. There is no legitimate "no check-in" case for
  new data — only historical data may lack a recorded timestamp.
- **Coverage gap in the current web join:** `checkInAtByTaskId` is built only from the realtime
  task listener, which is `limit(500)` (`useDriverMonitor.ts:451`). Tasks older than that are
  fetched into a *local* `taskById` inside an effect but never into the `tasks` state the map reads,
  so a join-based approach under-covers older trips even after the key is fixed.
- **Firestore range-query hazard:** the monitor loads trips with a range + `orderBy` on `createdAt`
  (`useDriverMonitor.ts:312-313`). A Firestore range/order query **excludes documents missing the
  ordered field** (the hook comments warn about this, lines 52-53). Switching the axis to
  `checkInAt` therefore requires `checkInAt` to be present on **100%** of trip_records, or those
  trips vanish from the monitor.

## Decision

Denormalize the check-in time onto `trip_records` as `checkInAt` and drive the monitor column by it.

1. **Authoritative mechanism — Cloud Function `onCreate` trigger.** A Firestore trigger on
   `trip_records/{id}` copies the check-in time from the trip's task
   (`tasks/{trip.taskId}.checkInAt`) into `trip_records.checkInAt` at creation. This is
   **version-independent**: it works regardless of the driver's mobile app version, and makes the
   denormalization server-authoritative. A direct mobile write is optional and not required for
   correctness.
2. **Historical fallback — `trip.createdAt`.** Backfill sets
   `checkInAt = task.checkInAt ?? trip.createdAt`, so the field is **always present** and no trip
   ever disappears from a `checkInAt`-ordered query. (The trigger applies the same fallback for any
   future trip whose task lacks a recorded check-in time.)
3. **One-time backfill** Cloud Function populates `checkInAt` on all existing `trip_records` using
   the same rule, before the web query axis is switched.
4. **Web query switch.** Once `checkInAt` is present on 100% of trip_records, switch the monitor's
   range + `orderBy` from `createdAt` to `checkInAt` so sort and date-range filter operate on
   check-in time.
5. **Web display.** The table cell and Excel export read `trip.checkInAt` directly (dropping the
   mis-keyed `checkInAtByTaskId[trip.id]` join). Rename the column header to **"Check-in" /
   "เช็คอิน"** (`driverMonitor.table.createdAt` value updated in en + th, or a new key).
6. **Schema.** Add `checkInAt` to the trip_record type/schema on both mobile (Dart model
   `toJson`/`fromJson`) and web (trip type consumed by `useDriverMonitor`).

## Consequences

- **Positive:** sort/filter/display all reflect the operationally meaningful moment (check-in), not
  the later trip-creation timestamp. Server trigger removes the mobile-version dependency and the
  500-task join coverage gap. Field is guaranteed present, so the query-axis switch is safe.
- **Negative / risks:**
  - The query-axis switch (step 4) is **gated on backfill completeness**. It must not ship before
    the backfill has populated every trip_record, or trips drop off the monitor.
  - Adds a new Firestore trigger (ongoing invocation cost, one per trip_record create) and a new
    backfill callable.
  - `checkInAt` for historical trips is an approximation (`createdAt`), not a true check-in time;
    acceptable per the fallback decision, but the two meanings are blended for old data.
  - Ordering by `checkInAt` requires a Firestore composite index matching the query's filters.
- **Follow-up:** verify the manual/self-created check-in flow also writes `task.checkInAt` so the
  trigger has a source for driver-created tasks.

## Alternatives considered

- **Fix the mis-keyed lookup only** (`trip.id` → `trip.taskId`) and keep the join. Rejected as the
  final state: leaves the 500-task coverage gap and cannot drive sort/filter (check-in lives on the
  task, not the trip). Retained as a possible *interim* display-only step if scope needs staging.
- **Mobile write only + one-time backfill.** Rejected as authoritative: trips created by
  un-updated app builds would miss `checkInAt` until every driver upgrades.
- **Keep the `createdAt` query axis; denormalize for display + in-memory sort only.** Safer (no
  disappearing docs) but does not satisfy the requirement to filter by check-in and re-queries/sorts
  awkwardly. Rejected in favor of the query switch guarded by a complete backfill.
- **Leave `checkInAt` null where no real check-in time exists.** Rejected: historical trips would
  vanish once the query sorts by `checkInAt`.

## Related

- Glossary: [glossary.md](../glossary.md) — check-in, `checkInAt`, trip_record, task, `taskId` vs `id`.
- Spec (to be created): `shared-docs/specs/checkin-time-on-trip-records.md` via `/spec-new`.
