/**
 * Billing document model: the row, customer and period types of a billing statement and the pure
 * helpers that group and date its rows (ADR 0009, ADR 0027).
 *
 * Nothing here imports a PDF, XLSX or ZIP library, so pages, `features/accounting/api/billing.ts`
 * and tests use these types and helpers without pulling the document renderers into their bundle.
 * The renderers are in `./billingDocumentRender.ts`, which callers load with `await import()` in
 * their download handlers (developer-spec.md §10.11, Appendix E §E.7 rows 3-5).
 */

// ─── Provider type (extends BILLING_PROVIDER with optional stamp/signature) ──

export interface BillingProviderInfo {
  name: string;
  /** ชื่อย่อบริษัทเจ้าของ (e.g. "WRT") — shown in the "Sub" column for own-fleet trips (ADR-0005). */
  shortName?: string;
  address: string;
  taxId: string;
  bankName?: string;
  accountNumber?: string;
  accountName?: string;
  withholdingTaxRate?: number; // 0–100 (%). Falls back to WITHHOLDING_TAX_RATE (1%)
  stampUrl?: string;
  signatureUrl?: string;
  signatoryName?: string;
}

// ─── Data types ─────────────────────────────────────────────────────────────

/** Which date a billing entity closes its period on (ADR 0027). Absent on the profile ⇒ "delivered". */
export type BillingDateBasis = "plan" | "delivered";

export interface BillingTripRow {
  id: string;
  /**
   * `trip_records/{id}` this row was billed from. Equals `id` for a normal trip; a multidrop stop's
   * `id` carries an `_sN` suffix, so a repair action (re-pricing via the server callable) must use
   * this instead. Absent on standby rows, which live in `standby_records`.
   */
  tripRecordId?: string;
  taskId?: string;
  spxTripId?: string;
  deliveredTimestamp?: Date;
  /** Admin-set actual pickup date-time from the task (ADR 0028) — operational, optional detail column. */
  actualPickupAt?: Date;
  /**
   * วันแผนงาน — `tasks.date`, the day the customer scheduled the job (ADR 0027/0028). The single
   * source of truth for the plan date; `billingDate` below is its frozen per-trip copy. Display only:
   * nothing prices off this, so an admin editing the task later can never silently move a settled bill.
   */
  planDate?: Date;
  /**
   * The date axis this row was billed on — `trip_records.billingDate`, frozen when the row was priced
   * (ADR 0027): the plan date for a plan-basis billing entity, the delivery instant for everyone else.
   * Absent on standby (always `endedAt`, ADR 0008) and on trips priced before ADR 0027 — use
   * `billingAxisDate()`, never this field raw.
   */
  billingDate?: Date;
  /**
   * Which axis the billing entity of this row bills on (ADR 0027). Stamped by `fetchBillingTripRows`
   * only when a single entity was requested — the only case a document is generated for. Undefined in
   * the "all" aggregate, where rows from both bases are mixed and no one basis describes the set.
   */
  billingDateBasis?: BillingDateBasis;
  billingEstimateThb: number;
  billingBaseRateThb?: number;
  billingLookupHubId?: string;
  billingLookupDestination?: string;
  billingRateMultiplier?: number;
  billingAddThbPerTrip?: number;
  /** Admin-set or เสริม-frozen price (ADR-0005) — a forced recompute leaves it alone. */
  billingManualOverride?: boolean;
  billingCustomerId?: string;
  vehicleClass?: string;
  driverName?: string;
  driverPhone?: string;
  /** ผู้รับเหมา (Sup) ของคนขับ — แสดงในคอลัมน์ Sup ของ Excel detail; "-" ถ้าเป็นรถตัวเอง */
  subcontractorName?: string;
  truckLicensePlate?: string;
  /** trucks/{id} for the vehicle that ran this row — the identity the plate filter matches on
   *  (a plate string is not an identity). See ADR 0005 §4-5. */
  truckId?: string;
  hubDisplayName?: string;
  /** Source-hub CODE (source_id) resolved by the page — used for the J&T origin-code rule (ADR-0005). */
  originHubCode?: string;
  destinationDisplayName?: string;
  /** Row type for grouping/display: "trip" = normal, "multidrop_stop" = expanded stop, "standby" = จอดรอ */
  rowType?: "trip" | "multidrop_stop" | "standby";
  stopIndex?: number;
  /** หลัก/เสริม — SUPPLEMENTARY rows show "เสริม" in หมายเหตุ (ADR-0005). */
  jobCategory?: "PRIMARY" | "SUPPLEMENTARY";
  // Rate round + fuel band, denormalized onto the trip when it was priced (ADR 0009 §4).
  // Read straight off the record: resolving them at render time through
  // `billingFuelAdjustmentId` could print a band that contradicts the frozen amount beside it.
  /** `yyyy-MM-dd` the price of this row last changed — groups rows into rounds. */
  billingRoundEffectiveFromDateStr?: string;
  billingFuelBandLowerThb?: number;
  billingFuelBandUpperThb?: number;
  billingReferenceFuelPriceThb?: number;
}

