/**
 * Pure per-trip billing math (no Firebase). Used by web admin and Cloud Functions.
 * Duplicated from `logitrack-web/lib/billingCompute.ts` — keep in sync when changing either file.
 */

export interface BillingRateEntry {
    id: string;
    customerId: string;
    importId: string;
    hubId: string;
    destinationCode: string;
    vehicleClass: string;
    rateThb: number;
    effectiveFromMs: number;
    /** หลัก/เสริม. Missing = "PRIMARY" (legacy rows). See ADR-0005. */
    jobCategory?: "PRIMARY" | "SUPPLEMENTARY";
    /** Voided announcement — never selected for pricing (ADR 0009 §1). */
    voided?: boolean;
    /**
     * What `rateThb` means (ADR 0029). `"base"` = the contract price at the anchor fuel band, which a
     * `base_band_step` round steps up/down. Missing = `"final"` (every row written before ADR 0029):
     * the price already includes fuel and only a legacy multiplier/add round may touch it.
     */
    priceBasis?: RatePriceBasis;
}

export type JobCategory = "PRIMARY" | "SUPPLEMENTARY";

/** ADR 0029 — what a rate row's `rateThb` means. Missing on a row = `"final"`. */
export type RatePriceBasis = "base" | "final";

/** ADR 0029 — a fuel round that steps a base price per fuel band (CJSF contract). */
export type FuelPricingModel = "base_band_step";

/**
 * Contract terms of a band-step fuel surcharge (ADR 0029). CJSF zone 03: band 0 = 42.01–44.00,
 * ฿2.00 bands, ±0.70% per band compounded, each step rounded to ฿5 from the previous rounded price.
 */
export interface FuelBandStepParams {
    /** Lower integer of the band that carries the base price — `42` means `42.01–44.00`. */
    anchorBandLowerThb: number;
    /** Width of one band in THB (CJSF: 2). */
    bandWidthThb: number;
    /** Step per band in basis points — `70` = 0.70%. Integer so the math stays exact. */
    stepBasisPoints: number;
    /** Each step is rounded to a multiple of this many THB (CJSF: 5). */
    roundToThb: number;
}

/** The CJSF zone-03 terms (`เรทน้ำมันเขต 03 WRT.xlsx`) — the default offered by the customer form. */
export const DEFAULT_FUEL_BAND_STEP_PARAMS: FuelBandStepParams = {
    anchorBandLowerThb: 42,
    bandWidthThb: 2,
    stepBasisPoints: 70,
    roundToThb: 5,
};

export interface FuelRateAdjustment {
    id: string;
    customerId: string;
    effectiveFromMs: number;
    rateMultiplier: number;
    addThbPerTrip: number;
    /** Retail diesel price this round was announced against — the band is derived from it. */
    referenceFuelPriceThb?: number;
    /** Voided announcement — never selected for pricing (ADR 0009 §1). */
    voided?: boolean;
    /** Missing = legacy multiplier/add round. `"base_band_step"` = ADR 0029 band-step round. */
    pricingModel?: FuelPricingModel;
    /** Signed band index of this round, frozen at announcement time (`0` = base price band). */
    bandStep?: number;
    /** Contract terms copied onto the round when it was announced, so the round is self-describing. */
    bandStepParams?: FuelBandStepParams;
}

export interface TripBillingComputed {
    customerId: string;
    baseRateThb: number;
    finalRateThb: number;
    rateImportId: string;
    lookupHubId: string;
    lookupDestination: string;
    fuelAdjustmentId?: string;
    rateMultiplier: number;
    addThbPerTrip: number;
    effectiveFromDateStr?: string;
    /**
     * The [[Rate round]] that priced this trip: the effective date of the fuel announcement that
     * applied, or of the rate entry when no fuel adjustment did (ADR 0009 §4).
     */
    roundEffectiveFromDateStr: string;
    fuelBandLowerThb?: number;
    fuelBandUpperThb?: number;
    referenceFuelPriceThb?: number;
    /** ADR 0029 — set only when a band-step round priced this trip. */
    fuelPricingModel?: FuelPricingModel;
    fuelBandStep?: number;
    fuelStepBasisPoints?: number;
}

/** Round + band provenance denormalized onto a priced record (ADR 0009 §4, ADR 0029). */
export interface BillingRoundProvenance {
    roundEffectiveFromDateStr: string;
    fuelBandLowerThb?: number;
    fuelBandUpperThb?: number;
    referenceFuelPriceThb?: number;
    fuelPricingModel?: FuelPricingModel;
    fuelBandStep?: number;
    fuelStepBasisPoints?: number;
}

/**
 * Derive the round + band a record was priced under. Shared by the single- and multi-delivery
 * paths so both persist the identical field set.
 */
