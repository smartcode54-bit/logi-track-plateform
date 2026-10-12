// TW7 (issue #87; developer-spec.md §10.13, §10.14 #6 and #8; Appendix E §E.4 row 17): the web holds no
// Firebase SDK, no Firestore page, no callable and no Firebase bridge. The owner allows Firebase in the
// product for Storage and FCM only; the web needs neither (uploads go to Go's presigned or local upload
// routes, §9.4 and §9.11, and the web sends no push), so the `firebase` package is gone entirely. These
// checks keep it gone: a page that imports Firebase again, a callable, or the Firebase public config
// fails here, in `pnpm lint` (eslint.config.mjs) and in `pnpm bundle:check` (scripts/bundle-report.mjs).
import { existsSync, readdirSync, readFileSync, statSync } from "fs";
import path from "path";
import { describe, expect, it } from "vitest";

const root = path.resolve(__dirname, "..");
const SOURCE_DIRS = ["app", "components", "context", "features", "hooks", "lib", "types", "validate", "test-utils"];
const THIS_FILE = path.relative(root, __filename);

function walk(dir: string, out: string[] = []): string[] {
    if (!existsSync(dir)) return out;
    for (const name of readdirSync(dir)) {
        const p = path.join(dir, name);
        if (statSync(p).isDirectory()) {
            if (name !== "node_modules" && name !== ".next") walk(p, out);
        } else if (/\.(m|c)?(j|t)sx?$/.test(name)) out.push(p);
    }
    return out;
}

const files = [...SOURCE_DIRS.flatMap((d) => walk(path.join(root, d))), path.join(root, "proxy.ts"), path.join(root, "next.config.ts")]
    .filter((f) => existsSync(f))
    .map((f) => ({ rel: path.relative(root, f), src: readFileSync(f, "utf8") }))
    .filter((f) => f.rel !== THIS_FILE);
// Tests may name a callable or a flag helper in a negative assertion; the code checks below read sources.
const sources = files.filter((f) => !/(\.test\.|\.spec\.|[\\/]__tests__[\\/])/.test(f.rel));

// Any import of the client SDK: `firebase`, `firebase/*`, `@firebase/*` or the removed `@/firebase/*` modules,
// static, re-exported, lazy or mocked (a `vi.mock` of a Firebase module means a test still expects one).
const FIREBASE_SPEC = /(?:from|import|vi\.mock|require)\s*\(?\s*["'](?:firebase(?:\/[^"']*)?|@firebase\/[^"']*|@\/firebase\/[^"']*)["']/;

describe("the web without Firebase (TW7)", () => {
    it("depends on no Firebase client package", () => {
        const pkg = JSON.parse(readFileSync(path.join(root, "package.json"), "utf8")) as Record<string, Record<string, string> | undefined>;
        for (const field of ["dependencies", "devDependencies", "optionalDependencies", "peerDependencies"]) {
            const names = Object.keys(pkg[field] ?? {});
            expect(names.filter((n) => n === "firebase" || n.startsWith("@firebase/")), field).toEqual([]);
        }
    });

    it("no source or test file imports or mocks a Firebase module (§10.14 #8)", () => {
        expect(files.filter((f) => FIREBASE_SPEC.test(f.src)).map((f) => f.rel)).toEqual([]);
    });

    it("no web code reaches firebase-admin either (it stays a devDependency of the ops scripts only)", () => {
        expect(files.filter((f) => /["']firebase-admin(?:\/[^"']*)?["']/.test(f.src)).map((f) => f.rel)).toEqual([]);
    });

    it("calls no Cloud Function: web-originated callable invocations = 0 (§10.14 #6)", () => {
        expect(sources.filter((f) => /\bhttpsCallable\b|\bgetFunctions\b/.test(f.src)).map((f) => f.rel)).toEqual([]);
    });

    it("names no Firebase or App Check public config (dropped from ENV_*_WEB, §16.1)", () => {
        expect(sources.filter((f) => /NEXT_PUBLIC_(FIREBASE|APP_CHECK)_[A-Z0-9_]*[A-Z0-9]\b/.test(f.src)).map((f) => f.rel)).toEqual([]);
    });

    it("has no Firebase client module, no Firestore page or adapter and no bridge route", () => {
        expect(existsSync(path.join(root, "firebase"))).toBe(false);
        expect(existsSync(path.join(root, "app/api/auth/firebase-token"))).toBe(false);
        expect(existsSync(path.join(root, "lib/firebaseBridge.ts"))).toBe(false);
        const leftovers = walk(path.join(root, "features"))
            .map((f) => path.relative(root, f))
            .filter((f) => /\/legacy\/|\.firestore\.tsx?$/.test(f));
        expect(leftovers).toEqual([]);
    });

    it("switches no page on a web flag: Go is the only source of every domain", () => {
        expect(sources.filter((f) => /\b(DomainPage|useDomainPageSource|useDomainSource|domainQueryFn|domainMutationFn|useWebFlags)\b/.test(f.src)).map((f) => f.rel)).toEqual([]);
        expect(files.filter((f) => f.src.includes("/v1/config/web-flags")).map((f) => f.rel)).toEqual([]);
    });
});
