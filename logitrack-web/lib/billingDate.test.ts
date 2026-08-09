import { describe, expect, it } from "vitest";
import {
    bangkokDateStr,
    bangkokMidnightFromDateStr,
    bangkokMidnightFromPickedDate,
    pickedDateToDateStr,
} from "./billingDate";

describe("bangkokMidnightFromDateStr", () => {
    it("resolves a date to Bangkok midnight, i.e. 17:00Z the previous day", () => {
        expect(bangkokMidnightFromDateStr("2026-08-16")?.toISOString()).toBe("2026-08-15T17:00:00.000Z");
    });

    it("does not depend on the host timezone", () => {
        // A literal +07:00 offset is absolute; `new Date("2026-08-16T00:00:00")` would not be.
        const iso = bangkokMidnightFromDateStr("2026-01-01")?.toISOString();
        expect(iso).toBe("2025-12-31T17:00:00.000Z");
    });

    it("is 7 hours earlier than the UTC-midnight form the old helper produced", () => {
        const bangkok = bangkokMidnightFromDateStr("2026-08-16")!.getTime();
        const utcMidnight = Date.UTC(2026, 7, 16);
        expect(utcMidnight - bangkok).toBe(7 * 60 * 60 * 1000);
    });

    it("rejects anything that is not a yyyy-MM-dd date", () => {
        expect(bangkokMidnightFromDateStr("")).toBeNull();
        expect(bangkokMidnightFromDateStr("16/08/2026")).toBeNull();
        expect(bangkokMidnightFromDateStr("2026-8-6")).toBeNull();
    });
});

describe("bangkokDateStr", () => {
    it("reads an instant on the Bangkok calendar, not the UTC one", () => {
        expect(bangkokDateStr(new Date("2026-08-15T17:00:00.000Z"))).toBe("2026-08-16");
        expect(bangkokDateStr(new Date("2026-08-16T16:59:59.000Z"))).toBe("2026-08-16");
        expect(bangkokDateStr(new Date("2026-08-16T17:00:00.000Z"))).toBe("2026-08-17");
    });

    it("round-trips with bangkokMidnightFromDateStr", () => {
        for (const s of ["2026-01-01", "2026-08-16", "2026-12-31"]) {
            expect(bangkokDateStr(bangkokMidnightFromDateStr(s)!)).toBe(s);
        }
    });
});

describe("bangkokMidnightFromPickedDate", () => {
    it("keeps the calendar day the picker showed the admin", () => {
        // The picker hands back local midnight; the day it displayed is the local day.
        const picked = new Date(2026, 7, 16, 0, 0, 0, 0);
        expect(pickedDateToDateStr(picked)).toBe("2026-08-16");
        expect(bangkokDateStr(bangkokMidnightFromPickedDate(picked))).toBe("2026-08-16");
    });

    it("normalizes a picked date that carries a time component", () => {
        const picked = new Date(2026, 7, 16, 13, 45, 0, 0);
        expect(bangkokMidnightFromPickedDate(picked).toISOString()).toBe("2026-08-15T17:00:00.000Z");
    });
});