export function resolveBillingRoundProvenance(
    rateEntryEffectiveFromMs: number,
    adjustment: FuelRateAdjustment | null
): BillingRoundProvenance {
    // The round is the FUEL ANNOUNCEMENT that priced this trip, because that is what carries the
    // band the invoice legend prints. It is NOT max(rateEntry, adjustment): a rate card imported
    // after the announcements would then stamp its own date on every trip, collapsing a month's
    // rounds into one and hiding them completely. The rate entry's date is the round only when no
    // fuel adjustment applied at all.
    const roundMs = adjustment?.effectiveFromMs ?? rateEntryEffectiveFromMs;
    const price = adjustment?.referenceFuelPriceThb;
    // A band-step round (ADR 0029) prints its OWN ฿2.00 band, read from the step frozen on the round —
    // not re-derived from the price with the ฿1.00 rule of ADR 0009 §3, which would print the wrong band.
    if (isValidBandStepRound(adjustment)) {
        const { bandStep, bandStepParams } = adjustment;
        const band = stepBandRange(bandStep, bandStepParams.anchorBandLowerThb, bandStepParams.bandWidthThb);
        return {
            roundEffectiveFromDateStr: bangkokDateStrFromMillis(roundMs),
            fuelBandLowerThb: band?.lowerThb,
            fuelBandUpperThb: band?.upperThb,
            referenceFuelPriceThb: typeof price === "number" ? price : undefined,
            fuelPricingModel: "base_band_step",
            fuelBandStep: bandStep,
            fuelStepBasisPoints: bandStepParams.stepBasisPoints,
        };
    }
    const band = typeof price === "number" ? fuelBandRange(price) : null;
    return {
        roundEffectiveFromDateStr: bangkokDateStrFromMillis(roundMs),
        fuelBandLowerThb: band?.lowerThb,
        fuelBandUpperThb: band?.upperThb,
        referenceFuelPriceThb: typeof price === "number" ? price : undefined,
    };
}

export interface TripBillingTimestamps {
    deliveredTimestamp?: unknown;
    createdAt?: unknown;
    /**
     * Explicit billing-date override in epoch ms. When set (> 0) it wins over
     * deliveredTimestamp/createdAt. Used for customers billed by their planned work date
     * (`customers.billingDateBasis === "plan"`, ADR 0027) instead of the delivery instant (ADR 0008 §3).
     */
    billingDateMs?: number;
}

export interface TaskBillingInput {
    sourceHub?: string | null;
    destination?: string | null;
    truckType?: string | null;
    /** Explicit billing customer chosen at assign time — overrides the hub-derived link (ADR 0027). */
    billingCustomerId?: string | null;
    sourceHubLinkedCustomerId?: string | null;
    destinationLinkedCustomerId?: string | null;
}

function normalizeCode(v: string | null | undefined): string {
    return (v ?? "").trim().toUpperCase();
}

export function extractHubId(sourceHub: string | null | undefined): string {
    const raw = (sourceHub ?? "").trim();
    if (!raw) return "";
    const code = raw.split(" - ")[0]?.trim() ?? raw;
    return normalizeCode(code);
}

export function normalizeDestinationCode(destination: string | null | undefined): string {
    const u = normalizeCode(destination);
    if (!u) return "";
    if (u.startsWith("SOCE")) return "SOCE";
    if (u.startsWith("SOCN")) return "SOCN";
    if (u.startsWith("SOCW")) return "SOCW";
    // SPK codes: extract just the code part (e.g., "SPK890103 - ลาดกระบัง26" or "SPK890103-ลาดกระบัง26" → "SPK890103")
    const dashIdx = u.indexOf("-");
    if (dashIdx > 0) {
        return u.slice(0, dashIdx).trim();
    }
    return u;
}

/**
 * Folds every spelling a vehicle class was ever stored in onto the class a task carries today
 * (TASK_TRUCK_TYPE_ENUM: 4W, 4WJ, 6WH, 10WH, 18WH, VAN).
 *
 * Both sides of the rate lookup pass through here, so a rate entry only matches a trip when the two
 * land on the same code. Rate cards written from the old truck-master dropdown ("Pickup", "6 Wheels")
 * and from an earlier normalize pass ("6W", "10W") therefore matched nothing at all — the codes they
 * produced are not in the enum. They are mapped here rather than left to rot, since an unmatched
 * rate card shows up as "No rate" on a trip that does have a price agreed.
 *
 * PICKUP and 4WH are the pre-2026-07 names for 4W.
 */
export function normalizeVehicleClass(vehicleClass: string | null | undefined): string {
    const u = normalizeCode(vehicleClass || "4WJ");
    if (!u) return "4WJ";
    const mapping: Record<string, string> = {
        "PICKUP": "4W",
        "4WH": "4W",
        "4 WHEELS": "4WJ",
        "4 WHEELS JUMBO": "4WJ",
        "6 WHEELS": "6WH",
        "6W": "6WH",
        "10 WHEELS": "10WH",
        "10W": "10WH",
        "18 WHEELS": "18WH",
        "18W": "18WH",
        "2 WHEELS": "2W",
    };
    return mapping[u] ?? u;
}

// ─── Bangkok calendar dates ──────────────────────────────────────────────────

/** Thailand is a fixed UTC+07:00 with no DST, so a constant offset is exact. */
const BANGKOK_OFFSET_MS = 7 * 60 * 60 * 1000;

/**
 * `yyyy-MM-dd` of an instant **in Bangkok**.
 *
 * Not interchangeable with `toISOString().slice(0, 10)`: since ADR 0009 §2 an `effectiveFrom` is
 * Bangkok midnight, i.e. 17:00Z on the *previous* day, so the UTC form reports the wrong date.
 */
