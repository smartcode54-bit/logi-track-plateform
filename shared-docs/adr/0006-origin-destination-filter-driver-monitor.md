# ADR 0006 — Filter by origin and destination on Driver Monitor (place identity over free text)

- **Status:** Accepted (2026-07-20) — implemented locally 2026-07-23, awaiting owner UI verification
  and deploy. Decision 6 (per-stop export filtering) required one adjustment the ADR did not
  anticipate: revenue is emitted on the lowest *surviving* stop index rather than on stop 1, which
  can now be filtered out (spec R11).
- **Deciders:** Samart Kas (product owner), Claude
- **Area:** logitrack-web (`features/drivers/`), read path only; logitrack-mobile write path noted as follow-up

## Context

Driver Monitor (`/app/driver-monitor`) can filter by driver, status, job type, partner, and — as of
[ADR 0005](0005-truck-plate-filter-billing-document-driver-monitor.md) — truck plate. It cannot
filter by **where a trip started or ended**, which is the question operations actually asks ("what
ran out of บางปู last week", "which trucks served ประเวศ18").

Grilling the request established four facts that shape the design.

### 1. `origin` and `destination` are free text, not identities

`validate/tripRecordSchema.ts:82-83` declares both as bare `z.string().optional()` — no enum, no code
constraint. The values that reach Firestore come from three different writers:

- **Hub picker (dominant path):** `loading_phase_page.dart:1836` sets
  `_originController.text = hub.sourceNameEn` — the hub's **English display name**, not its
  `source_id`. The controller is read at `:1252-1253` and passed to `submitLoadingPhaseRecord`
  at `:1377-1378`, which is the actual `trip_records` write.
- **OCR of a screenshot:** `ocr_screenshot_service.dart:219-220` fills the same fields via
  `_extractOrigin` (`:450-456`), which grabs the tail of a line such as
  `สถานีเริ่มต้น: ALANG-A - วังทองหลาง` — a composite code-plus-name string matching no master record.
- **Codes:** `add_delivery_stop_dialog.dart:79,98` writes `destination: code` — a clean SOC/hub code.

So one physical place exists in `trip_records` as `SPK890146`, as `ประเวศ18`, and as
`ALANG-A - วังทองหลาง`. The web already carries a patch for the same disease on the task side:
`useDriverMonitor.ts:618-624` normalizes a task's destination display-name → PDP code before billing
lookup, and that hack only exists because both forms are known to be in the wild.

`lib/hubDisplay.ts:61-73` (`resolveHubOrSocDisplay`) **returns the raw string unchanged** when it
resolves nothing, so today's Origin/Destination columns render unresolvable junk indistinguishably
from real place names. A filter built on raw string equality would inherit that and fragment one hub
into several options, each silently hiding the others' trips.

This is the same class of error as [[Licence plate]] matching, and as the billing `nameToCode` /
`codeToName` merge bug (`CLAUDE.md` §39): **a display string is not an identity.**

### 2. A multi-drop trip has N destinations, not one

`deliveryStopsProgress[]` (`tripRecordSchema.ts:57-59`) holds per-stop destinations, rendered as an
"N stops" badge at `DriverMonitorDashboard.tsx:887-908`. `trip.destination` is only meaningful for
single-stop trips. The Excel export at `DriverMonitorDashboard.tsx:441-481` already emits **one row
per stop**, setting `row[6]` per stop — so "does this trip match?" and "which rows does it export?"
are two different questions.

### 3. Some trips have no recoverable origin at all

Per the platform name-fields rule, `source_name_th` is required on a hub while `source_name_en` is
optional (billing name). Since the picker writes `sourceNameEn` (`:1836`), a hub created without an
English name yields an empty controller, and `submitLoadingPhaseRecord` (`:1377-1378`) converts empty
to `null`. Those trips carry **no origin**, and no read-time resolver can invent one.

### 4. Owner-asserted invariant

Origin is single-valued for every trip; only destination is multi-valued. The filter is a
**review/analysis** dimension on Driver Monitor, not a billing dimension — unlike on Billing Document,
where [[Invoice set vs preview set]] forces a Download guard.

## Decision

1. **Filter on resolved place identity, not on the raw string.** Each trip's raw `origin` /
   `destination` is resolved to a canonical key (hub `source_id` or SOC key) using the
   **`nameToCode` direction only**, plus code pass-through. Never the reverse map — merging the two
   directions is the documented cause of the "No rate" billing failure (`CLAUDE.md` §39). Two trips
   spelled differently but resolving to the same place are one filter option.

2. **Build options from the loaded rows, never from the `hubs` master.** Mirrors
   `buildPlateFilterOptions` (`lib/truckPlate.ts:80-105`): every offered option provably matches at
   least one visible trip, and no trip is unreachable by every option. The master-list approach
   (as used for the *editor* dropdown at `DriverMonitorDashboard.tsx:537-568`) is correct for
   *choosing* a destination and wrong for *filtering* by one.

3. **Label options with the canonical display name** via `resolveHubOrSocDisplay`, so the dropdown
   reads exactly like the column it filters.

4. **Three buckets, matching ADR 0005's plate precedent:**
   - *resolved* — keyed by canonical code, labeled by display name;
   - *unresolvable* — **one option per distinct raw string**, labeled with the raw value and flagged
     as not-in-master (the `isOrphan` treatment, `truckPlate.ts:36-37`);
   - *absent* — a single "not specified" bucket (the `PLATE_FILTER_NONE` treatment).

   Unresolvable values are never merged with each other and never hidden. The filter consequently
   doubles as a data-quality report on the free-text writers.

5. **Destination matches a trip if *any* of its stops matches.** For a multi-drop trip, match against
   every entry of `deliveryStopsProgress[]`, falling back to `trip.destination` when the array is
   empty. Matching only the planned `trip.destination` was rejected — it makes multi-drop trips
   unfindable by the stops they actually served.

6. **A destination-filtered export emits only the matching stop rows.** The per-stop loop at
   `DriverMonitorDashboard.tsx:449-481` must skip non-matching stops when a destination filter is
   active, so a filtered export totals exactly what was filtered. On screen the row stays whole (all
   stops visible in the badge) — the question there is "which trips went to X", and surrounding
   context helps; in a file that someone will sum, it does not.

7. **Origin and destination are two independent controls, ANDed** with each other and with the
   existing filters, following the established `tripMatchesClientFilters` shape
   (`useDriverMonitor.ts:157-200`). Both must be added to `ExportFilterCriteria`
   (`useDriverMonitor.ts:86-92`) so the export dialog stays in sync with the screen.

8. **Resolution is read-time only. No backfill, no Firestore schema change, no mobile release.**
   Resolution is a pure function of the trip plus the already-loaded hub maps
   (`sourceIdToName` at `useDriverMonitor.ts:515`, `hubDisplayNameToCode` at `:522`).

9. **No Download guard is needed here.** Driver Monitor exports a review spreadsheet, not an invoice;
   nothing consumes an invoice number. The ADR 0005 guard is specific to Billing Document.

## Consequences

**Positive**

- One physical place is one filter option regardless of how it was typed — the fragmentation that
  would make the feature quietly wrong is closed at the design level.
- Unlike the plate filter, the wide-range export path (`getTripsForExportResolved`) needs **no extra
  Firestore reads**: plates required a task join (`fetchTruckByTripId`) because the plate lives on the
  task, whereas origin/destination live on the trip doc itself and resolve against maps already in
  memory.
- Bad OCR values become visible and isolatable instead of blending into the column, giving the first
  concrete measure of how much free-text junk exists.
- Reuses an established, reviewed pattern (`lib/truckPlate.ts`), so the two filters behave alike.

**Negative / risks**

- The dropdown grows noisy when OCR misfires, since each distinct bad string is its own option. Judged
  the right trade: noise is information, and a clean dropdown that hides trips is worse.
- The unresolved bucket keeps growing until the write path is fixed. This ADR makes the growth visible
  but does not stop it.
- Resolution quality is bounded by hub master coverage; a renamed hub whose old display name no longer
  appears in `hubs` produces an unresolvable option even though the place is real.
- Point 6 adds a stop-level condition to an export loop that is already the most intricate code in the
  component.

**Follow-ups**

- **Fix the writer (separate ADR).** `loading_phase_page.dart:1836` should record the hub's
  `source_id` alongside the display name, so `trip_records.origin` carries an identity at write time.
  Deliberately out of scope here: it needs a mobile release and a version bump, and every historical
  trip still requires the resolver regardless.
- **`source_name_en` gap.** Hubs without an English name make the picker write an empty origin
  (Context §3). Worth an audit of how many hubs are affected — it is silent data loss today,
  independent of this filter.
- Implementation via `/spec-new`; this ADR is the *why*, not the *what to build*.

## Alternatives considered

- **Options from distinct raw strings.** Rejected: the same hub fragments into `SPK890146` /
  `ประเวศ18` / `ALANG-A - วังทองหลาง`, and selecting one silently drops the rest of that hub's trips —
  precisely the failure ADR 0005 exists to prevent.
- **Options from the `hubs` + `SOC_KEYS` master.** Rejected: yields options matching zero loaded
  trips, and makes any trip whose value doesn't resolve unreachable by every option — invented
  completeness, which [ADR 0003](0003-edit-forms-fail-loudly-on-legacy-docs.md) rejects.
- **Hide unresolved rows.** Rejected for the same reason, more severely: a filtered export would
  silently omit them.
- **A single "unresolved" bucket.** Rejected: two unrelated OCR errors become indistinguishable and no
  specific bad value can be isolated for repair.
- **Match only `trip.destination`, ignoring stops.** Rejected: multi-drop trips would be unfindable by
  the stops they actually served, so the filter would misreport where trucks went.
- **Export all stops of any matching trip.** Rejected: a "trips to ประเวศ18" export would contain rows
  for other destinations, and anyone summing it gets a wrong number.
- **Backfill `trip_records` to canonical codes.** Rejected: irreversibly rewrites historical
  operational records, cannot recover rows already `null`, and re-accumulates junk immediately because
  the writer is unchanged.
- **Commit the mobile write-path fix in this ADR.** Rejected as scope creep — it drags a mobile
  release into an otherwise pure web read-path decision, and does not remove the need for the
  resolver. Logged as a follow-up instead.

## Related

- Glossary: [../glossary.md](../glossary.md) — [[Place identity]], [[Unresolved place]],
  [[Delivery stop]], [[Licence plate]], [[Truck identity]], [[Orphan plate]].
- [ADR 0005](0005-truck-plate-filter-billing-document-driver-monitor.md) — the plate filter whose
  identity/provenance/bucket pattern this ADR follows.
- [ADR 0003](0003-edit-forms-fail-loudly-on-legacy-docs.md) — an honest gap beats an invented value.
- `CLAUDE.md` §39 — the `nameToCode` / `codeToName` merge bug that point 1 guards against.
- Conventions: [0000-adr-conventions.md](0000-adr-conventions.md).
