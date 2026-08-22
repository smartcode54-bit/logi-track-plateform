# ADR 0009 — Multiple rate rounds inside one billing period (fuel bands, half-open dates, immutable announcements)

- **Status:** Accepted (2026-08-04) — implementation pending
- **Deciders:** Samart Kas (product owner), Claude
- **Area:** logitrack-web (Rate Card, Billing Document, Income), Cloud Functions
  (`tripBillingOnDelivered`, `core/billingCompute`, `core/persistFuelMonthlySnapshot`), Firestore
  (`customer_rate_entries`, `customer_fuel_rate_adjustments`, `fuel_monthly_snapshots`,
  `fuel_daily_snapshots`, `trip_records`)

## Context

Prices move with diesel. A customer contract expresses the surcharge as a **fuel band** — a ฿1.00
range of the retail diesel price such as `36.01–37.00`, `37.01–38.00` — and announces a new round
whenever the price crosses a band edge. The owner confirmed this can happen **more than twice in a
single calendar month**, and that the resulting invoice must show the rounds, not one blended number.

Nothing in the system forbids more than one round per month today. What is missing is a *standard*:
the code silently does six things that are individually defensible and collectively wrong once a
month contains two or more boundaries. Every claim below is verifiable at the cited line.

### A. The rate model already slices by date — per trip, not per month

`selectBillingRateEntry` takes the newest row whose `effectiveFromMs <= billDateMs`, falling back to
the oldest row when the trip predates every announcement (`lib/billingCompute.ts:141-170`).
`selectFuelAdjustmentForBillingDate` does the same for the surcharge (`:172-181`). The billing date
is the service-completion instant fixed by [ADR 0008](0008-standby-billing-visibility-and-recompute-semantics.md)
§3. So N rounds in a month already produce N price slices, correctly, at the arithmetic level.

The invoice follows automatically: `groupToLineItems` keys on
`` `${vc}::${route}::${unitPrice}` `` (`lib/billingDocument.ts:244`), so one route at three prices
becomes three lines. Line dates render as a min–max range (`:203-225`).

### B. The band formula is off by one at exactly `x.00`

`addThbPerTrip` is derived in the browser at form-submit time:

```ts
const fuelBand = Math.floor(referenceFuelPrice);
const baselineFloor = parseInt(adjustmentForm.fuelBandBaselineFuelFloor) || 41;
const thbPerBaht = parseFloat(adjustmentForm.fuelBandThbPerBaht) || 10;
addThbPerTrip = (fuelBand - baselineFloor) * thbPerBaht;
```

(`app/app/accounting/rate-card/page.tsx:588-593`)

`Math.floor` classifies a price into `[n, n+1)`. The contract band is `(n, n+1]`. The two agree
everywhere **except the top edge**, where the price is exactly `x.00`:

| diesel | contract band (baseline `41.01–42.00` = +0) | `Math.floor` | result |
|---|---|---|---|
| 42.50 | `42.01–43.00` → +10 | `42` → +10 | correct |
| **42.00** | `41.01–42.00` → **+0** | **`42` → +10** | **+฿10/trip too much, for the whole round** |
| 42.01 | `42.01–43.00` → +10 | `42` → +10 | correct |

Thai diesel is repeatedly pinned at round numbers by price-cap policy, so `x.00` is not a rare input.
The error is invisible after the fact because the amount is frozen onto the trip and the band is not
stored next to it (see D).

`fuelBandBaselineFuelFloor` is stored as a bare integer (`41`, `parseInt` at `:590`) which does not
say whether it means `40.01–41.00` or `41.01–42.00`.

### C. `effectiveFrom` is midnight **UTC**, i.e. 07:00 Bangkok

```ts
function parseDateOnly(value: Date): Date {
    return new Date(Date.UTC(value.getFullYear(), value.getMonth(), value.getDate(), 0, 0, 0, 0));
}
```

(`features/accounting/api/billing.ts:99-101`, applied at `:115`, `:163`, `:238`, `:256`, `:353`)

The getters are local, the constructor is UTC. An admin in Bangkok picking "16 ส.ค." stores
`2026-08-16T00:00:00Z` = **16 Aug 07:00 ICT**. The billing date is a real instant, so every trip
delivered between 00:00 and 06:59 ICT on a switch day is priced at the **previous** round.

This is a line-haul operation whose deliveries land overnight, so the window is populated, not
theoretical. One round per month crosses this boundary once; three rounds cross it three times. The
visible symptom on the invoice is two lines for the same route with **overlapping** date ranges
(`1-16/8` and `16-31/8`), because `formatLineItemDates` prints min–max (`lib/billingDocument.ts:203-225`).