export function bangkokDateStrFromMillis(ms: number): string {
    return new Date(ms + BANGKOK_OFFSET_MS).toISOString().slice(0, 10);
}

/**
 * True when an announcement's effective DATE (Bangkok calendar) is on or before a trip's billing
 * DATE. Announcements are declared by *date* (ADR 0009 §2), so the boundary is the calendar day, not
 * the stored instant — a trip delivered any time on the effective day is in-round.
 *
 * Comparing dates (not instants) also absorbs rows written before 2026-08-09 (commit 299efc4), whose
 * `effectiveFrom` was stored at UTC midnight (07:00 ICT) instead of Bangkok midnight. That ≤7h skew
 * never crosses a day boundary — two distinct effective dates are ≥24h apart — so both storage
 * conventions land on the same Bangkok date, and overnight switch-day trips (00:00–06:59 ICT) price
 * under the correct round without a per-row datafix.
 */
export function isEffectiveOnOrBeforeBillingDate(effectiveFromMs: number, billDateMs: number): boolean {
    return bangkokDateStrFromMillis(effectiveFromMs) <= bangkokDateStrFromMillis(billDateMs);
}

// ─── Fuel bands (ADR 0009 §3) ────────────────────────────────────────────────

/**
 * Lower integer of the ฿1.00 fuel band containing `priceThb`.
 *
 * The contract band is **upper-inclusive** — `41.01–42.00` is one band, named `41`. `Math.floor`
 * classifies into `[n, n+1)` instead and so pushes a price of exactly `x.00` one band too high,
 * overcharging every trip of that round (Thai diesel sits on round numbers under price caps).
 * Working in integer satang keeps the boundary exact and free of binary-float drift.
 */
export function fuelBandFloor(priceThb: number): number {
    if (!Number.isFinite(priceThb)) return NaN;
    const satang = Math.round(priceThb * 100);
    return Math.ceil(satang / 100) - 1;
}

/** Inclusive bounds of the band containing `priceThb` — e.g. 42.00 → `{ 41.01, 42 }`. */
export function fuelBandRange(priceThb: number): { lowerThb: number; upperThb: number } | null {
    const floor = fuelBandFloor(priceThb);
    if (!Number.isFinite(floor)) return null;
    return {
        lowerThb: Math.round((floor + 0.01) * 100) / 100,
        upperThb: floor + 1,
    };
}

/**
 * Per-trip fuel surcharge in THB. `baselineBandFloor` names the band that carries +0, so `41`
 * means `41.01–42.00` → +0. Deliberately **signed**: diesel below the baseline is a real discount
 * and is never clamped to zero.
 */
export function computeFuelSurchargeThb(
    priceThb: number,
    baselineBandFloor: number,
    thbPerBaht: number
): number {
    const floor = fuelBandFloor(priceThb);
    if (!Number.isFinite(floor) || !Number.isFinite(baselineBandFloor) || !Number.isFinite(thbPerBaht)) {
        return NaN;
    }
    return Math.round((floor - baselineBandFloor) * thbPerBaht * 100) / 100;
}

// ─── Fuel band steps — base price × compounded % per band (ADR 0029) ─────────

/**
 * Signed index of the band containing `priceThb`, counted from the anchor band (index 0).
 *
 * Bands are **upper-inclusive**, like ADR 0009 §3: with anchor 42 and width 2, band 0 is
 * `42.01–44.00`, so 44.00 → 0, 44.01 → +1, 42.00 → −1. Integer satang keeps the edges exact.
 * Unbounded on purpose — a price outside the contract table extends the same rule (owner decision).
 */
export function stepBandIndex(priceThb: number, anchorBandLowerThb: number, bandWidthThb: number): number {
    if (!Number.isFinite(priceThb) || !Number.isFinite(anchorBandLowerThb) || !Number.isFinite(bandWidthThb)) {
        return NaN;
    }
    const satang = Math.round(priceThb * 100);
    const anchor = Math.round(anchorBandLowerThb * 100);
    const width = Math.round(bandWidthThb * 100);
    if (width <= 0) return NaN;
    return Math.ceil((satang - anchor) / width) - 1;
}

/** Inclusive bounds of band `step` — e.g. step +1, anchor 42, width 2 → `{ 44.01, 46 }`. */
export function stepBandRange(
    step: number,
    anchorBandLowerThb: number,
    bandWidthThb: number
): { lowerThb: number; upperThb: number } | null {
    if (!Number.isInteger(step) || !Number.isFinite(anchorBandLowerThb) || !Number.isFinite(bandWidthThb)) {
        return null;
    }
    const anchor = Math.round(anchorBandLowerThb * 100);
    const width = Math.round(bandWidthThb * 100);
    if (width <= 0) return null;
    return {
        lowerThb: (anchor + step * width + 1) / 100,
        upperThb: (anchor + (step + 1) * width) / 100,
    };
}

