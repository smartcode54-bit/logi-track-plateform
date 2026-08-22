# Spec: Multiple rate rounds inside one billing period

> **Status:** ✅ Done (2026-08-04) — implemented locally; not deployed, and the T10 migration has not been run
> **Owner:** Samart Kas
> **Created:** 2026-08-04
> **Domain:** accounting (rate card, billing document) + Cloud Functions (billing)
> **Related:** [ADR 0009](../adr/0009-multiple-rate-rounds-within-one-billing-period.md) — every decision here comes from that ADR (grilling session 2026-08-04). Depends on the period lock from [ADR 0008](../adr/0008-standby-billing-visibility-and-recompute-semantics.md) / [spec](standby-billing-visibility-and-recompute.md).

---

## 1. Problem & Goal

Diesel moves, so a customer announces a new price round whenever the retail price crosses a ฿1.00
band — more than twice in a single month is normal. The arithmetic already slices prices by date, but
five defects make a multi-round month unreliable: the band formula overcharges when diesel sits
exactly on `x.00`; `effectiveFrom` is stored at 07:00 ICT so overnight deliveries on a switch day are
priced at the previous round; the band that priced a record is not stored on it, and the announcement
row it points at can be edited or deleted; two announcements on one date are resolved by document id;
and the price the form pre-fills comes from a month document that is overwritten daily.

Goal: a month with N rounds bills correctly at every boundary, and every priced record can prove
which round and which fuel band produced its amount — on the invoice, in the detail sheet, and years
later.

## 2. Scope

**In scope** — all eight ADR 0009 decisions:
- Immutable announcement rows; correction by new row, removal by void (§1).
- `effectiveFrom` as an instant at Bangkok midnight; rounds as half-open intervals (§2).
- Fuel band `(n, n+1]` computed in integer satang (§3).
- Band + round denormalized onto every priced record, multi-delivery included (§4).
- Announcement priced from `fuel_daily_snapshots` of its own effective date (§5).
- Invoice `รอบ` column + legend block (§6).
- Detail sheet band + round columns (§7).
- Rate Card shows a period's rounds as ordered half-open intervals (§8).
- A **read-only** impact report naming the periods affected by the two corrected defects.

**Out of scope (ทำทีหลัง / ไม่ทำ):**
- **Rewriting any historical amount.** No recompute, no credit note, no re-issue. The impact report
  states what *would* change; the business decides separately. (Owner decision, 2026-08-04.)
- Per-customer band tables with uneven steps or uneven ฿-per-step — rejected in ADR 0009.
- Clamping a negative surcharge — the discount is intended.
- Row-level invoice lock (`billedOnInvoiceNumber`) — ADR 0008 follow-up.
- Standby and multi-drop *pricing* logic; เสริม rates (never take a fuel adjustment).
- SQL migration of `customer_rate_entries` / `customer_fuel_rate_adjustments`
  (`database-migration-plan.md` Phase 1) — not triggered. The append-only model here is compatible
  with it and needs no rework when it is.

## 3. Requirements

**Functional**

- R1. `effectiveFrom` is written as the instant of **Bangkok midnight** of the chosen calendar date,
  independent of the admin's browser timezone. Each announcement row also stores
  `effectiveFromDateStr` (`yyyy-MM-dd`) as the human-readable fact.
- R2. Existing announcement rows are normalized once from 07:00 ICT to 00:00 ICT of the **same
  calendar date**, and gain `effectiveFromDateStr`. No trip amount is recomputed by this.
- R3. The fuel band is the half-open-above interval `(n, n+1]`; its floor is
  `Math.ceil(Math.round(price * 100) / 100) - 1`. `baselineBandFloor` is the lower integer of the
  zero-surcharge band, so `41` means `41.01–42.00` → `+0`. The result stays signed.
- R4. The band helper is a pure function shared by web and Functions, unit-tested at the boundaries
  `x.00`, `x.01`, `x.99`, at the baseline, and below it.
- R5. Announcement rows in `customer_rate_entries` and `customer_fuel_rate_adjustments` cannot be
  updated or deleted. A mistake is corrected by writing a new row; a round that should not exist is
  voided (`voidedAt`, `voidedBy`, `voidedReason`). Enforced in Firestore rules, not only in the UI.
- R6. Voided rows are excluded from rate selection everywhere (`selectBillingRateEntry`,
  `selectFuelAdjustmentForBillingDate`) and shown struck-through in the Rate Card UI.
- R7. Every priced record stores its own round and band at compute time:
  `billingRoundEffectiveFromDateStr`, `billingFuelBandLowerThb`, `billingFuelBandUpperThb`,
  `billingReferenceFuelPriceThb`.
