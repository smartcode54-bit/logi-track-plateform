/**
 * Pure per-trip billing math (no Firebase). Used by web admin and Cloud Functions.
 * Keep `functions/src/core/billingCompute.ts` in sync when changing this file.
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
}

export type JobCategory = "PRIMARY" | "SUPPLEMENTARY";

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
}

/** Round + band provenance denormalized onto a priced record (ADR 0009 §4). */
export interface BillingRoundProvenance {
    roundEffectiveFromDateStr: string;
    fuelBandLowerThb?: number;
    fuelBandUpperThb?: number;
    referenceFuelPriceThb?: number;
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
}

export interface TaskBillingInput {
    sourceHub?: string | null;
    destination?: string | null;
    truckType?: string | null;
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
    const delivered = timestampLikeToMillis(trip.deliveredTimestamp);
    if (delivered > 0) return delivered;
    const created = timestampLikeToMillis(trip.createdAt);
    if (created > 0) return created;
    return Date.now();
}

export function resolveTaskCustomerId(task: TaskBillingInput | null | undefined): string {
    return (
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
    const rateMultiplier = matchedAdjustment?.rateMultiplier ?? 1;
    const addThbPerTrip = matchedAdjustment?.addThbPerTrip ?? 0;
    const useFlatExtraStopFee = typeof extraStopFeeThb === "number" && extraStopFeeThb >= 0;

    let baseRateThb = 0;
    let stopChargeThb = 0;
    const stopBreakdown: MultiDeliveryStopBilling[] = [];
    // The rate entry that produced the base leg — carries the round this trip was priced under.
    let baseRateEntry: BillingRateEntry | null = null;

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

        const baseFinal = computeFinalRateThb(baseMatch.rateThb, rateMultiplier, addThbPerTrip);
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
            const finalRate = computeFinalRateThb(baseRate, rateMultiplier, addThbPerTrip);
            if (idx === 0) {
                baseRateThb = finalRate;
                baseRateEntry = matchedRate;
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

    if (baseRateThb === 0 || stopBreakdown.length === 0 || !baseRateEntry) return null;

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

    const multiplier = matchedAdjustment?.rateMultiplier ?? 1;
    const addThbPerTrip = matchedAdjustment?.addThbPerTrip ?? 0;
    const finalRate = computeFinalRateThb(matchedRate.rateThb, multiplier, addThbPerTrip);

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
