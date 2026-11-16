# ADR 0031 — Per-trip P&L: contribution per trip, fixed costs and leakage at month level

- **Status:** Accepted (2026-11-16) — implemented per [spec](../specs/per-trip-pnl.md) (local, not yet deployed); **§2 job distance amended** (see Amendment below)
- **Deciders:** Samart Kas (product owner), Claude
- **Area:** logitrack-web (new Accounting → P&L page and its settings), Firestore (new effective-dated
  settings; reads `trip_records`, `standby_records`, `tasks`, `vehicle_expenses`, `maintenance`,
  `transactions`, `trucks`, `hub_soc_distances`, `soc_hub_distances`). **No mobile change. No Cloud
  Function required.**

## Context

The owner wants a P&L feature that shows the business **per trip**. Until now every P&L was a
one-off report built by hand: the October 2569 cost report and the November CJSF plan P&L, both made
from scripts and figures the owner supplied. This grilling session (2026-11-16) settled what "profit
per trip" means and where each cost comes from. Facts established in the code:

1. **Revenue per trip already exists and is computed on the server.**
   - Trips: `trip_records.billingEstimateThb` (`functions/src/tripBillingOnDelivered.ts`).
   - Standby events: standby billing on `standby_records` (`functions/src/standbyBilling.ts`).
2. **Fuel is not recorded per trip.** `vehicle_expenses` rows are per refuel, per truck/driver, with
   `amount`, `volumeLiters`, `odometer` and `status`. They carry no trip link
   (`logitrack-web/features/accounting/api/expenses.ts:25-54`).
3. **Trips carry no usable distance.**
   - `trip_records.distance` is a display string (`logitrack-mobile/lib/features/home/data/models/trip_record.dart:62`).
   - The numeric distance lives in `hub_soc_distances` / `soc_hub_distances` as `distanceKm`
     (`logitrack-web/functions/src/distances.ts:325`).
   - There is no odometer reading per trip.
4. **Driver pay config differs from what the owner actually uses.**
   - `driver_compensation_config` defaults to ฿300 weekday / ฿350 holiday per trip
     (`logitrack-web/validate/compensationConfigSchema.ts:57-58`, used at
     `logitrack-web/lib/compensationCompute.ts:92-93`).
   - Helpers are paid ฿400 per [[Helper-day]], not per trip
     (`logitrack-web/functions/src/driverCompensation.ts:221`; [ADR 0012](0012-helper-day-window.md)).
   - The owner's reports use ฿250 per trip.
5. **Trucks have no installment field, and renewals overwrite their insurance/tax amounts.**
   - The renewal form writes `insurancePremium` / `taxExpense` back onto the truck doc
     (`logitrack-web/features/trucks/components/renew/RenewalForm.tsx:108,134`).
   - A completed renewal also appends a history row `{truckId, type: tax|insurance, amount, date}` to
     `transactions` (`RenewalForm.tsx:176-189`, `logitrack-web/features/trucks/services/truckService.ts:104-109`).
6. **Maintenance bills are lumpy.** They are split into PM and CM per truck
   (`logitrack-web/validate/maintenanceSchema.ts:26,47,65`; display amount via `maintenanceDisplayCost`).
   No tyre bills exist yet.
7. **Subcontractor trucks exist, but there is no payout data.** See `ownershipType`
   (`logitrack-web/validate/truckSchema.ts:26`).
8. **The Firestore database supports no document triggers in its region.** Anything derived must be
   computed on read or by an invoked callable.
9. **October 2569 actuals** (cost report):
   - Actual km ran 15–18% above job km.
   - Fuel cost ≈ ฿4.17/km.
   - Fixed costs ≈ 35% of total cost.
   - Truck installments are the largest fixed item (฿80,000/month for 4 trucks).
10. **The date axes are already defined.** Plan date `billingDate` is the billing axis for opt-in
    customers ([ADR 0027](0027-plan-date-billing-axis.md)). `checkInAt` is the actual work date
    ([ADR 0028](0028-plan-date-vs-actual-work-date.md)).

## Decision

1. **The per-trip figure is the [[Trip contribution]], not a fully loaded profit.**
   - Formula: revenue − fuel on [[Job distance]] − driver per-trip pay − helper share − maintenance
     accrual on job distance.
   - Fixed costs are never allocated to a trip. They appear only at month / fleet level.
   - The page shows **break-even trips** = month [[Fixed cost]]s ÷ average contribution per trip.
