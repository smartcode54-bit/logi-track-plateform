/**
 * Verifies the Go billing golden vectors against this TypeScript engine (mv-go, developer-spec.md
 * §6.16). The vectors live in logitrack-api/testdata/golden/billing and are exported by its
 * export.mjs; the Go port checks the same files. Each check must return its `want`, or — where the
 * Go contract deliberately differs (R15, R16, R19, R20) — its `legacy` value. The codec mirrors
 * logitrack-api/testdata/golden/billing/codec.mjs.
 */
import { readFileSync, readdirSync } from "fs";
import path from "path";
import { describe, expect, it } from "vitest";
import * as bc from "./billingCompute";
import * as bd from "./billingDate";
import * as doc from "./billingDocument";
import { computeTripBilling } from "./billingRates";
import { jobCategoryFromCell, resolveDisplayJobCategory } from "./jobCategory";
import { BillingPeriodLocks, bangkokYearMonth, billingPeriodKey, type LockedPeriod } from "../functions/src/core/billingPeriodLock";
import type { Task } from "@/validate/taskSchema";
import type { TripRecord } from "@/validate/tripRecordSchema";

const DIR = path.resolve(__dirname, "../../logitrack-api/testdata/golden/billing");

type Json = null | boolean | number | string | Json[] | { [k: string]: Json };
interface Check {
    fn: string;
    args: Json[];
    want: Json;
    legacy?: Json;
    divergence?: string;
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
    if ("$local" in v) {
        const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})$/.exec(v.$local as string);
        if (!m) throw new Error(`bad $local ${String(v.$local)}`);
        return new Date(+m[1], +m[2] - 1, +m[3], +m[4], +m[5], +m[6], 0);
    }
    const o: Record<string, unknown> = {};
    for (const [k, x] of Object.entries(v)) o[k] = dec(x);
    return o;
}