- R8. The multi-delivery branch writes the **same** billing provenance field set as the
  single-delivery branch (it currently omits `billingAddThbPerTrip`, `billingRateImportId`,
  `billingEffectiveFromDateStr`).
- R9. The Rate Card fuel-adjustment form pre-fills the reference price from
  `fuel_daily_snapshots/{effectiveFromDateStr}`. If that day has no snapshot it shows an explicit
  "no price for this date" state and leaves the field empty — it never substitutes another day's
  price.
- R10. The invoice PDF gains a `รอบ` column and a legend block listing each round of the period:
  label, date span, band as a range (`37.01–38.00`), and surcharge. Round labels (`R1`, `R2`, …) are
  derived at render time by ordering the distinct rounds present, never stored.
- R11. `จำนวน × ราคา/หน่วย = รวม` remains exactly true on every invoice line; the round is a label on
  the existing grouping key, not a new total.
- R12. The detail sheet gains `รอบ` and `ช่วงราคาน้ำมัน` columns, read only from the denormalized
  fields of R7. A record without them prints blank, never a guess.
- R13. The Rate Card page shows the rounds covering a selected period as ordered half-open intervals,
  making a gap or an overlap visible before invoicing.
- R14. A read-only impact report lists, per customer and period: rounds whose reference price ended in
  `.00` (band overcharge), and trips delivered inside a 00:00–06:59 ICT switch window (boundary
  mispricing), with the amount delta each would have. It writes nothing.

**Non-functional**

- N1. i18n complete in both `en` and `th`; key counts equal.
- N2. Billing logic mirrored in **both** `lib/billingCompute.ts` and
  `functions/src/core/billingCompute.ts` in the same commit (project rule; identical line-for-line
  today).
- N3. Schema change lands in `shared-docs/schemas/trip-record.ts` (SSOT) **first**, then
  `logitrack-web/validate/tripRecordSchema.ts`.
- N4. No new Firestore composite index: R14 reuses the `trip_records (status, deliveredTimestamp)`
  pattern the recompute already relies on; announcement queries stay single-field equality.
- N5. Collection names via `COLLECTIONS` constants only — `FUEL_DAILY_SNAPSHOTS` already exists
  (`lib/collections.ts:58`).
- N6. Web-only change; no `pubspec.yaml` bump.

## 4. Design

**Data model (Firestore)**

- `customer_rate_entries`, `customer_fuel_rate_adjustments` — new fields:
  `effectiveFromDateStr: string` (`yyyy-MM-dd`), `voidedAt?: Timestamp`, `voidedBy?: string`,
  `voidedReason?: string`. `effectiveFrom` keeps its type; only the instant it encodes changes (R2).
- `trip_records` — four new optional fields (R7). Additive; every existing reader keeps working.
- `fuel_daily_snapshots/{yyyy-MM-dd}` — **read-only input**, already admin-readable
  (`firestore.rules:172-176`). No writer change: it is populated create-only by the existing sync
  (`core/persistFuelMonthlySnapshot.ts:69-82`).
- `fuel_monthly_snapshots` — demoted to dashboard display; no longer a billing input.

**Cloud Functions / billing**

- ⚠️ Both `lib/billingCompute.ts` and `functions/src/core/billingCompute.ts` change together (N2):
  - `fuelBandFloor(priceThb)` and `fuelBandRange(priceThb)` — the satang math of R3/R4.
  - `computeFuelSurchargeThb(price, baselineBandFloor, thbPerBaht)` — signed, no clamp.
  - `selectBillingRateEntry` / `selectFuelAdjustmentForBillingDate` filter out voided rows (R6).
  - `TripBillingComputed` carries the round + band fields so the caller can persist them (R7).
- `functions/src/tripBillingOnDelivered.ts` — persist the new fields in **both** branches; the
  multi-delivery branch is brought to parity (R8).
- `functions/src/billingImpactReport.ts` (new, onCall, admin-only) — R14. Read-only: it must not
  hold a `WriteBatch` at all, so a mistake cannot become a mutation.
- No Firestore document triggers (the database region supports none); everything stays callable.

**Web (Next.js)**

- `lib/billingDate.ts` (new, + test) — `bangkokMidnightFromDateStr("2026-08-16")` →
  `2026-08-15T17:00:00Z`, built from a fixed `+07:00` offset so the admin's browser timezone is
  irrelevant, plus `dateStrFromBangkokInstant()`. Replaces `parseDateOnly`
  (`features/accounting/api/billing.ts:99-101`) at all five call sites (`:115`, `:163`, `:238`,
  `:256`, `:353`).
