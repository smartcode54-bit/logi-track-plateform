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

The single timestamp that decides **which rate applies and which invoice a row lands on**: by default
the moment the service completed — `deliveredTimestamp` for a [[`trip_record`]], `endedAt` for a
[[Standby]].

Before [ADR 0008](adr/0008-standby-billing-visibility-and-recompute-semantics.md) three different
fields were doing this job in three places (rate selection, page grouping, recompute scan), which is
why a recompute could miss exactly the rows an invoice contained. `createdAt` is **provenance only**
and must never decide a period — it is `serverTimestamp()` in the admin backfill dialog
(`standby-backfill-dialog.tsx:338`), i.e. the day someone typed a past event in, not the day it
happened. See [[`createdAt` (trip_record)]].

**Per-billing-entity exception (ADR 0027).** A billing entity whose `billingDateBasis` is `"plan"`
(e.g. CJSF/JNT) reconciles by the **plan date** they send us day-by-day, not by when the driver
finished. The flag lives on the entity's profile — `customers.billingDateBasis` or, when a partner is
billed directly, `subcontractors.billingDateBasis`; resolution reads customers first, then
subcontractors (`resolveBillingDateBasis`). For such a customer the billing date of a trip is the task's plan date (`tasks.date`), not
`deliveredTimestamp` — so a job planned on the 30th but delivered on the 1st still bills in the
planned month. It is denormalized onto the trip as `trip_records.billingDate` (a queryable Timestamp
the billing math, page grouping and recompute scan all read for that customer). Everyone else, and
standby for every customer, stays on the delivery instant. See
[ADR 0027](adr/0027-plan-date-billing-axis.md).

## Plan date

วันแผนงาน — `tasks.date`, the day the customer scheduled the job, chosen by the admin at assign time
(the day-by-day plan a customer like CJSF sends). For a [[Billing date|plan-basis]] customer it is the
billing axis and the period filter on the Billing Document (denormalized to `trip_records.billingDate`,
ADR 0027). It is **not** necessarily the day the work was actually done — see [[Actual work date]].

## Actual work date

วันรับจริง — the day the job is actually done, which the model splits into two distinct facts (ADR 0028):

- **Recorded actual** — `tasks.checkInAt` (device `Timestamp` at check-in,
  `logitrack-mobile/.../checkin_repository.dart:124`) and `trip_records.deliveredTimestamp` (delivery).
  Driver-gated, post-hoc, authoritative, un-editable. No `actualDate` field duplicates it.
- **Scheduled/dispatch actual** — `tasks.actualPickupAt`, the real date-time the **admin dispatches**
  the driver to go, set on the assign form. The STD to `checkInAt`'s ATD.

Both are **decoupled from billing**: for a plan-basis customer the invoice follows the [[Plan date]],
never the actual date, and no warning is raised when they differ. Absent when a job was never
scheduled/checked-in. See [ADR 0028](adr/0028-plan-date-vs-actual-work-date.md).

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

**Overlay caveat ([ADR 0019](adr/0019-app-screenshots-as-mandatory-evidence.md)):** as of ADR 0019
`photos[]` is a **mixed** set — overlaid captures *plus* un-overlaid [[Customer-app screenshot]]s
(`checkin_app`, `truck_release`, `arrived` / `stop_{i}_arrived`). "Every entry is an overlaid capture"
is no longer true; a consumer that assumes the baked overlay (e.g. the ADR-0018 download) must expect
some entries without it.

## Photo type

The `type` string on an [[Evidence photo]], identifying the workflow step. Loading: `runsheet`,
`runsheet_extra_1..3` (`loading_phase_page.dart:36`), `pre_close`, `closing`, `seal`
(`loading_phase_page.dart:32`). Single delivery: `pre_open`, `opening`, `empty_container`,
`runsheet_received` (`delivery_phase_page.dart:22`). Multi-stop: `stop_{index}_{type}`.

