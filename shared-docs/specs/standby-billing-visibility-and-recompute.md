# Spec: Standby billing — visibility & recompute semantics

> **Status:** ✅ Done (2026-08-04) — implemented locally; not deployed
> **Owner:** Samart Kas
> **Created:** 2026-08-04
> **Domain:** accounting (billing) + drivers/standby (mobile)
> **Related:** [ADR 0008](../adr/0008-standby-billing-visibility-and-recompute-semantics.md) — every decision here comes from that ADR, approved point-by-point during the grilling session on 2026-08-04.

---

## 1. Problem & Goal

Standby events go missing on `/app/accounting/billing-document`, and recomputing prices does not
change what the page shows. Two independent causes: a standby can be born with no resolvable
customer (mobile writes no `customerId`, and `taskId` is nullable), and it is then discarded
silently by the page; separately, standby has no force-recompute path at all while the trip
recompute scans a different date axis than the invoice groups by.

Goal: no billable standby can disappear without the admin seeing it, and a recompute changes exactly
the rows the invoice for that period contains — but never rewrites an invoice already sent.

## 2. Scope

**In scope:**
- Mobile writes `customerId` + resolution provenance onto every new standby record (ADR §1–2).
- One billing-date axis — service completion — for rate selection, invoice grouping, and recompute
  scan (ADR §3).
- `forceRecompute` for standby; trip + standby recompute blocked on `sent`/`paid` periods (ADR §5).
- Billing Document surfaces unpriced / malformed standby instead of dropping it, and lets an admin
  fix one record at a time (ADR §6–7).
- The standby query stops failing silently (ADR §8).

**Out of scope (ทำทีหลัง / ไม่ทำ):**
- `billedOnInvoiceNumber` per row (row-level invoice lock) — ADR follow-up, needs a migration.
- Bulk backfill of the existing unpriced backlog — explicitly rejected (ADR §7).
- Repair pass for standby missing `endedAt` — this spec only makes them **visible**.
- Teaching the mobile hub resolver `sourceNameTh` — ADR follow-up.
- Any change to trip pricing maths, fuel adjustment, or multi-drop logic.

## 3. Requirements

