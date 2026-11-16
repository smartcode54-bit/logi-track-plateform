"use strict";
/**
 * Pure per-trip billing math (no Firebase). Used by web admin and Cloud Functions.
 * Duplicated from `logitrack-web/lib/billingCompute.ts` — keep in sync when changing either file.
 */
Object.defineProperty(exports, "__esModule", { value: true });
exports.DEFAULT_FUEL_BAND_STEP_PARAMS = void 0;
exports.resolveBillingRoundProvenance = resolveBillingRoundProvenance;
exports.extractHubId = extractHubId;
exports.normalizeDestinationCode = normalizeDestinationCode;
exports.normalizeVehicleClass = normalizeVehicleClass;
exports.bangkokDateStrFromMillis = bangkokDateStrFromMillis;
exports.isEffectiveOnOrBeforeBillingDate = isEffectiveOnOrBeforeBillingDate;
exports.announcementRecordedMs = announcementRecordedMs;
exports.compareAnnouncementsNewestFirst = compareAnnouncementsNewestFirst;
exports.fuelBandFloor = fuelBandFloor;
exports.fuelBandRange = fuelBandRange;
exports.computeFuelSurchargeThb = computeFuelSurchargeThb;
exports.stepBandIndex = stepBandIndex;
exports.stepBandRange = stepBandRange;
exports.priceAtBandStep = priceAtBandStep;
exports.parseBandStepParams = parseBandStepParams;
exports.parsePriceBasis = parsePriceBasis;
exports.parseFuelPricingModel = parseFuelPricingModel;
exports.isBandStepRound = isBandStepRound;
exports.priceRateEntryForRound = priceRateEntryForRound;
exports.timestampLikeToMillis = timestampLikeToMillis;
exports.getTripBillingDateMs = getTripBillingDateMs;
exports.resolveTaskCustomerId = resolveTaskCustomerId;
exports.selectBillingRateEntry = selectBillingRateEntry;
exports.selectFuelAdjustmentForBillingDate = selectFuelAdjustmentForBillingDate;
exports.computeFinalRateThb = computeFinalRateThb;
exports.snapshotCarriesFuel = snapshotCarriesFuel;
exports.isFrozenBillingSnapshot = isFrozenBillingSnapshot;
exports.computeMultiDeliveryBilling = computeMultiDeliveryBilling;
exports.selectStandbyRateEntry = selectStandbyRateEntry;
exports.computeStandbyBilling = computeStandbyBilling;
exports.computeTripBillingFromParts = computeTripBillingFromParts;
exports.diagnoseRoundPricingMismatch = diagnoseRoundPricingMismatch;
/** The CJSF zone-03 terms (`เรทน้ำมันเขต 03 WRT.xlsx`) — the default offered by the customer form. */
exports.DEFAULT_FUEL_BAND_STEP_PARAMS = {
    anchorBandLowerThb: 42,
    bandWidthThb: 2,
    stepBasisPoints: 70,
    roundToThb: 5,
};
/**
 * Derive the round + band a record was priced under. Shared by the single- and multi-delivery
 * paths so both persist the identical field set.
 */
