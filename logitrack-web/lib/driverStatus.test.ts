import { describe, it, expect } from "vitest";
import { isDriverActive, selectableDrivers } from "./driverStatus";

describe("isDriverActive", () => {
    it("treats Active, On-Duty and legacy Available as active, any case", () => {
        expect(isDriverActive({ status: "Active" })).toBe(true);
        expect(isDriverActive({ status: "On-Duty" })).toBe(true);
        expect(isDriverActive({ status: "available" })).toBe(true);
        expect(isDriverActive({ status: " ACTIVE " })).toBe(true);
    });

    it("treats a missing status as active (schema default)", () => {
        expect(isDriverActive({})).toBe(true);
        expect(isDriverActive({ status: "" })).toBe(true);
        expect(isDriverActive({ status: null })).toBe(true);
    });

    it("treats Inactive and other statuses as not active", () => {
        expect(isDriverActive({ status: "Inactive" })).toBe(false);
        expect(isDriverActive({ status: "inactive" })).toBe(false);
        expect(isDriverActive({ status: "Terminated" })).toBe(false);
    });
});

describe("selectableDrivers", () => {
    const drivers = [
        { id: "d1", authId: "u1", status: "Active" },
        { id: "d2", authId: "u2", status: "Inactive" },
        { id: "d3", authId: "u3", status: "Inactive" },
        { id: "d4", status: "On-Duty" },
    ];
    const ids = (list: { id: string }[]) => list.map((d) => d.id);

    it("drops inactive drivers", () => {
        expect(ids(selectableDrivers(drivers))).toEqual(["d1", "d4"]);
    });

    it("keeps an inactive driver the record already holds, by doc id or Auth UID", () => {
        expect(ids(selectableDrivers(drivers, ["d2"]))).toEqual(["d1", "d2", "d4"]);
        expect(ids(selectableDrivers(drivers, ["u3"]))).toEqual(["d1", "d3", "d4"]);
    });

    it("ignores blank keep ids", () => {
        expect(ids(selectableDrivers(drivers, ["", null, undefined, "  "]))).toEqual(["d1", "d4"]);
    });
});
