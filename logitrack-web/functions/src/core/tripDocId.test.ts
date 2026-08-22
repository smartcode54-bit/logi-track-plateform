import { describe, it, expect } from "vitest";
import { validateTripDocId } from "./tripDocId";

describe("validateTripDocId", () => {
    it("accepts the trip IDs drivers actually type", () => {
        expect(validateTripDocId("ZXZB26011192271")).toBeNull();
        expect(validateTripDocId("36601950")).toBeNull();
        expect(validateTripDocId("SPX-123_456")).toBeNull();
    });

    it("rejects an empty id", () => {
        expect(validateTripDocId("")).toContain("required");
    });

    it("rejects a slash, which would address a subcollection instead of a sibling doc", () => {
        expect(validateTripDocId("ZXZB/26011192271")).toContain("'/'");
    });

    it("rejects the relative path segments Firestore reserves", () => {
        expect(validateTripDocId(".")).toContain("'.'");
        expect(validateTripDocId("..")).toContain("'.'");
    });

    it("rejects the __reserved__ form but not a plain leading underscore", () => {
        expect(validateTripDocId("__name__")).toContain("double underscores");
        expect(validateTripDocId("__name")).toBeNull();
    });

    it("counts the 1500 limit in bytes, not characters", () => {
        expect(validateTripDocId("A".repeat(1500))).toBeNull();
        expect(validateTripDocId("A".repeat(1501))).toContain("too long");
        // Thai characters are 3 bytes each in UTF-8, so 501 of them already overflow.
        expect(validateTripDocId("ก".repeat(501))).toContain("too long");
    });
});