/* eslint-disable @typescript-eslint/no-explicit-any -- vector arguments are untyped JSON by design */
const idOf = (x: { id: string } | null) => (x ? x.id : null);
const CALL: Record<string, (...a: any[]) => unknown> = {
    normalizeVehicleClass: (v) => bc.normalizeVehicleClass(v),
    extractHubId: (v) => bc.extractHubId(v),
    normalizeDestinationCode: (v) => bc.normalizeDestinationCode(v),
    computeFinalRateThb: (b, m, a) => bc.computeFinalRateThb(b, m, a),
    fuelBandFloor: (p) => bc.fuelBandFloor(p),
    fuelBandRange: (p) => bc.fuelBandRange(p),
    computeFuelSurchargeThb: (p, b, k) => bc.computeFuelSurchargeThb(p, b, k),
    bangkokDateStrFromMillis: (ms) => bc.bangkokDateStrFromMillis(ms),
    isEffectiveOnOrBeforeBillingDate: (e, b) => bc.isEffectiveOnOrBeforeBillingDate(e, b),
    selectBillingRateEntry: (c, h, d, v, b, e, cat) => idOf(bc.selectBillingRateEntry(c, h, d, v, b, e, cat ?? undefined)),
    selectFuelAdjustmentForBillingDate: (c, b, a) => idOf(bc.selectFuelAdjustmentForBillingDate(c, b, a)),
    selectStandbyRateEntry: (c, b, r) => idOf(bc.selectStandbyRateEntry(c, b, r)),
    resolveBillingRoundProvenance: (ms, adj) => bc.resolveBillingRoundProvenance(ms, adj),
    computeTripBillingFromParts: (trip, task, e, f, cat) => bc.computeTripBillingFromParts(trip, task, e, f, cat ?? undefined),
    computeMultiDeliveryBilling: (trip, task, stops, vc, e, f, fee, cat) =>
        bc.computeMultiDeliveryBilling(trip, task, stops, vc, e, f, fee ?? undefined, cat ?? undefined),
    computeStandbyBilling: (ms, c, r) => bc.computeStandbyBilling(ms, c, r),
    getTripBillingDateMs: (trip) => bc.getTripBillingDateMs(trip),
    resolveTaskCustomerId: (task) => bc.resolveTaskCustomerId(task),
    snapshotCarriesFuel: (s) => bc.snapshotCarriesFuel(s),
    isFrozenBillingSnapshot: (s) => bc.isFrozenBillingSnapshot(s),
    computeTripBilling: (trip, task, rates, fuel) => computeTripBilling(trip as TripRecord, task as Task, rates, fuel),
    bangkokMidnightFromDateStr: (s) => bd.bangkokMidnightFromDateStr(s),
    bangkokDateStr: (d) => bd.bangkokDateStr(d),
    "bangkokDateStr(bangkokMidnightFromDateStr)": (s) => bd.bangkokDateStr(bd.bangkokMidnightFromDateStr(s)!),
    msBeforeUtcMidnight: (s: string) => {
        const [y, m, d] = s.split("-").map(Number);
        return Date.UTC(y, m - 1, d) - bd.bangkokMidnightFromDateStr(s)!.getTime();
    },
    pickedDateToDateStr: (d) => bd.pickedDateToDateStr(d),
    bangkokMidnightFromPickedDate: (d) => bd.bangkokMidnightFromPickedDate(d),
    "bangkokDateStr(bangkokMidnightFromPickedDate)": (d) => bd.bangkokDateStr(bd.bangkokMidnightFromPickedDate(d)),
    billingPeriodKey: (c, y, m) => billingPeriodKey(c, y, m),
    bangkokYearMonth: (ms) => bangkokYearMonth(ms),
    lockFor: (locks: LockedPeriod[], c, ms) => new BillingPeriodLocks(locks).lockFor(c ?? undefined, ms),
    collectBillingRounds: (rows) => doc.collectBillingRounds(rows),
    billingAxisDate: (row) => doc.billingAxisDate(row) ?? null,
    billingDateBasisOf: (rows) => doc.billingDateBasisOf(rows),
    formatFuelBand: (l, u) => doc.formatFuelBand(l ?? undefined, u ?? undefined),
    groupToLineItems: (rows, roundRows) =>
        roundRows === null ? doc.groupToLineItems(rows) : doc.groupToLineItems(rows, doc.collectBillingRounds(roundRows)),
    jobCategoryFromCell: (cell) => jobCategoryFromCell(cell),
    resolveDisplayJobCategory: (trip, task) => resolveDisplayJobCategory(trip, task),
    "Math.round": (x) => Math.round(x),
    round2: (x) => Math.round(x * 100) / 100,
    toFixed: (x: number, digits: number) => x.toFixed(digits),
    withholdingThb: (total, rate) => Math.round(total * rate * 100) / 100, // lib/billingDocument.ts:365
};
/* eslint-enable @typescript-eslint/no-explicit-any */

describe("lib/billingCompute.ts and functions/src/core/billingCompute.ts", () => {
    it("differ only in their header comment (line 3), so one vector set covers both", () => {
        const read = (p: string) => readFileSync(path.resolve(__dirname, p), "utf8").split("\n").filter((_, i) => i !== 2);
        expect(read("../functions/src/core/billingCompute.ts")).toEqual(read("./billingCompute.ts"));
    });
});

const files = readdirSync(DIR).filter((f) => f.endsWith(".json")).sort();

describe("golden vectors", () => {
    it("are present", () => {
        expect(files).toEqual([
            "billingCompute.json",
            "billingDate.json",
            "billingDocument.json",
            "billingPeriodLock.json",
            "billingRates.json",
            "characterisation.json",
            "jobCategory.json",
        ]);
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
                    const args = k.args.map(dec);
                    const before = Date.now();
                    const got = fn(...args);
                    const after = Date.now();
                    const label = `check ${i} ${k.fn}(${JSON.stringify(k.args).slice(0, 200)})`;
                    const expected = k.legacy !== undefined ? k.legacy : k.want;
                    if (expected !== null && typeof expected === "object" && !Array.isArray(expected) && expected.$now === true) {
                        expect(typeof got, label).toBe("number");
                        expect(got as number, label).toBeGreaterThanOrEqual(before);
                        expect(got as number, label).toBeLessThanOrEqual(after);
                        return;
                    }
                    expect(enc(got) ?? null, label).toEqual(expected);
                });
            });
        }
    });
}