/**
 * The date a row belongs to the statement by (ADR 0027).
 *
 * Every date a billing document prints — the invoice's line-item date ranges, the round legend's
 * spans, the detail sheet's วันที่ column — must be read on the axis that decided which period the
 * row lands in, or a plan-basis customer's September invoice prints October delivery dates and the
 * row falls out of their reconciliation (the exact "หลุดวางบิล" leak ADR 0027 closes).
 *
 * Falls back to the delivery instant for rows priced before ADR 0027 (no `billingDate` stamped) and
 * for standby, which stays on `endedAt` for every customer (ADR 0008). For a delivered-basis row
 * `billingDate` IS `deliveredTimestamp`, so this is a no-op for everyone but plan-basis customers.
 */
export function billingAxisDate(t: BillingTripRow): Date | undefined {
  return t.billingDate ?? t.deliveredTimestamp;
}

/**
 * The axis a set of billed rows was built on (ADR 0027).
 *
 * `fetchBillingTripRows` stamps the basis on rows only when a single billing entity was requested,
 * which is the only case a document is ever generated for — so one stamped row answers for the set.
 * Nothing stamped (the "all" aggregate, or a legacy caller) ⇒ the delivered default, i.e. today's
 * behaviour for every customer who never opted in.
 */
export function billingDateBasisOf(trips: BillingTripRow[]): BillingDateBasis {
  return trips.some((t) => t.billingDateBasis === "plan") ? "plan" : "delivered";
}

/** One price round present in a billing period — the invoice legend (ADR 0009 §6). */
export interface BillingRound {
  /** Display label, assigned by date order at render time and never stored. */
  label: string;
  effectiveFromDateStr: string;
  fuelBandLowerThb?: number;
  fuelBandUpperThb?: number;
  addThbPerTrip?: number;
  /** Span of the round inside this period, on the billing axis (`billingAxisDate`) — NOT the
   *  delivery instant, which for a plan-basis customer can sit in the neighbouring month. */
  firstBillingDate?: Date;
  lastBillingDate?: Date;
}

/**
 * Collect the distinct rounds a period's rows were priced under, oldest first.
 *
 * Labels are derived here rather than stored: a stored `R2` would renumber the moment a round is
 * voided or a back-dated row appears, and would then disagree with an invoice already sent.
 */
export function collectBillingRounds(trips: BillingTripRow[]): BillingRound[] {
  const byDate = new Map<string, BillingRound>();
  for (const t of trips) {
    const key = t.billingRoundEffectiveFromDateStr;
    if (!key) continue;
    const existing = byDate.get(key);
    const d = billingAxisDate(t);
    if (existing) {
      if (d) {
        if (!existing.firstBillingDate || d < existing.firstBillingDate) existing.firstBillingDate = d;
        if (!existing.lastBillingDate || d > existing.lastBillingDate) existing.lastBillingDate = d;
      }
      continue;
    }
    byDate.set(key, {
      label: "",
      effectiveFromDateStr: key,
      fuelBandLowerThb: t.billingFuelBandLowerThb,
      fuelBandUpperThb: t.billingFuelBandUpperThb,
      addThbPerTrip: t.billingAddThbPerTrip,
      firstBillingDate: d,
      lastBillingDate: d,
    });
  }
  return Array.from(byDate.values())
    .sort((a, b) => a.effectiveFromDateStr.localeCompare(b.effectiveFromDateStr))
    .map((r, i) => ({ ...r, label: `R${i + 1}` }));
}

/** `37.01–38.00`, or "-" when the row carries no band (legacy rows, or a percent-only round). */
export function formatFuelBand(lowerThb?: number, upperThb?: number): string {
  if (typeof lowerThb !== "number" || typeof upperThb !== "number") return "-";
  return `${lowerThb.toFixed(2)}–${upperThb.toFixed(2)}`;
}

export interface BillingCustomer {
  id: string;
  name: string;
  address?: string;
  taxId?: string;
  branchType?: string;
  branchNumber?: string;
  contactName?: string;
  contactPhone?: string;
  paymentTermsDays?: number;
  invoiceNote?: string;
}

export interface BillingPeriod {
  /** 1-based month (1–12) */
  month: number;
  year: number;
}