**Functional**
- R1. A standby created on mobile carries `customerId` when it can be resolved (from the task, else
  from the origin hub's `linkedCustomerId`), plus `customerResolvedFrom`.
- R2. When no customer can be resolved, mobile still saves the record and sets
  `customerResolved: false`. Submit is never blocked on this.
- R3. Standby billing selects its rate by `endedAt` (fallback `startedAt`, then `createdAt`), not by
  `createdAt`.
- R4. Standby recompute scans by `endedAt`; trip recompute scans by `deliveredTimestamp` — the same
  axes the Billing Document groups by.
- R5. `backfillStandbyBillingSnapshots` accepts `forceRecompute` and rewrites existing prices when it
  is set.
- R6. A record whose `(billingCustomerId, period)` has a `billing_statements` doc with status `sent`
  or `paid` is **not repriced**; the callable reports it as blocked, with the invoice number.
- R7. Billing Document returns standby rows that fail the billing preconditions, flagged, and shows a
  banner with the count. Flagged rows are excluded from the invoice-set totals.
- R8. The banner lets an admin assign a customer to one unpriced standby and price it immediately.
- R9. Standby records with no `endedAt` (invisible to every month) are counted and reported in the
  same banner.
- R10. A failed standby query surfaces as a visible error state, not only `console.warn`.

**Non-functional**
- N1. i18n complete in both `en` and `th`.
- N2. No new Firestore composite index required (verified: every new query maps to an existing one).
- N3. Billing logic changes mirrored in **both** `lib/billingCompute.ts` and
  `functions/src/core/billingCompute.ts` where they touch shared pure functions.
- N4. Mobile change ships with a `pubspec.yaml` bump.

## 4. Design

**Data model (Firestore)**
- `standby_records` — new optional fields written by mobile:
  `customerId: string`, `customerResolved: boolean`, `customerResolvedFrom: "task" | "origin_hub" | "manual"`.
  No new collection. Admin repair writes `customerId` + `customerResolvedFrom: "manual"`.
- `billing_statements` — read-only here; `status` + `period` drive the recompute lock.

**Cloud Functions / billing**
- New server-only helper `functions/src/core/billingPeriodLock.ts` — loads `sent`/`paid` statements
  once and answers "is (customerId, year, month) locked", plus the invoice number for the message.
  Deliberately **not** in `billingCompute.ts`, so the two-file sync rule stays untouched.
- `standbyBilling.ts` — billing date from `endedAt`; `forceRecompute` threaded through
  `tryWriteStandbyBillingSnapshot`; backfill + scheduled job scan `endedAt`; lock guard applied.
- `tripBillingOnDelivered.ts` — backfill scans `status == "delivered"` + `deliveredTimestamp`; lock
  guard applied on top of the existing [[Frozen price]] guard.

**Web (Next.js)**
- `features/accounting/api/billing.ts` — new `fetchStandbyBillingDiagnostics()`. Implemented as a
  **separate channel** rather than flagged rows inside `fetchBillingTripRows`: a price-less row must
  be structurally incapable of reaching the invoice set (ADR 0005), and `fetchBillingTripRows` also
  feeds the Billing Result page's re-download path, where a ฿0 row would land on a real invoice.
  It classifies each failure (`no_customer` / `no_rate` / `not_computed` / `no_ended_at`), runs a
  bounded second query to find records with no `endedAt` (a range filter can never return them), and
  reports `queryFailed` instead of swallowing the error.
- `features/accounting/components/UnpricedStandbyPanel.tsx` — banner + per-record customer picker +
  "price now" action.
- `app/app/accounting/billing-document/page.tsx` — renders the panel; invoice/preview sets exclude
  flagged rows.
- `app/app/utilities/backfill/page.tsx` + `app/app/accounting/rate-card/page.tsx` — pass
  `forceRecompute`; rate-card Recompute also runs the standby backfill.
- i18n: `context/locales/{en,th}/accounting.ts`.

**Mobile (Flutter)**
- `standby_repository.dart` — accept + write the three customer fields.
- `loading_phase_page.dart` — resolve the origin `HubDoc` already in memory and pass
  `linkedCustomerId` into `StandbyPage`; `standby_page.dart` forwards it.
- `pubspec.yaml` bump.

**Firestore Rules** — no change needed: `standby_records` already allows `update` for `isWebAdmin()`,
and the new fields are written by the record's own creator on `create`.

## 5. Affected files

`functions/src/core/billingPeriodLock.ts` (new), `functions/src/standbyBilling.ts`,
`functions/src/tripBillingOnDelivered.ts`, `features/accounting/api/billing.ts`,
`features/accounting/components/UnpricedStandbyPanel.tsx` (new),
`app/app/accounting/billing-document/page.tsx`, `app/app/utilities/backfill/page.tsx`,
`app/app/accounting/rate-card/page.tsx`, `context/locales/{en,th}/accounting.ts`,
`logitrack-mobile/lib/features/loading_phase/data/repositories/standby_repository.dart`,
`logitrack-mobile/lib/features/loading_phase/presentation/pages/standby_page.dart`,
`logitrack-mobile/lib/features/loading_phase/presentation/pages/loading_phase_page.dart`,
`logitrack-mobile/pubspec.yaml`.

## 6. Task breakdown

- [x] T1. (R6) `functions/src/core/billingPeriodLock.ts` — locked-period set from `billing_statements`.
- [x] T2. (R3,R4,R5,R6) `standbyBilling.ts` — `endedAt` billing date + scan axis, `forceRecompute`, lock guard.
- [x] T3. (R4,R6) `tripBillingOnDelivered.ts` — scan by `deliveredTimestamp`, lock guard.
- [x] T4. (R7,R9,R10) `fetchBillingTripRows` — flag instead of drop, `endedAt` diagnostic, surface query failure.
- [x] T5. (R7,R8) `UnpricedStandbyPanel` + wire into the Billing Document page; totals exclude flagged rows.
- [x] T6. (R5) Utilities + Rate Card recompute pass `forceRecompute`; Rate Card also recomputes standby.
- [x] T7. (R1,R2) Mobile — resolve + write `customerId` / `customerResolved` / `customerResolvedFrom`; bump `pubspec.yaml`.
- [x] T8. (N1) i18n `en` + `th`.
- [x] T9. Update `.vibe-rules.md` Change Log.

## 7. Acceptance criteria

- [x] AC1. (R3,R4) Standby rate selection and both recompute scans use the service-completion axis; no `createdAt` remains as a period decider.
- [x] AC2. (R5) `backfillStandbyBillingSnapshots({forceRecompute:true})` rewrites an existing `billingEstimateThb`.
- [x] AC3. (R6) A record in a `sent`/`paid` period is reported blocked with its invoice number and its price is unchanged.
- [x] AC4. (R7,R9) Billing Document shows the unpriced/`endedAt`-less counts; flagged rows are not in the invoice total.
- [x] AC5. (R8) Assigning a customer from the panel prices that record and it moves into the normal rows on reload.
- [x] AC6. (R10) A failed standby query renders an error state.
- [x] AC7. (N1) `en` and `th` key counts for the new namespace match.
- [x] AC8. `tsc --noEmit`, ESLint, Vitest, and `flutter analyze` on changed files all pass.

## 8. Risks & rollback

| Risk | Mitigation / rollback |
|------|----------------------|
| Changing the standby rate axis reprices historical records | For mobile-written records `createdAt === endedAt`, so they cannot move; only admin-backfilled / migrated ones can. The lock guard (R6) refuses to touch `sent`/`paid` periods, so any correction surfaces as a blocked report instead of a silent change. |
| Trip recompute now scans a different set | Same date window, different axis: it gains trips delivered in-window but created earlier (the ones the invoice actually contains) and loses trips created in-window but not yet delivered — which have no price to recompute anyway. |
| Lock guard reads `billing_statements` on every recompute | Loaded once per call and cached in a `Map`; statement volume is small (one per customer per month). |
| A recompute query needs an index that is not deployed | Both backfills query with an equality + a range on the same field and **no explicit `orderBy`**, so they reuse the exact composite indexes the Billing Document's own queries already rely on in prod. The first prod run returned `INTERNAL` because an explicit `orderBy(..., "desc")` asked for a differently-ordered index; the `orderBy` was removed and the query is now wrapped so any Firestore error (including the index-creation URL) reaches the UI instead of a bare `INTERNAL`. |
| Mobile writes a wrong customer from a mis-resolved hub | Provenance is recorded (`customerResolvedFrom`), and an unresolved hub yields `customerResolved: false` rather than a guess. No default customer is ever assigned (ADR §7). |

## 9. Open questions / follow-ups

- Row-level invoice lock (`billedOnInvoiceNumber`) — makes R6 exact instead of period-level.
- Repair pass for standby with no `endedAt`; this spec only counts them.
- Mobile hub resolver should also match `sourceNameTh` (`loading_phase_page.dart:838-843`).
- Verify the `standby_records (status, endedAt)` index is deployed in prod.
- **The single-trip `computeTripBillingSnapshot` is deliberately NOT gated by the period lock.** Only
  bulk recompute is. ADR 0002 made an explicit per-trip admin edit the one sanctioned way to move a
  settled price, and gating it would break the Edit Trip dialog. Worth revisiting once the row-level
  lock exists.
- **Nothing was verified against live data.** Local ADC returns `PERMISSION_DENIED` on Firestore for
  both `logitrack-prod` and `logi-track-wrt-dev`, so the acceptance criteria were checked by type
  system, unit tests, and code reading — not by running the flows. Needs a real run on dev.
- **`CLAUDE.md` #25 is stale:** it says `vitest.config.ts` excludes `functions/**`. The real config
  excludes only `node_modules`, `functions/node_modules`, `.next`, `out`, and `tests/**/*.spec.ts`,
  which is why `functions/src/core/billingPeriodLock.test.ts` runs in the normal suite.
