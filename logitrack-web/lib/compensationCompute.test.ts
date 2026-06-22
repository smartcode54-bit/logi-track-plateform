import { describe, it, expect } from "vitest";
import {
    roundTHB,
    assignRound,
    makeHolidayChecker,
    computeBasePay,
    computeFuelIncentive,
    computeTripVolumeIncentive,
    computeSso,
    applyDeductions,
    type BasePayTrip,
    type IncentiveTier,
    type SsoRuleInput,
} from "./compensationCompute";

const SSO_RULE: SsoRuleInput = {
    ratePercent: 5,
    baseExistingThb: 15000,
    baseNewThb: 12000,
    existingHiredBeforeYear: 2026,
    maxAgeInclusive: 55,
};
const ASOF = new Date("2026-06-30T12:00:00+07:00");

const FUEL_TIERS: IncentiveTier[] = [
    { min: 10, amountThb: 1000 },
    { min: 11, amountThb: 1100 },
    { min: 12, amountThb: 1200 },
    { min: 13, amountThb: 1400 },
    { min: 14, amountThb: 1800 },
];
const TRIP_TIERS: IncentiveTier[] = [
    { min: 50, amountThb: 1000 },
    { min: 60, amountThb: 1500 },
    { min: 70, amountThb: 2000 },
];

describe("roundTHB", () => {
    it("rounds half-up to whole baht", () => {
        expect(roundTHB(949.5)).toBe(950);
        expect(roundTHB(949.4)).toBe(949);
        expect(roundTHB(950)).toBe(950);
    });
});

describe("assignRound", () => {
    it("days 1-15 are R1, 16-end are R2 (Bangkok)", () => {
        expect(assignRound(new Date("2026-06-15T23:00:00+07:00"))).toBe("R1");
        expect(assignRound(new Date("2026-06-16T00:30:00+07:00"))).toBe("R2");
        expect(assignRound(new Date("2026-06-01T08:00:00+07:00"))).toBe("R1");
        expect(assignRound(new Date("2026-06-30T08:00:00+07:00"))).toBe("R2");
    });
});

describe("makeHolidayChecker", () => {
    const isHoliday = makeHolidayChecker(new Set(["2026-06-16"]));
    it("treats Sunday as holiday", () => {
        // 2026-01-04 is a Sunday
        expect(isHoliday(new Date("2026-01-04T10:00:00+07:00"))).toBe(true);
    });
    it("treats listed public holidays as holiday", () => {
        expect(isHoliday(new Date("2026-06-16T10:00:00+07:00"))).toBe(true);
    });
    it("treats a normal weekday as non-holiday", () => {
        expect(isHoliday(new Date("2026-06-17T10:00:00+07:00"))).toBe(false);
    });
});

describe("computeBasePay", () => {
    const cfg = { weekdayRateThb: 300, holidayRateThb: 350, payStandby: false };
    const d = (s: string) => new Date(s);
    const trips: BasePayTrip[] = [
        { deliveredAt: d("2026-06-02T08:00:00+07:00"), isStandby: false, isMultiStop: false },
        { deliveredAt: d("2026-06-03T08:00:00+07:00"), isStandby: false, isMultiStop: false },
        { deliveredAt: d("2026-06-04T08:00:00+07:00"), isStandby: false, isMultiStop: false }, // holiday by predicate
        { deliveredAt: d("2026-06-05T08:00:00+07:00"), isStandby: true, isMultiStop: false }, // standby excluded
        { deliveredAt: d("2026-06-06T08:00:00+07:00"), isStandby: false, isMultiStop: true }, // multi-stop excluded
    ];

    it("excludes standby + multi-stop, splits weekday/holiday, sums via roundTHB", () => {
        const holidayOn = new Set(["2026-06-04"]);
        const res = computeBasePay(trips, cfg, makeHolidayChecker(holidayOn));
        expect(res.weekdayTrips).toBe(2);
        expect(res.holidayTrips).toBe(1);
        expect(res.excludedTrips).toBe(2);
        expect(res.basePayThb).toBe(2 * 300 + 1 * 350); // 950
    });

    it("includes standby when payStandby is true", () => {
        const res = computeBasePay(trips, { ...cfg, payStandby: true }, () => false);
        // 4 non-multistop trips, all weekday (predicate false), multi-stop still excluded
        expect(res.weekdayTrips).toBe(4);
        expect(res.holidayTrips).toBe(0);
        expect(res.excludedTrips).toBe(1);
        expect(res.basePayThb).toBe(4 * 300);
    });
});

