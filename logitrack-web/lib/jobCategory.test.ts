import { describe, it, expect } from "vitest";
import { jobCategoryFromCell } from "./jobCategory";

describe("jobCategoryFromCell", () => {
    it("reads the Thai words admins actually type", () => {
        expect(jobCategoryFromCell("หลัก")).toBe("PRIMARY");
        expect(jobCategoryFromCell("เสริม")).toBe("SUPPLEMENTARY");
        expect(jobCategoryFromCell("งานเสริม")).toBe("SUPPLEMENTARY");
    });

    it("reads the English words and the enum itself", () => {
        expect(jobCategoryFromCell("Primary")).toBe("PRIMARY");
        expect(jobCategoryFromCell("supplementary")).toBe("SUPPLEMENTARY");
        expect(jobCategoryFromCell("SUPPLEMENTARY")).toBe("SUPPLEMENTARY");
    });

    it("tolerates surrounding whitespace", () => {
        expect(jobCategoryFromCell("  เสริม ")).toBe("SUPPLEMENTARY");
    });

    it("defaults a blank cell to PRIMARY", () => {
        expect(jobCategoryFromCell("")).toBe("PRIMARY");
        expect(jobCategoryFromCell(undefined)).toBe("PRIMARY");
        expect(jobCategoryFromCell(null)).toBe("PRIMARY");
    });

    it("returns undefined rather than guessing", () => {
        // Guessing PRIMARY on a misspelled เสริม would bill the job at the wrong rate.
        expect(jobCategoryFromCell("เสิรม")).toBeUndefined();
        expect(jobCategoryFromCell("secondary")).toBeUndefined();
    });
});
