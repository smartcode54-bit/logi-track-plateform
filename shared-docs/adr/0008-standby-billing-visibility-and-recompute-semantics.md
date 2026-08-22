# ADR 0008 — Standby billing: self-contained records, a service-completion billing date, and draft-only recompute

- **Status:** Accepted (2026-08-04) — implementation pending
- **Deciders:** Samart Kas (product owner), Claude
- **Area:** logitrack-web (Billing Document, Income, Utilities), Cloud Functions (`standbyBilling`,
  `tripBillingOnDelivered`), logitrack-mobile (standby submit flow), Firestore
  (`standby_records`, `billing_statements`)

## Context

Two defects were reported against `/app/accounting/billing-document`:

1. **Standby records do not all appear.** The page shows fewer standby events than
   `/app/standby-records` does for the same month.
2. **After a recompute, prices do not change.** Editing a rate that is effective for a period and
   re-running the recompute leaves the amounts on the billing document unchanged.

Grilling the code established the following facts. They are all verifiable at the cited lines.

### A. A standby record can be born unbillable, on the phone

`submitStandbyRecord` writes **no `customerId`** — the full field list is
`standby_repository.dart:76-94`. The server therefore has exactly one route to a customer:
`data.customerId` → else `taskId` → `task.sourceHubLinkedCustomerId` /
`task.destinationLinkedCustomerId` (`standbyBilling.ts:62-86`).

`taskId` is **nullable** (`standby_repository.dart:28`) and the mobile Standby button does not
require a task: `canStandby = !_saving && origin.isNotEmpty && dest.isNotEmpty`
(`loading_phase_page.dart:2116-2120`), while `_activeTaskId` is `String?`
(`loading_phase_page.dart:119`) and is left `null` on two early-return paths — no active task id
(`:185`) and task doc missing (`:192`). Draft restore repopulates origin/destination
(`:305-306`) **without** restoring `_activeTaskId`, so a resumed session, or a task that was
cancelled/deleted mid-shift, produces a standby with `taskId: null`.

Result: no `customerId`, no `taskId` → `resolveStandbyCustomerId` returns `""` →
`billingEstimateThb` is never written → the record is dropped, silently and permanently, at
`billing.ts:877`. No recompute can ever rescue it, because the failure is missing input, not a
stale price.

The owner confirmed this is **a legitimate business event that must still be billed**, not bad data.

Note the asymmetry inside the same page: the normal delivery flow **hard-blocks** when a hub cannot
be resolved (`loading_phase_page.dart:854`), but the Standby button has no such guard.

### B. The Billing Document page drops rows through four silent filters

`fetchBillingTripRows` (`features/accounting/api/billing.ts:738-750`, `:874-911`) applies, in order:

1. `where("status", "==", "completed")` — exact match.
2. `where("endedAt", ">=", start)` / `< end` — **a Firestore range filter never returns documents
   that lack the field**, so a standby with no `endedAt` is invisible in every month, forever.
3. `if (!billingAmt) return;` (`:877`) — no billing snapshot, no row. No counter, no warning.
4. `if (customerId !== "all" && cid !== customerId) return;` (`:880`) — client-side customer filter
   on `billingCustomerId`.

The whole standby query is additionally wrapped in a `try/catch` that only `console.warn`s
(`:748-750`): a missing or still-building composite index degrades to **zero standby rows with no
UI signal at all**. The index (`status` + `endedAt`) is declared at
`firestore.indexes.json:409-422`.

The Income page does **not** share filters 2: it uses `orderBy("createdAt","desc") + limit(300)` and
falls back `endedAt ?? startedAt ?? createdAt` for the date (`income/page.tsx:356-376`). That
structural difference is why the two pages disagree about the same month.

### C. Three different timestamps were acting as "the date of the period"

| record | rate selection | Billing Document grouping | recompute scan |
|---|---|---|---|
| trip | `deliveredTimestamp` → `createdAt` (`core/billingCompute.ts:125-131`) | `deliveredTimestamp` (`billing.ts:730-731`) | **`createdAt`** (`tripBillingOnDelivered.ts:759-760`) |
| standby | **`createdAt`** → `startedAt` (`standbyBilling.ts:119-122`) | **`endedAt`** (`billing.ts:744-745`) | **`createdAt`** (`standbyBilling.ts:311-312`) |

This is the mechanical cause of defect 2 for trips: recomputing "1–31 July" scans documents
*created* in July, but the July invoice is built from documents *delivered* in July — a trip created
30 June and delivered 1 July is on the invoice and is never touched by the recompute.

`createdAt` was considered as the single axis and **rejected on evidence**: it does not mean the same
thing across writers.

