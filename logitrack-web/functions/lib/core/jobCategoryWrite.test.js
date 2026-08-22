"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
const vitest_1 = require("vitest");
const jobCategoryWrite_1 = require("./jobCategoryWrite");
(0, vitest_1.describe)("resolveJobCategoryWrite (ADR 0010 R2)", () => {
    (0, vitest_1.describe)("update (isCreate = false)", () => {
        (0, vitest_1.it)("omits (undefined) when the client didn't send a category — leaves stored value untouched", () => {
            // The core fix: an update that doesn't resend jobCategory must NOT reset เสริม → หลัก.
            (0, vitest_1.expect)((0, jobCategoryWrite_1.resolveJobCategoryWrite)(undefined, false)).toBeUndefined();
        });
        (0, vitest_1.it)("keeps SUPPLEMENTARY when explicitly sent", () => {
            (0, vitest_1.expect)((0, jobCategoryWrite_1.resolveJobCategoryWrite)("SUPPLEMENTARY", false)).toBe("SUPPLEMENTARY");
        });
        (0, vitest_1.it)("keeps PRIMARY when explicitly sent", () => {
            (0, vitest_1.expect)((0, jobCategoryWrite_1.resolveJobCategoryWrite)("PRIMARY", false)).toBe("PRIMARY");
        });
    });
    (0, vitest_1.describe)("create (isCreate = true)", () => {
        (0, vitest_1.it)("defaults PRIMARY when nothing is sent", () => {
            (0, vitest_1.expect)((0, jobCategoryWrite_1.resolveJobCategoryWrite)(undefined, true)).toBe("PRIMARY");
        });
        (0, vitest_1.it)("honors SUPPLEMENTARY when sent", () => {
            (0, vitest_1.expect)((0, jobCategoryWrite_1.resolveJobCategoryWrite)("SUPPLEMENTARY", true)).toBe("SUPPLEMENTARY");
        });
        (0, vitest_1.it)("honors PRIMARY when sent", () => {
            (0, vitest_1.expect)((0, jobCategoryWrite_1.resolveJobCategoryWrite)("PRIMARY", true)).toBe("PRIMARY");
        });
    });
    (0, vitest_1.describe)("normalization", () => {
        (0, vitest_1.it)("normalizes any sent non-SUPPLEMENTARY value to PRIMARY (never omits once the key is present)", () => {
            // A present-but-unrecognized value normalizes to PRIMARY rather than leaving the write blank.
            (0, vitest_1.expect)((0, jobCategoryWrite_1.resolveJobCategoryWrite)("primary", false)).toBe("PRIMARY");
            (0, vitest_1.expect)((0, jobCategoryWrite_1.resolveJobCategoryWrite)("", false)).toBe("PRIMARY");
            (0, vitest_1.expect)((0, jobCategoryWrite_1.resolveJobCategoryWrite)("garbage", true)).toBe("PRIMARY");
        });
    });
});
//# sourceMappingURL=jobCategoryWrite.test.js.map