### D. A record cannot prove which round priced it

The single-delivery snapshot writes `billingBaseRateThb`, `billingRateImportId`,
`billingFuelAdjustmentId`, `billingRateMultiplier`, `billingAddThbPerTrip`,
`billingEffectiveFromDateStr` (`functions/src/tripBillingOnDelivered.ts:373-385`). Three gaps follow:

1. **The fuel price/band is absent.** `referenceFuelPriceThbPerLitre` lives only on the adjustment
   doc, reachable through `billingFuelAdjustmentId`.
2. **That doc is mutable and deletable in place.** `updateCustomerFuelRateAdjustment` overwrites the
   whole field set with no version, `deleteCustomerFuelRateAdjustment` removes it
   (`features/accounting/api/billing.ts:333-364`). Printing the band by lookup at render time can
   therefore show a band that contradicts the frozen amount beside it, or a dangling id.
3. **Multi-delivery writes less.** That branch writes only `billingRateMultiplier` and
   `billingFuelAdjustmentId` — no `billingAddThbPerTrip`, `billingRateImportId`, or
   `billingEffectiveFromDateStr` (`:293-305`). Multidrop rows have no round provenance at all.

`billingEffectiveFromDateStr` is also a false friend: it is the **fuel adjustment's** effective date,
not the rate card's (`lib/billingCompute.ts:444-446`). In a month whose changes were rate-card
changes it is `null`.

### E. Two announcements on the same date are resolved arbitrarily

`effectiveFrom` is truncated to date granularity (C), and `selectBillingRateEntry` sorts descending
on `effectiveFromMs` (`lib/billingCompute.ts:162-165`). Equal keys tie; `Array.prototype.sort` is
stable, so the winner is simply the first element of the input array — which comes from
`.where("customerId","==",…).get()` with no `orderBy` (`functions/src/tripBillingOnDelivered.ts:195-201`),
i.e. **document-id order**, and ids are auto-generated (`doc(colRef)` at
`features/accounting/api/billing.ts:126`).

So re-importing a corrected sheet for a date that already has one does **not** replace it. Both rows
persist, there is no `effectiveTo` and no supersede step, and the price is decided by a random id.

### F. The reference fuel price offered to the admin is the wrong day's

`fuel_monthly_snapshots/{yyyy-MM}` is one doc per Bangkok month, **overwritten on every sync**
(`functions/src/core/persistFuelMonthlySnapshot.ts:2-3`, `:58-66`), and the form prefills from its
latest value (`app/app/accounting/rate-card/page.tsx:1763`). Entering a round on the 20th that takes
effect on the 16th prefills the 20th's price → wrong band → wrong surcharge for the whole round.

The right input already exists and is untouched by any reader: `fuel_daily_snapshots/{yyyy-MM-dd}`,
written **create-only, never overwritten** (`:69-82`).

### Owner-asserted invariants

- Rounds are **forward announcements** driven by diesel movement plus contract terms; more than two
  in a month is expected.
- The band table is **arithmetic**: uniform ฿1.00 steps, uniform ฿-per-step. No per-customer band
  table is needed.
- The adjustment is **symmetric**: diesel below the baseline yields a genuine discount, so a negative
  `addThbPerTrip` is intended and must not be clamped.
- The customer must see **the rounds** on the invoice, and the rate must appear on **every record**
  in the detail sheet.

## Decision

1. **A round is a fact, and facts are immutable.** A row in `customer_rate_entries` /
   `customer_fuel_rate_adjustments` records an announcement that was made. Editing one in place is
   forbidden; a mistake is corrected by writing a **new row**, and a round that should never have
   existed is **voided** (`voidedAt`, `voidedReason`), never deleted. `updateCustomerFuelRateAdjustment`
   and `deleteCustomerFuelRateAdjustment` are withdrawn from the announcement path. Without this,
   decisions 4 and 6 cannot be honest — a printed band must still mean what it meant when the money
   was computed.

2. **`effectiveFrom` is an instant at Bangkok midnight, and a round is the half-open interval
   `[effectiveFrom, nextEffectiveFrom)`.** "มีผล 16 ส.ค." means 16 Aug 00:00:00 ICT
   (`2026-08-15T17:00:00Z`). `parseDateOnly` is replaced by a Bangkok-midnight constructor, reusing
   the timezone discipline already proven in `bangkokMonthKey` / `bangkokDayKey`
   (`functions/src/core/persistFuelMonthlySnapshot.ts:13-33`). Boundaries stay **half-open** so no
   instant belongs to two rounds and none belongs to zero.

