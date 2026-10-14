// UI8 (issue #184) acceptance "no web page reads every page of GET /v1/customers": the `['customers','options']`
// directory of T25 (`customerOptionsQuery`, `useCustomerOptions`, `fetchCustomerOptions`) is gone; pickers search
// on the server, names come by id (`['customers','ref',id]`, `GET /v1/customers?ids=`), codes by search
// (`fetchCustomersByCode`). Likewise the truck-assignment counter reads `meta.statusCounts`, not
// `['trucks','options']`. This keeps both from coming back in the app's source (comments aside); the runtime side
// is pinned by the page tests (Income, rate card, sources, truck assignment, the driver forms' saves).
import { readdirSync, readFileSync, statSync } from "fs";
import path from "path";
import { describe, expect, it } from "vitest";

const root = path.resolve(__dirname, "../../..");

function walk(dir: string): string[] {
    return readdirSync(dir).flatMap((name) => {
        const f = path.join(dir, name);
        if (statSync(f).isDirectory()) return name === "node_modules" || name.startsWith(".") ? [] : walk(f);
        return /\.(ts|tsx)$/.test(name) && !/\.test\.(ts|tsx)$/.test(name) ? [f] : [];
    });
}

/** Source without comments (a comment may name what the code no longer does). */
const code = (file: string) =>
    readFileSync(file, "utf8")
        .replace(/\/\*[\s\S]*?\*\//g, "")
        .replace(/^\s*\/\/.*$/gm, "");

const sources = ["app", "components", "context", "features", "hooks", "lib"].flatMap((d) => walk(path.join(root, d)));

describe("no read of every customer (UI8)", () => {
    it("the customer directory and the truck options list are gone from the app", () => {
        const offenders = sources.flatMap((f) => {
            const src = code(f);
            return [
                /\bcustomerOptionsQuery\b/,
                /\buseCustomerOptions\b/,
                /\bfetchCustomerOptions\b/,
                /\bcustomers\.options\s*\(/,
                /\btruckOptionsQuery\b/,
                /\buseTruckOptions\b/,
                /\btrucks\.options\s*\(/,
            ]
                .filter((rx) => rx.test(src))
                .map((rx) => `${path.relative(root, f)}: ${rx.source}`);
        });
        expect(offenders).toEqual([]);
    });

    it("no module walks the pages of GET /v1/customers", () => {
        const walkers = sources.filter((f) => /fetchAllPages\s*(<[^>]*>)?\s*\(\s*(CUSTOMERS_PATH|["'`]\/v1\/customers["'`])/.test(code(f)));
        expect(walkers.map((f) => path.relative(root, f))).toEqual([]);
    });
});