/**
 * Price of a base rate `step` bands away from the anchor band.
 *
 * Reproduces the contract sheet exactly: every step starts from the PREVIOUS step's ROUNDED price,
 * `ROUND(prev × (1 ± bp/10000) / roundTo, 0) × roundTo`, so it is not `base × (1+r)^n`. Step 0 is the
 * base price unchanged (e.g. 922.5 stays 922.5). Computed in integer satang with half-up rounding:
 * binary floats misround the exact ties Excel resolves upward (฿2,500 × 1.007 = 2,517.5 → 2,520,
 * where the naive float path gives 2,515).
 */
export function priceAtBandStep(
    baseThb: number,
    step: number,
    stepBasisPoints: number,
    roundToThb: number
): number {
    if (!Number.isFinite(baseThb) || baseThb < 0 || !Number.isInteger(step)) return NaN;
    if (!Number.isInteger(stepBasisPoints) || stepBasisPoints < 0 || stepBasisPoints >= 10000) return NaN;
    const unit = Math.round(roundToThb * 100);
    if (!Number.isFinite(unit) || unit <= 0) return NaN;
    const factor = step >= 0 ? 10000 + stepBasisPoints : 10000 - stepBasisPoints;
    const denominator = 10000 * unit;
    let satang = Math.round(baseThb * 100);
    for (let i = 0; i < Math.abs(step); i += 1) {
        // round-half-up(satang × factor / denominator) × unit, all integers.
        satang = Math.floor((2 * satang * factor + denominator) / (2 * denominator)) * unit;
    }
    return satang / 100;
}

/** Read contract terms from a stored map; `null` when anything is missing or out of range. */
export function parseBandStepParams(raw: unknown): FuelBandStepParams | null {
    if (!raw || typeof raw !== "object") return null;
    const r = raw as Record<string, unknown>;
    const anchorBandLowerThb = Number(r.anchorBandLowerThb);
    const bandWidthThb = Number(r.bandWidthThb);
    const stepBasisPoints = Number(r.stepBasisPoints);
    const roundToThb = Number(r.roundToThb);
    if (!Number.isFinite(anchorBandLowerThb)) return null;
    if (!Number.isFinite(bandWidthThb) || bandWidthThb <= 0) return null;
    if (!Number.isInteger(stepBasisPoints) || stepBasisPoints <= 0 || stepBasisPoints >= 10000) return null;
    if (!Number.isFinite(roundToThb) || roundToThb <= 0) return null;
    return { anchorBandLowerThb, bandWidthThb, stepBasisPoints, roundToThb };
}

/** A stored `priceBasis` value; anything but `"base"` is the legacy `"final"`. */
export function parsePriceBasis(raw: unknown): RatePriceBasis {
    return raw === "base" ? "base" : "final";
}

/** `"base_band_step"` when stored so, else `undefined` (a legacy multiplier/add round). */
export function parseFuelPricingModel(raw: unknown): FuelPricingModel | undefined {
    return raw === "base_band_step" ? "base_band_step" : undefined;
}

export function isBandStepRound(adjustment: FuelRateAdjustment | null | undefined): boolean {
    return adjustment?.pricingModel === "base_band_step";
}

function isValidBandStepRound(
    adjustment: FuelRateAdjustment | null | undefined
): adjustment is FuelRateAdjustment & { bandStep: number; bandStepParams: FuelBandStepParams } {
    return (
        isBandStepRound(adjustment) &&
        Number.isInteger(adjustment?.bandStep) &&
        adjustment?.bandStepParams != null
    );
}

/** Why a rate row and the round that applies to it cannot be combined (ADR 0029 guard). */
export type RoundPricingMismatch =
    /** A band-step round would compound a % on top of a price that already includes fuel. */
    | "final_rate_with_band_step_round"
    /** A ±฿/trip or × round would shift a base price as if it were the agreed final price. */
    | "base_rate_with_legacy_round"
    /** The round says band-step but carries no step / terms — never guess. */
    | "invalid_band_step_round";

export type RoundPricingResult =
    | { ok: true; finalRateThb: number; rateMultiplier: number; addThbPerTrip: number }
    | { ok: false; reason: RoundPricingMismatch };

/**
 * The one place a rate row and a fuel round become a price.
 *
 * - legacy round (or none) on a `final` row → `rateThb × multiplier + add` (unchanged behaviour)
 * - band-step round on a `base` row → `priceAtBandStep`; reported as multiplier 1 and
 *   add = final − base so `base × mult + add = final` holds on every stored snapshot
 * - no round on a `base` row → the base price (the anchor band)
 * - any other pairing is refused rather than priced: it is the signature of a route that was never
 *   re-imported as a base price, or a round announced in the wrong mode (fail loud, ADR 0029).
 */