- `features/accounting/api/billing.ts` — write `effectiveFromDateStr`; add
  `voidCustomerRateEntry()` / `voidCustomerFuelRateAdjustment()`; **remove**
  `updateCustomerRateEntry`, `deleteCustomerRateEntry`, `updateCustomerFuelRateAdjustment`,
  `deleteCustomerFuelRateAdjustment` from the announcement path (R5); add
  `getFuelDailySnapshot(dateStr)` (R9).
- `app/app/accounting/rate-card/page.tsx` — band formula delegated to the shared helper (the inline
  `Math.floor` at `:588-593` goes away); daily-price prefill keyed on the effective date (R9);
  row actions become Void with a required reason; rounds panel for the selected period (R13).
- `lib/billingDocument.ts` — `LineItem` gains `roundKey`; `groupToLineItems` keys on
  `` `${vc}::${route}::${unitPrice}` `` unchanged (R11) and labels rounds after grouping; invoice
  table gains a ~12mm `รอบ` column (fixed columns 120mm → 132mm of 182mm usable, `รายการ` ~62mm →
  ~50mm); legend block drawn above the table; `DetailRow` (`:572-586`) gains `round` + `fuelBand`
  (R12).
- `app/app/utilities/billing-impact/page.tsx` (new) — R14 report UI. Route capability
  `security_view_overview`, matching the sibling `/app/utilities/backfill`
  (`lib/capabilities.ts:365-366`); sidebar already highlights on `/app/utilities` prefix.
- i18n: `context/locales/{en,th}/accounting.ts` (rounds, band, void, impact report).

**Mobile (Flutter)** — none. Mobile neither reads announcement rows nor renders invoices.

**Firestore Rules**

`customer_rate_entries` and `customer_fuel_rate_adjustments` currently allow blanket `write`
(`firestore.rules:138-146`), which includes update and delete. Split them so immutability is enforced
by the database, not by the absence of a button:

```
allow create: if isAppCheckVerified() && request.auth != null && isWebAdmin();
allow update: if isAppCheckVerified() && request.auth != null && isWebAdmin()
  && request.resource.data.diff(resource.data).affectedKeys()
       .hasOnly(['voidedAt', 'voidedBy', 'voidedReason', 'updatedAt']);
allow delete: if false;
```

## 5. Affected files

`shared-docs/schemas/trip-record.ts`, `logitrack-web/validate/tripRecordSchema.ts`,
`logitrack-web/lib/billingCompute.ts` (+ `.test.ts`),
`logitrack-web/functions/src/core/billingCompute.ts`,
`logitrack-web/functions/src/tripBillingOnDelivered.ts`,
`logitrack-web/functions/src/billingImpactReport.ts` (new),
`logitrack-web/functions/src/index.ts`,
`logitrack-web/lib/billingDate.ts` (new) + `.test.ts`,
`logitrack-web/features/accounting/api/billing.ts`,
`logitrack-web/app/app/accounting/rate-card/page.tsx`,
`logitrack-web/lib/billingDocument.ts`,
`logitrack-web/app/app/utilities/billing-impact/page.tsx` (new),
`logitrack-web/lib/capabilities.ts`,
`logitrack-web/context/locales/{en,th}/accounting.ts`,
`logitrack-web/firestore.rules`,
`shared-docs/.vibe-rules.md` (Change Log).

## 6. Task breakdown

- [x] T1. (R3,R4,N2) Band math in both `billingCompute.ts` files + boundary tests.
- [x] T2. (R1,N5) `lib/billingDate.ts` + test; replace `parseDateOnly` at all five call sites; write `effectiveFromDateStr`.
- [x] T3. (R5,R6) Void replaces update/delete: API, rules, Rate Card row actions; voided rows excluded from selection in both compute files.
- [x] T4. (R7,R8,N3) New trip fields — SSOT schema first, then web schema, then persist in both branches of `tripBillingOnDelivered.ts`.
- [x] T5. (R9) Daily-snapshot prefill keyed on the effective date, with an explicit "no price" state.
- [x] T6. (R10,R11) Invoice `รอบ` column + legend block.
- [x] T7. (R12) Detail sheet `รอบ` + `ช่วงราคาน้ำมัน` columns.
- [x] T8. (R13) Rate Card rounds panel for the selected period.
- [x] T9. (R14) `billingImpactReport` callable + Utilities page + route capability.
- [x] T10. (R2) One-off normalization of stored `effectiveFrom` → Bangkok midnight + `effectiveFromDateStr`; run **after** T2 so writer and data agree.
- [x] T11. (N1) i18n `en` + `th`.
- [x] T12. Update `.vibe-rules.md` Change Log.

## 7. Acceptance criteria

