"use strict";
/**
 * Driver compensation — pure compute engine.
 *
 * 🔁 MUST stay logically in sync with `lib/compensationCompute.ts`
 * (project rule: compute logic duplicated across lib/ and functions/src/core/).
 *
 * No Firestore / IO here — all inputs are passed in, so functions are
 * deterministic and unit-testable. (Story 2.1: base pay; fuel / trip-volume /
 * SSO / installments are added in later stories.)
 */
Object.defineProperty(exports, "__esModule", { value: true });
exports.roundTHB = roundTHB;
exports.bangkokParts = bangkokParts;
exports.bangkokDateKey = bangkokDateKey;
exports.assignRound = assignRound;
exports.makeHolidayChecker = makeHolidayChecker;
exports.computeBasePay = computeBasePay;
exports.computeFuelIncentive = computeFuelIncentive;
exports.computeTripVolumeIncentive = computeTripVolumeIncentive;
exports.computeHelperPay = computeHelperPay;
exports.ageYearsAt = ageYearsAt;
exports.computeSso = computeSso;
exports.applyDeductions = applyDeductions;
/** Round to whole THB (half-up). */
function roundTHB(n) {
    return Math.round(n);
}
/** Bangkok (UTC+7) wall-clock parts for an instant. */
function bangkokParts(d) {
    const bkk = new Date(d.getTime() + 7 * 60 * 60000);
    return {
        y: bkk.getUTCFullYear(),
        m: bkk.getUTCMonth() + 1,
        day: bkk.getUTCDate(),
        dow: bkk.getUTCDay(), // 0 = Sunday
    };
}
/** Bangkok yyyy-MM-dd key. */
function bangkokDateKey(d) {
    const p = bangkokParts(d);
    return `${p.y}-${String(p.m).padStart(2, "0")}-${String(p.day).padStart(2, "0")}`;
}
/** Semi-monthly round for a delivered date: R1 = days 1–15, R2 = 16–end (Bangkok). */
function assignRound(d) {
    return bangkokParts(d).day <= 15 ? "R1" : "R2";
}
/**
 * Holiday checker — Sunday OR a date present in the holiday-key set
 * (yyyy-MM-dd Bangkok, from working_holiday_calendar).
 */
function makeHolidayChecker(holidayKeys) {
    return (d) => {
        if (bangkokParts(d).dow === 0)
            return true; // Sunday
        return holidayKeys.has(bangkokDateKey(d));
    };
}
/**
 * Base per-trip pay (FR5–FR7). Excludes multi-stop trips entirely (OQ9) and
 * standby trips unless `payStandby` is set. Holiday = Sunday or public holiday.
 * Caller passes trips already scoped to the period/round being computed.
 */
function computeBasePay(trips, cfg, isHoliday) {
    let weekdayTrips = 0;
    let holidayTrips = 0;
    let excludedTrips = 0;
    for (const t of trips) {
        if (t.isMultiStop || (t.isStandby && !cfg.payStandby)) {
            excludedTrips++;
            continue;
        }
        if (isHoliday(t.deliveredAt))
            holidayTrips++;
        else
            weekdayTrips++;
    }
    const basePayThb = roundTHB(weekdayTrips * cfg.weekdayRateThb + holidayTrips * cfg.holidayRateThb);
    return { weekdayTrips, holidayTrips, excludedTrips, basePayThb };
}
/**
 * Fuel-efficiency incentive (FR9, FR9.1). Gated by `refuelCount > minRefuels`
 * for the whole incentive (anti-gaming). Picks the highest tier whose `min`
 * km/L is reached; below all tiers (or ineligible) → 0.
 */
function computeFuelIncentive(kmPerLitre, refuelCount, tiers, minRefuels) {
    const eligible = kmPerLitre != null && refuelCount > minRefuels;
    if (!eligible)
        return { amountThb: 0, eligible: false };
    const sorted = [...tiers].sort((a, b) => a.min - b.min);
    let amountThb = 0;
    for (const t of sorted) {
        if (kmPerLitre >= t.min)
            amountThb = t.amountThb;
    }
    return { amountThb, eligible: true };
}
/**
 * Trip-volume incentive (FR12) — highest reached tier ONLY (not additive).
 * Below the lowest tier → 0.
 */
function computeTripVolumeIncentive(tripCount, tiers) {
    const sorted = [...tiers].sort((a, b) => a.min - b.min);
    let amountThb = 0;
    for (const t of sorted) {
        if (tripCount >= t.min)
            amountThb = t.amountThb;
    }
    return amountThb;
}
/**
 * Helper / training-day pay — flat rate per eligible non-driving attendance day.
 * Caller passes the count of eligible helper days (days with no delivered trip).
 */
function computeHelperPay(helperDayCount, ratePerDay) {
    return roundTHB(Math.max(0, helperDayCount) * Math.max(0, ratePerDay));
}
/** Whole-years age at `asOf` (Bangkok). */
function ageYearsAt(birthDate, asOf) {
    const b = bangkokParts(birthDate);
    const n = bangkokParts(asOf);
    let age = n.y - b.y;
    if (n.m < b.m || (n.m === b.m && n.day < b.day))
        age--;
    return age;
}
/**
 * Social-security employee deduction (FR13). 0 if still in probation
 * (probationPassed=false) or age > maxAgeInclusive. Base is chosen by hire year
 * (existing if hired before existingHiredBeforeYear, else new; missing hireDate
 * → treated as existing). Returns whole THB.
 */
function computeSso(driver, rule, asOf) {
    if (!driver.probationPassed)
        return 0;
    if (driver.birthDate && ageYearsAt(driver.birthDate, asOf) > rule.maxAgeInclusive)
        return 0;
    const isExisting = driver.hireDate
        ? bangkokParts(driver.hireDate).y < rule.existingHiredBeforeYear
        : true;
    const base = isExisting ? rule.baseExistingThb : rule.baseNewThb;
    return roundTHB((base * rule.ratePercent) / 100);
}
/**
 * Apply deductions so net pay is never negative (FR15.1). SSO is taken first
 * (mandatory), then penalties in order; each penalty takes
 * min(installmentThb, remaining, available). Any shortfall stays as
 * `remainingAfterThb` to carry to the next round.
 */
function applyDeductions(earningsThb, ssoThb, penalties) {
    let available = Math.max(0, earningsThb);
    const ssoAppliedThb = Math.min(available, Math.max(0, ssoThb));
    available -= ssoAppliedThb;
    const perPenalty = [];
    let penaltiesAppliedThb = 0;
    for (const p of penalties) {
        const want = Math.min(Math.max(0, p.installmentThb), Math.max(0, p.remainingThb));
        const applied = Math.min(want, available);
        available -= applied;
        penaltiesAppliedThb += applied;
        perPenalty.push({ id: p.id, appliedThb: applied, remainingAfterThb: p.remainingThb - applied });
    }
    return { ssoAppliedThb, penaltiesAppliedThb, perPenalty, netThb: roundTHB(available) };
}
//# sourceMappingURL=compensationCompute.js.map