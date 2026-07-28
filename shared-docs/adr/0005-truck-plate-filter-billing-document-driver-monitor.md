# ADR 0005 — Filter by truck licence plate on Billing Document and Driver Monitor (identity, provenance, and the invoice guard)

- **Status:** Accepted (2026-07-20) — implemented locally 2026-07-22, awaiting owner UI verification
  and deploy. The §"Follow-ups" measurement was run: 1 of 812 prod trips (0.1%) loses its displayed
  plate under decision 6, so no `backfillTripTruckData` re-run was needed.
- **Deciders:** Samart Kas (product owner), Claude
- **Area:** logitrack-web — `app/app/accounting/billing-document/page.tsx`,
  `features/drivers/components/DriverMonitorDashboard.tsx`, `features/drivers/hooks/useDriverMonitor.ts`,
  `features/accounting/api/billing.ts`, `lib/billingDocument.ts`

## Context

Admins asked to filter both `/app/accounting/billing-document` and `/app/driver-monitor` by truck
licence plate ("what did truck 70-1234 run this month?"). Both pages already *display* a plate, so the
request reads as a small addition. Grilling the code showed it is not, for four reasons.

### 1. The two pages read the plate from different documents

- **Driver Monitor** — `DriverMonitorDashboard.tsx:331-339` (`getLicensePlate`) prefers
  `trip.truckLicensePlate` (the snapshot stamped onto `trip_records`), and falls back to
  `driver.activeTruck.truckPlate`.
- **Billing Document** — `features/accounting/api/billing.ts:770` reads **`task.licensePlate` only**.
  `TaskInfo` (`:753`) never fetches the trip snapshot, and `BillingTripRow`
  (`lib/billingDocument.ts:56`) carries `truckLicensePlate` but **no `truckId`**.

These normally converge, because check-in stamps the confirmed truck back onto the **task**
(`check_in_page.dart:1040-1048`) and the loading phase copies task → trip
(`loading_phase_page.dart:208,1392`). Rows created before the per-task truck work merged
(`ae34000`, 2026-07-15) have no such guarantee. So the *same trip* can render plate A on Driver
Monitor and plate B on the invoice.

### 2. `activeTruck` is live state, and a filter turns a guess into a query result

`getLicensePlate:336-337` already documents the hazard — the author avoided `currentAssignment`
because it *"would restamp a historical row with today's truck."* `activeTruck` has the **same defect
with a shorter window**: a driver mid-trip on truck B causes every plate-less historical trip of
theirs to render as B. Tolerable as a display; as a **filter predicate** it returns trips the selected
truck provably never ran, and the result set changes as drivers check in and out. That makes
plate-filtered exports (`useDriverMonitor.ts:673`, `getTripsForExportResolved`) irreproducible.

This is the failure mode [ADR 0003](0003-edit-forms-fail-loudly-on-legacy-docs.md) §1 and §5 name
directly: *"trading a visible form error for an invisible data defect"* and *"inventing one is worse
than an empty field."*

### 3. A plate string is not a truck identity

`grep` over `lib/`, `features/`, and `validate/` finds **no plate-normalisation helper** — plates are
raw strings everywhere. `TruckPlateField.tsx:47,52` documents the gap in the data:

> *"A legacy task can carry a plate with no `truckId`, or a plate whose truck was deleted."*

So the invariant "every plate string corresponds to exactly one `trucks` doc" **does not hold**, and a
filter keyed purely on the string is vulnerable to formatting drift (`70-1234` vs `70 - 1234` vs
`70-1234 กรุงเทพมหานคร`) with nothing in the codebase to reconcile it.

### 4. On Billing Document, the filtered set *is* the invoice

`filteredTrips` (`billing-document/page.tsx:119-133`) currently feeds four consumers at once:

| Consumer | Line | Destination |
|---|---|---|
| preview table | `:469` | screen |
| `breakdown` (per-type subtotals) | `:155-168` | screen **and** `saveBillingStatement(...breakdown)` `:212` |
| `grandTotal` → `withholdingTax` → `totalNet` | `:170-172` | screen **and** the statement registry `:210-212` |
| `canDownload` | `:231` | enables the button |

