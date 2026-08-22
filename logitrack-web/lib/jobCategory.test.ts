import { describe, it, expect } from "vitest";
import { jobCategoryFromCell, resolveDisplayJobCategory } from "./jobCategory";

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

describe("resolveDisplayJobCategory (ADR 0010 R2)", () => {
    it("prefers the trip's own value over the task", () => {
        // setTripJobCategory corrections land on the trip; they must win immediately.
        expect(resolveDisplayJobCategory("SUPPLEMENTARY", "PRIMARY")).toBe("SUPPLEMENTARY");
        expect(resolveDisplayJobCategory("PRIMARY", "SUPPLEMENTARY")).toBe("PRIMARY");
    });

    it("falls back to the task when the trip cache was never written", () => {
        // The billing bridge early-returns on "No rate" and older trips predate the seed.
        expect(resolveDisplayJobCategory(undefined, "SUPPLEMENTARY")).toBe("SUPPLEMENTARY");
        expect(resolveDisplayJobCategory(null, "PRIMARY")).toBe("PRIMARY");
    });

    it("returns undefined when neither side carries a value", () => {
        // Callers render the loud marker; defaulting to หลัก would mis-bill a เสริม trip.
        expect(resolveDisplayJobCategory(undefined, undefined)).toBeUndefined();
        expect(resolveDisplayJobCategory(undefined)).toBeUndefined();
    });

    it("does not coerce near-miss spellings from either side", () => {
        expect(resolveDisplayJobCategory("primary")).toBeUndefined();
        expect(resolveDisplayJobCategory("", "supplementary")).toBeUndefined();
        expect(resolveDisplayJobCategory("เสริม")).toBeUndefined();
    });
});