function resolveBillingRoundProvenance(rateEntryEffectiveFromMs, adjustment) {
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
function normalizeCode(v) {
    return (v ?? "").trim().toUpperCase();
}
function extractHubId(sourceHub) {
    const raw = (sourceHub ?? "").trim();
    if (!raw)
        return "";
    const code = raw.split(" - ")[0]?.trim() ?? raw;
    return normalizeCode(code);
}
function normalizeDestinationCode(destination) {
    const u = normalizeCode(destination);
    if (!u)
        return "";
    if (u.startsWith("SOCE"))
        return "SOCE";
    if (u.startsWith("SOCN"))
        return "SOCN";
    if (u.startsWith("SOCW"))
        return "SOCW";
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
function normalizeVehicleClass(vehicleClass) {
    const u = normalizeCode(vehicleClass || "4WJ");
    if (!u)
        return "4WJ";
    const mapping = {
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
function bangkokDateStrFromMillis(ms) {
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
function isEffectiveOnOrBeforeBillingDate(effectiveFromMs, billDateMs) {
    return bangkokDateStrFromMillis(effectiveFromMs) <= bangkokDateStrFromMillis(billDateMs);
}
/** Write time of an announcement row: its stored timestamp, else the ms suffix of its import id, else 0. */
function announcementRecordedMs(row) {
    if (typeof row.recordedAtMs === "number" && Number.isFinite(row.recordedAtMs) && row.recordedAtMs > 0) {
        return row.recordedAtMs;
    }
    const suffix = /_(\d{12,})$/.exec(row.importId ?? "");
    return suffix ? Number(suffix[1]) : 0;
}
/**
 * Newest-first order of announcement rows (ADR 0009 §9). Every reader that picks "the row in effect"
 * sorts with this, so the server, the web preview and the Rate Card list always agree.
 *
 * 1. Later Bangkok effective DAY first — the same granularity as `isEffectiveOnOrBeforeBillingDate`.
 *    Comparing the raw instant let a row stored before 2026-08-09 (07:00 ICT) outrank a correction
 *    re-imported for the same day (00:00 ICT) every time.
 * 2. Same day: the row written LAST wins. Re-importing a day is how an admin corrects it.
 * 3. id — only so a remaining tie breaks the same way everywhere (it used to be Firestore doc order,
 *    i.e. random).
 */
function compareAnnouncementsNewestFirst(a, b) {
    const dayA = bangkokDateStrFromMillis(a.effectiveFromMs);
    const dayB = bangkokDateStrFromMillis(b.effectiveFromMs);
    if (dayA !== dayB)
        return dayA < dayB ? 1 : -1;
    const recordedDiff = announcementRecordedMs(b) - announcementRecordedMs(a);
    if (recordedDiff !== 0)
        return recordedDiff;
    return a.id < b.id ? 1 : a.id > b.id ? -1 : 0;
}
/** Row for a record dated before every row: the earliest effective day, the last written that day. */
function earliestAnnouncement(rows) {
    if (rows.length === 0)
        return null;
    const earliestDay = rows
        .map((r) => bangkokDateStrFromMillis(r.effectiveFromMs))
        .reduce((min, day) => (day < min ? day : min));
    return rows
        .filter((r) => bangkokDateStrFromMillis(r.effectiveFromMs) === earliestDay)
        .sort(compareAnnouncementsNewestFirst)[0];
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
function fuelBandFloor(priceThb) {
    if (!Number.isFinite(priceThb))
        return NaN;
    const satang = Math.round(priceThb * 100);
    return Math.ceil(satang / 100) - 1;
}
/** Inclusive bounds of the band containing `priceThb` — e.g. 42.00 → `{ 41.01, 42 }`. */
function fuelBandRange(priceThb) {
    const floor = fuelBandFloor(priceThb);
    if (!Number.isFinite(floor))
        return null;
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
function computeFuelSurchargeThb(priceThb, baselineBandFloor, thbPerBaht) {
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
function stepBandIndex(priceThb, anchorBandLowerThb, bandWidthThb) {
    if (!Number.isFinite(priceThb) || !Number.isFinite(anchorBandLowerThb) || !Number.isFinite(bandWidthThb)) {
        return NaN;
    }
    const satang = Math.round(priceThb * 100);
    const anchor = Math.round(anchorBandLowerThb * 100);
    const width = Math.round(bandWidthThb * 100);
    if (width <= 0)
        return NaN;
    return Math.ceil((satang - anchor) / width) - 1;
}
/** Inclusive bounds of band `step` — e.g. step +1, anchor 42, width 2 → `{ 44.01, 46 }`. */
function stepBandRange(step, anchorBandLowerThb, bandWidthThb) {
    if (!Number.isInteger(step) || !Number.isFinite(anchorBandLowerThb) || !Number.isFinite(bandWidthThb)) {
        return null;
    }
    const anchor = Math.round(anchorBandLowerThb * 100);
    const width = Math.round(bandWidthThb * 100);
    if (width <= 0)
        return null;
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
function priceAtBandStep(baseThb, step, stepBasisPoints, roundToThb) {
    if (!Number.isFinite(baseThb) || baseThb < 0 || !Number.isInteger(step))
        return NaN;
    if (!Number.isInteger(stepBasisPoints) || stepBasisPoints < 0 || stepBasisPoints >= 10000)
        return NaN;
    const unit = Math.round(roundToThb * 100);
    if (!Number.isFinite(unit) || unit <= 0)
        return NaN;
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
function parseBandStepParams(raw) {
    if (!raw || typeof raw !== "object")
        return null;
    const r = raw;
    const anchorBandLowerThb = Number(r.anchorBandLowerThb);
    const bandWidthThb = Number(r.bandWidthThb);
    const stepBasisPoints = Number(r.stepBasisPoints);
    const roundToThb = Number(r.roundToThb);
    if (!Number.isFinite(anchorBandLowerThb))
        return null;
    if (!Number.isFinite(bandWidthThb) || bandWidthThb <= 0)
        return null;
    if (!Number.isInteger(stepBasisPoints) || stepBasisPoints <= 0 || stepBasisPoints >= 10000)
        return null;
    if (!Number.isFinite(roundToThb) || roundToThb <= 0)
        return null;
    return { anchorBandLowerThb, bandWidthThb, stepBasisPoints, roundToThb };
}
/** A stored `priceBasis` value; anything but `"base"` is the legacy `"final"`. */
function parsePriceBasis(raw) {
    return raw === "base" ? "base" : "final";
}
/** `"base_band_step"` when stored so, else `undefined` (a legacy multiplier/add round). */
function parseFuelPricingModel(raw) {
    return raw === "base_band_step" ? "base_band_step" : undefined;
}
function isBandStepRound(adjustment) {
    return adjustment?.pricingModel === "base_band_step";
}
function isValidBandStepRound(adjustment) {
    return (isBandStepRound(adjustment) &&
        Number.isInteger(adjustment?.bandStep) &&
        adjustment?.bandStepParams != null);
}
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
function priceRateEntryForRound(entry, adjustment) {
    const isBase = entry.priceBasis === "base";
    if (isBandStepRound(adjustment)) {
        if (!isBase)
            return { ok: false, reason: "final_rate_with_band_step_round" };
        if (!isValidBandStepRound(adjustment))
            return { ok: false, reason: "invalid_band_step_round" };
        const { bandStep, bandStepParams } = adjustment;
        const finalRateThb = priceAtBandStep(entry.rateThb, bandStep, bandStepParams.stepBasisPoints, bandStepParams.roundToThb);
        if (!Number.isFinite(finalRateThb))
            return { ok: false, reason: "invalid_band_step_round" };
        return {
            ok: true,
            finalRateThb,
            rateMultiplier: 1,
            addThbPerTrip: Math.round((finalRateThb - entry.rateThb) * 100) / 100,
        };
    }
    if (adjustment && isBase)
        return { ok: false, reason: "base_rate_with_legacy_round" };
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
function timestampLikeToMillis(val) {
    if (!val)
        return 0;
    if (typeof val === "number" && Number.isFinite(val))
        return val;
    if (val instanceof Date)
        return val.getTime();
    if (typeof val.toMillis === "function") {
        return val.toMillis();
    }
    if (typeof val.toDate === "function") {
        return val.toDate().getTime();
    }
    return 0;
}
function getTripBillingDateMs(trip) {
    // An explicit plan-date override (ADR 0027) wins over the delivery instant (ADR 0008 §3).
    if (typeof trip.billingDateMs === "number" && Number.isFinite(trip.billingDateMs) && trip.billingDateMs > 0) {
        return trip.billingDateMs;
    }
    const delivered = timestampLikeToMillis(trip.deliveredTimestamp);
    if (delivered > 0)
        return delivered;
    const created = timestampLikeToMillis(trip.createdAt);
    if (created > 0)
        return created;
    return Date.now();
}
function resolveTaskCustomerId(task) {
    return (
    // Explicit choice at assign time wins over the hub-derived link (ADR 0027).
    task?.billingCustomerId?.trim() ||
        task?.sourceHubLinkedCustomerId?.trim() ||
        task?.destinationLinkedCustomerId?.trim() ||
        "");
}
function selectBillingRateEntry(customerId, hubId, destinationCode, vehicleClass, billDateMs, rateEntries, jobCategory = "PRIMARY") {
    const normalizedVehicleClass = normalizeVehicleClass(vehicleClass);
    const candidates = rateEntries.filter((entry) => entry.voided !== true &&
        entry.customerId === customerId &&
        entry.hubId === hubId &&
        entry.destinationCode === destinationCode &&
        normalizeVehicleClass(entry.vehicleClass) === normalizedVehicleClass &&
        (entry.jobCategory ?? "PRIMARY") === jobCategory);
    if (candidates.length === 0)
        return null;
    // Primary: effective on or before trip date, by Bangkok calendar day (newest first; same day →
    // the row written last, ADR 0009 §9)
    const effective = candidates
        .filter((e) => isEffectiveOnOrBeforeBillingDate(e.effectiveFromMs, billDateMs))
        .sort(compareAnnouncementsNewestFirst);
    if (effective.length > 0)
        return effective[0];
    // Fallback: trip is before all effective dates → use the oldest rate card
    return earliestAnnouncement(candidates);
}
function selectFuelAdjustmentForBillingDate(customerId, billDateMs, fuelAdjustments) {
    const matched = fuelAdjustments
        .filter((adj) => adj.voided !== true &&
        adj.customerId === customerId &&
        isEffectiveOnOrBeforeBillingDate(adj.effectiveFromMs, billDateMs))
        .sort(compareAnnouncementsNewestFirst);
    return matched[0] ?? null;
}
/** Final trip rate in THB, rounded to 2 decimal places (same as legacy billing snapshot). */
function computeFinalRateThb(baseRateThb, rateMultiplier, addThbPerTrip) {
    return Math.round((baseRateThb * rateMultiplier + addThbPerTrip) * 100) / 100;
}
/**
 * True when a stored snapshot was priced WITH a fuel adjustment — an adjustment id, a multiplier
 * other than 1, or a non-zero per-trip add. A เสริม snapshot must never carry one (ADR-0005: เสริม
 * is a fixed price fuel never moves), so on a เสริม row this is the signature of a corrupted price.
 */
function snapshotCarriesFuel(s) {
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
function isFrozenBillingSnapshot(s) {
    if (s.billingManualOverride === true)
        return true;
    return s.jobCategory === "SUPPLEMENTARY" && !snapshotCarriesFuel(s);
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
function computeMultiDeliveryBilling(trip, task, deliveryStops, vehicleClass, rateEntries, fuelAdjustments, extraStopFeeThb, jobCategory = "PRIMARY") {
    if (!task || deliveryStops.length < 2)
        return null;
    const customerId = resolveTaskCustomerId(task);
    if (!customerId)
        return null;
    const hubId = extractHubId(task.sourceHub);
    const normalizedVehicleClass = normalizeVehicleClass(vehicleClass);
    const billDateMs = getTripBillingDateMs(trip);
    // Supplementary (เสริม) rate cards are fixed, separately-agreed prices (ADR-0005) —
    // they never move with primary fuel-rate adjustments.
    const matchedAdjustment = jobCategory === "SUPPLEMENTARY"
        ? null
        : selectFuelAdjustmentForBillingDate(customerId, billDateMs, fuelAdjustments);
    // Trip-level multiplier/add are those of the BASE leg. For a band-step round (ADR 0029) the add is
    // per route (final − base), so each stop's own amount lives in stopBreakdown.
    let rateMultiplier = matchedAdjustment?.rateMultiplier ?? 1;
    let addThbPerTrip = matchedAdjustment?.addThbPerTrip ?? 0;
    const useFlatExtraStopFee = typeof extraStopFeeThb === "number" && extraStopFeeThb >= 0;
    let baseRateThb = 0;
    let stopChargeThb = 0;
    const stopBreakdown = [];
    // The rate entry that produced the base leg — carries the round this trip was priced under.
    let baseRateEntry = null;
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
                if (m) {
                    baseMatch = m;
                    baseIdx = i;
                    baseDest = normStops[i];
                    break;
                }
            }
        }
        if (!baseMatch)
            return null; // no rate card for any stop → cannot bill
        baseRateEntry = baseMatch;
        const basePriced = priceRateEntryForRound(baseMatch, matchedAdjustment);
        if (!basePriced.ok)
            return null;
        const baseFinal = basePriced.finalRateThb;
        rateMultiplier = basePriced.rateMultiplier;
        addThbPerTrip = basePriced.addThbPerTrip;
        baseRateThb = baseFinal;
        stopBreakdown.push({ stopIndex: 1, destination: baseDest, baseRateThb: baseMatch.rateThb, finalRateThb: baseFinal });
        // Exclude exactly one delivered stop (the base) from the extra-stop charges.
        const excludeIdx = baseIdx >= 0 ? baseIdx : 0;
        let extraSeq = 2;
        deliveryStops.forEach((_stop, idx) => {
            if (idx === excludeIdx)
                return;
            stopChargeThb += extraStopFeeThb;
            stopBreakdown.push({
                stopIndex: extraSeq++,
                destination: normStops[idx],
                baseRateThb: extraStopFeeThb,
                finalRateThb: extraStopFeeThb,
            });
        });
    }
    else {
        // Legacy per-route mode: stop[0] = base rate, stop[1+] = per-route rate-card lookup.
        deliveryStops.forEach((stop, idx) => {
            const destination = normalizeDestinationCode(stop.destination);
            const matchedRate = selectBillingRateEntry(customerId, hubId, destination, normalizedVehicleClass, billDateMs, rateEntries, jobCategory);
            if (!matchedRate)
                return;
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
            }
            else {
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
    if (pricingMismatch || baseRateThb === 0 || stopBreakdown.length === 0 || !baseRateEntry)
        return null;
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
/** Select the standby rate effective on or before billDateMs; fallback to oldest if none match. */
function selectStandbyRateEntry(customerId, billDateMs, rateEntries) {
    const candidates = rateEntries.filter((e) => e.customerId === customerId);
    if (candidates.length === 0)
        return null;
    const effective = candidates
        .filter((e) => isEffectiveOnOrBeforeBillingDate(e.effectiveFromMs, billDateMs))
        .sort((a, b) => b.effectiveFromMs - a.effectiveFromMs);
    if (effective.length > 0)
        return effective[0];
    // Fallback: use oldest rate if standby happened before all effective dates
    return candidates.sort((a, b) => a.effectiveFromMs - b.effectiveFromMs)[0];
}
/**
 * Compute standby billing for a completed standby record.
 * Fixed rate per event — does not depend on duration.
 */
function computeStandbyBilling(billDateMs, customerId, rateEntries) {
    if (!customerId)
        return null;
    const matched = selectStandbyRateEntry(customerId, billDateMs, rateEntries);
    if (!matched)
        return null;
    return {
        customerId,
        rateThb: matched.rateThb,
        rateEntryId: matched.id,
        effectiveFromDateStr: bangkokDateStrFromMillis(matched.effectiveFromMs),
    };
}
function computeTripBillingFromParts(trip, task, rateEntries, fuelAdjustments, jobCategory = "PRIMARY") {
    if (!task)
        return null;
    const customerId = resolveTaskCustomerId(task);
    if (!customerId)
        return null;
    const hubId = extractHubId(task.sourceHub);
    const destination = normalizeDestinationCode(task.destination);
    const vehicleClass = normalizeVehicleClass(task.truckType);
    const billDateMs = getTripBillingDateMs(trip);
    const matchedRate = selectBillingRateEntry(customerId, hubId, destination, vehicleClass, billDateMs, rateEntries, jobCategory);
    if (!matchedRate)
        return null;
    // Supplementary (เสริม) rate cards are fixed, separately-agreed prices (ADR-0005) —
    // they never move with primary fuel-rate adjustments.
    const matchedAdjustment = jobCategory === "SUPPLEMENTARY"
        ? null
        : selectFuelAdjustmentForBillingDate(customerId, billDateMs, fuelAdjustments);
    // ADR 0029: a base-price row only combines with a band-step round, a final-price row only with a
    // legacy one. A refused pairing is "no price", so the caller fails loud instead of mis-billing.
    const priced = priceRateEntryForRound(matchedRate, matchedAdjustment);
    if (!priced.ok)
        return null;
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
function diagnoseRoundPricingMismatch(trip, task, rateEntries, fuelAdjustments, jobCategory = "PRIMARY") {
    if (!task)
        return null;
    const customerId = resolveTaskCustomerId(task);
    if (!customerId)
        return null;
    const billDateMs = getTripBillingDateMs(trip);
    const matchedRate = selectBillingRateEntry(customerId, extractHubId(task.sourceHub), normalizeDestinationCode(task.destination), normalizeVehicleClass(task.truckType), billDateMs, rateEntries, jobCategory);
    if (!matchedRate)
        return null;
    const adjustment = jobCategory === "SUPPLEMENTARY"
        ? null
        : selectFuelAdjustmentForBillingDate(customerId, billDateMs, fuelAdjustments);
    const priced = priceRateEntryForRound(matchedRate, adjustment);
    return priced.ok ? null : priced.reason;
}
//# sourceMappingURL=billingCompute.js.map