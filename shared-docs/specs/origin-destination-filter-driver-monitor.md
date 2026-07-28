# Spec: Origin & destination filter (Driver Monitor)

> **Status:** 🟡 Implemented locally (2026-07-23) — T1–T8 done, awaiting owner UI verification (AC2–AC11, AC13) and deploy
> **Owner:** Samart Kas
> **Created:** 2026-07-22
> **Domain:** drivers (Driver Monitor) — **web only, read path only**
> **Related:** [ADR 0006](../adr/0006-origin-destination-filter-driver-monitor.md) (authoritative — this spec implements it), [ADR 0005](../adr/0005-truck-plate-filter-billing-document-driver-monitor.md) + [spec](truck-plate-filter.md) (the pattern being reused), [glossary.md](../glossary.md) ([[Place identity]], [[Unresolved place]], [[Delivery stop]])

---

## 1. Problem & Goal

Driver Monitor (`/app/driver-monitor`) filters by driver, status, job type, partner, truck plate and
vehicle class — but not by **where a trip started or ended**, which is what operations actually asks
("what ran out of บางปู last week", "which trucks served ประเวศ18"). Today that question is answered
by eye-scanning the table or exporting to Excel and filtering there.

The naive implementation — a dropdown of the distinct `trip.origin` / `trip.destination` strings —
would be quietly wrong. Those fields are free text written by three different writers, so **one
physical place exists in `trip_records` under several spellings** (`SPK890146`, `ประเวศ18`,
`ALANG-A - วังทองหลาง`). Selecting one spelling would silently hide the other trips to the same place.

Goal: filter by origin and by destination on **resolved place identity**, so one place is one option
regardless of how it was typed, and no trip is unreachable by every option.

## 2. Scope

**In scope:**
- Shared place-resolution + filter-matching SSOT in `lib/placeFilter.ts` (new) + unit tests.
- Origin filter and destination filter (two independent searchable comboboxes) on Driver Monitor —
  filter bar **and** export dialog.
- Destination matching across **all** delivery stops of a multi-drop trip.
- Per-stop row filtering in the Excel export when a destination filter is active.
- Generalising `components/plate-filter-combobox.tsx` into a reusable combobox so the plate, origin
  and destination filters look and behave identically.
- i18n `en` + `th` (`context/locales/{en,th}/driverMonitor.ts`).

**Out of scope (ทำทีหลัง / ไม่ทำ):**
- **The mobile write path.** `loading_phase_page.dart:1836` writes the hub's English display name
  instead of `source_id`. Fixing it needs a mobile release and does not remove the need for a
  read-time resolver (every historical trip still has free text). ADR 0006 §Follow-ups — separate ADR.
- **Backfilling `trip_records.origin` / `.destination` to codes** — rejected in ADR 0006: it rewrites
  historical operational records, cannot recover rows already `null`, and re-accumulates junk
  immediately because the writer is unchanged.
- **Billing Document / Income.** Origin-destination is a review dimension here; ADR 0005's
  invoice-set-vs-preview-set guard is Billing-Document-specific and is not touched.
- Firestore schema, indexes, or rules changes — all resolution is client-side over already-loaded data.
- Any change to billing computation (`lib/billingCompute.ts` / `functions/src/core/billingCompute.ts`
  untouched → the two-file sync rule does not apply).
- The `source_name_en` hub-coverage audit (ADR 0006 §Follow-ups) — reported by this filter, fixed separately.

## 3. Requirements

**Functional**

- **R1.** New SSOT `lib/placeFilter.ts` exporting `resolvePlaceKey()`, `placeFilterKey()`,
  `buildPlaceFilterOptions()`, `rowMatchesPlaceFilter()`, and the sentinels
  `PLACE_FILTER_ALL = "all"` / `PLACE_FILTER_NONE = "__none__"`. Both controls use it; neither
  re-implements matching. Mirrors `lib/truckPlate.ts`.
- **R2.** Resolution uses the **`nameToCode` direction only**, plus code pass-through:
  a raw value is a code if the hub/SOC code→label map knows it (after `normalizeSocIdToKey`);
  otherwise it is looked up in `hubDisplayNameToCode`; otherwise it is [[Unresolved place]].
  The reverse map is **never** consulted — merging the two directions is the documented cause of the
  "No rate" billing failure (`CLAUDE.md` §39, ADR 0006 §1).