export function priceRateEntryForRound(
    entry: Pick<BillingRateEntry, "rateThb" | "priceBasis">,
    adjustment: FuelRateAdjustment | null
): RoundPricingResult {
    const isBase = entry.priceBasis === "base";
    if (isBandStepRound(adjustment)) {
        if (!isBase) return { ok: false, reason: "final_rate_with_band_step_round" };
        if (!isValidBandStepRound(adjustment)) return { ok: false, reason: "invalid_band_step_round" };
        const { bandStep, bandStepParams } = adjustment;
        const finalRateThb = priceAtBandStep(
            entry.rateThb,
            bandStep,
            bandStepParams.stepBasisPoints,
            bandStepParams.roundToThb
        );
        if (!Number.isFinite(finalRateThb)) return { ok: false, reason: "invalid_band_step_round" };
        return {
            ok: true,
            finalRateThb,
            rateMultiplier: 1,
            addThbPerTrip: Math.round((finalRateThb - entry.rateThb) * 100) / 100,
        };
    }
    if (adjustment && isBase) return { ok: false, reason: "base_rate_with_legacy_round" };
    const rateMultiplier = adjustment?.rateMultiplier ?? 1;
    const addThbPerTrip = adjustment?.addThbPerTrip ?? 0;
    return {
        ok: true,
        finalRateThb: computeFinalRateThb(entry.rateThb, rateMultiplier, addThbPerTrip),
        rateMultiplier,
        addThbPerTrip,
    };
}

/** Milliseconds from Firestore Timestamp, Date, number, or similar. */
export function timestampLikeToMillis(val: unknown): number {
    if (!val) return 0;
    if (typeof val === "number" && Number.isFinite(val)) return val;
    if (val instanceof Date) return val.getTime();
    if (typeof (val as { toMillis?: () => number }).toMillis === "function") {
        return (val as { toMillis: () => number }).toMillis();
    }
    if (typeof (val as { toDate?: () => Date }).toDate === "function") {
        return (val as { toDate: () => Date }).toDate().getTime();
    }
    return 0;
}

export function getTripBillingDateMs(trip: TripBillingTimestamps): number {
    // An explicit plan-date override (ADR 0027) wins over the delivery instant (ADR 0008 §3).
    if (typeof trip.billingDateMs === "number" && Number.isFinite(trip.billingDateMs) && trip.billingDateMs > 0) {
        return trip.billingDateMs;
    }
    const delivered = timestampLikeToMillis(trip.deliveredTimestamp);
    if (delivered > 0) return delivered;
    const created = timestampLikeToMillis(trip.createdAt);
    if (created > 0) return created;
    return Date.now();
}

export function resolveTaskCustomerId(task: TaskBillingInput | null | undefined): string {
    return (
        // Explicit choice at assign time wins over the hub-derived link (ADR 0027).
        task?.billingCustomerId?.trim() ||
        task?.sourceHubLinkedCustomerId?.trim() ||
        task?.destinationLinkedCustomerId?.trim() ||
        ""
    );
}

export function selectBillingRateEntry(
    customerId: string,
    hubId: string,
    destinationCode: string,
    vehicleClass: string,
    billDateMs: number,
    rateEntries: BillingRateEntry[],
    jobCategory: JobCategory = "PRIMARY"
): BillingRateEntry | null {
    const normalizedVehicleClass = normalizeVehicleClass(vehicleClass);
    const candidates = rateEntries.filter(
        (entry) =>
            entry.voided !== true &&
            entry.customerId === customerId &&
            entry.hubId === hubId &&
            entry.destinationCode === destinationCode &&
            normalizeVehicleClass(entry.vehicleClass) === normalizedVehicleClass &&
            (entry.jobCategory ?? "PRIMARY") === jobCategory
    );
    if (candidates.length === 0) return null;

    // Primary: effective on or before trip date, by Bangkok calendar day (newest first)
    const effective = candidates
        .filter((e) => isEffectiveOnOrBeforeBillingDate(e.effectiveFromMs, billDateMs))
        .sort((a, b) => b.effectiveFromMs - a.effectiveFromMs);
    if (effective.length > 0) return effective[0];

    // Fallback: trip is before all effective dates → use oldest rate card
    const oldest = candidates.sort((a, b) => a.effectiveFromMs - b.effectiveFromMs);
    return oldest[0];
}

export function selectFuelAdjustmentForBillingDate(
    customerId: string,
    billDateMs: number,
    fuelAdjustments: FuelRateAdjustment[]
): FuelRateAdjustment | null {
    const matched = fuelAdjustments
        .filter(
            (adj) =>
                adj.voided !== true &&
                adj.customerId === customerId &&
                isEffectiveOnOrBeforeBillingDate(adj.effectiveFromMs, billDateMs)
        )
        .sort((a, b) => b.effectiveFromMs - a.effectiveFromMs);
    return matched[0] ?? null;
}

/** Final trip rate in THB, rounded to 2 decimal places (same as legacy billing snapshot). */
export function computeFinalRateThb(baseRateThb: number, rateMultiplier: number, addThbPerTrip: number): number {
    return Math.round((baseRateThb * rateMultiplier + addThbPerTrip) * 100) / 100;
}

/** The fuel provenance fields a stored billing snapshot carries (trip_records). */
export interface BillingSnapshotFuelFields {
    billingFuelAdjustmentId?: unknown;
    billingRateMultiplier?: unknown;
    billingAddThbPerTrip?: unknown;
}

/**
 * True when a stored snapshot was priced WITH a fuel adjustment — an adjustment id, a multiplier
 * other than 1, or a non-zero per-trip add. A เสริม snapshot must never carry one (ADR-0005: เสริม
 * is a fixed price fuel never moves), so on a เสริม row this is the signature of a corrupted price.
 */
