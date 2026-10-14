// UI3C (#143): the public copy says only what PRODUCT.md holds evidence for, under one brand. No "Logi-Track",
// "LogisticsPro" or "Logistics Pro"; no "thousands of professionals" or other counts; no "global logistics"; no
// third-party marks or customer names; no links to pages that do not exist; and the footer's company is GORATECH,
// not a former name with a fixed year.
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

import enLanding from "@/context/locales/en/landing";
import thLanding from "@/context/locales/th/landing";
import enSupport from "@/context/locales/en/support";
import thSupport from "@/context/locales/th/support";
import en from "@/context/locales/en";
import th from "@/context/locales/th";
import { WEB_ROOT } from "@/test-utils/importGraph";

const BRAND_VARIANTS = /Logi-Track|LogisticsPro|Logistics Pro|Logi Track/i;
const FABRICATED = /thousands|นับพัน|hundreds of|หลายร้อย|trusted by|ไว้วางใจ|\bleading\b|ชั้นนำ|\bbest\b|ดีที่สุด|\bAI\b|\belite\b|\d+\s*%|\d+\s*\+/i;
const GLOBAL = /global|ระดับโลก|ทั่วโลก|worldwide|international/i;
/** Third-party marks: none is approved for marketing (PRODUCT.md, Evidence on Hand). */
const MARKS = /Shopee|J&T|\bSPX\b|\bSPK\b|Lazada|Kerry|Flash Express|CJSF|\bTTP\b/i;

/** The fleet-size options are a choice, not a claim; `{year}` is computed. */
const CHOICES = /^landing\.join\.form\.(fleet|vehicle)\./;

describe("the public copy (en + th)", () => {
    for (const [lang, dict] of [
        ["en", { ...enLanding, ...enSupport }],
        ["th", { ...thLanding, ...thSupport }],
    ] as const) {
        it(`${lang}: one brand, no fabricated claims, no "global" positioning`, () => {
            for (const [key, value] of Object.entries(dict)) {
                expect(value, key).not.toMatch(BRAND_VARIANTS);
                expect(value, key).not.toMatch(GLOBAL);
                expect(value, key).not.toMatch(MARKS);
                if (!CHOICES.test(key)) expect(value, key).not.toMatch(FABRICATED);
            }
        });
    }

    it("has the same keys in both languages", () => {
        expect(Object.keys(thLanding).sort()).toEqual(Object.keys(enLanding).sort());
        expect(Object.keys(thSupport).sort()).toEqual(Object.keys(enSupport).sort());
    });

    it("leaves no brand variant in any translation of the web", () => {
        for (const [lang, dict] of [
            ["en", en],
            ["th", th],
        ] as const) {
            for (const [key, value] of Object.entries(dict)) expect(value, `${lang} ${key}`).not.toMatch(BRAND_VARIANTS);
        }
    });

    it("names no former company, fixed year, personal address or dead link in the public pages' source", () => {
        const files = [
            "app/page.tsx",
            "app/join-network/page.tsx",
            "app/app/support/page.tsx",
            "app/layout.tsx",
            "components/navigation.tsx",
            ...fs.readdirSync(path.join(WEB_ROOT, "components", "landing")).filter((f) => f.endsWith(".tsx")).map((f) => `components/landing/${f}`),
        ];
        for (const file of files) {
            const src = fs.readFileSync(path.join(WEB_ROOT, file), "utf8");
            expect(src, file).not.toMatch(BRAND_VARIANTS);
            expect(src, file).not.toMatch(/Wanpen|Radchada|WANPEN|© 20\d\d/);
            expect(src, file).not.toMatch(/@gmail\.com/);
            expect(src, file).not.toMatch(/href="#"/);
            // Display weight only: no font-black / font-extrabold on the public pages (DESIGN.md, The Two Weights Rule).
            expect(src, file).not.toMatch(/font-(black|extrabold)/);
            // No shouting kicker (DESIGN.md, The No Shouting Rule).
            expect(src, file).not.toMatch(/uppercase/);
        }
        expect(fs.existsSync(path.join(WEB_ROOT, "app", "support", "page.tsx"))).toBe(false);
        expect(fs.existsSync(path.join(WEB_ROOT, "components", "footer.tsx"))).toBe(false);
    });
});
