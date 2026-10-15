import { describe, expect, it } from "vitest";
import { formatThb, formatThbSatang } from "./shared";

describe("accounting money formatters", () => {
    it("preserves compact formatting for existing consumers", () => {
        expect(formatThb(325)).toBe("฿325");
        expect(formatThb(1234.5)).toBe("฿1,234.5");
    });
    it("keeps two decimal places for billing amounts", () => {
        expect(formatThbSatang(0)).toBe("฿0.00");
        expect(formatThbSatang(325)).toBe("฿325.00");
        expect(formatThbSatang(1234.5)).toBe("฿1,234.50");
    });
    it.each([null, undefined, NaN, Infinity])("leaves unavailable amounts as a dash (%s)", (amount) => {
        expect(formatThb(amount)).toBe("—");
        expect(formatThbSatang(amount)).toBe("—");
    });
});
