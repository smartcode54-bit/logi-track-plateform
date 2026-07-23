import { describe, it, expect } from "vitest";
import {
    VEHICLE_CLASS_FILTER_ALL,
    VEHICLE_CLASS_FILTER_NONE,
    resolveVehicleClassKey,
    buildVehicleClassOptions,
    rowMatchesVehicleClass,
} from "./vehicleClass";

describe("resolveVehicleClassKey", () => {
    it("collapses spelling variants of the same class to one key", () => {
        expect(resolveVehicleClassKey("6 Wheels")).toBe("6WH");
        expect(resolveVehicleClassKey("6W")).toBe("6WH");
        expect(resolveVehicleClassKey("6WH")).toBe("6WH");
    });

    it("buckets blank as NONE rather than inventing the billing 4WJ default", () => {
        expect(resolveVehicleClassKey("")).toBe(VEHICLE_CLASS_FILTER_NONE);
        expect(resolveVehicleClassKey("   ")).toBe(VEHICLE_CLASS_FILTER_NONE);
        expect(resolveVehicleClassKey(null)).toBe(VEHICLE_CLASS_FILTER_NONE);
        expect(resolveVehicleClassKey(undefined)).toBe(VEHICLE_CLASS_FILTER_NONE);
    });

    it("keeps an unknown non-blank class as its own key, not an invented known class", () => {
        expect(resolveVehicleClassKey("FORKLIFT")).toBe("FORKLIFT");
    });
});

describe("buildVehicleClassOptions", () => {
    it("dedups by resolved key and counts rows", () => {
        const opts = buildVehicleClassOptions(["6 Wheels", "6WH", "4WJ", "6W"]);
        const six = opts.find((o) => o.value === "6WH");
        const four = opts.find((o) => o.value === "4WJ");
        expect(six?.count).toBe(3);
        expect(four?.count).toBe(1);
    });

    it("sorts the no-class bucket last", () => {
        const opts = buildVehicleClassOptions(["6WH", "", "4WJ"]);
        expect(opts[opts.length - 1].value).toBe(VEHICLE_CLASS_FILTER_NONE);
    });

    it("gives the no-class bucket an empty label for the caller to i18n", () => {
        const opts = buildVehicleClassOptions([null]);
        expect(opts).toHaveLength(1);
        expect(opts[0]).toMatchObject({ value: VEHICLE_CLASS_FILTER_NONE, label: "", count: 1 });
    });
});

describe("rowMatchesVehicleClass", () => {
    it("matches everything under ALL", () => {
        expect(rowMatchesVehicleClass("6WH", VEHICLE_CLASS_FILTER_ALL)).toBe(true);
        expect(rowMatchesVehicleClass(null, VEHICLE_CLASS_FILTER_ALL)).toBe(true);
    });

    it("matches a class regardless of spelling", () => {
        expect(rowMatchesVehicleClass("6 Wheels", "6WH")).toBe(true);
        expect(rowMatchesVehicleClass("4WJ", "6WH")).toBe(false);
    });

    it("matches the no-class bucket only for rows with no class", () => {
        expect(rowMatchesVehicleClass("", VEHICLE_CLASS_FILTER_NONE)).toBe(true);
        expect(rowMatchesVehicleClass("6WH", VEHICLE_CLASS_FILTER_NONE)).toBe(false);
    });
});
