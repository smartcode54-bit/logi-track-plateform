/**
 * Verifies the Go LINE Flex golden vectors against these TypeScript builders (mv-go T49, developer-spec.md §7.6). The
 * vectors live in logitrack-api/testdata/golden/line and are exported by its export.mjs; the Go port
 * (internal/notify/lineflex.go) checks the same file. A card's want is its exact JSON.stringify text, so the Go cards
 * are byte-equal to these. The codec mirrors logitrack-api/testdata/golden/billing/codec.mjs.
 */
import { readFileSync, readdirSync } from "fs";
import path from "path";
import { describe, expect, it } from "vitest";
import * as lm from "./lineMessage";

const DIR = path.resolve(__dirname, "../../logitrack-api/testdata/golden/line");

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

function dec(v: Json): unknown {
    if (v === null || typeof v !== "object") return v;
    if (Array.isArray(v)) return v.map(dec);
    if ("$date" in v) return new Date(v.$date as string);
    const o: Record<string, unknown> = {};
    for (const [k, x] of Object.entries(v)) o[k] = dec(x);
    return o;
}

const CARDS = new Set(["buildCheckinMessage", "buildDeliveredMessage", "buildStandbyMessage"]);

/* eslint-disable @typescript-eslint/no-explicit-any -- vector arguments are untyped JSON by design */
const CALL: Record<string, (...a: any[]) => unknown> = {
    formatBuddhistShortDate: (d) => lm.formatBuddhistShortDate(d),
    formatBangkokHm: (d) => lm.formatBangkokHm(d),
    resolveDriverNameTh: (d, fallback) => lm.resolveDriverNameTh(d, fallback ?? undefined),
    resolveDriverCustomerCode: (d, code) => lm.resolveDriverCustomerCode(d, code),
    buildCheckinMessage: (ctx) => lm.buildCheckinMessage(ctx),
    buildDeliveredMessage: (ctx) => lm.buildDeliveredMessage(ctx),
    buildStandbyMessage: (ctx) => lm.buildStandbyMessage(ctx),
    buildDelayNote: (keys) => lm.buildDelayNote(keys),
};
/* eslint-enable @typescript-eslint/no-explicit-any */

describe("lib/lineMessage.ts and functions/src/core/lineMessage.ts", () => {
    it("are one module: equal after their header comment, so one vector set covers both", () => {
        const body = (p: string) => {
            const src = readFileSync(path.resolve(__dirname, p), "utf8");
            return src.slice(src.indexOf("*/"));
        };
        expect(body("../functions/src/core/lineMessage.ts")).toEqual(body("./lineMessage.ts"));
    });
});

const files = readdirSync(DIR).filter((f) => f.endsWith(".json")).sort();

describe("LINE Flex golden vectors", () => {
    it("are present", () => {
        expect(files).toEqual(["lineMessage.json"]);
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
                    const value = CARDS.has(k.fn) ? JSON.stringify(got) : got;
                    expect(value, `check ${i} ${k.fn}(${JSON.stringify(k.args).slice(0, 200)})`).toEqual(k.want);
                });
            });
        }
    });
}