describe("computeFuelIncentive", () => {
    it("maps km/L to the highest reached tier when eligible", () => {
        expect(computeFuelIncentive(10, 6, FUEL_TIERS, 5).amountThb).toBe(1000);
        expect(computeFuelIncentive(13.9, 6, FUEL_TIERS, 5).amountThb).toBe(1400);
        expect(computeFuelIncentive(14, 6, FUEL_TIERS, 5).amountThb).toBe(1800);
        expect(computeFuelIncentive(20, 6, FUEL_TIERS, 5).amountThb).toBe(1800);
    });
    it("returns 0 below the lowest tier", () => {
        expect(computeFuelIncentive(9.9, 6, FUEL_TIERS, 5).amountThb).toBe(0);
    });
    it("gates the whole incentive on > minRefuels", () => {
        expect(computeFuelIncentive(14, 5, FUEL_TIERS, 5)).toEqual({ amountThb: 0, eligible: false });
        expect(computeFuelIncentive(14, 6, FUEL_TIERS, 5).eligible).toBe(true);
    });
    it("returns 0 when km/L is null", () => {
        expect(computeFuelIncentive(null, 10, FUEL_TIERS, 5)).toEqual({ amountThb: 0, eligible: false });
    });
});

describe("computeTripVolumeIncentive", () => {
    it("uses the highest reached tier only (not additive)", () => {
        expect(computeTripVolumeIncentive(49, TRIP_TIERS)).toBe(0);
        expect(computeTripVolumeIncentive(50, TRIP_TIERS)).toBe(1000);
        expect(computeTripVolumeIncentive(65, TRIP_TIERS)).toBe(1500);
        expect(computeTripVolumeIncentive(70, TRIP_TIERS)).toBe(2000);
        expect(computeTripVolumeIncentive(100, TRIP_TIERS)).toBe(2000);
    });
});

describe("computeSso", () => {
    const d = (s: string) => new Date(s);
    it("is 0 while in probation", () => {
        expect(computeSso({ hireDate: d("2024-01-01T00:00:00+07:00"), birthDate: null, probationPassed: false }, SSO_RULE, ASOF)).toBe(0);
    });
    it("existing (hired before cutoff) uses 15000 base @5% = 750", () => {
        expect(computeSso({ hireDate: d("2025-05-01T00:00:00+07:00"), birthDate: d("1990-01-01T00:00:00+07:00"), probationPassed: true }, SSO_RULE, ASOF)).toBe(750);
    });
    it("new (hired in/after cutoff) uses 12000 base @5% = 600", () => {
        expect(computeSso({ hireDate: d("2026-02-01T00:00:00+07:00"), birthDate: d("1990-01-01T00:00:00+07:00"), probationPassed: true }, SSO_RULE, ASOF)).toBe(600);
    });
    it("is 0 when older than maxAgeInclusive", () => {
        expect(computeSso({ hireDate: d("2025-01-01T00:00:00+07:00"), birthDate: d("1969-01-01T00:00:00+07:00"), probationPassed: true }, SSO_RULE, ASOF)).toBe(0);
    });
    it("missing hireDate is treated as existing", () => {
        expect(computeSso({ hireDate: null, birthDate: null, probationPassed: true }, SSO_RULE, ASOF)).toBe(750);
    });
});

describe("applyDeductions", () => {
    it("deducts SSO then penalty; net = earnings - sso - penalty", () => {
        const r = applyDeductions(5000, 750, [{ id: "p1", remainingThb: 3000, installmentThb: 3000 }]);
        expect(r.ssoAppliedThb).toBe(750);
        expect(r.penaltiesAppliedThb).toBe(3000);
        expect(r.perPenalty[0].remainingAfterThb).toBe(0);
        expect(r.netThb).toBe(1250);
    });
    it("never goes negative; unpaid penalty carries forward", () => {
        const r = applyDeductions(1000, 750, [{ id: "p1", remainingThb: 3000, installmentThb: 3000 }]);
        expect(r.ssoAppliedThb).toBe(750);
        expect(r.penaltiesAppliedThb).toBe(250);
        expect(r.perPenalty[0].remainingAfterThb).toBe(2750);
        expect(r.netThb).toBe(0);
    });
    it("honors a split installment smaller than the balance", () => {
        const r = applyDeductions(5000, 0, [{ id: "p1", remainingThb: 3000, installmentThb: 1000 }]);
        expect(r.penaltiesAppliedThb).toBe(1000);
        expect(r.perPenalty[0].remainingAfterThb).toBe(2000);
        expect(r.netThb).toBe(4000);
    });
});