Extended by [ADR 0019](adr/0019-app-screenshots-as-mandatory-evidence.md) with four **un-overlaid**
[[Customer-app screenshot]] types: `checkin_app` (ranked first), `truck_release` (end of the loading
group), and `arrived` / `stop_{i}_arrived` (start of each delivery / stop group).

**Order invariant:** the stored `photos[]` order is **insertion/replace order**, not workflow order —
the array is built by `mergeTripPhotosReplacingTypes` (`delivery_trip_repository.dart:187`). Any
"in workflow order" presentation (e.g. the bulk photo download in
[ADR 0018](adr/0018-driver-self-download-trip-photos.md) §3) must sort by an **explicit type-rank**
(loading → single delivery → multi-stop by ascending index), with unknown/legacy types last. The
ADR-0019 types must be added to that rank or they fall into the unknown-last bucket.

## Customer-app screenshot

A driver-supplied image of **the customer's own app** (SPX / J&T hub app), attached as extra proof at
three workflow moments ([ADR 0019](adr/0019-app-screenshots-as-mandatory-evidence.md)): at
[[check-in]] (`checkin_app` — "มาถึง / เข้า stand"), at loading save (`truck_release` — "ปล่อยรถ"), and
before the first delivery photo (`arrived` / `stop_{i}_arrived` — "มาถึง"). Distinct from an
[[Evidence photo]]: it is **un-overlaid** (`skipOverlay: true`), because it was captured elsewhere and
stamping the driver's current GPS/time would fabricate provenance; it may come from **camera OR
gallery**; and it is **mandatory** — its gate blocks save/check-in/delivery.

**What "mandatory" does and does not mean:** it guarantees *an image is attached*, not that it is the
*correct* image. Un-overlaid + gallery-allowed means a stale or reused screenshot passes the gate;
this reuse gap is accepted (ADR 0019 §6). A hard anti-reuse boundary would be a separate ADR.

**Storage:** the check-in one has no `trip_record` yet ([[Check-in]] is task-level), so it is written
to `tasks.checkInAppScreenshotUrl` and **copied forward** into `trip_records.photos[]` as
`{url, type:'checkin_app'}` at the [[Loading phase]] — a best-effort
[[Denormalization (in this codebase)|denormalization]] with the task as source of truth. The other two
are written straight into `trip_records.photos[]`. See [[Photo type]] for the rank.

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

## Customer LINE notification

A Flex message pushed to a customer's / partner's **LINE group** from the **Wanpenradchada** Official
Account at three moments — [[Check-in]], job-complete (delivered), and [[Standby]] — by the callable
`sendCustomerLineNotification` (`functions/src/lineNotify.ts`, `asia-southeast1`). Mobile fires it
best-effort so a LINE failure never blocks the write ([ADR 0025](adr/0025-customer-line-group-notifications.md)).
The destination group is the `lineGroupId` on the trip's resolved `customers`/`subcontractors` doc; empty
= off. The channel access token is a Functions secret (`LINE_CHANNEL_ACCESS_TOKEN`), never Firestore. The
driver name is always Thai (`fullNameTh`), and the driver **รหัส** is `customerDriverIds[<customer code>]`
— **never** the national ID card, because the message leaves the company (ADR 0025 §C). Idempotent per
record via `lineCheckinNotifiedAt` / `lineDeliveredNotifiedAt` / `lineNotifiedAt`. Incident/delay has
**no** message of its own — see [[Delay note]].

## Evidence gallery

A public, read-only web page showing a job's evidence photos, opened from the `📷 ดูรูปหลักฐาน` button on
a delivered or [[Standby]] card. Served by the HTTP function `tripEvidence` (`functions/src/tripEvidence.ts`,
`asia-southeast1`), gated by an **unguessable token** on the record (`trip_records.evidenceToken` for a
trip, `standby_records.evidenceToken` for a standby) — no login, so a whole group can view it. Photos are
read server-side with the Admin SDK, so Firestore/Storage are never opened for public reads
([ADR 0025](adr/0025-customer-line-group-notifications.md) §7). For a delivered trip the gallery includes
both the trip photos and any [[Delay note|incident]] photos. Bearer-style: anyone holding the link can
view it; expiry/revoke is a follow-up.

