import { describe, it, expect } from "vitest";
import { driverDisplayName, matchDriverOptionId, type DriverNameSource } from "./driverName";

const somchai: DriverNameSource = {
    id: "drv_1",
    fullNameTh: "สมชาย ใจดี",
    firstName: "Somchai",
    lastName: "Jaidee",
};
const noThai: DriverNameSource = {
    id: "drv_2",
    firstName: "Anan",
    lastName: "Kumar",
};
const drivers = [somchai, noThai];

describe("driverDisplayName", () => {
    it("prefers the Thai full name — the rule for every report and invoice", () => {
        expect(driverDisplayName(somchai)).toBe("สมชาย ใจดี");
    });

    it("falls back through name → firstName+lastName → email → id", () => {
        expect(driverDisplayName({ name: "Legacy Name" })).toBe("Legacy Name");
        expect(driverDisplayName(noThai)).toBe("Anan Kumar");
        expect(driverDisplayName({ email: "a@b.com" })).toBe("a@b.com");
        expect(driverDisplayName({}, "drv_9")).toBe("drv_9");
    });

    it("treats a whitespace-only Thai name as absent", () => {
        expect(driverDisplayName({ fullNameTh: "   ", firstName: "A", lastName: "B" })).toBe("A B");
    });
});

describe("matchDriverOptionId", () => {
    it("uses the stored driverId — a name is not an identity", () => {
        expect(matchDriverOptionId(drivers, "drv_1", "anything at all")).toBe("drv_1");
    });

    it("falls back to the Thai name for legacy tasks with no driverId", () => {
        expect(matchDriverOptionId(drivers, "", "สมชาย ใจดี")).toBe("drv_1");
    });

    it("also matches the older `firstName lastName` form still stored on old tasks", () => {
        expect(matchDriverOptionId(drivers, undefined, "Somchai Jaidee")).toBe("drv_1");
    });

    it("ignores a driverId that no longer exists and still resolves by name", () => {
        expect(matchDriverOptionId(drivers, "deleted_driver", "Somchai Jaidee")).toBe("drv_1");
    });

    it("returns undefined when nothing matches, so the caller can show its placeholder", () => {
        expect(matchDriverOptionId(drivers, "", "")).toBeUndefined();
        expect(matchDriverOptionId(drivers, null, "ไม่มีคนนี้")).toBeUndefined();
        expect(matchDriverOptionId([], "drv_1", "สมชาย ใจดี")).toBeUndefined();
    });

    it("does not match a driver with no name at all against an empty-ish stored name", () => {
        expect(matchDriverOptionId([{ id: "drv_3" }], "", "   ")).toBeUndefined();
    });
});
