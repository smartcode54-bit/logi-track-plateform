/**
 * Verifies the Go compensation golden vectors against this TypeScript engine (mv-go T44, developer-spec.md §6.15,
 * §6.16). The vectors live in logitrack-api/testdata/golden/hr and are exported by its export.mjs; the Go port
 * (internal/hr/compute) checks the same files. Each check must return its `want`. The codec mirrors
 * logitrack-api/testdata/golden/billing/codec.mjs.
 */
import { readFileSync, readdirSync } from "fs";
import path from "path";
import { describe, expect, it } from "vitest";
import * as cc from "./compensationCompute";

const DIR = path.resolve(__dirname, "../../logitrack-api/testdata/golden/hr");

type Json = null | boolean | number | string | Json[] | { [k: string]: Json };
interface Check {
    fn: string;
    args: Json[];
    want: Json;
}
interface VectorFile {
    source: string;
    cases: { name: string; line: number; added?: string; checks: Check[] }[];
}

function enc(v: unknown): Json | undefined {
    if (v === undefined) return undefined;
    if (v === null) return null;
    if (typeof v === "number") {
        if (Number.isNaN(v)) return { $num: "NaN" };
        if (v === Infinity) return { $num: "Infinity" };
        if (v === -Infinity) return { $num: "-Infinity" };
        if (Object.is(v, -0)) return { $num: "-0" };
        return v;
    }
    if (v instanceof Date) return { $date: v.toISOString() };
    if (Array.isArray(v)) return v.map((x) => enc(x) ?? null);
    if (typeof v === "object") {
        const o: { [k: string]: Json } = {};
        for (const [k, x] of Object.entries(v as Record<string, unknown>)) {
            const e = enc(x);
            if (e !== undefined) o[k] = e;
        }
        return o;
    }
    return v as Json;
}

function dec(v: Json): unknown {
    if (v === null || typeof v !== "object") return v;
    if (Array.isArray(v)) return v.map(dec);
    if ("$num" in v) return ({ NaN: NaN, Infinity: Infinity, "-Infinity": -Infinity, "-0": -0 } as Record<string, number>)[v.$num as string];
    if ("$date" in v) return new Date(v.$date as string);
    const o: Record<string, unknown> = {};
    for (const [k, x] of Object.entries(v)) o[k] = dec(x);
    return o;
}

/* eslint-disable @typescript-eslint/no-explicit-any -- vector arguments are untyped JSON by design */
const holidayFn = (h: any) => (h && h.never ? () => false : cc.makeHolidayChecker(new Set(h ? h.keys : [])));
const CALL: Record<string, (...a: any[]) => unknown> = {
    roundTHB: (n) => cc.roundTHB(n),
    assignRound: (d) => cc.assignRound(d),
    bangkokDateKey: (d) => cc.bangkokDateKey(d),
    isHoliday: (keys, d) => cc.makeHolidayChecker(new Set(keys))(d),
    computeBasePay: (trips, cfg, holiday) => cc.computeBasePay(trips, cfg, holidayFn(holiday)),
    computeFuelIncentive: (km, refuels, tiers, min) => cc.computeFuelIncentive(km, refuels, tiers, min),
    computeTripVolumeIncentive: (n, tiers) => cc.computeTripVolumeIncentive(n, tiers),
    computeSso: (driver, rule, asOf) => cc.computeSso(driver, rule, asOf),
    computeHelperPay: (n, rate) => cc.computeHelperPay(n, rate),
    helperWindowKey: (d) => cc.helperWindowKey(d),
    eligibleHelperDayKeys: (keys, delivered) => cc.eligibleHelperDayKeys(keys, delivered),
    ageYearsAt: (birth, asOf) => cc.ageYearsAt(birth, asOf),
    applyDeductions: (earnings, sso, penalties) => cc.applyDeductions(earnings, sso, penalties),
};
/* eslint-enable @typescript-eslint/no-explicit-any */

describe("lib/compensationCompute.ts and functions/src/core/compensationCompute.ts", () => {
    it("differ only in their header comment (line 4), so one vector set covers both", () => {
        const read = (p: string) => readFileSync(path.resolve(__dirname, p), "utf8").split("\n").filter((_, i) => i !== 3);
        expect(read("../functions/src/core/compensationCompute.ts")).toEqual(read("./compensationCompute.ts"));
    });
});

const files = readdirSync(DIR).filter((f) => f.endsWith(".json")).sort();

describe("compensation golden vectors", () => {
    it("are present", () => {
        expect(files).toEqual(["characterisation.json", "compensationCompute.json"]);
    });
});

for (const file of files) {
    const vf = JSON.parse(readFileSync(path.join(DIR, file), "utf8")) as VectorFile;
    describe(`${file} (${vf.source})`, () => {
        for (const c of vf.cases) {
            it(c.name, () => {
                c.checks.forEach((k, i) => {
                    const fn = CALL[k.fn];
                    expect(fn, `no TypeScript mapping for ${k.fn}`).toBeTypeOf("function");
                    const got = fn(...k.args.map(dec));
                    expect(enc(got) ?? null, `check ${i} ${k.fn}(${JSON.stringify(k.args).slice(0, 200)})`).toEqual(k.want);
                });
            });
        }
    });
}