- [x] AC1. (R3,R4) `fuelBandFloor(42.00) === 41` and `fuelBandFloor(42.01) === 42`; a round whose reference price is exactly `x.00` produces `+0` at the baseline band, not `+฿10`.
- [x] AC2. (R1) An announcement saved as "16 ส.ค." stores `2026-08-15T17:00:00Z` and `effectiveFromDateStr: "2026-08-16"`, and does so identically with the browser timezone forced to UTC.
- [x] AC3. (R1) A trip delivered 16 Aug 03:00 ICT selects the round effective 16 Aug — not the previous one.
- [x] AC4. (R2) After the normalization every announcement row is at 00:00 ICT with a matching `effectiveFromDateStr`, and **no** `trip_records.billingEstimateThb` changed (diff count = 0).
- [x] AC5. (R5) A direct `updateDoc` of `rateThb`, and a `deleteDoc`, on an announcement row are both rejected by the emulator rules test; a void write succeeds.
- [x] AC6. (R6) A voided round is not selected for any trip and is struck through in the Rate Card list.
- [x] AC7. (R7,R8) A newly priced single-delivery trip **and** a multi-delivery trip both carry the full provenance set including band bounds and reference price.
- [x] AC8. (R9) Entering a round effective on a date whose daily snapshot is missing leaves the price field empty and shows the "no price for this date" state; no other day's price appears.
- [x] AC9. (R10,R11) A month with three rounds renders three legend entries and a `รอบ` label on every line, and every line satisfies `จำนวน × ราคา/หน่วย = รวม`.
- [x] AC10. (R12) Detail rows show the band range; a legacy record with no denormalized band prints blank.
- [x] AC11. (R14) The impact report lists affected periods with deltas and performs zero writes (verified by rules-denied write path or absence of any write call).
- [x] AC12. (N1) `en` and `th` key counts for the accounting namespace match.
- [x] AC13. `tsc --noEmit`, ESLint, and Vitest pass; CI green.

## 8. Risks & rollback

| Risk | Mitigation / rollback |
|------|----------------------|
| T10 shifts every historical boundary 7 hours earlier, so a **future** recompute of an old period would produce different numbers than were invoiced | Money already computed is frozen on `trip_records` and is untouched (AC4 asserts a zero diff). The divergence is exactly what R14 reports, per period, before anyone recomputes. The ADR 0008 period lock then refuses to reprice a `sent`/`paid` period — but that lock **is implemented locally and not yet deployed**, so T10 must not ship to prod ahead of it. |
| The band fix silently changes prices going forward for rounds already entered with an `x.00` reference price | Those rounds are announcement rows and are now immutable — the fix applies to newly computed trips only. R14 names the existing rounds affected so they can be re-announced deliberately with a new row rather than corrected in place. |
| Removing update/delete strands admins who use them today to fix typos | Void ships in the same task (T3), and correction-by-new-row is the documented path. Rollback is a rules-only revert; the API functions stay removed. |
| Rules `hasOnly([...])` on update blocks a legitimate future field addition | Additive fields go through `create`; if an update path is ever needed the allow-list is a one-line change, and it fails closed (write denied) rather than open. |
| `รอบ` column squeezes `รายการ`; long route labels wrap and lengthen the invoice | The footer block already relocates itself to a fresh page when it would overflow (`lib/billingDocument.ts:380-389`); verify with a 3-round month against the longest route label in prod data. |
| Both compute files drift | Same commit, enforced by N2; the boundary tests live in `lib/billingCompute.test.ts` and exercise the shared function names. |
| Nothing verifiable against live data locally | Local ADC returns `PERMISSION_DENIED` on Firestore for both projects, so AC2–AC11 are checked by unit tests, rules emulator, and code reading; a real dev run is required before prod. |

## 9. Open questions / follow-ups

- **Confirm with each customer's contract that the band is `36.01–37.00` (upper-inclusive) before T1
  ships.** The owner asserted this form; a contract written `36.00–36.99` would be made wrong by the
  same fix.
- Whether a month whose only change was a rate-card change (no fuel movement) should still appear in
  the invoice legend — it would render a round with no band.
- Whether to credit-note or absorb the historical `.00` overcharge — deliberately deferred; R14 exists
  to inform that call.
- `shared-docs/schemas/trip-record.ts` is already stale versus
  `logitrack-web/validate/tripRecordSchema.ts`: it lacks `billingStopChargeThb`,
  `billingIsMultiDelivery`, and `billingMultiDeliveryBreakdown`. T4 should close that gap while it is
  in the file.
- The ADR 0008 period lock is a hard prerequisite for T10 reaching prod; sequence the two deploys.
