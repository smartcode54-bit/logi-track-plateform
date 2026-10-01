import { describe, expect, it } from "vitest";
import {
    MONTH_FILTER_ALL,
    MONTH_FILTER_NONE,
    buildMonthFilterOptions,
    monthFilterKey,
    rowMatchesMonthFilter,
} from "./monthFilter";

describe("monthFilterKey", () => {
    it("reads the month on the Bangkok calendar, not UTC", () => {
        // 30 Sep 17:30Z = 1 Oct 00:30 ICT — an overnight delivery is October in Thailand.
        expect(monthFilterKey(new Date("2026-09-30T17:30:00Z"))).toBe("2026-10");
        // 30 Sep 16:59Z = 30 Sep 23:59 ICT — still September.
        expect(monthFilterKey(new Date("2026-09-30T16:59:00Z"))).toBe("2026-09");
    });

    it("puts a missing or invalid date in the no-date bucket", () => {
        expect(monthFilterKey(undefined)).toBe(MONTH_FILTER_NONE);
        expect(monthFilterKey(null)).toBe(MONTH_FILTER_NONE);
        expect(monthFilterKey(new Date("not a date"))).toBe(MONTH_FILTER_NONE);
    });
});

describe("buildMonthFilterOptions", () => {
    it("counts rows per month and orders months chronologically across a year boundary", () => {
        const options = buildMonthFilterOptions([
            new Date("2027-01-05T03:00:00Z"),
            new Date("2026-12-20T03:00:00Z"),
            new Date("2026-12-21T03:00:00Z"),
        ]);
        expect(options.map((o) => [o.value, o.count])).toEqual([
            ["2026-12", 2],
            ["2027-01", 1],
        ]);
        expect(options[0]).toMatchObject({ year: 2026, month: 12 });
    });

    it("sorts the no-date bucket last", () => {
        const options = buildMonthFilterOptions([undefined, new Date("2026-09-10T03:00:00Z"), null]);
        expect(options.map((o) => o.value)).toEqual(["2026-09", MONTH_FILTER_NONE]);
        expect(options[1]).toMatchObject({ count: 2, year: 0, month: 0 });
    });

    it("returns nothing for no rows", () => {
        expect(buildMonthFilterOptions([])).toEqual([]);
    });
});

describe("rowMatchesMonthFilter", () => {
    const sep = new Date("2026-09-10T03:00:00Z");

    it("ALL (or empty) matches every row, including rows with no date", () => {
        expect(rowMatchesMonthFilter(sep, MONTH_FILTER_ALL)).toBe(true);
        expect(rowMatchesMonthFilter(undefined, MONTH_FILTER_ALL)).toBe(true);
        expect(rowMatchesMonthFilter(sep, "")).toBe(true);
    });

    it("a month key matches only rows in that Bangkok month", () => {
        expect(rowMatchesMonthFilter(sep, "2026-09")).toBe(true);
        expect(rowMatchesMonthFilter(sep, "2026-10")).toBe(false);
    });

    it("the no-date bucket matches only rows without a date", () => {
        expect(rowMatchesMonthFilter(undefined, MONTH_FILTER_NONE)).toBe(true);
        expect(rowMatchesMonthFilter(sep, MONTH_FILTER_NONE)).toBe(false);
    });
});