## Delay note

How an incident/delay report reaches the customer. Instead of a separate LINE message, when a delivered
trip has ≥1 `incidentReport` (linked only by `tripId`, `incident_report_repository.dart:81-96`) the
delivered card's `หมายเหตุ` line **names the delay cause(s)** in Thai — mapped from the locale-independent
`incident_cause_*` key stored in `delayCause` (selected as a key at `incident_report_page.dart:371`) — and
the [[Evidence gallery]] plus the button's photo count include the incident's map/situation photos under a
"เหตุล่าช้า" group ([ADR 0025](adr/0025-customer-line-group-notifications.md) §5).

---

# Platform migration (mv-go)

> Added 2026-10-09 with [ADR 0029](adr/0029-migrate-firebase-stack-to-go-postgres.md) (Proposed):
> the strangler move from Firebase to Go + PostgreSQL 18. Full detail in `developer-spec.md` §10–§13.

## Ownership class

Which system **writes** a Firestore collection during the strangler migration, decided per collection
from which client writes it today, so that no document ever has two masters:

- **A — web-only** (e.g. `customers`, `companies`, the four rate tables, `billing_statements`,
  `billing_counters`, fuel snapshots, `payroll`, `driver_penalties`, `security_events`): PostgreSQL
  becomes the writer at that domain's phase and the Firestore copy is frozen — except `customers`,
  carrier profiles and `companies`, which are also projected back to Firestore during P1–P5 because
  Cloud Functions still read them.
- **B — mobile reads, web writes** (`hubs`, hub/SOC distances, `trucks`, `drivers` master fields,
  `truckAssignment`, `holidays`, `broadcasts`, `settings/mobile_app`): PostgreSQL writer plus a
  [[Compat projection]] back to Firestore until P7b. Mobile-written sub-fields (`drivers.fcmToken`,
  `drivers.activeTruck`, `mobile_installations`, driver-created hubs) stay mirrored Firestore→PG.
- **C — mobile writes** (`tasks`, `trip_records`, `standby_records`, `incidentReport`,
  `vehicle_expenses`, `maintenance`, `chats` + messages, `leave_requests`): Firestore stays the
  single writer until P7b; Go reads the PostgreSQL mirror and changes data by [[Write-back]].
  `users` is the exception: users, credentials and sessions are PostgreSQL-owned from P0, and only
  the mobile-written `lastLogin*` and `fcmTokens` fields are mirrored until P7b.

`cmd/etl` loads every collection at the start of P1 and keeps mirroring every collection that is not
yet PostgreSQL-owned. The Go env `PG_OWNED_DOMAINS` records which domains have flipped. Defined in
[ADR 0029](adr/0029-migrate-firebase-stack-to-go-postgres.md) Decision 11.

## Compat projection

The one-way **PostgreSQL → Firestore** copy of a class B [[Ownership class|collection]] after
PostgreSQL becomes its writer, so installed 3.x APKs that read Firestore directly keep working until
the P7b flip, when every projection stops (and, during P1–P5, of the class A customers, carrier
profiles and companies that Cloud Functions still read). An idempotent upsert keyed by
`legacy_doc_id`. A row first created in PostgreSQL gets the uuid string as its Firestore doc id
(stored back in `legacy_doc_id`) and is written in legacy shape (legacy status literals such as
`Checked in` or `PENDING`, `dateStr` as `ddMMyyyy`, `driverId` = `drivers.legacy_doc_id`). It never
writes mobile-owned fields and skips rows that originated in Firestore, so it cannot loop. Verified
by a daily reconciliation (counts + per-row checksum of projected fields). Defined in
[ADR 0029](adr/0029-migrate-firebase-stack-to-go-postgres.md).

## Write-back

