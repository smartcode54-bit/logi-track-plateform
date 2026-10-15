import { describe, expect, it } from "vitest";
import en from "@/context/locales/en/subcontractors";
import th from "@/context/locales/th/subcontractors";
import { subcontractorDefaultValues, subcontractorSchema } from "./subcontractorSchema";

describe("partner validation localization", () => {
    it("keeps every English and Thai key in parity", () => {
        expect(Object.keys(en).sort()).toEqual(Object.keys(th).sort());
    });

    const invalid = {
        ...subcontractorDefaultValues, name: " ", contactPerson: "", phone: "", idCardNumber: "123",
        email: "invalid", website: "invalid", fleetSize: -1,
    };

    it("uses locale keys for schema messages", () => {
        const result = subcontractorSchema.safeParse(invalid);
        expect(result.success).toBe(false);
        if (result.success) return;
        expect(result.error.issues).toHaveLength(7);
        for (const issue of result.error.issues) {
            expect(issue.message).toMatch(/^subcontractors\.validation\./);
            expect(Object.keys(en)).toContain(issue.message);
            expect(Object.keys(th)).toContain(issue.message);
        }
    });

    it("validates a company tax ID with a locale key", () => {
        const result = subcontractorSchema.safeParse({ ...subcontractorDefaultValues, type: "company", name: "Partner", contactPerson: "Contact", phone: "0812345678", taxId: "invalid" });
        expect(result.success).toBe(false);
        if (!result.success) expect(result.error.issues[0].message).toBe("subcontractors.validation.taxId");
    });

    it.each([-1, 1.5, "invalid", Infinity, 2147483648])("rejects an invalid fleet size (%s) with a locale key", (fleetSize) => {
        const result = subcontractorSchema.safeParse({ ...subcontractorDefaultValues, name: "Partner", contactPerson: "Contact", phone: "0812345678", idCardNumber: "3100100123451", fleetSize });
        expect(result.success).toBe(false);
        if (!result.success) expect(result.error.issues[0].message).toBe("subcontractors.validation.fleetSize");
    });
});
