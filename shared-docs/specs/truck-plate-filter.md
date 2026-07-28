# Spec: Truck licence-plate filter (Billing Document + Driver Monitor)

> **Status:** 🟡 Implemented locally (2026-07-22) — T1–T10 done, awaiting owner UI verification (AC2–AC9) and deploy
> **Owner:** Samart Kas
> **Created:** 2026-07-20
> **Domain:** accounting (Billing Document) + drivers (Driver Monitor) — **web only**
> **Related:** [ADR 0005](../adr/0005-truck-plate-filter-billing-document-driver-monitor.md) (authoritative — this spec implements it), [glossary.md](../glossary.md) ([[Licence plate]], [[Truck identity]], [[Orphan plate]], [[Invoice set vs preview set]], [[activeTruck]])

---

## 1. Problem & Goal

Admins cannot answer *"what did truck 70-1234 run this month?"* on either
`/app/accounting/billing-document` or `/app/driver-monitor`. Both pages display a plate but neither
lets you filter by one, so the question is answered by eye-scanning or by exporting to Excel.

Worse, the two pages resolve the plate from **different documents** and can disagree about the same
trip (`billing.ts:770` reads `task.licensePlate`; `DriverMonitorDashboard.tsx:331-339` reads
`trip.truckLicensePlate` and falls back to live `activeTruck` state).

Goal: a plate filter on both pages that returns the same rows for the same truck, is reproducible
across time, and **cannot cause a partial invoice to be issued**.

## 2. Scope

**In scope:**
- Shared plate-resolution + filter-matching SSOT in `lib/truckPlate.ts` (new).
- Plate filter (searchable combobox) on Driver Monitor — filter bar **and** export dialog.
- Plate filter (searchable combobox) on Billing Document — **preview-only**, with a Download guard.
- Split Billing Document's single `filteredTrips` into an **invoice set** and a **preview set**.
- Thread `truckId` through `TaskInfo` → `BillingTripRow`.
- Replace Driver Monitor's `activeTruck` display fallback with the trip's own task plate.
- Read-only diagnostic script to size the display regression **before** UI work (blocking).
- i18n `en` + `th`.

**Out of scope (ทำทีหลัง / ไม่ทำ):**
- Income page (`app/app/accounting/income/page.tsx`) — same filter may be wanted later; not now.
- Plate **normalisation** (`lib/plateKey.ts`) — explicitly rejected in ADR 0005 (province collision risk).
- Mobile (Flutter) — no mobile surface filters by plate.
- Any change to billing **computation**. `lib/billingCompute.ts` / `functions/src/core/billingCompute.ts`
  are **not touched**, so the two-file sync rule does not apply here.