How the Go API changes a class C collection while Firestore is still its single writer (P2–P7a): Go
validates, authorizes and computes, then writes the Firestore document with the Admin SDK — the same
operations today's callables and the web's direct writes perform. The change reaches PostgreSQL
through the mirror; the handler waits up to 3 s for the mirror acknowledgement (Redis
`cache:mirror_ack:{collection}:{doc_id}`) and otherwise answers `202` with the Firestore document
echoed (see [[Mirror lag]]). The layer (~15 operations) is throwaway by design: at P7b the repository
flips to PostgreSQL. Defined in [ADR 0029](adr/0029-migrate-firebase-stack-to-go-postgres.md).

## Callable shim

A Cloud Function kept under its **existing name and contract** whose body only forwards to the Go
API, so installed APKs that call it by name keep working (`.vibe-rules.md:365-393` as of commit
`4f552099`). Applies to the five mobile-called callables: `setDriverClaims`,
`sendCustomerLineNotification`, `computeTripBillingSnapshot`, `addDeliveryStop`,
`markBroadcastRead`. Each shim calls the matching `/v1/mobile/*` route on the public listener with
two credentials: `X-Api-Key` (the secret of an `api_keys` row with scope `cf_shim`, held in the
Functions params `LOGITRACK_API_BASE_URL` and `LOGITRACK_API_KEY`) and the caller's Firebase ID token
forwarded as `Authorization: Bearer`. Go verifies that token against the securetoken issuer from P2
onward, whatever `AUTH_FIREBASE_BRIDGE_MODE` says, and acts as that driver (`driver:self`); there is
no shared static token and no acting-user header. Retirement evidence is the Cloud Functions
invocation metric per callable plus the shim's own counter; it is deleted only after zero traffic for
two app release cycles (P8). Defined in [ADR 0029](adr/0029-migrate-firebase-stack-to-go-postgres.md).

## Mirror lag

The delay between a Firestore write to a mirrored collection (any collection not yet
PostgreSQL-owned — every class C collection until P7b — or a mobile-owned field of class B; see
[[Ownership class]]) and the matching PostgreSQL row update by the one-way Firestore→PG mirror.
Target p95 < 5 s during P2–P7, alerted on and reconciled daily. It matters because Go serves class C
reads from PostgreSQL, so a [[Write-back]] could otherwise look lost. It disappears at P7b, when
PostgreSQL becomes the writer. Defined in [ADR 0029](adr/0029-migrate-firebase-stack-to-go-postgres.md).

## ETL quarantine

The work list of things `cmd/etl` could not map faithfully from Firestore: rows in `etl.quarantine`
with a reason code (`tenant_unresolved`, `driver_unresolved`, `task_ambiguous`, `bad_timestamp`,
`status_out_of_vocab`, `duplicate_hub_source_id`, …; the full list is in Appendix A §A.3) and the
raw source, managed with `etl quarantine list|resolve`. Policy: **never silently default, never drop
a billable row**. An unmappable nullable field loads as `NULL` with a field-level entry; a row whose
tenant cannot be resolved still loads, under the [[Quarantine tenant]]; legacy duplicates that block
a new UNIQUE constraint load and are reported for the owner to choose winners, and those constraints
(migration `0010`) are applied only after the owner signs off the quarantine report (production
order `goose up-to 9` → ETL → sign-off → `goose up`). Driver references keep the raw value in
`legacy_driver_ref` with `driver_ref_match` (`doc_id` / `auth_uid` / `name` / `none`). Defined in
[ADR 0029](adr/0029-migrate-firebase-stack-to-go-postgres.md).

## Quarantine tenant