- **R3.** Three option buckets, mirroring the plate filter:
  - **resolved** — value `code:<CODE>`, labelled via `resolveHubOrSocDisplay` so the dropdown reads
    exactly like the column it filters;
  - **unresolvable** — value `raw:<exact string>`, **one option per distinct raw string**, labelled
    with the raw value and badged as not-in-master. Never merged with each other, never hidden;
  - **absent** — a single `PLACE_FILTER_NONE` bucket for trips whose value is null/empty.
- **R4.** Options are built from the **loaded trips**, never from the `hubs` master or `SOC_KEYS`, so
  every option matches ≥1 visible trip and no trip is unreachable by every option (ADR 0006 §2).
  Each option shows its trip count.
- **R5.** Origin matches on `trip.origin` only — origin is single-valued per the owner-asserted
  invariant (ADR 0006 §4).
- **R6.** Destination matches a trip when **any** of its `deliveryStopsProgress[]` entries matches,
  falling back to `trip.destination` when that array is empty (ADR 0006 §5).
- **R7.** Origin and destination are two independent controls, ANDed with each other and with every
  existing filter, inside `tripMatchesClientFilters` (`useDriverMonitor.ts:157-200`).
- **R8.** Both join `ExportFilterCriteria` (`useDriverMonitor.ts:86-92`) and are honoured by
  `getTripsForExport` and `getTripsForExportResolved`.
- **R9.** With a destination filter active, the Excel export emits **only matching stop rows** — the
  per-stop loop (`DriverMonitorDashboard.tsx:449-481`) skips non-matching stops, so a filtered export
  totals exactly what was filtered (ADR 0006 §6).
- **R10.** On screen the row stays **whole**: a matching multi-drop trip still shows all its stops in
  the "N stops" badge. Narrowing happens in the file, not in the table (ADR 0006 §6).
- **R11.** Revenue attribution in the export is unchanged: the trip's billing amount is emitted on
  the **first row emitted for that trip**, so a destination-filtered export never double-counts and
  never drops a trip's revenue. (Today `row[10]` is set on `stop.index === 1`, which may be filtered
  out — this requirement makes the existing rule survive R9.)
- **R12.** If the current selection disappears from the options (e.g. the date range narrows), it
  stays readable in the trigger rather than silently resetting to "all" — the plate filter's
  behaviour (`plate-filter-combobox.tsx:51-53`).
- **R13.** `components/plate-filter-combobox.tsx` is generalised into `components/filter-combobox.tsx`
  (configurable icon + i18n key suffixes + option shape); `PlateFilterCombobox` becomes a thin
  wrapper so Billing Document and the plate filter are behaviourally unchanged.

**Non-functional**

- **N1.** i18n complete in `en` **and** `th` — `context/locales/{en,th}/driverMonitor.ts`. No
  hardcoded strings (`.vibe-rules.md` MANDATORY).
- **N2.** Zero new Firestore reads. Resolution is a pure function of the trip plus maps already in
  memory (`sourceIdToName` `:542`, `hubDisplayNameToCode` `:551`). Unlike the plate filter, the
  wide-range export path needs **no** task join, because origin/destination live on the trip doc.
- **N3.** `lib/placeFilter.ts` is pure and unit-tested (`lib/placeFilter.test.ts`), following
  `lib/truckPlate.test.ts`.
- **N4.** TypeScript strict — no `any` in new code.
- **N5.** Options and predicates are `useMemo`/`useCallback`-ised; resolution runs once per trip per
  render pass, not once per option.

## 4. Design

**Data model (Firestore):** no changes. Fields read already exist —
`trip_records.origin` / `.destination` (`validate/tripRecordSchema.ts:82-83`),
`trip_records.deliveryStopsProgress[].destination` (`:57-59`), `hubs.source_id` /
`source_name_th` / `source_name_en`.

**Resolution (`lib/placeFilter.ts`)**

```
resolvePlaceKey(raw, { codeToLabel, nameToCode }) → { key, kind }
   raw empty/null                          → { key: PLACE_FILTER_NONE, kind: "absent" }
   codeToLabel[raw] or codeToLabel[soc(raw)] → { key: `code:${soc(raw)}`, kind: "resolved" }
   nameToCode.get(raw.trim())               → { key: `code:${code}`,     kind: "resolved" }
   otherwise                                → { key: `raw:${raw.trim()}`, kind: "unresolved" }
```

