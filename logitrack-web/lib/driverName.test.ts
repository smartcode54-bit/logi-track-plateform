import { describe, it, expect } from "vitest";
import {
    driverDisplayName,
    driverFilterKeys,
    driverIdentityKeys,
    matchDriverOptionId,
    type DriverNameSource,
} from "./driverName";

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

describe("driverIdentityKeys", () => {
    it("returns the current Auth UID and every past one from authIdHistory", () => {
        expect(
            driverIdentityKeys({ authId: "uid_new", authIdHistory: ["W47P8pGs", "uid_old2"] })
        ).toEqual(["uid_new", "W47P8pGs", "uid_old2"]);
    });

    it("includes the doc id first when given, so old trips keyed by uid still resolve", () => {
        expect(
            driverIdentityKeys({ authId: "uid_new", authIdHistory: ["uid_old"] }, "drv_1")
        ).toEqual(["drv_1", "uid_new", "uid_old"]);
    });

    it("accepts the legacy authUid field and de-dupes repeated ids", () => {
        expect(driverIdentityKeys({ authUid: "uid_x", authIdHistory: ["uid_x", "uid_y"] })).toEqual([
            "uid_x",
            "uid_y",
        ]);
    });

    it("ignores blank / whitespace entries and a missing history", () => {
        expect(driverIdentityKeys({ authId: "uid_1", authIdHistory: ["", "   ", "uid_2"] })).toEqual([
            "uid_1",
            "uid_2",
        ]);
        expect(driverIdentityKeys({ authId: "uid_1" })).toEqual(["uid_1"]);
        expect(driverIdentityKeys(null, "drv_9")).toEqual(["drv_9"]);
        expect(driverIdentityKeys(null)).toEqual([]);
    });
});

describe("driverFilterKeys", () => {
    const options = [
        { id: "uid_new", identityKeys: ["drv_1", "uid_new", "W47P8pGs"] },
        { id: "uid_b", identityKeys: ["drv_2", "uid_b"] },
    ];

    it("returns null for \"all\" so the caller skips the filter", () => {
        expect(driverFilterKeys(options, "all")).toBeNull();
        expect(driverFilterKeys(options, "")).toBeNull();
    });

    it("matches every past uid of the picked driver — old refuels stay visible after a re-link", () => {
        const keys = driverFilterKeys(options, "uid_new");
        expect(keys?.has("W47P8pGs")).toBe(true);
        expect(keys?.has("drv_1")).toBe(true);
        expect(keys?.has("uid_b")).toBe(false);
    });

    it("still matches the id itself when the option has no keys or is no longer listed", () => {
        expect([...(driverFilterKeys([{ id: "uid_c" }], "uid_c") ?? [])]).toEqual(["uid_c"]);
        expect([...(driverFilterKeys(options, "uid_gone") ?? [])]).toEqual(["uid_gone"]);
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
