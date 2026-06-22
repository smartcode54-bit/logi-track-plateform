import { describe, it, expect } from "vitest";
import {
    roundTHB,
    assignRound,
    makeHolidayChecker,
    computeBasePay,
    type BasePayTrip,
} from "./compensationCompute";

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