3. **The fuel band is `(n, n+1]` and is computed in satang.** The band floor is
   `Math.ceil(satang / 100) - 1` where `satang = Math.round(price * 100)` — exact at `x.00`, free of
   float drift, and it reproduces `Math.floor` everywhere the two ever agreed:

   ```ts
   const satang    = Math.round(referenceFuelPriceThbPerLitre * 100);
   const bandFloor = Math.ceil(satang / 100) - 1;          // 4200 → 41, 4201 → 42
   const addThbPerTrip = (bandFloor - baselineBandFloor) * thbPerBaht;
   ```

   `baselineBandFloor` is defined explicitly as **the lower integer of the zero-surcharge band**: `41`
   means the band `41.01–42.00` carries `+0`. The result stays signed — a price below the baseline
   produces a discount, by design.

4. **Every priced record carries its own round, denormalized at compute time.** The trip snapshot
   gains `billingFuelBandLowerThb`, `billingFuelBandUpperThb`, `billingReferenceFuelPriceThb`, and
   `billingRoundEffectiveFromDateStr` (the *rate round's* date, distinct from the existing
   fuel-adjustment field in D). The **multi-delivery branch writes the identical field set** as the
   single-delivery branch — the asymmetry at `tripBillingOnDelivered.ts:293-305` is a defect, not a
   design.

   Nothing on an invoice is ever resolved by following `billingFuelAdjustmentId` at render time.

5. **The announcement is priced from the diesel price of its own effective date.** The Rate Card form
   reads `fuel_daily_snapshots/{effectiveFrom-as-yyyy-MM-dd}`, not the month doc. When that day has
   no snapshot the form **says so and refuses to auto-fill**, leaving the admin to enter the
   contract's announced price by hand; it must never silently substitute another day's price.
   `fuel_monthly_snapshots` remains a dashboard convenience and is no longer a billing input.

6. **The invoice shows the rounds explicitly: a `รอบ` column plus a legend block above the table.**
   The legend lists every round in the period — label, date span, band as a *range*, and the
   surcharge — and each line item carries its round label:

   ```
   เรตตามช่วงราคาน้ำมัน (ดีเซล บางจาก)
     R1   1-15 ส.ค. 69   36.01–37.00   ปรับ +0
     R2  16-24 ส.ค. 69   37.01–38.00   ปรับ +10/เที่ยว
     R3  25-31 ส.ค. 69   38.01–39.00   ปรับ +20/เที่ยว

   ลำดับ รอบ  ประเภทรถ  รายการ                วันที่จัดส่ง   จำนวน  ราคา/หน่วย        รวม
     1   R1    4WJ    SPK-GW → ลาดกระบัง    1-15/8/69     20    1,200.00   24,000.00
     2   R2    4WJ    SPK-GW → ลาดกระบัง   16-24/8/69     12    1,210.00   14,520.00
   ```

   The existing invariant `จำนวน × ราคา/หน่วย = รวม` (`lib/billingDocument.ts:242-243`) is preserved:
   the round is a *label* on an existing grouping key, not a new way to compute a total. Sectioning
   the table per round with subtotals was rejected — see Alternatives.

7. **The detail sheet carries the band on every row.** `generateDetailExcelBuffer`'s `DetailRow`
   (`lib/billingDocument.ts:572-586`) gains `fuelBand` (the range, e.g. `37.01–38.00`) and `round`,
   both read from the denormalized snapshot fields of decision 4 — so a record whose rate was never
   captured prints blank rather than a plausible guess.

8. **A month with more than one round is normal, and the boundary is auditable.** No UI warns about,
   blocks, or merges multiple rounds in a period. The Rate Card page instead shows the rounds of the
   selected period as the ordered, half-open intervals of decision 2, so an admin can see a gap or an
   overlap before invoicing rather than after.

## Consequences

**Positive**

- The `x.00` overcharge stops, and with the band denormalized (4) a past invoice can be re-derived
  and defended line by line instead of being re-argued.
- Bangkok midnight (2) removes a 7-hour mispricing window per boundary — the failure mode that grows
  linearly with the number of rounds — and removes the overlapping date ranges the customer currently
  sees on a split route.
- Immutability (1) converts "which announcement priced this?" from a lookup that can lie into a fact
  stored beside the amount.
- Same-day ambiguity (E) disappears: a corrected round is a new row with a later `effectiveFrom`, and
  a wrong one is voided, so the half-open intervals of (2) stay total and disjoint by construction.

**Negative / risks**

- **Existing rows keep their 07:00 ICT semantics.** Reinterpreting stored `effectiveFrom` values as
  Bangkok midnight shifts every historical boundary 7 hours earlier and reprices trips delivered in
  those windows — including months already invoiced. The migration must therefore be an explicit,
  reviewed backfill, guarded by the [[Draft period]] rule
  ([ADR 0008](0008-standby-billing-visibility-and-recompute-semantics.md) §5) so a `sent`/`paid`
  period is reported as blocked rather than silently rewritten.
