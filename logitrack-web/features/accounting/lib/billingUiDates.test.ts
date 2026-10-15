// @vitest-environment node
import { afterAll, describe, expect, it } from "vitest";
import { bangkokDateTimeInput, bangkokExcelDateTime, bangkokInputInstant, bangkokMonthRange, formatBangkokDateTime, formatBillingDay } from "./billingUiDates";

const originalTZ = process.env.TZ;
afterAll(() => { process.env.TZ = originalTZ; });

describe.each(["UTC", "America/New_York"])("billing UI dates in %s", (zone) => {
    it("shows, edits and exports a 02:00 ICT delivery on its Bangkok day", () => {
        process.env.TZ = zone;
        const instant = "2026-10-04T19:00:00Z";
        expect(formatBangkokDateTime(instant)).toBe("05/10/2026 02:00");
        expect(bangkokDateTimeInput(instant)).toBe("2026-10-05T02:00");
        expect(bangkokExcelDateTime(instant)).toBe("2026-10-05 02:00");
        expect(bangkokInputInstant("2026-10-05T02:00")).toBe("2026-10-04T19:00:00.000Z");
        expect(formatBillingDay("2026-10-05")).toBe("05/10/2026");
        expect(bangkokMonthRange(new Date("2026-09-30T19:00:00Z"))).toEqual({ from: "2026-10-01", to: "2026-10-31", year: 2026, month: 10 });
    });
});

it("leaves absent or invalid timestamps blank for exports and a dash on screen", () => {
    expect(bangkokExcelDateTime(null)).toBe("");
    expect(bangkokExcelDateTime("bad")).toBe("");
    expect(formatBangkokDateTime(null)).toBe("—");
});
