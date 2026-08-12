import { describe, it, expect } from "vitest";
import { resolveJobCategoryWrite } from "./jobCategoryWrite";

describe("resolveJobCategoryWrite (ADR 0010 R2)", () => {
    describe("update (isCreate = false)", () => {
        it("omits (undefined) when the client didn't send a category — leaves stored value untouched", () => {
            // The core fix: an update that doesn't resend jobCategory must NOT reset เสริม → หลัก.
            expect(resolveJobCategoryWrite(undefined, false)).toBeUndefined();
        });

        it("keeps SUPPLEMENTARY when explicitly sent", () => {
            expect(resolveJobCategoryWrite("SUPPLEMENTARY", false)).toBe("SUPPLEMENTARY");
        });

        it("keeps PRIMARY when explicitly sent", () => {
            expect(resolveJobCategoryWrite("PRIMARY", false)).toBe("PRIMARY");
        });
    });

    describe("create (isCreate = true)", () => {
        it("defaults PRIMARY when nothing is sent", () => {
            expect(resolveJobCategoryWrite(undefined, true)).toBe("PRIMARY");
        });

        it("honors SUPPLEMENTARY when sent", () => {
            expect(resolveJobCategoryWrite("SUPPLEMENTARY", true)).toBe("SUPPLEMENTARY");
        });

        it("honors PRIMARY when sent", () => {
            expect(resolveJobCategoryWrite("PRIMARY", true)).toBe("PRIMARY");
        });
    });

    describe("normalization", () => {
        it("normalizes any sent non-SUPPLEMENTARY value to PRIMARY (never omits once the key is present)", () => {
            // A present-but-unrecognized value normalizes to PRIMARY rather than leaving the write blank.
            expect(resolveJobCategoryWrite("primary", false)).toBe("PRIMARY");
            expect(resolveJobCategoryWrite("", false)).toBe("PRIMARY");
            expect(resolveJobCategoryWrite("garbage", true)).toBe("PRIMARY");
        });
    });
});