`soc()` is `normalizeSocIdToKey` from `validate/taskSchema`, so `SOCE-xxx` variants collapse the same
way `resolveHubOrSocDisplay` already collapses them for display. The hook owns both maps and passes
them in; the lib stays free of React and Firestore.

**Matching**

- Origin: `rowMatchesPlaceFilter([trip.origin], selected, maps)`.
- Destination: `rowMatchesPlaceFilter(destinationsOf(trip), selected, maps)` where `destinationsOf`
  returns `deliveryStopsProgress.map(s => s.destination)` when non-empty, else `[trip.destination]`.

One helper, two call sites — the multi-value case is the general one and the single-value case is a
one-element array, so there is no second matching path to keep in sync.

**Hook (`useDriverMonitor`)**

- `originFilter` / `destinationFilter` state (default `PLACE_FILTER_ALL`).
- `originOptions` / `destinationOptions` — `useMemo` over `trips`, using the same maps.
- Both added to `tripMatchesClientFilters`, `ExportFilterCriteria`, and the page-reset effect
  (`:849-852`).
- Expose `matchesDestinationFilter(rawDestination, selected)` for the export's per-stop loop, so the
  component never re-derives the predicate.

**Component (`DriverMonitorDashboard`)**

- Two `FilterCombobox`es in the filter bar after the vehicle-class Select, and the same two in the
  export dialog grid.
- Export loop: when `destinationFilter !== PLACE_FILTER_ALL`, `stops.filter(matches…)` before the
  `forEach`; revenue lands on the first emitted row (R11). Single-stop trips are unaffected — the trip
  already passed the filter to reach the loop.

**Why not reuse the plate filter's `truckFilterKey` shape verbatim:** a plate row has one identity;
a trip has *N* destinations. Keeping `placeFilter` value-list-based from the start avoids retro-fitting
multiplicity into a single-value API.

## 5. Affected files

| File | Change |
|---|---|
| `logitrack-web/lib/placeFilter.ts` | **new** — resolution, keys, options, matching |
| `logitrack-web/lib/placeFilter.test.ts` | **new** — unit tests (R2, R3, R5, R6) |
| `logitrack-web/components/filter-combobox.tsx` | **new** — generic searchable filter combobox (R13) |
| `logitrack-web/components/plate-filter-combobox.tsx` | thin wrapper over the generic one |
| `logitrack-web/features/drivers/hooks/useDriverMonitor.ts` | state, options, predicate, export criteria |
| `logitrack-web/features/drivers/components/DriverMonitorDashboard.tsx` | two comboboxes ×2 surfaces; per-stop export filtering |
| `logitrack-web/context/locales/en/driverMonitor.ts` | i18n keys |
| `logitrack-web/context/locales/th/driverMonitor.ts` | i18n keys |
| `shared-docs/.vibe-rules.md` | Change Log + the "display string ≠ place identity" pattern |
| `shared-docs/adr/0006-*.md`, `shared-docs/adr/README.md` | status → implemented |

## 6. Task breakdown

- [x] **T1.** `lib/placeFilter.ts` + `lib/placeFilter.test.ts` (R1–R6, N3) — 23 tests.
- [x] **T2.** `components/filter-combobox.tsx`; refactor `PlateFilterCombobox` onto it and confirm
      Driver Monitor + Billing Document plate filters are unchanged (R13). *Behavioural check is
      AC11, folded into the same browser pass as ADR 0005's own outstanding ACs.*
- [x] **T3.** `useDriverMonitor`: `originFilter` / `destinationFilter` state, options, predicate,
      `ExportFilterCriteria`, page reset, `matchesDestinationFilter` (R7, R8).
- [x] **T4.** `DriverMonitorDashboard`: comboboxes in the filter bar and the export dialog (R7, R8, R12).
      Also renamed the hook's options at the destructure site to `originFilterOptions` /
      `destinationFilterOptions` — the component already had a local `destinationOptions` (the
      `hubs` + `SOC_KEYS` master list for `DeliveryStopsEditor`), and the collision was a TS2451
      build error. The two are deliberately different things per ADR 0006 §2.
- [x] **T5.** Export per-stop filtering + revenue attribution (R9, R10, R11). `buildExportTable`
      takes the active `destinationFilter`; revenue lands on `Math.min(index)` of the *emitted*
      stops (stop 1 unfiltered), and `lastStopIndex` still comes from the full stop list.