export function snapshotCarriesFuel(s: BillingSnapshotFuelFields): boolean {
    const adjId = typeof s.billingFuelAdjustmentId === "string" && s.billingFuelAdjustmentId.trim() !== "";
    const mult = s.billingRateMultiplier == null ? 1 : Number(s.billingRateMultiplier);
    const add = s.billingAddThbPerTrip == null ? 0 : Number(s.billingAddThbPerTrip);
    return adjId || (Number.isFinite(mult) && mult !== 1) || (Number.isFinite(add) && add !== 0);
}

/**
 * Frozen price (ADR-0005 / ADR-0002): a snapshot a forced recompute must leave alone.
 *
 * - An explicit manual override is always frozen.
 * - A เสริม label is frozen only while its price is a clean fixed price. A เสริม-labelled row that
 *   carries fuel was never an agreed เสริม price — it was written by a path that ignored หลัก/เสริม
 *   (the old Driver Monitor client writer) — so freezing it would lock the corruption in forever.
 *   The server always writes `billingManualOverride: true` on a priced เสริม trip, so every
 *   correctly-priced เสริม row is still caught by the first rule.
 */
export function isFrozenBillingSnapshot(
    s: BillingSnapshotFuelFields & { billingManualOverride?: unknown; jobCategory?: unknown }
): boolean {
    if (s.billingManualOverride === true) return true;
    return s.jobCategory === "SUPPLEMENTARY" && !snapshotCarriesFuel(s);
}

export interface DeliveryStopForBilling {
    index: number;
    destination: string;
}

export interface MultiDeliveryStopBilling {
    stopIndex: number;
    destination: string;
    baseRateThb: number;
    finalRateThb: number;
}

export interface MultiDeliveryBillingResult {
    customerId: string;
    baseRateThb: number; // Rate of stop 1
    stopChargeThb: number; // Sum of rates for stop 3+
    totalBillingThb: number; // baseRateThb + stopChargeThb
    stopBreakdown: MultiDeliveryStopBilling[];
    rateMultiplier: number;
    fuelAdjustmentId?: string;
    // Parity with the single-delivery snapshot (ADR 0009 §4) — this path used to omit all of
    // these, leaving multidrop rows with no provenance to print on an invoice.
    addThbPerTrip: number;
    rateImportId: string;
    effectiveFromDateStr?: string;
    roundEffectiveFromDateStr: string;
    fuelBandLowerThb?: number;
    fuelBandUpperThb?: number;
    referenceFuelPriceThb?: number;
    /** ADR 0029 — set only when a band-step round priced the base leg. */
    fuelPricingModel?: FuelPricingModel;
    fuelBandStep?: number;
    fuelStepBasisPoints?: number;
}

/**
 * Compute billing for multi-delivery task.
 * Rule: stop[0] = planned destination (base rate from rate card), stop[1+] = extra stops.
 * Origin (source hub) is never in deliveryStops — it is already accounted for in the base rate.
 *
 * Extra stop charging:
 *   - If `extraStopFeeThb` is provided (>= 0): each extra stop is charged this flat service fee
 *     (from customer_service_fees, feeType "extra_stop"). Fuel multiplier is NOT applied to it.
 *   - If `extraStopFeeThb` is undefined: legacy behaviour — each extra stop is looked up in the
 *     rate card per route (with fuel multiplier applied).
 */
