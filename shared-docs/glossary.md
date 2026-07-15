# Domain Glossary

Shared vocabulary for LogiTrack. Terms are grounded in the actual data model and code, so AI agents
and humans read the same words the same way. Add a term whenever a discussion turns on what a word
precisely means. Link related terms with `[[wiki-style]]` names where useful.

> Seeded 2026-07-15 during the grilling session behind [ADR 0001](adr/0001-checkin-time-on-trip-records.md).

---

## Check-in

The act of a driver confirming arrival at a pickup/hub before loading begins. Recorded by mobile in
`checkin_repository.dart`, which updates the **task** document: sets `status: "Checked in"` and writes
`checkInAt`, `checkInPhotoUrl`, `checkInLat`, `checkInLng`. Check-in is a **task-level** event, not a
trip-level one.

**Invariant:** a driver must check in before loading, and the trip_record is created at loading.
Therefore every trip_record is preceded by a check-in, and a check-in time is always available at trip
creation. (Historical data may lack a *recorded* `checkInAt`; see the fallback in ADR 0001.)

## `checkInAt`

The canonical check-in **timestamp**, stored on the **task** (`tasks.checkInAt`, Firestore
`Timestamp`). Declared in the task schema as `checkInAt: z.any().optional()`
(`validate/taskSchema.ts:100`, `shared-docs/schemas/taskSchema.ts:100`). It stays **only** on the task
— it is **not** denormalized onto `trip_records`.

The Driver Monitor's **Check-in** column resolves this value by a **live join**: `useDriverMonitor`
builds `checkInAtByTaskId` (keyed by both `task.id` and `task.taskId`, from the full-coverage
`taskById`) and the table looks it up by [[`taskId` vs `id`|`trip.taskId`]], falling back to
`trip.createdAt`. (ADR 0001 originally planned a denormalized `trip_records.checkInAt` via a Firestore
trigger; that was dropped because Firestore in `asia-southeast3` supports no document triggers — see
the ADR Update.)

## Task

A unit of assigned work (`tasks` collection). Carries assignment, route, vehicle
(`truckId`/`licensePlate`/`truckType`), `jobCategory` (หลัก/เสริม), check-in fields, and `status`
(including `"Checked in"`). Identified two ways — see [[`taskId` vs `id`]].

## `trip_record`

The record of an executed trip (`trip_records` collection), created by mobile at the **loading**
phase — *after* check-in. Model in `logitrack-mobile/lib/features/home/data/models/trip_record.dart`.
Carries `createdAt`; it does **not** carry check-in time — check-in stays on the task and the Driver
Monitor resolves it via a live join (see [[`checkInAt`]]). Links back to its task via `trip.taskId`.

## `createdAt` (trip_record)

The moment the `trip_record` document was created — i.e. at **loading**, which is *later* than
[[check-in]]. Used today as the Driver Monitor's range + `orderBy` axis
(`useDriverMonitor.ts:312-313`). Not the same as check-in time; conflating the two is the bug ADR 0001
corrects. Also surfaced as [[Depart]] once check-in gets its own column.

## Depart

The moment the driver creates the `trip_record` — i.e. departs after loading. **Not a separate field
or event:** "Depart" is a display alias of [[`createdAt` (trip_record)]]. On the Driver Monitor the
**Depart** column ("Depart" / "ออกเดินทาง") shows `trip.createdAt`, sitting between the
[[check-in]] column and the Driver column. Display-only — the table still sorts and filters by
[[`checkInAt`]]. (Decision recorded in the check-in spec, not a new schema concept.)

## `taskId` vs `id`

A recurring source of mis-keyed lookups:

- **`task.id`** — the task's Firestore **document id**.
- **`task.taskId`** — a business/task identifier field on the task; in practice also usable as a task
  doc id (the monitor queries tasks by `documentId() in [trip.taskId...]`,
  `useDriverMonitor.ts:492`).
- **`trip.id`** — the **trip_record's** document id (a *different* document from its task).
- **`trip.taskId`** — the trip's pointer to its task's doc id. **This is the correct join key** from a
  trip to its task. Using `trip.id` to look up task-keyed data is the defect described in ADR 0001.

## Loading phase

The mobile stage after check-in where the driver records loading and the `trip_record` is created
(`loading_phase_page.dart`). At this point `task.checkInAt` is already read into `_taskCheckedInAt`
(line ~199), so the check-in time is available to stamp onto the trip_record.

## Denormalization (in this codebase)

Copying a value from its source-of-truth document onto a consuming document to avoid a cross-document
join at read time (e.g. `truckId`/`licensePlate` onto transactions; `checkInAt` onto `trip_records`
per ADR 0001). Kept consistent by a write-time mechanism — here, a Firestore `onCreate` trigger plus a
one-time backfill for history.

## Driver Monitor

The web admin page at `/app/driver-monitor` (`app/app/driver-monitor/page.tsx` +
`features/drivers/components/DriverMonitorDashboard.tsx`, data via
`features/drivers/hooks/useDriverMonitor.ts`). Lists trip_records in a date range with driver, route,
status, billing, and the timestamp column that ADR 0001 retargets from [[`createdAt` (trip_record)]]
to [[`checkInAt`]].