- [x] **T6.** i18n `en` + `th` (N1) — 8 keys under `driverMonitor.filter.*`.
- [x] **T7.** 2026-07-23: `tsc --noEmit` 0 errors (was 2 × TS2451), ESLint 0 errors / 20 warnings
      (all pre-existing `no-explicit-any` + `no-img-element`), Vitest 165/165 green. CI not yet run —
      the branch is uncommitted.
- [x] **T8.** `.vibe-rules.md` Change Log + Confirmed Pattern; flip ADR 0006 + README status.
- [ ] **T9. (report, not a gate)** Note the unresolved-option count seen on a typical month and hand
      it to the owner — it is the first concrete measure of free-text junk and feeds the mobile
      write-path ADR. *Read off the badged options during the verification pass (AC9); no script.*

## 7. Acceptance criteria

- [ ] **AC1. (R2, R3)** `placeFilter.test.ts` covers: a code passes through; a TH display name
      resolves to its code; an EN display name resolves to the same code (one option, not two); an
      SOC variant normalises; an OCR string (`"ALANG-A - วังทองหลาง"`) stays unresolved under its own
      key; empty/null lands in `PLACE_FILTER_NONE`; and the reverse direction is never used — a hub
      **code** never resolves to a display-name key.
- [ ] **AC2. (R4)** Every option, when selected, returns ≥1 row. Selecting each option in turn and
      summing the counts accounts for every loaded trip exactly once, per control.
- [ ] **AC3. (R2)** A hub recorded three ways (code, TH name, EN name) appears as **one** option and
      selecting it returns all three trips.
- [ ] **AC4. (R6)** A multi-drop trip whose stop 3 is ประเวศ18 is returned when filtering destination
      by ประเวศ18, even though `trip.destination` is a different place.
- [ ] **AC5. (R10)** That same trip still shows its full "N stops" badge on screen while the filter
      is active.
- [ ] **AC6. (R9, R11)** Exporting under that destination filter yields **only** the ประเวศ18 stop
      rows, and the trip's revenue appears exactly once across the exported rows.
- [ ] **AC7. (R7)** Origin and destination combine (AND) with each other and with driver / status /
      job type / partner / plate / vehicle class; clearing one leaves the others applied.
- [ ] **AC8. (R8)** An export over a date range **wider** than the loaded window honours both filters
      and needs no extra Firestore reads (N2) — verify no task fetch is triggered.
- [ ] **AC9. (R3)** An unresolved value is visible as its own badged option and can be selected to
      isolate exactly those trips.
- [ ] **AC10. (R12)** Narrowing the date range so the selected place has no rows leaves the selection
      readable in the trigger and the table empty — not a silent reset to "all".
- [ ] **AC11. (R13)** The plate filter on **both** pages behaves exactly as before the refactor.
- [ ] **AC12.** `tsc --noEmit` passes, ESLint clean, Vitest green, CI green.
- [ ] **AC13. (N1)** Every new string renders in both `en` and `th` via the language toggle.

## 8. Risks & rollback

| Risk | Mitigation |
|---|---|
| Dropdown noise when OCR misfires — each bad string is its own option. | Accepted in ADR 0006: noise is information; a clean dropdown that hides trips is worse. Counts make the junk obvious. |
| The per-stop export condition lands in the most intricate code in the component. | R11 pins revenue attribution explicitly; AC6 tests it directly. |
| Refactoring `PlateFilterCombobox` (T2) touches a feature awaiting owner verification. | T2 is a pure extraction with AC11 as its gate. If ADR 0005 has not been verified in the browser yet, do T2 **after** it has. |
| Hub master coverage bounds resolution quality — a renamed hub yields an unresolved option. | Visible as a badged option rather than a wrong merge; feeds T9's report. |
| Unresolved bucket keeps growing until the mobile writer is fixed. | Out of scope by decision; T9 sizes it for the follow-up ADR. |

**Rollback:** entirely client-side and additive — revert the commit. No data is written, so nothing
needs undoing in Firestore.

## 9. Open questions / follow-ups

1. **Filter-bar density.** The bar already holds driver, status, job type, partner, plate, vehicle
   class and search; two more controls make eight. Do they go inline, or behind a "more filters"
   disclosure? *Proposed: inline for now, since the bar already wraps; revisit if it gets unwieldy.*
2. **Should the same filter land on Income / Billing Document later?** Deferred — the ADR 0005
   invoice guard would apply there, and no one has asked yet.
3. **Mobile write-path ADR** (`loading_phase_page.dart:1836` should record `source_id`) — the real
   fix; this spec only makes the damage visible and navigable.
