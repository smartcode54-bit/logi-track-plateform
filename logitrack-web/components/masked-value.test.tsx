import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

import { MaskedValue } from "./masked-value";
import { maskedMoneyText, maskedValueText } from "@/lib/maskedValue";
import { adaptProjection } from "@/lib/projectionMeta";
import en from "@/context/locales/en/common";
import th from "@/context/locales/th/common";

let language: "en" | "th" = "en";
vi.mock("@/context/language", () => ({ useLanguage: () => ({ t: (key: string) => ((language === "en" ? en : th) as Record<string, string>)[key] }) }));

describe("MaskedValue", () => {
    it.each(["en", "th"] as const)("renders redacted null money with an accessible %s label before formatting", (locale) => {
        language = locale;
        const source = adaptProjection({ data: { amount: null }, meta: { redacted: [{ path: "/data/amount", kind: "money" }] } }).data;
        const format = vi.fn(() => "must not run");
        render(<MaskedValue value={source.amount} source={source} path="amount" format={format} />);
        expect(screen.getByRole("img", { name: (locale === "en" ? en : th)["common.maskedValue"] }).textContent).toBe("****");
        expect(format).not.toHaveBeenCalled();
    });

    it("distinguishes genuine missing values and zero from masked money; never renders NaN", () => {
        expect(maskedMoneyText(null)).toBe("—");
        expect(maskedMoneyText(0)).toBe("฿0");
        expect(maskedMoneyText(1234.5)).toBe("฿1,234.5");
        expect(maskedMoneyText(1234)).toBe("฿1,234");
        expect(maskedValueText("   ", { missing: "No name" })).toBe("No name");
        expect(maskedValueText("", { redacted: true })).toBe("****");
        expect(maskedMoneyText(NaN)).toBe("—");
        expect(maskedValueText("****", { format: () => "raw fallback" })).toBe("****");
        expect(maskedValueText(null, { redacted: true })).toBe("****");
    });
});