The single `tenants` row with `kind = 'quarantine'` (partial unique, like the one `own_fleet` row),
with the fixed id `00000000-0000-7000-8000-00000000000f` inserted by migration `0002` (the own-fleet
id, by contrast, is chosen per environment through `OWN_FLEET_TENANT_ID`). A row whose tenant chain
does not resolve gets `tenant_id` = this tenant and `tenant_source = 'quarantine'`: RLS hides it from
every tenant, and only a platform admin lists it (`GET /v1/tenants/quarantine/rows`) to re-home it.
Legacy tasks with no resolvable driver land here and are re-homed in bulk once the owner confirms.
The scheduler job `tenancy.orphan-scan` counts these rows and raises a security event when the count
is above zero. This makes the ADR 0026 rule "an unresolvable row is readable by nobody" explicit
instead of accidental. See [[ETL quarantine]]. Defined in
[ADR 0029](adr/0029-migrate-firebase-stack-to-go-postgres.md) Decision 4.

## BFF (web backend-for-frontend)

The Next.js server layer between the browser and Go; the browser only ever calls same-origin
`/api/*`. The generic proxy `app/api/go/[...path]/route.ts` (Node runtime) reads the HttpOnly cookie
`lt_at`, adds `Authorization: Bearer`, `X-Request-Id` and `X-Forwarded-For`, forwards to
`GO_API_INTERNAL_URL` over the private network, streams the response back (SSE included), and checks
`Origin` / `Sec-Fetch-Site` on mutations. It never refreshes tokens (it cannot see `lt_rt`) and
answers `404` for Go's session paths (`v1/auth/` login, google, refresh, tenant, logout, logout-all,
sse-ticket, exchange) and `v1/bridge/*`. Cookies are set only by the auth route handlers
`POST /api/auth/login`, `POST /api/auth/google`, `GET /api/auth/google/nonce`,
`POST /api/auth/refresh`, `GET /api/auth/refresh?next=` (navigations redirected by `proxy.ts`),
`POST /api/auth/logout`, `POST /api/auth/tenant` and `POST /api/auth/firebase-token`; Go returns
tokens in the JSON body and never sets cookies. `lt_at` (Path `/`) and `lt_rt` (Path `/api/auth`) are
HttpOnly, Secure, SameSite=Lax. On a `401` with `token_expired` the browser's `goFetch` runs one
shared refresh (cross-tab lock; forced past the 120 s no-op when `details.reason` is
`claims_changed`) and retries once. The anonymous waitlist and partner-interest forms use the
unauthenticated, rate-limited BFF route handlers `POST /api/forms/waitlist` and
`POST /api/forms/partner-interest`, which call Go's internal `POST /v1/waitlist` and
`POST /v1/partner-interest`. It holds **no business logic**. It exists
because production was a static export with no server (`next.config.ts:10`); the edge gate
`proxy.ts` verifies `lt_at` against Go's JWKS and checks route capabilities from `GET /v1/me` before
any `/app/*` page renders. See [[Internal vs public listener]]. Defined in
[ADR 0029](adr/0029-migrate-firebase-stack-to-go-postgres.md) Decision 9.

## Internal vs public listener

The Go `api` process binds two HTTP listeners. **Internal** (`API_INTERNAL_ADDR`, private network
only, reached by the [[BFF (web backend-for-frontend)|BFF]] and by the APK publish CLI): every
`/v1/*` route — including the web SSE stream `/v1/events`, mobile-release administration
(`/v1/app-releases*`, `/v1/app-installations*`) and the anonymous web forms (`/v1/waitlist`,
`/v1/partner-interest`) — plus `/.well-known/jwks.json` and `/healthz`. **Public** (`API_PUBLIC_ADDR`,
behind Caddy): only `/v1/mobile/*` (including mobile SSE `/v1/mobile/events?ticket=` and the
[[Callable shim]] targets) and `/v1/auth/*` (mobile has no BFF), `/public/v1/*` (reserved for
signature-verified third-party postbacks — none exist today; the only HTTP function is
`tripEvidence`), `/evidence/*` and `/healthz`. Any other path on the public listener answers `404`,
not `401`, so the surface is not revealed. `X-Forwarded-For` is trusted only from
`TRUSTED_PROXY_CIDRS`. Appendix B marks the listener of every route. Defined in
[ADR 0029](adr/0029-migrate-firebase-stack-to-go-postgres.md) Decision 10.