- **The `Math.floor` → `ceil-1` fix is a repricing event too.** Every past round whose reference
  price ended in `.00` was billed one band too high. Correcting it going forward is trivial;
  deciding whether to credit-note the past is a business call this ADR does not make. The rounds
  affected are identifiable — `referenceFuelPriceThbPerLitre` is stored on each adjustment row.
- Decision 4 denormalizes data that also exists on the adjustment row, so the two can diverge if
  anyone bypasses decision 1. The snapshot is authoritative for money; the adjustment row is the
  announcement.
- Decision 5 can leave the form unable to auto-fill (no daily snapshot for a back-dated round). That
  is deliberate friction: a wrong auto-filled band is worse than a blank field, and the daily
  collection only began accumulating when it was introduced, so early dates simply have no data.
- The `รอบ` column consumes ~12mm of a table whose fixed columns already total 120mm of 182mm usable
  width (`lib/billingDocument.ts:366-374`), squeezing `รายการ` from ~62mm to ~50mm. Long route
  labels will wrap.
- Withdrawing edit/delete (1) removes a repair path admins use today; voiding must ship in the same
  change or the page becomes strictly less usable.

**Follow-ups**

- Spec the implementation via `/spec-new` — this ADR records *why*, not *what to build*.
- Keep `lib/billingCompute.ts` and `functions/src/core/billingCompute.ts` in sync in the same commit
  (project rule; they are line-for-line identical today).
- Backfill plan for the two repricing events above, ordered: fix forward → identify affected periods
  → replay only draft periods → report blocked periods for a business decision.
- Decide whether `customer_rate_entries` rounds also need the legend treatment when a month's change
  was a rate-card change with no fuel movement (the legend would show a round with no band).
- Audit whether any customer's contract defines the band as `[n, n+1)` rather than `(n, n+1]` before
  the `ceil-1` fix ships; the owner asserted the `36.01–37.00` form.

## Alternatives considered

- **One blended rate for the whole month.** Rejected by the owner: the customer expects to see the
  rounds. It also destroys the audit trail — a single average cannot be re-derived from the contract.
- **Section the invoice table per round with a subtotal per round.** Rejected: clearest of the three
  drafts, but it lengthens the invoice and forces a second reconciliation of round subtotals against
  the grand total. The `รอบ` column plus legend conveys the same fact without a new total.
- **Keep the table unchanged and put the rounds only in a footer note.** Rejected: the reader cannot
  tell which line belongs to which round, which is exactly the question a multi-round month raises.
- **Store a per-customer band table** (explicit ranges → surcharge). Rejected on the owner's
  statement that the steps are uniform ฿1.00 with a uniform ฿-per-step; a table would add a schema
  and a UI for a degree of freedom nobody uses. Revisit if a contract ever prices bands unevenly.
- **Clamp `addThbPerTrip` at 0 so the price never falls below the base rate.** Rejected: the owner
  confirmed the adjustment is symmetric and a fall below the baseline is a real discount.
- **Fix the band edge with `Math.floor(price - 0.01)`.** Rejected: it reintroduces binary-float error
  at exactly the boundary it is meant to fix. Integer satang has no such failure.
- **Resolve the fuel band by following `billingFuelAdjustmentId` when rendering the invoice.**
  Rejected: the referenced doc is mutable and deletable (D), so the printed band could contradict the
  frozen amount printed beside it — the single worst outcome for a document sent to a customer.
- **Make `effectiveFrom` a full timestamp the admin picks, not a date.** Rejected: rounds are
  announced by date, and an admin-picked time invites boundaries at arbitrary instants that no
  contract mentions. Bangkok midnight is the contract's meaning.

## Related

- Glossary: [../glossary.md](../glossary.md) — [[Rate round]], [[Fuel band]], [[Announcement row]],
  [[Billing date]], [[Billing period]], [[Draft period]], [[Frozen price]].
- [ADR 0008](0008-standby-billing-visibility-and-recompute-semantics.md) — the billing-date axis this
  ADR slices, and the draft-only recompute guard the migrations depend on.
- [ADR 0002](0002-edit-job-category-on-delivered-trip.md) — the sanctioned path that moves a frozen
  price.
- Legacy [ADR-0005 (BMAD)](../../logitrack-web/_bmad-output/planning-artifacts/adr/ADR-0005-supplementary-trips.md)
  — เสริม rates are fixed prices and never take a fuel adjustment
  (`lib/billingCompute.ts:238-243`, `:423-428`), so they are outside every round in this ADR.
