import { describe, expect, it } from "vitest";
import en from "@/context/locales/en/trucks";
import th from "@/context/locales/th/trucks";
import masterEn from "@/context/locales/en/masterData";
import masterTh from "@/context/locales/th/masterData";
import renewalsEn from "@/context/locales/en/renewals";
import renewalsTh from "@/context/locales/th/renewals";
import { createTruckSchema, truckDefaultValues } from "@/validate/truckSchema";
describe("fleet demo localization", () => {
    it("keeps full English and Thai key parity", () => {
        expect(Object.keys(en).sort()).toEqual(Object.keys(th).sort());
        expect(Object.keys(renewalsEn).sort()).toEqual(Object.keys(renewalsTh).sort());
    });
    it.each([{ ...en, ...masterEn }, { ...th, ...masterTh }])("renders schema errors in the selected language", (dictionary) => {
        const schema = createTruckSchema((key) => dictionary[key as keyof typeof dictionary] ?? key);
        const result = schema.safeParse(truckDefaultValues);
        expect(result.success).toBe(false);
        if (!result.success) for (const issue of result.error.issues) expect(issue.message).not.toMatch(/^trucks\.|Invalid input/);
    });
});