`handleDownload` (`:192-229`) calls `saveBillingStatement` — a **write that consumes an invoice
number** and persists `tripCount` and `totalAmount` — then `downloadBillingZip`. Any filter wired into
`filteredTrips` therefore narrows the invoice actually issued and recorded. The existing charge-type
and หลัก/เสริม toggles (`:122-129`) do exactly that, deliberately. A plate filter is a **review**
dimension, not a billing dimension, and must not.

### Owner-asserted invariants

- The plate filter exists for **review and dispute handling** ("customer questions one truck's
  trips"), never to split an invoice per truck.
- An invoice is always issued for the **whole** customer + month set, subject only to the existing
  charge-type and หลัก/เสริม toggles.
- Admins want the per-truck **subtotal** visible on screen while reviewing.

## Decision

1. **The plate filter is preview-only on Billing Document. It never reaches the invoice.**
   Split the single `filteredTrips` into two derived sets: the **invoice set** (customer + month +
   charge-type + หลัก/เสริม toggles) which feeds `handleDownload`, `saveBillingStatement`, and
   `downloadBillingZip`; and the **preview set** (invoice set + plate filter) which feeds the table.

2. **The summary cards and totals follow the plate filter** so the per-truck subtotal is readable at a
   glance. This is a deliberate divergence between the number on screen and the number on the invoice,
   and it is made safe only by point 3.

3. **Download is blocked while a plate filter is active.**
   `canDownload` (`:231`) gains `&& plateFilter === "all"`. The button is disabled with an explicit
   reason (*"ล้างตัวกรองทะเบียนก่อนออกบิล"* / *"Clear the plate filter to generate the invoice"*).
   Because point 2 lets the screen show ฿14,200 while the invoice would be ฿412,500, a mismatched
   invoice must be made **structurally impossible** rather than left to operator discipline — an
   invoice number is consumed and a statement row persisted, and there is no void/credit-note flow.

4. **The filter matches on `truckId`, with the raw plate string as a fallback bucket.**
   Match `row.truckId === selected.truckId`, or — when the row has no `truckId` — 
   `row.truckLicensePlate === selected.plate`. Options are built from the **loaded rows** (the
   established `partnerOptions` pattern, `useDriverMonitor.ts:423-430`), never from the `trucks`
   collection, so every option provably matches at least one row and orphan plates stay reachable.
   Orphan-plate options are labelled as not-in-fleet (*"ไม่มีในระบบ"*).

5. **`BillingTripRow` gains `truckId`.** Thread it through `TaskInfo` (`billing.ts:753`) and the three
   `rows.push` sites (`:834`, `:863`, `:895`) so Billing Document can match on identity like Driver
   Monitor does. No new query — `truckId` is already on the task docs being fetched at `:762-775`.

6. **`getLicensePlate` falls back to the trip's own task, not to `activeTruck`.**
   New chain: `trip.truckLicensePlate ?? taskById[trip.taskId]?.licensePlate ?? "-"`. The
   `driver.activeTruck.truckPlate` branch (`:338`) is removed. `useDriverMonitor` already assembles a
   full-coverage `taskById` (`:476-506`, including the chunked `documentId() in` fetch at `:489-505`
   for trips older than the realtime `limit(500)`) and currently discards it except for check-in times
   (`:510-515`); expose a `plateByTripId` map alongside `checkInAtByTaskId`.

7. **One resolver serves both the column and the filter on Driver Monitor**, which is safe *only*
   because point 6 made it deterministic. What is displayed is what is filtered.

8. **The plate filter joins `ExportFilterCriteria`** (`useDriverMonitor.ts:75-83`) so exports honour
   it. Point 6 is what makes such an export reproducible.

## Consequences

**Positive**

- Billing Document and Driver Monitor resolve a plate from the same source for the first time; the
  same trip can no longer show plate A on screen and plate B on the invoice.
- Plate-filtered results and exports are reproducible — they depend only on stored per-trip data, not
  on who is driving what right now.
- A plate filter cannot cause a partial invoice to be issued under a consumed invoice number.
- Filtering on `truckId` survives plate re-registration and formatting drift, while the fallback
  bucket keeps legacy/orphan rows findable rather than silently absent.

**Negative / risks**

- **Screen total ≠ invoice total while a plate is selected** (point 2). Mitigated structurally by
  point 3, but the UI must still label the cards clearly as the filtered subtotal.
- **Display regression on Driver Monitor**: rows that today show a plate via `activeTruck` and have
  neither a trip snapshot nor a task plate will now show `-`. Per ADR 0003 §5 this is the correct
  trade — an honest gap over an invented value — but it is user-visible.
- Two filter groups with opposite semantics now sit in the same Billing Document filter card: the
  charge-type / หลัก/เสริม toggles change the invoice, the plate filter does not. The UI must
  distinguish them visually, or the disabled Download button will read as a bug.
- Orphan-plate buckets rely on exact string equality, so the same physical truck recorded with two
  spellings appears as two options. Accepted: no normaliser is introduced (see Alternatives).

**Follow-ups**

- Spec the UI via `/spec-new` before building — this ADR fixes the *why*, not the layout.
- i18n keys in `context/locales/{en,th}/accounting.ts` and `.../driverMonitor.ts` (en + th, per
  `.vibe-rules.md`).
- Measure how many loaded trips have neither `truckId` nor `truckLicensePlate` nor a task plate. If
  material, that argues for a `backfillTripTruckData` re-run rather than a UI change.
- Consider whether Income (`app/app/accounting/income/page.tsx`) should get the same filter; out of
  scope here.

## Alternatives considered

- **Make the plate a billing dimension (filter narrows the invoice).** Rejected by the owner: plates
  are a review dimension. Invoices are issued per customer + month, and a per-truck invoice has no
  counterpart in the customer contracts.
- **Cards always show the invoice set; only the table narrows.** Safest, and removes the divergence in
  point 2 entirely — rejected because reading a truck's subtotal is the primary use case, and the
  point-3 guard makes the divergence non-dangerous.
- **Confirm dialog on download instead of blocking.** Rejected: an acknowledgement step in front of an
  irreversible, invoice-number-consuming write is weaker than removing the path, and dialogs get
  click-through.
- **Match on the plate string only (no `truckId`).** Cheapest — no change to `BillingTripRow` — but
  breaks silently on formatting drift with no normaliser to lean on.
- **Introduce `lib/plateKey.ts` to normalise plates (strip spaces/dashes/province).** Rejected for now:
  it risks *collisions* across provinces (`70-1234 กทม` vs `70-1234 ชลบุรี`), which is worse than a
  missed match, and `truckId` already solves the identity problem for post-`ae34000` data. Revisit only
  if orphan-plate volume proves material.
- **Keep the `activeTruck` fallback and accept non-deterministic filtering.** Initially chosen, then
  reversed during grilling once the export-reproducibility consequence (`getTripsForExportResolved`)
  and the conflict with ADR 0003 §5 were made concrete.
- **Source filter options from the `trucks` collection.** Rejected: offers plates with zero matching
  rows, and makes orphan plates unreachable — the exact rows an admin is most likely hunting.

## Related

- Glossary: [../glossary.md](../glossary.md) — [[Licence plate]], [[Truck identity]], [[Orphan plate]],
  [[Invoice set]], [[activeTruck]].
- [ADR 0003](0003-edit-forms-fail-loudly-on-legacy-docs.md) — never invent a value to paper over a
  legacy gap; the principle behind decisions 6 and 7.
- [ADR 0001](0001-checkin-time-on-trip-records.md) — the `taskById` full-coverage map reused by
  decision 6 was built for the Check-in column.
- Per-task truck selection (`CLAUDE.md` §40, merge `ae34000`, 2026-07-15) — the work that introduced
  `tasks.truckId` and `drivers.activeTruck`.
- Legacy BMAD [ADR-0005](../../logitrack-web/_bmad-output/planning-artifacts/adr/ADR-0005-supplementary-trips.md)
  — *different namespace*; the `ADR-0005` referenced in `lib/billingDocument.ts:58,65` is that one, not
  this document.