| writer | `createdAt` | `endedAt` |
|---|---|---|
| mobile (`standby_repository.dart:45,83,92`) | `DateTime.now()` — device clock | the **same** `now` instant |
| web backfill dialog (`standby-backfill-dialog.tsx:332,338`) | **`serverTimestamp()`** — the day the admin typed it | the real (back-dated) end time |
| migration script (`migrate-standby-trips.js:158-159`) | `trip.createdAt` | `trip.updatedAt` |

So for mobile-written standby `createdAt === endedAt` exactly — choosing `endedAt` changes nothing
for them — while for the admin backfill dialog, whose entire purpose is entering past events,
`createdAt` is the wrong month by construction.

### D. Standby has no force-recompute path at all

- `tryWriteStandbyBillingSnapshot` returns `skipped` as soon as `billingEstimateThb` is a number
  (`standbyBilling.ts:108-110`). There is **no `forceRecompute` parameter**.
- `backfillStandbyBillingSnapshots` skips such records before attempting anything (`:332`).
- The Utilities UI sends only `{fromDateStr, toDateStr}` (`utilities/backfill/page.tsx:58`).
- The Rate Card **Recompute** button calls only `backfillTripBillingSnapshots`
  (`rate-card/page.tsx:626-634`) — standby is never recomputed by it.

Trips do have `forceRecompute` (`tripBillingOnDelivered.ts:120`, `:751`, `:772`), bounded by the
[[Frozen price]] guard (`:126-129`, ADR-0005) and by `maxWrite` (default 200, capped 500, `:750`).

### E. Nothing links a priced record to the invoice it was billed on

`billing_statements` stores totals and counts only — no line items, no trip/standby ids
(`lib/billingStatement.ts:33-62`) — with `status: "draft" | "sent" | "paid" | "cancelled"` (`:31`).
A recompute that moves prices under an already-sent invoice leaves no trace and no detectable
mismatch.

## Decision

1. **A standby record must be self-contained for billing.** Mobile writes the resolved customer onto
   the record at submit time — `customerId`, plus provenance (`customerResolvedFrom:
   "task" | "origin_hub" | "manual"`). The record must never again depend on a task that may be
   edited, cancelled, or deleted after the fact. Resolution reuses the `HubDoc` the page already
   holds (`_allHubs`, loaded by `_loadHubs()` at `loading_phase_page.dart:355`, resolved at
   `:838-843`) → `HubDoc.linkedCustomerId` — no new fetch and no new string matching.

2. **Unresolvable customer is flagged, never blocking.** If neither a task nor an origin hub yields a
   customer, mobile still saves the record and sets `customerResolved: false`. The driver's job is to
   photograph evidence, not to repair master data. A blocked submit would lose the evidence entirely.
   Web is responsible for surfacing and resolving the flag.

3. **The billing date of a record is the moment the service completed** — `deliveredTimestamp` for a
   trip, `endedAt` for a standby. This one axis is used for **all three** purposes: selecting the
   effective rate, grouping rows into an invoice period, and scanning during recompute. `createdAt`
   remains a provenance field only and must not be used to decide a period.

4. **`endedAt` becomes a required field for a billable standby.** Every writer sets it, and the
   Billing Document must never rely on a range filter to exclude records — see #6.

5. **Recompute overwrites only while the period is still `draft`.** A record whose (customer, period)
   has a `billing_statements` doc in `sent` or `paid` status is not repriced; the callable reports it
   as blocked with the invoice number, and the admin must cancel or credit-note that invoice first.
   [[Frozen price]] (ADR-0005) continues to apply on top of this and is not weakened.

6. **The Billing Document stops dropping rows silently.** `fetchBillingTripRows` returns standby
   records that fail the billing preconditions, flagged (`missingBilling`, with the reason), and the
   page renders a banner — "standby N รายการยังไม่มีราคา" — with a link to fix them. Rows without a
   price are excluded from the [[Invoice set vs preview set|invoice set]] totals; the banner exists
   so the admin can never bill a month while unpriced standby is sitting in it unseen.

7. **The existing backlog is repaired case-by-case through that UI, not by a blind bulk script.**
   Guessing a customer for an old standby produces a confidently wrong invoice; a default customer
   (as `backfillTaskCustomerLinks` does with `TTP`) was rejected for exactly that reason.

8. **The standby query stops failing silently.** The `try/catch` at `billing.ts:748-750` must surface
   the failure in the UI (an error state, not only `console.warn`), because an unbuilt index and
   "there were no standby events" are currently indistinguishable to the user.

## Consequences

**Positive**

