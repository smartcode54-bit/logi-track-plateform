/**
 * Driver compensation — pure compute engine.
 *
 * 🔁 MUST stay logically in sync with `functions/src/core/compensationCompute.ts`
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
