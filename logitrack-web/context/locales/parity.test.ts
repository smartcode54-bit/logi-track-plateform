import { describe, expect, it } from "vitest";
import en from "./en";
import th from "./th";
import enAccounting from "./en/accounting";
import thAccounting from "./th/accounting";
import enDriverMonitor from "./en/driverMonitor";
import thDriverMonitor from "./th/driverMonitor";
import { LANGUAGES, loadDictionary, loadNamespace, SPLIT_NAMESPACES } from "./load";

// Every key of a language: the base dictionary plus the namespaces loaded per route group (TW4).
const enAll = { ...en, ...enAccounting, ...enDriverMonitor };
const thAll = { ...th, ...thAccounting, ...thDriverMonitor };

describe("translation dictionaries", () => {
    it("have the same keys in English and Thai", () => {
        expect(Object.keys(thAll).filter((k) => !(k in enAll)).sort()).toEqual([]);
        expect(Object.keys(enAll).filter((k) => !(k in thAll)).sort()).toEqual([]);
    });

    it("hold only string values", () => {
        for (const dictionary of [enAll, thAll]) {
            expect(Object.entries(dictionary).filter(([, v]) => typeof v !== "string")).toEqual([]);
        }
    });

    it("load per language through load.ts", async () => {
        expect(LANGUAGES).toEqual(["en", "th"]);
        expect(await loadDictionary("en")).toBe(en);
        expect(await loadDictionary("th")).toBe(th);
    });

    it("load the split namespaces per language through load.ts", async () => {
        expect(SPLIT_NAMESPACES).toEqual(["accounting", "driverMonitor"]);
        expect(await loadNamespace("en", "accounting")).toBe(enAccounting);
        expect(await loadNamespace("th", "accounting")).toBe(thAccounting);
        expect(await loadNamespace("en", "driverMonitor")).toBe(enDriverMonitor);
        expect(await loadNamespace("th", "driverMonitor")).toBe(thDriverMonitor);
    });
});