export interface LineItem {
  vehicleClass: string;
  route: string;
  count: number;
  unitPrice: number;
  total: number;
  dates: Date[];
  /** Standby lists each working day (14,17,20); others use a min–max range (2-31). */
  enumerateDays: boolean;
  /** Round label (R1/R2/…) resolved from the legend; "" when the rows carry no round. */
  roundLabel: string;
}

const beYear = (d: Date) => d.getFullYear() + 543;

/**
 * Delivery date label for a line item, in Thai Buddhist year.
 * A statement covers a single billing month, so all dates share month/year.
 *  - range (trips / ค่าโยก): same day → "5/5/2569"; span → "2-31/5/2569"
 *  - enumerated (standby): distinct days → "14,17,20/5/2569"
 */
export function formatLineItemDates(dates: Date[], enumerateDays: boolean): string {
  const valid = dates.filter(Boolean).sort((a, b) => a.getTime() - b.getTime());
  if (valid.length === 0) return "-";
  const ref = valid[valid.length - 1];
  const monthYear = `${ref.getMonth() + 1}/${beYear(ref)}`;

  if (enumerateDays) {
    const days = [...new Set(valid.map((d) => d.getDate()))].sort((a, b) => a - b);
    return `${days.join(",")}/${monthYear}`;
  }

  const min = valid[0];
  const max = valid[valid.length - 1];
  const sameMonth = min.getFullYear() === max.getFullYear() && min.getMonth() === max.getMonth();
  if (sameMonth) {
    const dMin = min.getDate();
    const dMax = max.getDate();
    const dayPart = dMin === dMax ? `${dMin}` : `${dMin}-${dMax}`;
    return `${dayPart}/${monthYear}`;
  }
  // Cross-month fallback (rare): spell out both ends fully
  return `${min.getDate()}/${min.getMonth() + 1}/${beYear(min)}-${max.getDate()}/${max.getMonth() + 1}/${beYear(max)}`;
}

/**
 * Group trips by vehicleClass + route for the invoice body table.
 *
 * The grouping key is unchanged (ADR 0009 §6): the round is a *label* on an existing group, not a
 * new way to total, so `count × unitPrice = total` still holds on every line. A route priced in two
 * rounds already produced two lines, because the unit price is part of the key.
 */
export function groupToLineItems(trips: BillingTripRow[], rounds: BillingRound[] = []): LineItem[] {
  const roundLabelByDate = new Map(rounds.map((r) => [r.effectiveFromDateStr, r.label]));
  const map = new Map<string, LineItem>();
  for (const t of trips) {
    const isStandby = t.rowType === "standby";
    const isStop    = t.rowType === "multidrop_stop";
    const vc = t.vehicleClass ?? "-";
    // Origin shows the hub CODE (e.g. SPK-GW), matching the Excel detail sheet — the invoice and its
    // Excel companion must present the same origin. Destination stays the display NAME: the two ends
    // of the route are deliberately asymmetric (same rule as generateDetailExcelBuffer's originLabel).
    const originLabel = t.originHubCode || t.billingLookupHubId || t.hubDisplayName || "-";
    const baseRoute = [
      originLabel,
      t.destinationDisplayName ?? t.billingLookupDestination  ?? "-",
    ].join(" → ");
    // ค่าโยก (multidrop) จัดกลุ่มรวมเป็นรายการเดียว ไม่ระบุเส้นทาง — แจกแจงด้วยช่วงวันที่เหมือนเที่ยวปกติ
    const route = isStandby ? `${baseRoute} (Stand by)`
                : isStop    ? "ค่าโยก"
                : baseRoute;
    // Use final (adjusted) rate as unit price so quantity × unitPrice = total
    const unitPrice = t.billingEstimateThb;
    // The round joins the key so a line can never span two rounds. Price alone is not enough:
    // two rounds can land on the same unit price for a route (a round that only moved other
    // routes), and those rows would then merge into one line carrying an arbitrary round label.
    const roundKey = t.billingRoundEffectiveFromDateStr ?? "";
    const key = `${vc}::${route}::${unitPrice}::${roundKey}`;
    // Billing axis, not the delivery instant (ADR 0027): `formatLineItemDates` takes the month/year
    // from these dates on the premise that a statement covers one month, which only holds on the axis
    // the period was built from.
    const d = billingAxisDate(t);
    const existing = map.get(key);
    if (existing) {
      existing.count += 1;
      existing.total += t.billingEstimateThb;
      if (d) existing.dates.push(d);
    } else {
      map.set(key, {
        vehicleClass: vc,
        route,
        count: 1,
        unitPrice,
        total: t.billingEstimateThb,
        dates: d ? [d] : [],
        enumerateDays: isStandby,
        roundLabel: roundLabelByDate.get(t.billingRoundEffectiveFromDateStr ?? "") ?? "",
      });
    }
  }
  return Array.from(map.values());
}