2. **Fuel per trip = job distance × the truck's actual fuel cost per km in that month.**
   - [[Job distance]] is the round trip from the distance tables: hub → destination
     (`hub_soc_distances`) plus destination → hub (`soc_hub_distances`). A multi-drop trip uses the
     sum of its legs plus last stop → hub.
   - [[Truck fuel cost per km]] = the truck's approved fuel spend (`vehicle_expenses`, `type: "fuel"`)
     ÷ its odometer km in the month.
   - Fuel for [[Off-plan km]] (odometer km − job km) is a **month-level line "ระยะเกินใบงาน"**, shown
     per truck. It is never spread into trips.
3. **Driver per-trip pay comes from a P&L-owned, effective-dated setting, not
   `driver_compensation_config`** (owner's choice; Claude recommended the payroll config).
   - One rate per trip, the same on weekdays and holidays (owner: ฿250).
   - A standby event counts as a trip at the same rate.
   - Monthly driver costs (fuel-efficiency bonus, employer SSO) are month-level [[Fixed cost]] items.
4. **Scope is own trucks only** (`ownershipType: "own"`). A subcontractor truck's trips have no
   contribution. At most, the page shows their count and revenue.
5. **Helper pay is split across that helper-day's trips.**
   - The helper-day rate is a P&L setting.
   - Each [[Helper-day]] (the ADR 0012 window) is divided equally among the trips that helper rode in
     that window.
   - A helper-day with no delivered trip goes to a month-level line.
6. **Maintenance: accrue per trip, report actual bills per month.**
   - Per trip: job distance × P&L-set ฿/km for servicing + tyres (owner's figures: 0.14 + 0.30).
   - Month: actual PM + CM bills, plus a line [[Maintenance variance]] = actual bills − the accrual the
     month's trips already carried. CM is never accrued on trips.
7. **A trip belongs to the [[P&L month]] of its actual work date.**
   - Use the task's `checkInAt` (Asia/Bangkok), falling back to `deliveredTimestamp`. Never use
     `billingDate`.
   - The page states that for plan-date customers (CJSF), month revenue can differ from the billing
     documents at month edges.
8. **Fixed costs come from two sources.**
   - **Insurance and vehicle tax** are read from the renewal history in `transactions`. Each amount is
     spread evenly over 12 months from its renewal date, or until the next renewal of the same type.
     A month with no history falls back to the truck doc's current amount and shows a warning.
   - **All other fixed items** come from a P&L fixed-cost list: truck installment with its term, GPS,
     accounting, audit, SSO, bonus. Each item is per truck or per company, monthly or yearly, and
     effective from–to.
9. **The P&L is computed on read, in the browser,** from the source collections by one pure function
   (e.g. `logitrack-web/lib/pnlCompute.ts` with tests).
   - Nothing is stored and there is no month close. Export to Excel gives a frozen copy.
   - Settings are effective-dated, so an old month recomputes to the same figures unless its source
     data was corrected. A correction flowing through is intended.
10. **Defaults recorded without separate grilling** (the owner may change them in the spec):
    - **Access:** new capabilities (view P&L / manage P&L settings), Admin by default. Customers and
      partners never see the P&L.
    - **Fail loud** (house rule, ADR 0003/0010):
      - A trip with no revenue (unpriced) or no job distance is listed with a badge, excluded from
        totals, and counted.
      - A truck-month without usable odometer readings uses the fleet-average ฿/km and shows a
        warning.
    - **Tolls and other vehicle expenses** (`vehicle_expenses` `type: "other"`) are month-level lines.
      They carry no trip link.
    - **Editing a setting** appends a new effective-dated row. Months before its effective date keep
      their figures.

## Amendment (2026-11-16, owner, during spec approval)

Decision §2's multi-drop rule ("sum of its legs plus last stop → hub") cannot be computed: the
distance tables hold Hub↔SOC pairs only, never stop → stop. The owner replaced it, and added a
standby rule:

- **Multi-drop job distance = the round trip to the farthest stop** (max over stops of hub → stop +
  stop → hub). The km detoured between stops falls into [[Off-plan km]].
- **Standby job distance = 2 × the one-way leg of the previous route** — no new job ran, and the
  drive back to the yard is included. The previous route is the same truck's latest delivered trip
  at most 3 days before the standby; without one, the standby's own job route is used (with a badge).

Other decisions taken in the spec (§4.3): maintenance bills are dated by completion
(`endDate` → `checkOutAt` → `startDate`), `insurance_claim` bills are not a cost, fuel ฿/km is
fill-to-fill with a fuel-reconciliation month line, and break-even is shown with and without the
month lines.

Further owner changes the same day (spec R2b, R18, R21):

- **Fixed costs (§8):** the list gains พรบ. (`cmi`) and ภาษีรถ (`vehicle_tax`), both per truck, plus
  owner-named categories. A truck's ภาษีรถ item **replaces** its renewal-history tax for that month
  (never both); without one, tax still comes from `transactions`. Insurance stays renewal-only.
- **Export (§9):** the Excel export is replaced by one self-contained HTML file. Its data is frozen at
  export; only the four P&L rates can be changed in the file (what-if), nothing is written back.
- **Year view:** January → latest month as columns, each month computed exactly like the month view.

## Consequences

**Positive**
- A lane's contribution is stable from month to month, so lanes can be compared and repriced with the
  customer (e.g. Sathon ≈ ฿295/trip, Huai Khwang ≈ ฿201/trip).
- The two biggest leaks are visible as their own lines: [[Off-plan km]] fuel (≈ ฿17,600/month in
  October) and [[Maintenance variance]].
- No backend, no stored results and no migration. The feature uses data the system already collects.
  The only new data is the P&L settings.

**Negative / risks**
- **Driver pay diverges from payroll.** The P&L uses its own rate (฿250) while
  `driver_compensation_config` says ฿300/฿350. If payroll becomes the pay system without aligning the
  config, the P&L will overstate profit by ฿50–100 per trip.
- Accuracy depends on odometer readings on refuel receipts and on complete distance tables. A missing
  hub↔SOC pair removes the trip from totals (fail loud).
- Monthly ฿/km per truck is noisy when a truck has few refuels, because a full tank bought late in a
  month covers next month's km.
- Because the P&L is computed live, a past month can change after a source correction. An Excel
  exported earlier will not match.
- Subcontractor work is invisible in the P&L.
- The tyre ฿/km is still an estimate, because there are no tyre bills.

**Follow-ups**
- `/spec-new` for the page and settings: data model of the settings collections, Firestore rules,
  capabilities, i18n en + th.
- Decide in the spec how a maintenance bill is dated (completion vs start).
- Consider showing the payroll-config rate next to the P&L rate as a reminder of the divergence.
- Revisit the BigQuery analytics layer (`shared-docs/database-migration-plan.md:240-263`) only when
  multi-tenant ([ADR 0026](0026-multi-tenant-carrier-isolation.md)) or data volume makes in-browser
  computation too slow.

## Alternatives considered

- **Fully loaded profit per trip** (allocate fixed costs per trip). Rejected: the fixed cost per trip
  moves with monthly volume (≈ ฿340 at 300 trips, ≈ ฿425 at 240). The same lane would look profitable
  in a busy month and loss-making in a quiet one, which misleads lane decisions.
- **Spread off-plan fuel into trips.** Rejected: it hides the leak and penalizes lanes that did not
  cause it.
- **Driver pay from `driver_compensation_config`.** Recommended by Claude for a single source of
  truth. The owner rejected it because the config (300/350) is not what is paid (250). The divergence
  is recorded as a risk.
- **Include subcontractor trucks.** Deferred: there is no payout data, and partners log in
  (`partnerScopeId`), so the P&L would need partner-proof visibility.
- **Helper pay as a month line only.** Rejected: lanes that need a helper would look better than they
  are.
- **Estimated maintenance only at month level.** Rejected: it does not match cash paid, hides CM, and
  keeps the tyre estimate forever.
- **Month by billing date.** Rejected: costs follow real dates, and off-plan km would be wrong at month
  edges. CJSF and other customers would also sit on different axes.
- **Insurance/tax entered in P&L settings, or read from the truck doc fields.** Rejected: settings
  duplicate the entry, and the truck fields are overwritten at renewal, which rewrites history.
- **Monthly close with stored results.** Rejected for v1: it needs a close and reopen flow and risks
  stored figures drifting from source data. The P&L is an internal report, not a legal document like
  an invoice.
- **BigQuery / SQL analytics layer.** Not needed at about 300 trips per month. See follow-ups.

## Related

- Glossary: [[Trip contribution]], [[Job distance]], [[Off-plan km]], [[Truck fuel cost per km]],
  [[Maintenance variance]], [[Fixed cost]], [[P&L month]], [[Helper-day]].
- [ADR 0009](0009-multiple-rate-rounds-within-one-billing-period.md): the effective-dated,
  append-only settings pattern this follows.
- [ADR 0012](0012-helper-day-window.md): helper-day window.
- [ADR 0015](0015-supplementary-trips.md): supplementary trips are rows like any other trip.
- [ADR 0026](0026-multi-tenant-carrier-isolation.md): future tenant scoping of the P&L.
- [ADR 0027](0027-plan-date-billing-axis.md), [ADR 0028](0028-plan-date-vs-actual-work-date.md):
  date axes.
- Source reports: October 2569 cost report and the CJSF plan P&L (Claude artifacts, 2026-11).