- Per-truck invoicing (splitting a customer's invoice by truck) — rejected in ADR 0005.
- New Firestore collections, indexes, or rules changes — all filtering is client-side over rows the
  pages already load.

## 3. Requirements

**Functional**

- **R1.** New SSOT `lib/truckPlate.ts` exporting: `resolveTripPlate()`, `truckFilterKey()`,
  `buildPlateFilterOptions()`, `rowMatchesPlateFilter()`, and the sentinels
  `PLATE_FILTER_ALL = "all"` / `PLATE_FILTER_NONE = "__none__"`. Both pages use it; neither
  re-implements matching. Mirrors the `lib/driverName.ts` / `lib/truckType.ts` precedent.
- **R2.** Plate resolution chain is `trip.truckLicensePlate ?? task.licensePlate ?? null`.
  The `driver.activeTruck.truckPlate` branch (`DriverMonitorDashboard.tsx:338`) is **removed**;
  unresolvable rows render `"-"`. (ADR 0005 §6)
- **R3.** Filter matches on [[Truck identity]] first: `row.truckId === selected.truckId`. A row with no
  `truckId` matches only by exact plate string against an [[Orphan plate]] option. (ADR 0005 §4)
- **R4.** Filter options are built from the **loaded rows**, never from the `trucks` collection —
  the established `partnerOptions` pattern (`useDriverMonitor.ts:423-430`). Every option therefore
  matches ≥1 row. Each option shows its row count; orphan options are badged *"ไม่มีในระบบ"*.
- **R5.** A `PLATE_FILTER_NONE` bucket collects rows that resolve to no plate at all, mirroring
  `DRIVER_MONITOR_PARTNER_NONE` (`useDriverMonitor.ts:63`).
- **R6.** If the currently-selected plate disappears from the options (e.g. the date range narrows),
  the selection stays visible in the list rather than silently resetting — mirroring
  `partnerFilterSelectCodes` (`DriverMonitorDashboard.tsx:251-261`).
- **R7.** Driver Monitor: plate filter appears in the filter bar (after `partnerFilter`,
  `:774-789`) and participates in `tripMatchesClientFilters`.
- **R8.** Driver Monitor: plate filter is part of `ExportFilterCriteria` (`useDriverMonitor.ts:75-83`)
  and is honoured by `getTripsForExport` / `getTripsForExportResolved`. (ADR 0005 §8)
- **R9.** Driver Monitor: the plate **column** and the plate **filter** use the same resolver, so what
  is displayed is what is filtered. (ADR 0005 §7)
- **R10.** `BillingTripRow` gains `truckId?: string`, populated via `TaskInfo` (`billing.ts:753`) at
  all three `rows.push` sites (`:834`, `:863`, `:895`). No new query — `truckId` is already on the
  task docs fetched at `:762-775`. (ADR 0005 §5)
- **R11.** Billing Document: `filteredTrips` splits into **`invoiceTrips`** (customer + month +
  charge-type + หลัก/เสริม toggles) and **`previewTrips`** (`invoiceTrips` + plate filter).
  `handleDownload`, `saveBillingStatement`, and `downloadBillingZip` consume **`invoiceTrips` only**.
  (ADR 0005 §1)
- **R12.** Billing Document: the preview table, summary cards, `breakdown`, `grandTotal`,
  `withholdingTax`, and `totalNet` are all derived from **`previewTrips`** — so the cards show the
  selected truck's subtotal. (ADR 0005 §2)
- **R13.** Billing Document: `canDownload` (`:231`) gains `&& plateFilter === PLATE_FILTER_ALL`.
  With a plate selected the Download button is **disabled** and an explanatory line is shown.
  (ADR 0005 §3)
- **R14.** Billing Document: while a plate filter is active, a banner above the table states the
  filtered count vs the invoice count, so the divergence introduced by R12 is never silent.
- **R15.** Billing Document: the plate filter is visually separated from the charge-type and
  หลัก/เสริม toggles, which **do** change the invoice — a labelled "review filter" group, so the
  disabled Download button reads as intended rather than as a bug.

**Non-functional**

- **N1.** i18n complete in `en` **and** `th` — `context/locales/{en,th}/accounting.ts` and
  `context/locales/{en,th}/driverMonitor.ts`. No hardcoded strings (`.vibe-rules.md` MANDATORY).
- **N2.** Options and filtering are `useMemo`-ised over already-loaded rows. No new Firestore reads,
  no new indexes, no added cost.
- **N3.** `lib/truckPlate.ts` is pure and unit-tested (`lib/truckPlate.test.ts`), following
  `lib/truckType.test.ts`.
- **N4.** TypeScript strict — no `any` in new code.
- **N5.** Filter results must be **reproducible**: identical inputs yield identical rows regardless of
  who is driving what at query time (this is what R2 buys).

## 4. Design

**Data model (Firestore)**

- **No schema changes. No new collections, indexes, or rules.** Every field read already exists:
  `trip_records.truckId` / `.truckLicensePlate` (`validate/tripRecordSchema.ts:91-92`),
  `tasks.truckId` / `.licensePlate` (`validate/taskSchema.ts`).
- In-memory type change only: `BillingTripRow.truckId?: string` (`lib/billingDocument.ts:39-66`) and
  `TaskInfo.truckId?: string` (`features/accounting/api/billing.ts:753`).

**Cloud Functions / billing**

- ⚠️ **Not touched.** No change to `lib/billingCompute.ts` or `functions/src/core/billingCompute.ts`,
  so the mandatory two-file billing sync does **not** apply. This feature never changes an amount —
  it only chooses which already-computed rows are displayed.
- One new **read-only ops script**: `functions/scripts/diagnose-trip-plate-coverage.js`, following
  `diagnose-billing.js` (same `--project` flag, ADC auth, writes nothing).

**Shared SSOT — `lib/truckPlate.ts` (new)**

```ts
export const PLATE_FILTER_ALL  = "all";
export const PLATE_FILTER_NONE = "__none__";

export interface PlateFilterOption {
  /** Stable select value: `id:<truckId>` | `plate:<raw>` | PLATE_FILTER_NONE */
  value: string;
  /** Plate string for display, or null for the no-plate bucket. */
  label: string | null;
  /** True when the row carries a plate but no truckId — an [[Orphan plate]]. */
  isOrphan: boolean;
  count: number;
}

/** trip snapshot → task plate → null. NEVER falls back to activeTruck (ADR 0005 §6). */
export function resolveTripPlate(input: {
  tripPlate?: string | null;
  taskPlate?: string | null;
}): string | null;

/** Identity key for grouping/matching: truckId when present, else the raw plate. */
export function truckFilterKey(input: {
  truckId?: string | null;
  plate?: string | null;
}): string;

export function buildPlateFilterOptions(
  rows: { truckId?: string | null; plate?: string | null }[]
): PlateFilterOption[];

export function rowMatchesPlateFilter(
  row: { truckId?: string | null; plate?: string | null },
  selected: string
): boolean;
```

Encoding the option value as `id:` / `plate:` keeps identity and string matching unambiguous in a
single select value, and keeps orphan plates addressable.

**Web (Next.js)**

*Driver Monitor* — `features/drivers/`
- `hooks/useDriverMonitor.ts`
  - Expose `plateByTripId: Record<string, string>`, built inside the **existing** effect at `:472-589`
    from the full-coverage `taskById` (`:476-506`) — the same map that already feeds
    `checkInAtByTaskId` (`:510-515`) and is otherwise discarded. No new query.
  - New state `plateFilter` + `setPlateFilter`; new derived `plateOptions`.
  - `tripMatchesClientFilters` gains a `plateFilter` parameter (R7).
  - `ExportFilterCriteria` gains `plateFilter` (R8); both export resolvers pass it through.
  - Reset to page 1 when `plateFilter` changes (join the existing effect at `:698-700`).
- `components/DriverMonitorDashboard.tsx`
  - `getLicensePlate` (`:331-339`) delegates to `resolveTripPlate` with the task plate — the
    `activeTruck` branch is deleted (R2).
  - New searchable combobox after the partner Select (`:789`), built on the `Popover + Command`
    pattern already used by `features/tasks/components/TruckPlateField.tsx`.
  - Same control added to the export dialog beside `exportFilters.partnerFilter` (`:1122`).

*Billing Document* — `app/app/accounting/billing-document/page.tsx`
- `filteredTrips` (`:119-133`) → **`invoiceTrips`**; new **`previewTrips` = invoiceTrips + plate filter**.
- `breakdown` (`:155-168`), `grandTotal`/`withholdingTax`/`totalNet` (`:170-172`), and the table
  (`:469`) read `previewTrips` (R12).
- `handleDownload` (`:192-229`) — `tripCount`, `...breakdown`, `totalAmount`, and
  `downloadBillingZip` all read `invoiceTrips` (R11).
- `canDownload` (`:231`) gains the plate guard (R13).
- Plate combobox rendered in its own bordered "review filter" group in the filter card, visually
  distinct from the two invoice-affecting toggle groups (`:291-359`) (R15).
- Banner above the table when a plate is active (R14).

*Feature-architecture note:* per `.vibe-rules.md`, data access stays in `features/<domain>/api/`
(`billing.ts` for R10) and pure logic in `lib/` (R1); page components hold only UI state.

**i18n keys** (`en` + `th`, flat dotted keys in the existing namespace objects)

`context/locales/{en,th}/driverMonitor.ts`
| Key | en |
|---|---|
| `driverMonitor.filter.licensePlate` | Licence plate |
| `driverMonitor.filter.allLicensePlates` | All plates |
| `driverMonitor.filter.plateNotSpecified` | No plate recorded |
| `driverMonitor.filter.plateSearch` | Search plate… |
| `driverMonitor.filter.plateNotInFleet` | Not in fleet |
| `driverMonitor.filter.plateNoResults` | No matching plate |

`context/locales/{en,th}/accounting.ts`
| Key | en |
|---|---|
| `accounting.billingDocument.filters.licensePlate` | Licence plate |
| `accounting.billingDocument.filters.allLicensePlates` | All plates |
| `accounting.billingDocument.filters.plateSearch` | Search plate… |
| `accounting.billingDocument.filters.plateNotInFleet` | Not in fleet |
| `accounting.billingDocument.filters.reviewFilterGroup` | Review filter (does not affect the invoice) |
| `accounting.billingDocument.preview.filteredBanner` | Showing {shown} of {total} trips for {plate} — the invoice still covers all {total}. |
| `accounting.billingDocument.download.clearPlateWarning` | Clear the plate filter to generate the invoice. |

Thai values to be authored alongside (e.g. `download.clearPlateWarning` → *"ล้างตัวกรองทะเบียนก่อนออกบิล"*).

**Mobile (Flutter)** — none. No `pubspec.yaml` bump.

**Firestore Rules** — none.

## 5. Affected files

| File | Change |
|---|---|
| `logitrack-web/lib/truckPlate.ts` | **new** — SSOT resolver + filter matching (R1–R5) |
| `logitrack-web/lib/truckPlate.test.ts` | **new** — unit tests (N3) |
| `logitrack-web/functions/scripts/diagnose-trip-plate-coverage.js` | **new** — read-only coverage diagnostic (T1) |
| `logitrack-web/features/drivers/hooks/useDriverMonitor.ts` | `plateByTripId`, `plateFilter`, `plateOptions`, export criteria |
| `logitrack-web/features/drivers/components/DriverMonitorDashboard.tsx` | resolver swap, filter combobox, export-dialog control |
| `logitrack-web/features/accounting/api/billing.ts` | `TaskInfo.truckId` + 3 `rows.push` sites (R10) |
| `logitrack-web/lib/billingDocument.ts` | `BillingTripRow.truckId` (R10) |
| `logitrack-web/app/app/accounting/billing-document/page.tsx` | invoice/preview split, guard, banner (R11–R15) |
| `logitrack-web/context/locales/{en,th}/driverMonitor.ts` | i18n |
| `logitrack-web/context/locales/{en,th}/accounting.ts` | i18n |
| `shared-docs/.vibe-rules.md` | Change Log 2026-07-20 + plate-resolution pattern |

## 6. Task breakdown

- [x] **T1. (BLOCKING)** `functions/scripts/diagnose-trip-plate-coverage.js` — count `trip_records`
      that have **no** `truckId`, **no** `truckLicensePlate`, and whose linked task has no
      `licensePlate`. These are the rows that regress from a guessed plate to `"-"` under R2.
      Report totals and a per-month breakdown. **Read-only.** Run against `logitrack-prod`.
- [x] **T2. (GATE)** Review T1's number with the owner. If material, run `backfillTripTruckData`
      (`functions/src/backfillTripTruckData.ts`, Utilities → Backfill) and re-run T1 before T3.
      **Result (prod, 2026-07-22):** 812 trips — 811 (99.9%) carry a trip snapshot, **1 (0.1%)
      regresses to `"-"`** (`LT0Q3224HBKS1`, 2 Mar 2026, no `taskId`). Immaterial → **no backfill
      run**, proceed as specced. Two findings worth carrying forward: only **8.1%** of trips have a
      `truckId` (the rest match via the plate-string bucket — expected for pre-`ae34000` data), and
      `3ฒง1988` (7 trips) is almost certainly a typo of `4ฒง1988` (313 trips), so it appears as its
      own filter option by design (R4: never merge unresolved values).
- [x] **T3.** `lib/truckPlate.ts` + `lib/truckPlate.test.ts` (R1–R5, N3).
- [x] **T4.** `useDriverMonitor`: expose `plateByTripId` from the existing `taskById` effect; add
      `plateFilter` state, `plateOptions`, filter predicate, export criteria (R6–R8).
      *Shipped as `getTripTruck` / `truckByTripId` (truckId + plate + vehicle class in one map)
      rather than a plate-only `plateByTripId`, since the vehicle-class filter needs the same join.*
- [x] **T5.** `DriverMonitorDashboard`: swap `getLicensePlate` to the shared resolver (R2, R9); add
      the searchable combobox to the filter bar and the export dialog (R7, R8).
- [x] **T6.** `billing.ts` + `billingDocument.ts`: thread `truckId` into `BillingTripRow` (R10).
- [x] **T7.** `billing-document/page.tsx`: split `invoiceTrips` / `previewTrips`, rewire cards and
      totals, add the combobox in a "review filter" group, add the banner and the Download guard
      (R11–R15).
- [x] **T8.** i18n `en` + `th` for both namespaces (N1).
- [x] **T9.** `tsc --noEmit`, ESLint, Vitest (2026-07-22: 0 type errors; ESLint 0 errors / 26
      pre-existing warnings; 142 tests green incl. `truckPlate` 17 + `vehicleClass` 9). CI green
      still to confirm on push (AC10).
- [x] **T10.** Update `.vibe-rules.md` Change Log 2026-07-20 + record the plate-resolution pattern
      under Confirmed Patterns.

**Scope addition beyond the ADR:** a **vehicle-class** filter (`lib/vehicleClass.ts` + tests) was
built alongside the plate filter on both pages. It follows the identical shape — options from loaded
rows, a "not specified" bucket, and on Billing Document it is a review filter subject to the same
Download guard.

## 7. Acceptance criteria

- [ ] **AC1. (R1, R3, N3)** `lib/truckPlate.test.ts` covers: same `truckId` with two different plate
      strings matches as one truck; two rows with the same plate but different `truckId` do **not**
      merge; an orphan plate (no `truckId`) is reachable via its own option; a row with neither
      resolves to the `PLATE_FILTER_NONE` bucket.
- [ ] **AC2. (R2, R9)** With a driver checked into truck B, a historical trip of theirs that has no
      plate snapshot and no task plate shows `"-"` — **not** B — and does not appear under any plate
      filter. Verified against a real trip identified by T1.
- [ ] **AC3. (R2)** A trip whose task carries a plate but whose `trip_records` doc has no snapshot
      displays the **task** plate and is returned when filtering by it.
- [ ] **AC4. (R4, R6)** Every plate option returns ≥1 row when selected. Narrowing the date range so
      the selected plate has no rows leaves the selection visible (not silently reset to "all").
- [ ] **AC5. (R7, R9)** On Driver Monitor, filtering by a plate returns exactly the rows whose plate
      column shows that plate — no more, no fewer.
- [ ] **AC6. (R8, N5)** Exporting under a plate filter yields only that truck's rows, and re-running
      the same export after another driver checks into that truck produces an **identical** row set.
- [ ] **AC7. (R11, R13)** On Billing Document with a plate selected, the Download button is disabled
      and shows the clear-the-filter message. Clearing the filter re-enables it.
- [ ] **AC8. (R11)** With the plate filter cleared, the generated ZIP and the `billing_statements`
      row contain the **full** customer+month set — byte-identical to today's output for the same
      inputs (regression check: the split must not change existing behaviour).
- [ ] **AC9. (R12, R14)** With a plate selected, the summary cards show that truck's subtotal **and**
      the banner states "Showing N of M — the invoice still covers all M".
- [ ] **AC10. (R10, N4)** `tsc --noEmit` passes, ESLint clean, Vitest green, CI green.
- [ ] **AC11. (N1)** Every new string renders correctly with the language toggle in both `en` and
      `th`; no raw key text appears in the UI.

## 8. Risks & rollback

| Risk | Mitigation / rollback |
|---|---|
| **Display regression**: rows that show a plate today via `activeTruck` fall back to `"-"` (R2). | T1 sizes it **before** any UI work; T2 gates on running `backfillTripTruckData` if material. Accepted per ADR 0003 §5 — an honest gap beats an invented value. |
| **Screen total ≠ invoice total** while a plate is selected (R12). | Structurally guarded by R13 (Download disabled) and made explicit by R14 (banner). This is the ADR's central trade and must not be softened without superseding it. |
| **The invoice/preview split silently changes today's billing output.** | AC8 is a byte-identical regression check with the filter cleared. The split is the highest-risk edit in this spec — `handleDownload` touches a write that consumes an invoice number. |
| Two filter groups with opposite semantics in one card confuses admins into thinking Download is broken. | R15 (labelled "review filter" group) + R13's inline explanation. |
| Same physical truck spelled two ways appears as two orphan options. | Accepted — no normaliser (ADR 0005 Alternatives). `truckId` already solves this for post-`ae34000` rows; T1 will show whether orphans are material. |
| Plate options grow long on wide date ranges. | Searchable combobox (owner decision, 2026-07-20) rather than a plain Select. |
| **Rollback:** all changes are client-side reads + one additive optional field. | Revert the commit — no migration, no schema change, no data to unwind. |

## 9. Open questions / follow-ups

- **T1's number decides T2.** If a large share of trips lack any plate provenance, the right fix may
  be a `backfillTripTruckData` re-run (or extending it) rather than shipping the regression.
- Should Income (`app/app/accounting/income/page.tsx`) get the same filter? Deferred; if yes it
  reuses `lib/truckPlate.ts` unchanged.
- `lib/truckPlate.ts` is web-only. If mobile ever needs plate grouping, mirror it as
  `logitrack-mobile/lib/core/utils/truck_plate.dart` — the pattern `lib/truckType.ts` ↔
  `core/utils/truck_type.dart` already establishes.
- ADR 0005 notes the "cards always show the invoice set" alternative is one line simpler. If AC9's
  banner proves insufficient in review, that alternative is the fallback — it needs no new ADR, only
  a spec revision (R12 + R14).

---

> ⚠️ `.gitignore` has a broad `*.md` rule — this spec file is **untracked**. Commit with
> `git add -f shared-docs/specs/truck-plate-filter.md`.