- Every future standby carries its own customer, so billing no longer depends on task lifecycle —
  which is what made cancelled/deleted tasks silently destroy revenue.
- "The rate effective in the period" becomes true by construction: the date that selects the rate is
  the same date that decides which invoice the row lands on.
- Recompute becomes trustworthy: it covers exactly the rows the invoice contains, and it cannot
  rewrite history under an invoice already sent to a customer.
- Unbilled work becomes visible instead of vanishing, which is the difference between a revenue leak
  and a to-do list.

**Negative / risks**

- Decision 3 changes the standby rate-selection axis from `createdAt` to `endedAt`. For
  mobile-written records this is a no-op (`createdAt === endedAt`, `standby_repository.dart:45`),
  but records created by the admin backfill dialog or the migration script **may reprice**, and some
  will move to a different month. Those months must be reviewed before anything is re-issued —
  decision 5 blocks the write where the invoice is already `sent`/`paid`, so the correction surfaces
  as a blocked report rather than a silent change.
- Decision 5 needs a (customer, period) → statement lookup that does not exist yet. Until records
  carry the invoice they were billed on, the guard is period-level, not row-level: a row added to a
  month *after* that month was invoiced is indistinguishable from one that was on the invoice. The
  row-level link (`billedOnInvoiceNumber`) is a follow-up, not part of this ADR.
- Standby records that still lack `endedAt` need a repair pass before decision 4 can be enforced; the
  Income page's `endedAt ?? startedAt ?? createdAt` fallback (`income/page.tsx:376`) shows such
  records exist or existed.
- The mobile hub resolver reused by decision 1 matches only `sourceNameEn` **or** `sourceId`
  (`loading_phase_page.dart:838-843`), while ADR-era feature #38 made `source_name_th` the required
  primary name and `source_name_en` optional (and the picker writes `null` for an empty English name
  — see [[Unresolved place]]). A hub with no English name therefore resolves to nothing, and the
  standby will be flagged under decision 2 rather than resolved. Fixing that resolver to also match
  `sourceNameTh` is a follow-up.

**Follow-ups**

- Spec the implementation via `/spec-new` before coding — this ADR records *why*, not *what to build*.
- Repair pass for standby without `endedAt`.
- Teach the mobile hub resolver `sourceNameTh`.
- Consider `billedOnInvoiceNumber` on trip/standby records to make decision 5 row-level.
- Verify the `standby_records` (`status`, `endedAt`) composite index is deployed in prod.
- Reconcile `lib/billingCompute.ts` and `functions/src/core/billingCompute.ts` in the same commit —
  they are currently in sync line-for-line and the project rule requires they stay that way.

## Alternatives considered

- **Keep `createdAt` as the single billing-date axis everywhere.** Rejected on evidence: `createdAt`
  is `serverTimestamp()` in the admin backfill dialog (`standby-backfill-dialog.tsx:338`), so a June
  event entered in August would be billed in August at August rates; it would also make the newly
  built inline `deliveredTimestamp` edit inert, since `createdAt` cannot be corrected from any UI.
- **Block the mobile standby submit until a customer resolves.** Rejected: the evidence photos are
  the irreplaceable part of the record, and a driver at a hub cannot fix a missing
  `hubs.linkedCustomerId`. Losing the record is strictly worse than saving it flagged.
- **Fall back to a default customer when resolution fails** (the `TTP` approach in
  `backfillCustomerLinks.ts`). Rejected: it converts a visible gap into an invisible wrong invoice.
- **Let recompute overwrite prices unconditionally and warn on the page.** Rejected by the owner: a
  sent invoice is a commitment; the system must not be able to contradict a document already in the
  customer's hands.
- **Freeze at row level immediately** by stamping the invoice number on each billed record.
  Deferred, not rejected — it is the more precise version of decision 5 but needs a migration for
  historical statements, which have no line items.
- **Bulk-backfill the existing unpriced standby backlog.** Rejected — see decision 7.

## Related

- Glossary: [../glossary.md](../glossary.md) — [[Standby]], [[Billing date]], [[Billing period]],
  [[Unpriced standby]], [[Draft period]], [[Frozen price]], [[Invoice set vs preview set]],
  [[Unresolved place]].
- [ADR 0005](0005-truck-plate-filter-billing-document-driver-monitor.md) — invoice set vs preview
  set; the review-filter guard on the same page.
- [ADR 0002](0002-edit-job-category-on-delivered-trip.md) — the one sanctioned path that moves a
  frozen price.
- Legacy [ADR-0005 (BMAD)](../../logitrack-web/_bmad-output/planning-artifacts/adr/ADR-0005-supplementary-trips.md)
  — supplementary trips and frozen pricing.
