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

/** Round to whole THB (half-up). */
export function roundTHB(n: number): number {
    return Math.round(n);
}

/** Bangkok (UTC+7) wall-clock parts for an instant. */
export function bangkokParts(d: Date): { y: number; m: number; day: number; dow: number } {
    const bkk = new Date(d.getTime() + 7 * 60 * 60000);
    return {
        y: bkk.getUTCFullYear(),
        m: bkk.getUTCMonth() + 1,
        day: bkk.getUTCDate(),
        dow: bkk.getUTCDay(), // 0 = Sunday
    };
}

/** Bangkok yyyy-MM-dd key. */
export function bangkokDateKey(d: Date): string {
    const p = bangkokParts(d);
    return `${p.y}-${String(p.m).padStart(2, "0")}-${String(p.day).padStart(2, "0")}`;
}

/** Semi-monthly round for a delivered date: R1 = days 1–15, R2 = 16–end (Bangkok). */
export function assignRound(d: Date): "R1" | "R2" {
    return bangkokParts(d).day <= 15 ? "R1" : "R2";
}

/**
 * Holiday checker — Sunday OR a date present in the holiday-key set
 * (yyyy-MM-dd Bangkok, from working_holiday_calendar).
 */
export function makeHolidayChecker(holidayKeys: Set<string>): (d: Date) => boolean {
    return (d) => {
        if (bangkokParts(d).dow === 0) return true; // Sunday
        return holidayKeys.has(bangkokDateKey(d));
    };
}

export interface BasePayTrip {
    /** Delivered timestamp — assigns the trip to its round and weekday/holiday class. */
    deliveredAt: Date;
    isStandby: boolean;
    isMultiStop: boolean;
}

export interface BasePayConfig {
    weekdayRateThb: number;
    holidayRateThb: number;
    /** When false, standby trips are excluded from pay (FR3). */
    payStandby: boolean;
}

export interface BasePayResult {
    weekdayTrips: number;
    holidayTrips: number;
    excludedTrips: number;
    basePayThb: number;
}

/**
 * Base per-trip pay (FR5–FR7). Excludes multi-stop trips entirely (OQ9) and
 * standby trips unless `payStandby` is set. Holiday = Sunday or public holiday.
 * Caller passes trips already scoped to the period/round being computed.
 */
export function computeBasePay(
    trips: BasePayTrip[],
    cfg: BasePayConfig,
    isHoliday: (d: Date) => boolean,
): BasePayResult {
    let weekdayTrips = 0;
    let holidayTrips = 0;
    let excludedTrips = 0;
    for (const t of trips) {
        if (t.isMultiStop || (t.isStandby && !cfg.payStandby)) {
            excludedTrips++;
            continue;
        }
        if (isHoliday(t.deliveredAt)) holidayTrips++;
        else weekdayTrips++;
    }
    const basePayThb = roundTHB(
        weekdayTrips * cfg.weekdayRateThb + holidayTrips * cfg.holidayRateThb,
    );
    return { weekdayTrips, holidayTrips, excludedTrips, basePayThb };
}

export interface IncentiveTier {
    /** Inclusive threshold (km/L for fuel, trip count for volume). */
    min: number;
    amountThb: number;
}

/**
 * Fuel-efficiency incentive (FR9, FR9.1). Gated by `refuelCount > minRefuels`
 * for the whole incentive (anti-gaming). Picks the highest tier whose `min`
 * km/L is reached; below all tiers (or ineligible) → 0.
 */
export function computeFuelIncentive(
    kmPerLitre: number | null,
    refuelCount: number,
    tiers: IncentiveTier[],
    minRefuels: number,
): { amountThb: number; eligible: boolean } {
    const eligible = kmPerLitre != null && refuelCount > minRefuels;
    if (!eligible) return { amountThb: 0, eligible: false };
    const sorted = [...tiers].sort((a, b) => a.min - b.min);
    let amountThb = 0;
    for (const t of sorted) {
        if ((kmPerLitre as number) >= t.min) amountThb = t.amountThb;
    }
    return { amountThb, eligible: true };
}

/**
 * Trip-volume incentive (FR12) — highest reached tier ONLY (not additive).
 * Below the lowest tier → 0.
 */
export function computeTripVolumeIncentive(tripCount: number, tiers: IncentiveTier[]): number {
    const sorted = [...tiers].sort((a, b) => a.min - b.min);
    let amountThb = 0;
    for (const t of sorted) {
        if (tripCount >= t.min) amountThb = t.amountThb;
    }
    return amountThb;
}

/**
 * Helper / training-day pay — flat rate per eligible non-driving attendance day.
 * Caller passes the count of eligible helper days (days with no delivered trip).
 */
export function computeHelperPay(helperDayCount: number, ratePerDay: number): number {
    return roundTHB(Math.max(0, helperDayCount) * Math.max(0, ratePerDay));
}

/** Whole-years age at `asOf` (Bangkok). */
export function ageYearsAt(birthDate: Date, asOf: Date): number {
    const b = bangkokParts(birthDate);
    const n = bangkokParts(asOf);
    let age = n.y - b.y;
    if (n.m < b.m || (n.m === b.m && n.day < b.day)) age--;
    return age;
}

export interface SsoRuleInput {
    ratePercent: number;
    baseExistingThb: number;
    baseNewThb: number;
    existingHiredBeforeYear: number;
    maxAgeInclusive: number;
}

export interface SsoDriverInput {
    hireDate: Date | null;
    birthDate: Date | null;
    probationPassed: boolean;
}

/**
 * Social-security employee deduction (FR13). 0 if still in probation
 * (probationPassed=false) or age > maxAgeInclusive. Base is chosen by hire year
 * (existing if hired before existingHiredBeforeYear, else new; missing hireDate
 * → treated as existing). Returns whole THB.
 */
export function computeSso(driver: SsoDriverInput, rule: SsoRuleInput, asOf: Date): number {
    if (!driver.probationPassed) return 0;
    if (driver.birthDate && ageYearsAt(driver.birthDate, asOf) > rule.maxAgeInclusive) return 0;
    const isExisting = driver.hireDate
        ? bangkokParts(driver.hireDate).y < rule.existingHiredBeforeYear
        : true;
    const base = isExisting ? rule.baseExistingThb : rule.baseNewThb;
    return roundTHB((base * rule.ratePercent) / 100);
}

export interface ScheduledPenalty {
    id: string;
    /** Outstanding balance before this round. */
    remainingThb: number;
    /** Amount scheduled to deduct this round (full = remaining, or split = remaining/งวด). */
    installmentThb: number;
}

export interface DeductionResult {
    ssoAppliedThb: number;
    penaltiesAppliedThb: number;
    perPenalty: { id: string; appliedThb: number; remainingAfterThb: number }[];
    /** earnings − SSO − penalties, clamped ≥ 0 (FR15.1). */
    netThb: number;
}

/**
 * Apply deductions so net pay is never negative (FR15.1). SSO is taken first
 * (mandatory), then penalties in order; each penalty takes
 * min(installmentThb, remaining, available). Any shortfall stays as
 * `remainingAfterThb` to carry to the next round.
 */
export function applyDeductions(
    earningsThb: number,
    ssoThb: number,
    penalties: ScheduledPenalty[],
): DeductionResult {
    let available = Math.max(0, earningsThb);
    const ssoAppliedThb = Math.min(available, Math.max(0, ssoThb));
    available -= ssoAppliedThb;

    const perPenalty: { id: string; appliedThb: number; remainingAfterThb: number }[] = [];
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