export function computeMultiDeliveryBilling(
    trip: TripBillingTimestamps,
    task: TaskBillingInput | null | undefined,
    deliveryStops: DeliveryStopForBilling[],
    vehicleClass: string,
    rateEntries: BillingRateEntry[],
    fuelAdjustments: FuelRateAdjustment[],
    extraStopFeeThb?: number,
    jobCategory: JobCategory = "PRIMARY"
): MultiDeliveryBillingResult | null {
    if (!task || deliveryStops.length < 2) return null;
    const customerId = resolveTaskCustomerId(task);
    if (!customerId) return null;

    const hubId = extractHubId(task.sourceHub);
    const normalizedVehicleClass = normalizeVehicleClass(vehicleClass);
    const billDateMs = getTripBillingDateMs(trip);
    // Supplementary (เสริม) rate cards are fixed, separately-agreed prices (ADR-0005) —
    // they never move with primary fuel-rate adjustments.
    const matchedAdjustment =
        jobCategory === "SUPPLEMENTARY"
            ? null
            : selectFuelAdjustmentForBillingDate(customerId, billDateMs, fuelAdjustments);
    // Trip-level multiplier/add are those of the BASE leg. For a band-step round (ADR 0029) the add is
    // per route (final − base), so each stop's own amount lives in stopBreakdown.
    let rateMultiplier = matchedAdjustment?.rateMultiplier ?? 1;
    let addThbPerTrip = matchedAdjustment?.addThbPerTrip ?? 0;
    const useFlatExtraStopFee = typeof extraStopFeeThb === "number" && extraStopFeeThb >= 0;

    let baseRateThb = 0;
    let stopChargeThb = 0;
    const stopBreakdown: MultiDeliveryStopBilling[] = [];
    // The rate entry that produced the base leg — carries the round this trip was priced under.
    let baseRateEntry: BillingRateEntry | null = null;
    // A rate row that cannot be combined with the round (ADR 0029) fails the whole trip, loudly —
    // billing the other stops and silently dropping one would under-charge.
    let pricingMismatch = false;

    if (useFlatExtraStopFee) {
        // Flat extra-stop fee mode: the trip's BASE rate comes from the PLANNED
        // destination (task.destination), independent of the order the stops were
        // delivered in. Every OTHER delivered stop is an extra stop charged the flat
        // fee (no rate-card lookup, no fuel multiplier).
        //
        // This is order-independent on purpose: deliveryStopsProgress[0] is whichever
        // stop the driver completed first, which is NOT necessarily the planned route.
        const normStops = deliveryStops.map((s) => normalizeDestinationCode(s.destination));
        const plannedDest = normalizeDestinationCode(task.destination ?? "");

        // Pick the base stop: prefer the delivered stop matching the planned
        // destination; otherwise fall back to the first delivered stop that has a
        // matching rate-card row.
        let baseIdx = plannedDest ? normStops.findIndex((d) => d === plannedDest) : -1;
        let baseDest = baseIdx >= 0 ? normStops[baseIdx] : plannedDest;
        let baseMatch = baseDest
            ? selectBillingRateEntry(customerId, hubId, baseDest, normalizedVehicleClass, billDateMs, rateEntries, jobCategory)
            : null;
        if (!baseMatch) {
            for (let i = 0; i < normStops.length; i++) {
                const m = selectBillingRateEntry(customerId, hubId, normStops[i], normalizedVehicleClass, billDateMs, rateEntries, jobCategory);
                if (m) { baseMatch = m; baseIdx = i; baseDest = normStops[i]; break; }
            }
        }
        if (!baseMatch) return null; // no rate card for any stop → cannot bill
        baseRateEntry = baseMatch;

        const basePriced = priceRateEntryForRound(baseMatch, matchedAdjustment);
        if (!basePriced.ok) return null;
        const baseFinal = basePriced.finalRateThb;
        rateMultiplier = basePriced.rateMultiplier;
        addThbPerTrip = basePriced.addThbPerTrip;
        baseRateThb = baseFinal;
        stopBreakdown.push({ stopIndex: 1, destination: baseDest, baseRateThb: baseMatch.rateThb, finalRateThb: baseFinal });

        // Exclude exactly one delivered stop (the base) from the extra-stop charges.
        const excludeIdx = baseIdx >= 0 ? baseIdx : 0;
        let extraSeq = 2;
        deliveryStops.forEach((_stop, idx) => {
            if (idx === excludeIdx) return;
            stopChargeThb += extraStopFeeThb as number;
            stopBreakdown.push({
                stopIndex: extraSeq++,
                destination: normStops[idx],
                baseRateThb: extraStopFeeThb as number,
                finalRateThb: extraStopFeeThb as number,
            });
        });
    } else {
        // Legacy per-route mode: stop[0] = base rate, stop[1+] = per-route rate-card lookup.
        deliveryStops.forEach((stop, idx) => {
            const destination = normalizeDestinationCode(stop.destination);
            const matchedRate = selectBillingRateEntry(
                customerId,
                hubId,
                destination,
                normalizedVehicleClass,
                billDateMs,
                rateEntries,
                jobCategory
            );
            if (!matchedRate) return;

            const baseRate = matchedRate.rateThb;
            const priced = priceRateEntryForRound(matchedRate, matchedAdjustment);
            if (!priced.ok) {
                pricingMismatch = true;
                return;
            }
            const finalRate = priced.finalRateThb;
            if (idx === 0) {
                baseRateThb = finalRate;
                baseRateEntry = matchedRate;
                rateMultiplier = priced.rateMultiplier;
                addThbPerTrip = priced.addThbPerTrip;
            } else {
                stopChargeThb += finalRate;
            }
            stopBreakdown.push({
                stopIndex: idx + 1,
                destination,
                baseRateThb: baseRate,
                finalRateThb: finalRate,
            });
        });
    }

    if (pricingMismatch || baseRateThb === 0 || stopBreakdown.length === 0 || !baseRateEntry) return null;

    return {
        customerId,
        baseRateThb,
        stopChargeThb,
        totalBillingThb: baseRateThb + stopChargeThb,
        stopBreakdown,
        rateMultiplier,
        fuelAdjustmentId: matchedAdjustment?.id,
        addThbPerTrip,
        rateImportId: baseRateEntry.importId,
        effectiveFromDateStr: matchedAdjustment
            ? bangkokDateStrFromMillis(matchedAdjustment.effectiveFromMs)
            : undefined,
        ...resolveBillingRoundProvenance(baseRateEntry.effectiveFromMs, matchedAdjustment),
    };
}

// ─── Standby Billing ─────────────────────────────────────────────────────────

export interface StandbyRateEntry {
    id: string;
    customerId: string;
    /** Fixed THB charged per standby event (regardless of duration). */
    rateThb: number;
    effectiveFromMs: number;
    note?: string;
}

export interface StandbyBillingComputed {
    customerId: string;
    rateThb: number;
    rateEntryId: string;
    effectiveFromDateStr?: string;
}

