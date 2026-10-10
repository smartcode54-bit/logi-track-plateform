import { describe, expect, it } from "vitest";
import en from "./en";
import th from "./th";
import { LANGUAGES, loadDictionary } from "./load";

describe("translation dictionaries", () => {
    it("have the same keys in English and Thai", () => {
        expect(Object.keys(th).filter((k) => !(k in en)).sort()).toEqual([]);
        expect(Object.keys(en).filter((k) => !(k in th)).sort()).toEqual([]);
    });

    it("hold only string values", () => {
        for (const dictionary of [en, th]) {
            expect(Object.entries(dictionary).filter(([, v]) => typeof v !== "string")).toEqual([]);
        }
    });

    it("load per language through load.ts", async () => {
        expect(LANGUAGES).toEqual(["en", "th"]);
        expect(await loadDictionary("en")).toBe(en);
        expect(await loadDictionary("th")).toBe(th);
    });
});
