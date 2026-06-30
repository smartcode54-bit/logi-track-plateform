# ADR-0005: Supplementary trips (เที่ยวเสริม) — jobCategory, separate rate card, frozen pricing

- **Status:** Accepted (2026-06-30)
- **Context owners:** Billing / Task Assignment
- **Related:** [billing destination resolution](../glossary.md), `customer_rate_entries`, `tripBillingOnDelivered.js`

## Context

Operations run two kinds of trips against a customer hub: the **primary** contracted route (หลัก) and **supplementary** ad-hoc trips (เสริม) that are negotiated separately. Today the system has no way to mark a trip as supplementary, and **all** billing is derived from the single `customer_rate_entries` rate card via `selectBillingRateEntry(customerId, hubId, destinationCode, vehicleClass, billDate)`. Two problems:

1. A supplementary trip is billed at a **separately agreed price** (examples: 4WJ บางปู→Wang Thonglang12 = 1,250; PICKUP-as-4W บางปู→Lat Krabang26 = 950) that does **not** come from the primary rate card.
2. Once a supplementary trip has happened, its price is **locked** — it must not move when the primary rate card or fuel adjustment is re-imported or when a forced recompute runs.

There is already a `jobType` field on trip records, but it means **`first_mile | line_haul`** (auto-detected from origin) — the *same* axis as `taskSchema.taskType`. It is **not** primary/supplementary. A `billingManualOverride` flag exists on `writeTripBillingSnapshot` but the recompute path (`tripBillingOnDelivered.js`) **ignores it when `forceRecompute === true`**, so it does not currently guarantee a frozen price.

## Decisions

### Locked (confirmed with user, 2026-06-30 grilling)

1. **`jobCategory: "PRIMARY" | "SUPPLEMENTARY"` lives on `trip_records` only**, written at billing time (not on `tasks`). **Not** reusing `jobType` (collision with first_mile/line_haul). Default **`PRIMARY`**. UI displays **หลัก / เสริม**.
2. **Migration script** backfills every existing `trip_records` doc to `jobCategory = "PRIMARY"`.
3. **`jobCategory` is DERIVED, not manually selected** (revised — see "Derivation" below). หลัก/เสริม is a property of the **route** (which rate card the hub→destination+vehicle appears in for the trip's date), not a per-task toggle. There is **no** หลัก/เสริม selector in the assign dialogs or import templates.
4. **Supplementary trips have their own rate card** (separate from the primary `customer_rate_entries`).
5. **Frozen pricing:** a SUPPLEMENTARY trip's billing snapshot is computed **once** (at delivery, from the supplementary rate card) and then **never recomputed** — `forceRecompute` must skip it. Primary rate-card / fuel re-imports never change a เสริม price.
6. **Both display rules ship this round** (origin code, PICKUP→4W) — see below.

### Derivation (revised 2026-06-30, third round)

A route **starts as เสริม and may be promoted to หลัก later** — a transition over time, never both at once. Because rate cards are **date-effective** (`effectiveFrom`), the category can be derived from the rate card at billing time, so **no manual selector is needed**:

- At delivery, look up the rate by the trip's billing date: **try the PRIMARY card first**; if an entry is effective on that date → `jobCategory = PRIMARY`. Otherwise try the **SUPPLEMENTARY** card; if matched → `jobCategory = SUPPLEMENTARY` + freeze (`billingManualOverride`).
- The trip's `jobCategory` is written onto the trip at billing time (for the report remark + the recompute-skip guard).
- A trip delivered while the route was เสริม keeps its frozen price forever; trips after the route is promoted to หลัก bill from the primary card. Primary-first precedence resolves any overlap.
- Consequence: the `jobCategory` field on **tasks is dropped**; assign dialogs / import templates are **unchanged**.

### Locked (confirmed 2026-06-30, second round)

7. **Supplementary rate card = a `jobCategory` dimension on `customer_rate_entries`**, not a brand-new collection. `selectBillingRateEntry` gains a `jobCategory` argument and filters on it; the rate-card import screen gets a หลัก/เสริม selector so a sheet is imported into one category. "Separate" is enforced by the filter.
8. **Freeze mechanism = set `billingManualOverride = true` on every SUPPLEMENTARY snapshot**, AND patch `tripBillingOnDelivered` so the `forceRecompute` branch skips any trip with `billingManualOverride === true` **or** `jobCategory === "SUPPLEMENTARY"`.
9. **Excel report remark:** หมายเหตุ column shows **`"เสริม"`** for supplementary rows, blank for primary.
10. **Display rule — origin code (J&T only):** in the billing report (**on-screen table + Excel**) เส้นทาง column, when the customer is **J&T**, the **source hub** renders as its **hub code / `source_id`** (e.g. `SPK-GW`) instead of the linked-customer billing name (`J&T EXPRESS บางปู`). Destination unchanged. Other customers keep the resolved name. The code is resolved **name→`source_id` via the `hubs` collection**, so trips whose `billingLookupHubId` snapshot stored a *name* (not a code) still render the code.
11. **Display rule — PICKUP→4WH (global):** in the Excel report ประเภท column, vehicle class **`PICKUP` renders as `4WH`** (the canonical 4-wheel type already in the enum) for all customers.
12. **Excel "Sub" column** (renamed from "Sup"): shows the **operating company short name** — the **subcontractor name** when the trip was run by a subcontractor, otherwise the **owner company `shortName`** (e.g. `WRT`) for own-fleet trips. `shortName` is a new optional field on the company profile (`companySchema`), set in Settings → Company profile.

## Consequences

- **Schema/migration:** two schemas gain a defaulted enum; a one-shot backfill script writes `PRIMARY` to all history. Low risk (additive, defaulted), but the script touches every task and trip doc.
- **Billing engine:** `selectBillingRateEntry` signature changes (one more filter arg) — all call sites must pass `jobCategory`. The supplementary lookup runs at delivery only; thereafter the snapshot is authoritative.
- **Recompute safety:** closing the `forceRecompute` gap protects เสริม prices but also means a genuinely wrong เสริม snapshot can only be fixed by an explicit manual edit, not a bulk recompute — acceptable, matches "price is agreed and fixed."
- **Report:** remark + two display transforms are localized to `generateDetailExcelBuffer` and the billing-document page row builder (display rules), consistent with ADR scope "Excel only" from the prior round.

## Alternatives considered

- **Reuse `jobType`** for primary/supplementary — rejected, collides with first_mile/line_haul.
- **Separate supplementary rate-card collection** — viable but doubles import/CRUD/query surface; deferred unless the category filter proves insufficient (decision 7).
- **Compute เสริม from primary card then lock** — rejected by user: เสริม has its own agreed prices, not derivable from the primary card.