/** Select the standby rate effective on or before billDateMs; fallback to oldest if none match. */
export function selectStandbyRateEntry(
    customerId: string,
    billDateMs: number,
    rateEntries: StandbyRateEntry[]
): StandbyRateEntry | null {
    const candidates = rateEntries.filter((e) => e.customerId === customerId);
    if (candidates.length === 0) return null;

    const effective = candidates
        .filter((e) => isEffectiveOnOrBeforeBillingDate(e.effectiveFromMs, billDateMs))
        .sort((a, b) => b.effectiveFromMs - a.effectiveFromMs);
    if (effective.length > 0) return effective[0];

    // Fallback: use oldest rate if standby happened before all effective dates
    return candidates.sort((a, b) => a.effectiveFromMs - b.effectiveFromMs)[0];
}

/**
 * Compute standby billing for a completed standby record.
 * Fixed rate per event — does not depend on duration.
 */
export function computeStandbyBilling(
    billDateMs: number,
    customerId: string,
    rateEntries: StandbyRateEntry[]
): StandbyBillingComputed | null {
    if (!customerId) return null;
    const matched = selectStandbyRateEntry(customerId, billDateMs, rateEntries);
    if (!matched) return null;
    return {
        customerId,
        rateThb: matched.rateThb,
        rateEntryId: matched.id,
        effectiveFromDateStr: bangkokDateStrFromMillis(matched.effectiveFromMs),
    };
}

export function computeTripBillingFromParts(
    trip: TripBillingTimestamps,
    task: TaskBillingInput | null | undefined,
    rateEntries: BillingRateEntry[],
    fuelAdjustments: FuelRateAdjustment[],
    jobCategory: JobCategory = "PRIMARY"
): TripBillingComputed | null {
    if (!task) return null;
    const customerId = resolveTaskCustomerId(task);
    if (!customerId) return null;

    const hubId = extractHubId(task.sourceHub);
    const destination = normalizeDestinationCode(task.destination);
    const vehicleClass = normalizeVehicleClass(task.truckType);
    const billDateMs = getTripBillingDateMs(trip);

    const matchedRate = selectBillingRateEntry(
        customerId,
        hubId,
        destination,
        vehicleClass,
        billDateMs,
        rateEntries,
        jobCategory
    );
    if (!matchedRate) return null;

    // Supplementary (เสริม) rate cards are fixed, separately-agreed prices (ADR-0005) —
    // they never move with primary fuel-rate adjustments.
    const matchedAdjustment =
        jobCategory === "SUPPLEMENTARY"
            ? null
            : selectFuelAdjustmentForBillingDate(customerId, billDateMs, fuelAdjustments);

    // ADR 0029: a base-price row only combines with a band-step round, a final-price row only with a
    // legacy one. A refused pairing is "no price", so the caller fails loud instead of mis-billing.
    const priced = priceRateEntryForRound(matchedRate, matchedAdjustment);
    if (!priced.ok) return null;
    const multiplier = priced.rateMultiplier;
    const addThbPerTrip = priced.addThbPerTrip;
    const finalRate = priced.finalRateThb;

    return {
        customerId,
        baseRateThb: matchedRate.rateThb,
        finalRateThb: finalRate,
        rateImportId: matchedRate.importId,
        lookupHubId: hubId,
        lookupDestination: destination,
        fuelAdjustmentId: matchedAdjustment?.id,
        rateMultiplier: multiplier,
        addThbPerTrip,
        effectiveFromDateStr: matchedAdjustment
            ? bangkokDateStrFromMillis(matchedAdjustment.effectiveFromMs)
            : undefined,
        ...resolveBillingRoundProvenance(matchedRate.effectiveFromMs, matchedAdjustment),
    };
}

/**
 * Why a trip whose route DOES have a rate row still got no price: the row and the round that applies
 * cannot be combined (ADR 0029). Used only to make the error say what to fix — "import the base price
 * for this route" — instead of a bare "No rate". `null` = not a basis/round mismatch (e.g. genuinely
 * no rate row). For a multi-drop trip this checks the planned destination, i.e. the base leg.
 */
export function diagnoseRoundPricingMismatch(
    trip: TripBillingTimestamps,
    task: TaskBillingInput | null | undefined,
    rateEntries: BillingRateEntry[],
    fuelAdjustments: FuelRateAdjustment[],
    jobCategory: JobCategory = "PRIMARY"
): RoundPricingMismatch | null {
    if (!task) return null;
    const customerId = resolveTaskCustomerId(task);
    if (!customerId) return null;
    const billDateMs = getTripBillingDateMs(trip);
    const matchedRate = selectBillingRateEntry(
        customerId,
        extractHubId(task.sourceHub),
        normalizeDestinationCode(task.destination),
        normalizeVehicleClass(task.truckType),
        billDateMs,
        rateEntries,
        jobCategory
    );
    if (!matchedRate) return null;
    const adjustment =
        jobCategory === "SUPPLEMENTARY"
            ? null
            : selectFuelAdjustmentForBillingDate(customerId, billDateMs, fuelAdjustments);
    const priced = priceRateEntryForRound(matchedRate, adjustment);
    return priced.ok ? null : priced.reason;
}
