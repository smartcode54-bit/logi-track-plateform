"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
const vitest_1 = require("vitest");
const tripDocId_1 = require("./tripDocId");
(0, vitest_1.describe)("validateTripDocId", () => {
    (0, vitest_1.it)("accepts the trip IDs drivers actually type", () => {
        (0, vitest_1.expect)((0, tripDocId_1.validateTripDocId)("ZXZB26011192271")).toBeNull();
        (0, vitest_1.expect)((0, tripDocId_1.validateTripDocId)("36601950")).toBeNull();
        (0, vitest_1.expect)((0, tripDocId_1.validateTripDocId)("SPX-123_456")).toBeNull();
    });
    (0, vitest_1.it)("rejects an empty id", () => {
        (0, vitest_1.expect)((0, tripDocId_1.validateTripDocId)("")).toContain("required");
    });
    (0, vitest_1.it)("rejects a slash, which would address a subcollection instead of a sibling doc", () => {
        (0, vitest_1.expect)((0, tripDocId_1.validateTripDocId)("ZXZB/26011192271")).toContain("'/'");
    });
    (0, vitest_1.it)("rejects the relative path segments Firestore reserves", () => {
        (0, vitest_1.expect)((0, tripDocId_1.validateTripDocId)(".")).toContain("'.'");
        (0, vitest_1.expect)((0, tripDocId_1.validateTripDocId)("..")).toContain("'.'");
    });
    (0, vitest_1.it)("rejects the __reserved__ form but not a plain leading underscore", () => {
        (0, vitest_1.expect)((0, tripDocId_1.validateTripDocId)("__name__")).toContain("double underscores");
        (0, vitest_1.expect)((0, tripDocId_1.validateTripDocId)("__name")).toBeNull();
    });
    (0, vitest_1.it)("counts the 1500 limit in bytes, not characters", () => {
        (0, vitest_1.expect)((0, tripDocId_1.validateTripDocId)("A".repeat(1500))).toBeNull();
        (0, vitest_1.expect)((0, tripDocId_1.validateTripDocId)("A".repeat(1501))).toContain("too long");
        // Thai characters are 3 bytes each in UTF-8, so 501 of them already overflow.
        (0, vitest_1.expect)((0, tripDocId_1.validateTripDocId)("ก".repeat(501))).toContain("too long");
    });
});
//# sourceMappingURL=tripDocId.test.js.map