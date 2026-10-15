import { describe, expect, it } from "vitest";
import { renewalAmountError } from "./renewalAmount";
describe("Go renewal amount", () => {
    it("accepts zero for completion", () => expect(renewalAmountError("0", true)).toBeNull());
    it("requires an amount only for completion", () => {
        expect(renewalAmountError("", true)).toBe("renewals.form.amountRequired");
        expect(renewalAmountError("", false)).toBeNull();
    });
    it.each(["-1", "1000001", "NaN"])("rejects %s", (amount) => expect(renewalAmountError(amount, true)).toBeTruthy());
    it("requires cents precision", () => expect(renewalAmountError("1.001", true)).toBe("renewals.form.amountPrecision"));
});
