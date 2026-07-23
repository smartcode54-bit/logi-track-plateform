import { describe, it, expect } from "vitest";
import {
    PLATE_FILTER_ALL,
    PLATE_FILTER_NONE,
    buildPlateFilterOptions,
    resolveTripPlate,
    rowMatchesPlateFilter,
    truckFilterKey,
} from "./truckPlate";

describe("resolveTripPlate", () => {
    it("prefers the trip's own snapshot over the task plate", () => {
        expect(resolveTripPlate({ tripPlate: "70-1234", taskPlate: "71-5678" })).toBe("70-1234");
    });

    it("falls back to the task plate when the trip has no snapshot", () => {
        expect(resolveTripPlate({ tripPlate: "", taskPlate: "71-5678" })).toBe("71-5678");
        expect(resolveTripPlate({ tripPlate: null, taskPlate: "71-5678" })).toBe("71-5678");
        expect(resolveTripPlate({ taskPlate: "71-5678" })).toBe("71-5678");
    });

    it("returns null rather than inventing a plate (ADR 0005 §6 — never uses activeTruck)", () => {
        expect(resolveTripPlate({})).toBeNull();
        expect(resolveTripPlate({ tripPlate: "", taskPlate: "" })).toBeNull();
        expect(resolveTripPlate({ tripPlate: "   ", taskPlate: null })).toBeNull();
    });
});

describe("truckFilterKey", () => {
    it("keys on truckId when present", () => {
        expect(truckFilterKey({ truckId: "abc123", plate: "70-1234" })).toBe("id:abc123");
    });

    it("falls back to the raw plate string when there is no truckId", () => {
        expect(truckFilterKey({ plate: "70-1234" })).toBe("plate:70-1234");
    });

    it("returns the no-plate bucket when the row has neither", () => {
        expect(truckFilterKey({})).toBe(PLATE_FILTER_NONE);
        expect(truckFilterKey({ truckId: "  ", plate: "  " })).toBe(PLATE_FILTER_NONE);
    });
});

describe("buildPlateFilterOptions", () => {
    it("groups the same truckId under one option even when plate spellings differ", () => {
        const options = buildPlateFilterOptions([
            { truckId: "abc123", plate: "70-1234" },
            { truckId: "abc123", plate: "70-1234 กรุงเทพมหานคร" },
            { truckId: "abc123", plate: "70 - 1234" },
        ]);
        expect(options).toHaveLength(1);
        expect(options[0].value).toBe("id:abc123");
        expect(options[0].count).toBe(3);
        expect(options[0].isOrphan).toBe(false);
    });

    it("does NOT merge two different trucks that share a plate string", () => {
        const options = buildPlateFilterOptions([
            { truckId: "abc123", plate: "70-1234" },
            { truckId: "def456", plate: "70-1234" },
        ]);
        expect(options).toHaveLength(2);
        expect(options.map((o) => o.value).sort()).toEqual(["id:abc123", "id:def456"]);
    });

    it("gives an orphan plate its own reachable option", () => {
        const options = buildPlateFilterOptions([
            { truckId: "abc123", plate: "70-1234" },
            { plate: "70-9999" },
            { plate: "70-9999" },
        ]);
        const orphan = options.find((o) => o.value === "plate:70-9999");
        expect(orphan).toBeDefined();
        expect(orphan?.isOrphan).toBe(true);
        expect(orphan?.count).toBe(2);
    });

    it("collects plateless rows into the no-plate bucket, sorted last", () => {
        const options = buildPlateFilterOptions([
            {},
            { truckId: "abc123", plate: "70-1234" },
            {},
        ]);
        expect(options).toHaveLength(2);
        const none = options[options.length - 1];
        expect(none.value).toBe(PLATE_FILTER_NONE);
        expect(none.label).toBeNull();
        expect(none.count).toBe(2);
    });

    it("recovers a label from a later row when the first row of a truck lacks the plate", () => {
        const options = buildPlateFilterOptions([
            { truckId: "abc123" },
            { truckId: "abc123", plate: "70-1234" },
        ]);
        expect(options).toHaveLength(1);
        expect(options[0].label).toBe("70-1234");
    });

    it("returns an empty list for no rows", () => {
        expect(buildPlateFilterOptions([])).toEqual([]);
    });
});

describe("rowMatchesPlateFilter", () => {
    const withId = { truckId: "abc123", plate: "70-1234" };
    const orphan = { plate: "70-9999" };
    const plateless = {};

    it("matches everything under PLATE_FILTER_ALL", () => {
        expect(rowMatchesPlateFilter(withId, PLATE_FILTER_ALL)).toBe(true);
        expect(rowMatchesPlateFilter(orphan, PLATE_FILTER_ALL)).toBe(true);
        expect(rowMatchesPlateFilter(plateless, PLATE_FILTER_ALL)).toBe(true);
    });

    it("matches by truck identity regardless of plate spelling", () => {
        expect(rowMatchesPlateFilter({ truckId: "abc123", plate: "70 - 1234" }, "id:abc123")).toBe(true);
        expect(rowMatchesPlateFilter({ truckId: "def456", plate: "70-1234" }, "id:abc123")).toBe(false);
    });

    it("only ever matches a plate: selection against rows with no truckId", () => {
        expect(rowMatchesPlateFilter(orphan, "plate:70-9999")).toBe(true);
        // Same plate string, but this row has an identity — it belongs to id:abc123, not the bucket.
        expect(rowMatchesPlateFilter({ truckId: "abc123", plate: "70-9999" }, "plate:70-9999")).toBe(false);
    });

    it("isolates the no-plate bucket", () => {
        expect(rowMatchesPlateFilter(plateless, PLATE_FILTER_NONE)).toBe(true);
        expect(rowMatchesPlateFilter(withId, PLATE_FILTER_NONE)).toBe(false);
    });

    it("treats an empty selection as no filter", () => {
        expect(rowMatchesPlateFilter(withId, "")).toBe(true);
    });
});
