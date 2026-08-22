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
join at read time (e.g. `truckId`/`licensePlate` onto transactions). Kept consistent by the **writing
client** and/or a one-time **backfill callable** — this project has **no Firestore document triggers**
(the database region `asia-southeast3` supports none; every reactive write is an app-invoked callable).
Note: `checkInAt` is deliberately **not** denormalized — it was considered for `trip_records` under
ADR 0001 but kept on the task and resolved by a live join instead, precisely because no trigger could
make a write-time copy authoritative (see [[`checkInAt`]] / ADR 0001 Update).

## Driver Monitor

The web admin page at `/app/driver-monitor` (`app/app/driver-monitor/page.tsx` +
`features/drivers/components/DriverMonitorDashboard.tsx`, data via
`features/drivers/hooks/useDriverMonitor.ts`). Lists trip_records in a date range with driver, route,
status, billing, and the check-in timestamp column that ADR 0001 fixes to show the task's
[[`checkInAt`]] via a corrected live join (instead of the trip's [[`createdAt` (trip_record)]]).

## jobCategory (หลัก/เสริม)

The billing classification of a trip: `"PRIMARY"` (หลัก, the contracted route) or `"SUPPLEMENTARY"`
(เสริม, a separately-agreed ad-hoc trip). It selects **which rate card** bills the trip
(`selectBillingRateEntry` filters on it, `functions/src/core/billingCompute.ts:157`), whether the
**fuel multiplier** applies (SUPPLEMENTARY skips it), and — for SUPPLEMENTARY — triggers the
[[Frozen price]] behavior.

**Source of truth is the *task*** (`tasks.jobCategory`, set at assign time per [ADR 0016](adr/0016-explicit-job-category-at-assign.md)). The value on
`trip_records.jobCategory` is a **denormalized cache** of it. That cache is (a) **seeded at trip
creation** by the mobile client copying the task value, (b) **refreshed at billing time** by
`tripBillingOnDelivered.ts`, and (c) **corrected** by the `setTripJobCategory` callable (ADR 0002).
Before ADR 0010 the cache had only writer (b) — a fragile, retry-less, early-returning price
computation — so a trip whose billing was skipped or failed ("No rate", not-yet-billed, etc.) carried
**no** category, and readers silently defaulted it to หลัก (dangerous on an invoice) or showed `—`.
Per **ADR 0010** every reader now resolves `trip.jobCategory` → `task.jobCategory` → a loud "unknown"
marker, never a guessed หลัก. **Not** the same axis as `jobType` (`first_mile | line_haul`) — reusing
that field was explicitly rejected ([ADR 0015](adr/0015-supplementary-trips.md)). Correcting it on an already-billed trip is a dedicated
admin action that re-derives the price (ADR 0002).

## Frozen price

A billing snapshot that must not move on recompute. Any trip whose [[jobCategory (หลัก/เสริม)]]
resolves to `SUPPLEMENTARY` is also written with `billingManualOverride: true`; the recompute guard
`tripFrozen = billingManualOverride === true || jobCategory === "SUPPLEMENTARY"` makes even a
`forceRecompute` (bulk backfill, fuel re-import) skip it (`tripBillingOnDelivered.ts:126-129`). The
freeze protects separately-agreed เสริม prices. It is deliberately escapable **only** by an explicit
admin edit — the `setTripJobCategory` callable in ADR 0002 — which is the one path allowed to move a
frozen price, by re-deriving it and clearing the override when the category becomes PRIMARY.

## Licence plate (ทะเบียนรถ)

A **display string**, not an identity. Stored denormalized in several places with different
provenance: `trucks.licensePlate` (fleet master, canonical), `tasks.licensePlate` (the truck assigned
to *that job*, rewritten by the driver at check-in — `check_in_page.dart:1040-1048`),
`trip_records.truckLicensePlate` (snapshot copied from the task at loading —
`loading_phase_page.dart:208,1392`), and `drivers.activeTruck.truckPlate` / 
`drivers.currentAssignment.truckPlate`.

There is **no normalisation helper anywhere in the codebase** — `70-1234`, `70 - 1234`, and
`70-1234 กรุงเทพมหานคร` are three distinct values. Never compare plates to establish that two rows
concern the same vehicle; use [[Truck identity]]. Introducing a normaliser was considered and rejected
in [ADR 0005](adr/0005-truck-plate-filter-billing-document-driver-monitor.md) because stripping the
province suffix can *collide* across provinces.

## Truck identity

`trucks/{truckId}` — the only stable identifier for a vehicle. Carried on
`tasks.truckId`, `trip_records.truckId` (`validate/tripRecordSchema.ts:91`), and
`drivers.activeTruck.truckId`. Survives plate re-registration and formatting drift, which
[[Licence plate]] does not. Filtering, grouping, and joining on a vehicle must key on `truckId`;
plates are for display and for the [[Orphan plate]] fallback only. Introduced platform-wide by the
per-task truck work (`CLAUDE.md` §40, merge `ae34000`, 2026-07-15) — rows predating it may carry a
plate with no `truckId`.

## Orphan plate

A `licensePlate` string on a task or trip with **no corresponding `trucks` doc** — either a legacy row
written before [[Truck identity]] existed, or one whose truck was since deleted. Named and handled
explicitly in `features/tasks/components/TruckPlateField.tsx:47,52`. Their existence is why the
invariant *"every plate corresponds to exactly one truck"* is false, and why plate-based UI must give
orphans a reachable fallback bucket rather than dropping them
([ADR 0005](adr/0005-truck-plate-filter-billing-document-driver-monitor.md) §4).

## Invoice set vs preview set

On `/app/accounting/billing-document`, two derived row sets that must be kept apart:

- **Invoice set** — customer + month, narrowed only by the charge-type and [[jobCategory (หลัก/เสริม)]]
  toggles. This is what `handleDownload` bills: it feeds `saveBillingStatement` (a **write** that
  consumes an invoice number and persists `tripCount` / `totalAmount`) and `downloadBillingZip`.
- **Preview set** — the invoice set narrowed further by **review-only** filters such as truck plate.
  Affects the on-screen table and summary cards only.

A filter is a *billing dimension* only if a customer could legitimately be invoiced for that subset
alone. Plate is not; it is a review dimension, and Download is disabled while a plate filter is active
so the two sets can never silently diverge into a wrong invoice
([ADR 0005](adr/0005-truck-plate-filter-billing-document-driver-monitor.md) §1-3).

## `activeTruck`

`drivers/{id}.activeTruck` = `{truckId, truckPlate, taskId}` — **"the truck this driver is responsible
for right now."** Written at check-in (`check_in_page.dart:1062-1071`), cleared when the job ends. It
exists because Firestore rules cannot query tasks, so the maintenance gate needs the current truck
readable on the driver doc.

It is **live state**, and therefore invalid as a fallback when resolving a *historical* row's truck:
a driver mid-trip on truck B would restamp every plate-less past trip of theirs as B, and any filter
or export built on it yields different rows on different days. Distinct from
`currentAssignment` (the driver's *home* truck binding, a default at assign time) and from
`tasks.truckId` (the truck for *that job*) — the three must never be collapsed. See
[ADR 0005](adr/0005-truck-plate-filter-billing-document-driver-monitor.md) §6.

## Place identity

The canonical key for a physical location: a hub `source_id` or a SOC key. It is **not** what
`trip_records.origin` / `.destination` contain — those are `z.string().optional()`
(`validate/tripRecordSchema.ts:82-83`) holding whatever the writer produced: a hub's English display
name from the picker (`loading_phase_page.dart:1836` → `:1377-1378`), OCR text such as
`ALANG-A - วังทองหลาง` (`ocr_screenshot_service.dart:219-220,450-456`), or an actual code
(`add_delivery_stop_dialog.dart:79`).

So one place can be stored as `SPK890146`, `ประเวศ18`, **and** `ALANG-A - วังทองหลาง`. Grouping,
filtering, or joining on a place must first resolve the raw value to its identity — via the
**`nameToCode` direction only**, never the merged bidirectional map that caused the "No rate" billing
failure (`CLAUDE.md` §39). Same relationship to its display string as [[Truck identity]] has to
[[Licence plate]]. Rows that resolve to nothing are an [[Unresolved place]]. Defined in
[ADR 0006](adr/0006-origin-destination-filter-driver-monitor.md) §1.

## Unresolved place

An `origin` / `destination` string that resolves to no hub or SOC — typically OCR noise, a renamed
hub, or a value predating the master record. `resolveHubOrSocDisplay`
(`logitrack-web/lib/hubDisplay.ts:61-73`) **returns such a value unchanged**, so on screen it is
indistinguishable from a real place name.

Distinct from an *absent* value (`null` — e.g. a hub with no `source_name_en`, since the picker writes
`sourceNameEn` and empty becomes `null` at `loading_phase_page.dart:1377-1378`). The two must not be
merged: place-filter UI gives **each distinct unresolvable string its own reachable option** and
absent values a single "not specified" bucket — the same split as [[Orphan plate]] versus a missing
plate ([ADR 0006](adr/0006-origin-destination-filter-driver-monitor.md) §4).

## Delivery stop

One entry of `trip_records.deliveryStopsProgress[]` (`validate/tripRecordSchema.ts:57-59`):
`{index, destination, status, deliveredAt, …}`. A multi-drop trip therefore has **N destinations, not
one**, and `trip.destination` is only meaningful when the array is empty.

Two consequences that must be kept apart: a trip **matches** a destination filter if *any* stop
matches, but a filtered **export** emits only the matching stop rows — the export loop
(`DriverMonitorDashboard.tsx:449-481`) already writes one spreadsheet row per stop, so exporting all N
would put other destinations into a file someone will sum
([ADR 0006](adr/0006-origin-destination-filter-driver-monitor.md) §5-6).

## Standby

A billable event where a driver checked in and then **had no delivery to run** ("งานหมด"). Stored in
its own collection `standby_records`, written by mobile `submitStandbyRecord`
(`standby_repository.dart:26-113`), which also flips the linked task to `Completed` and the linked
trip to `status: "standby"`. Priced as a **flat rate per event**, independent of duration —
`computeStandbyBilling` (`core/billingCompute.ts:380-394`) picks the customer's
`standby_rate_entries` row effective on the [[Billing date]], falling back to the oldest entry
(`selectStandbyRateEntry:359-374`) and then to `customer_service_fees` with `feeType: "standby"`
(`standbyBilling.ts:136-147`).

A standby is **not** a trip: it never appears in the `trip_records` query behind the Billing Document,
which filters `status == "delivered"` (`billing.ts:729`). Old standby that still lives as
`trip_records.status == "standby"` is therefore invisible to billing until migrated
(`functions/scripts/migrate-standby-trips.js`).

**Invariant ([ADR 0008](adr/0008-standby-billing-visibility-and-recompute-semantics.md) §1):** a
standby record carries its own `customerId`. It must remain billable after its task is edited,
cancelled, or deleted — a standby with no task is a legitimate event, not bad data.

## Billing date

The single timestamp that decides **which rate applies and which invoice a row lands on**: the moment
the service completed — `deliveredTimestamp` for a [[`trip_record`]], `endedAt` for a [[Standby]].

Before [ADR 0008](adr/0008-standby-billing-visibility-and-recompute-semantics.md) three different
fields were doing this job in three places (rate selection, page grouping, recompute scan), which is
why a recompute could miss exactly the rows an invoice contained. `createdAt` is **provenance only**
and must never decide a period — it is `serverTimestamp()` in the admin backfill dialog
(`standby-backfill-dialog.tsx:338`), i.e. the day someone typed a past event in, not the day it
happened. See [[`createdAt` (trip_record)]].

## Billing period

A `(customerId, {month, year})` pair — the unit an invoice is issued for. Materialised as a
`billing_statements` doc holding **totals only, no line items** (`lib/billingStatement.ts:33-62`),
numbered `{CUSTOMER_CODE}-{YYYYMM}-{SEQ}`. A row belongs to the period its [[Billing date]] falls in.

Because statements store no row ids, there is currently **no way to ask which rows a given invoice
was built from** — the reason the recompute guard in
[ADR 0008](adr/0008-standby-billing-visibility-and-recompute-semantics.md) §5 is period-level rather
than row-level.

## Draft period

A [[Billing period]] whose `billing_statements` doc is absent or `status: "draft"` — as opposed to
`sent` / `paid`, which mean a document is already in the customer's hands
(`lib/billingStatement.ts:31`).

**Invariant ([ADR 0008](adr/0008-standby-billing-visibility-and-recompute-semantics.md) §5):**
recompute may rewrite prices **only** in a draft period. In a sent/paid period the write is refused
and reported with the invoice number; correcting it requires cancelling or credit-noting that invoice
first. This sits *on top of* [[Frozen price]] — a frozen row stays frozen even in a draft period.

## Unpriced standby

A [[Standby]] record that cannot produce a billable row: no `billingEstimateThb`, because no customer
could be resolved (no `customerId`, no `taskId`, or a task with no linked customer —
`standbyBilling.ts:62-86`) or no standby rate / service fee exists for that customer.

Today such a record is **discarded without a trace** at `billing.ts:877`, so unbilled work is
indistinguishable from no work.
[ADR 0008](adr/0008-standby-billing-visibility-and-recompute-semantics.md) §6 makes it visible on the
Billing Document and excluded from the [[Invoice set vs preview set|invoice set]]; §7 requires it be
fixed case-by-case, never by assigning a default customer.

Distinct from a **stale** price: an unpriced record is missing *input*, so no amount of recompute can
fix it — the opposite of the defect recompute exists to solve.

## Rate round

รอบปรับราคา — one price announcement, valid over the **half-open interval**
`[effectiveFrom, nextEffectiveFrom)`. A [[Billing period]] may contain several; the owner confirmed
more than two in a month is normal when diesel moves.

A round is selected per record, never per month: the newest row whose `effectiveFromMs <=` the
[[Billing date]] wins (`lib/billingCompute.ts:141-170` for the rate card, `:172-181` for the
surcharge). N rounds therefore produce N price slices, and the invoice splits into N lines for an
affected route because `groupToLineItems` keys on `vehicleClass::route::unitPrice`
(`lib/billingDocument.ts:244`).

**Invariant ([ADR 0009](adr/0009-multiple-rate-rounds-within-one-billing-period.md) §2):**
`effectiveFrom` is **Bangkok midnight**, and the intervals are half-open — no instant belongs to two
rounds, none to zero. Rows written before that ADR store `Date.UTC(...)`
(`features/accounting/api/billing.ts:99-101`), i.e. **07:00 ICT**, so every boundary has a 7-hour
window that was priced at the previous round. See [[Announcement row]].

## Fuel band

ช่วงราคาน้ำมัน — a ฿1.00 range of the retail diesel price, written `36.01–37.00`, that maps to a flat
per-trip surcharge. Uniform steps, uniform ฿-per-step; the band is the **half-open-above** interval
`(n, n+1]` identified by its lower integer `n`. `baselineBandFloor` names the band that carries `+0`,
so `41` means `41.01–42.00` → `+0`.

The surcharge is **signed**: diesel below the baseline is a genuine discount and is never clamped to
zero.

**Invariant ([ADR 0009](adr/0009-multiple-rate-rounds-within-one-billing-period.md) §3):** the band
floor is `Math.ceil(satang / 100) - 1` on integer satang. `Math.floor(price)`
(`app/app/accounting/rate-card/page.tsx:589`) classifies into `[n, n+1)` instead and so puts a price
of exactly `x.00` — common under Thai price caps — one band too high, overcharging the whole round.

**Invariant (§4):** the band is denormalized onto each priced record
(`billingFuelBandLowerThb` / `billingFuelBandUpperThb` / `billingReferenceFuelPriceThb`) at compute
time. It is never resolved at render time by following `billingFuelAdjustmentId`, because that doc is
mutable — see [[Announcement row]] and [[Frozen price]].

## Announcement row

A row in `customer_rate_entries` or `customer_fuel_rate_adjustments`: the record that an announcement
*was made*, not the current opinion of what a price should be.

**Invariant ([ADR 0009](adr/0009-multiple-rate-rounds-within-one-billing-period.md) §1):**
announcement rows are **immutable**. A mistake is corrected by writing a new row; a round that should
not exist is **voided** (`voidedAt`, `voidedReason`), never deleted. The in-place
`updateCustomerFuelRateAdjustment` / `deleteCustomerFuelRateAdjustment`
(`features/accounting/api/billing.ts:333-364`) are withdrawn from this path — editing one silently
changes the meaning of every [[Frozen price]] already computed from it.

Two rows sharing an `effectiveFrom` are **not** resolved by recency: the sort key ties, the sort is
stable, and the winner is whichever auto-generated document id Firestore returns first
(`functions/src/tripBillingOnDelivered.ts:195-201` — no `orderBy`). Immutability plus a later
`effectiveFrom` is what keeps [[Rate round]] intervals disjoint.

Distinct from `fuel_daily_snapshots/{yyyy-MM-dd}`, the create-only observation of what diesel
actually cost that day (`functions/src/core/persistFuelMonthlySnapshot.ts:69-82`) — the *input* an
announcement is priced from (§5), as opposed to `fuel_monthly_snapshots/{yyyy-MM}`, which is
overwritten on every sync (`:58-66`) and is therefore not a billing input at all.

## Evidence photo

A photo captured by the driver during a trip's loading or delivery phase and stored under
`trip_records`. It is **not a raw snapshot**: `photo_overlay_service.dart:177` bakes a burned-in
overlay (GPS, reverse-geocoded address, Thai-era timestamp, `LogiTrack Pro` branding, a Google-Maps
QR) into the JPEG **before** upload, and the pre-overlay original is never stored. Uploaded to
`trip_records/{tripId}/{photoType}.jpg` (`trip_records_repository.dart:10,217-228`), referenced in
`trip_records.photos[]` as `{url, type, geocoding}` (`trip_record.dart:274`), and served from a
**public-read** Storage path (`storage.rules:35`).

**Completeness invariant:** the flat `photos[]` array is the whole set. Multi-drop delivery writes each
stop's photos to **both** `deliveryStopsProgress[].photos` **and** the merged flat `photos[]`
(`delivery_trip_repository.dart:187-195`), so enumerating `TripRecord.photos` yields loading +
single-delivery + per-stop with no join. Defined in
[ADR 0018](adr/0018-driver-self-download-trip-photos.md).

## Photo type

The `type` string on an [[Evidence photo]], identifying the workflow step. Loading: `runsheet`,
`runsheet_extra_1..3` (`loading_phase_page.dart:36`), `pre_close`, `closing`, `seal`
(`loading_phase_page.dart:32`). Single delivery: `pre_open`, `opening`, `empty_container`,
`runsheet_received` (`delivery_phase_page.dart:22`). Multi-stop: `stop_{index}_{type}`.

**Order invariant:** the stored `photos[]` order is **insertion/replace order**, not workflow order —
the array is built by `mergeTripPhotosReplacingTypes` (`delivery_trip_repository.dart:187`). Any
"in workflow order" presentation (e.g. the bulk photo download in
[ADR 0018](adr/0018-driver-self-download-trip-photos.md) §3) must sort by an **explicit type-rank**
(loading → single delivery → multi-stop by ascending index), with unknown/legacy types last.

## Assigned round

รอบเวลาของงานตาม assign — the dispatch slot an admin set when assigning the job: the task's `date` +
`time` (HH:MM) (`validate/taskSchema.ts:61-63`). It lives **only on the task**; `trip_records` carries
`taskId` but not the time, and the trip's `createdAt`/`std` is when the driver *saved loading*, not the
assigned slot (`trip_record.dart:73-84`). Resolve it by fetching the task via
[[`taskId` vs `id`|`trip.taskId`]] at read time and **fall back to `trip.createdAt`** when there is no
`taskId` (driver-created manual trips, legacy rows) — same fetch-not-denormalize choice as
[[`checkInAt`]]. Used to label downloaded evidence-photo files in
[ADR 0018](adr/0018-driver-self-download-trip-photos.md) §4-5.

---

# Driver compensation

> Folded in from the BMAD driver-compensation glossary on 2026-08-09 when that pipeline was retired
> ([ADR 0017](adr/0017-retire-bmad-wds-tooling.md)). Grounded in the compensation compute engine
> (`lib/compensationCompute.ts` ↔ `functions/src/core/compensationCompute.ts`) and the payroll UI.

## Helper (ผู้ช่วย)

A driver who rides along on another driver's task to **assist or to train**, without being the
assigned (main) driver of that task. "Helper" and "trainee/training" are the **same role for pay
purposes** — there is no separate rate or rule; intent does not change compensation. See
[[Helper assignment]], [[Helper-day]].

## Main driver (คนขับหลัก)

The driver assigned to a task (`tasks.driverId`). Earns trip pay for the task, **not** helper pay.

## Helper assignment

Designating the [[Helper]] for a task. Stored on `tasks.helperDriverIds` (Auth UID). **Exactly one
helper per task** — the field stays an array **only** for Firestore `array-contains` index
compatibility and is capped at length 1 ([ADR 0011](adr/0011-helper-pay-data-model.md)). Primary path:
the **admin** sets it when assigning the task; fallback: the **main driver** may set it at check-in
(mobile) when the admin hasn't. Admin can review/edit before the payout run is generated.

## Helper-day (วันทำงานของผู้ช่วย)

**One** unit of helper pay = **one work window** on which a driver acted as helper, regardless of how
many tasks they helped on in that window. The "day" is **not** the calendar day; it is the **work
window 12:00:00 of day D → 11:59:59 of day D+1** (Asia/Bangkok), **keyed to day D**, so a shift that
crosses midnight is one day ([ADR 0012](adr/0012-helper-day-window.md)). Anchored by the main driver's
task **`checkInAt`**; if absent, fall back to **`tasks.date`** at 12:00. De-duplicated to one per
[[Window key]]. A helper still earns the day even if **no trip was delivered** in the window
(check-in is proof of work).

## Window key (D)

The day a [[Helper-day]] window starts (its 12:00 side): `bangkokDateKey(checkInAt − 12h)`. Determines
both de-duplication and [[Round (R1 / R2)]] membership — a check-in at 11:00 on the 16th keys to the
15th → R1 ([ADR 0012](adr/0012-helper-day-window.md)).

## Eligible helper-day

A [[Window key]] on which the driver had **no own assigned task / delivered trip** in the same window.
A driving window always pays as trips, never additionally as a helper-day; the exclusion is evaluated
**per window key**, not per calendar day. `helperDayRateThb` (default 400) is the flat THB per eligible
helper-day; the payroll line-item category is `HELPER_PAY` (`EARNING`).

## Line-item breakdown

Every payroll line item stores its own breakdown at **generation time** (`quantity`, `unitRate`,
`description`, `meta`) so the detail view (`PayrollReviewDialog`) shows *how* each figure was reached
and stays correct even if config later changes ([ADR 0013](adr/0013-payroll-lineitem-breakdown.md)).
The UI falls back to name+amount for older payrolls that lack the fields. **Trip-pay split:**
`TRIP_COMMISSION` is emitted as **two** lines when both apply — weekday (`weekdayRateThb`) and holiday
(`holidayRateThb`). **Penalty breakdown:** penalty lines carry `meta =
{ installmentIndex, installmentsTotal, remainingThb, totalThb }`.

## Cash advance (เบิกล่วงหน้า)

Money paid to a driver **before** a pay round closes, recorded by admin/HR at the withdrawal date and
deducted **in full, once**, in the **next** pay round — no installments, no interest, no carry-over
([ADR 0014](adr/0014-cash-advance.md); collection `driver_advances`, **implementation pending**). The
**deduction round** is computed once at creation from `withdrawnAt` (Asia/Bangkok): withdrawn day ≤ 15
→ **R2 same month**; day ≥ 16 → **R1 next month** — so **R1 can now carry a deduction**, breaking the
old "deductions only in R2" assumption. The **advance cap** (≤ ½ of earnings so far) is enforced by
admin/HR judgement, **not the system**. Payroll line-item category `CASH_ADVANCE` (`DEDUCTION`).

## Round (R1 / R2)

Semi-monthly pay window: **R1** = days 1–15, **R2** = 16–end. [[Helper-day]]s are assigned to a round
by their [[Window key]] day D, not the raw check-in timestamp
([ADR 0012](adr/0012-helper-day-window.md)).

## Recompute (payroll)

Re-running `generateDriverPayoutRun` for a period+round. Overwrites `DRAFT` payouts only; an `APPROVED`
payout is corrected via a post-approval adjustment (Story 3.4). Distinct from billing
[[Rate round|recompute]], which concerns trip prices.

## เที่ยวเสริม (supplementary trip)

A trip billed at a **separately agreed price** that does not come from the primary rate card; keyed by
[[jobCategory (หลัก/เสริม)]] `= SUPPLEMENTARY` and given [[Frozen price]] treatment once it has
happened ([ADR 0015](adr/0015-supplementary-trips.md), explicit at assign time per
[ADR 0016](adr/0016-explicit-job-category-at-assign.md)). The **supplementary rate card** is not a
separate collection — it is `customer_rate_entries` rows tagged `jobCategory = SUPPLEMENTARY`, a filter
dimension on `selectBillingRateEntry`. **Report display rules (Excel detail):** for J&T the source-hub
origin shows the hub **code** (`SPK-GW`) not the billing name; vehicle class **`PICKUP` displays as
`4WH`**; supplementary rows carry **`เสริม`** in หมายเหตุ.
