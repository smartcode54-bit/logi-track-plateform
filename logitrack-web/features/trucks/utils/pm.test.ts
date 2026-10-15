import { describe, expect, it } from "vitest";
import { classifyPM } from "./pm";
const now = new Date("2026-10-15T15:00:00Z");
const empty = { nextServiceDate: null, nextServiceMileage: null, currentMileage: null };

describe("PM classification for cards, rows and filters", () => {
    it("does not invent a schedule or a mileage reading", () => {
        expect(classifyPM(empty, now).status).toBe("not_configured");
        expect(classifyPM({ ...empty, nextServiceMileage: 1000 }, now).status).toBe("not_configured");
    });
    it.each([[0, "due"], [2000, "due"], [2001, "normal"], [-1, "overdue"]] as const)("classifies %i km remaining, including a zero odometer", (km, status) => {
        expect(classifyPM({ ...empty, currentMileage: km < 0 ? 1 : 0, nextServiceMileage: km < 0 ? 0 : km }, now)).toMatchObject({ km, status });
    });
    it.each([["2026-10-14", "overdue"], ["2026-10-15", "due"], ["2026-11-14", "due"], ["2026-11-15", "normal"]] as const)("classifies %s on the Bangkok calendar", (nextServiceDate, status) => {
        expect(classifyPM({ ...empty, nextServiceDate }, now).status).toBe(status);
    });
    it("overdue wins over the other due-soon condition", () => {
        expect(classifyPM({ nextServiceDate: "2026-10-14", nextServiceMileage: 2000, currentMileage: 0 }, now).status).toBe("overdue");
    });
});
