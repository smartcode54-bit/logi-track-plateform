// UI1 (issue #141) AC: one layering scale. The z-index tokens in app/globals.css order the layers
// (raised < sticky < modal < popover < toast < tooltip), and no source file reaches past the scale with an
// arbitrary or bare z-index above 50 (the old 1000+ ladder: dialog 1001/1002, popover 1101, per-dialog
// `z-[1005]` fixes). Shared components also stay calm: no gradient, glassmorphism or oversized shadow.
import { existsSync, readdirSync, readFileSync, statSync } from "fs";
import path from "path";
import { describe, expect, it } from "vitest";

const root = path.resolve(__dirname, "..");
const SOURCE_DIRS = ["app", "components", "context", "features", "hooks", "lib", "types", "validate", "test-utils"];
const THIS_FILE = path.relative(root, __filename).split(path.sep).join("/");
/** Above every layer on purpose: an invisible print iframe appended to <body> for the length of a print. */
const Z_INDEX_ALLOWED = new Set(["lib/print-image-url.ts"]);

function walk(dir: string, out: string[] = []): string[] {
    if (!existsSync(dir)) return out;
    for (const name of readdirSync(dir)) {
        const p = path.join(dir, name);
        if (statSync(p).isDirectory()) {
            if (name !== "node_modules" && name !== ".next") walk(p, out);
        } else if (/\.((m|c)?(j|t)sx?|css)$/.test(name)) out.push(p);
    }
    return out;
}

const files = SOURCE_DIRS.flatMap((d) => walk(path.join(root, d)))
    .map((f) => ({ rel: path.relative(root, f).split(path.sep).join("/"), src: readFileSync(f, "utf8") }))
    .filter((f) => f.rel !== THIS_FILE);

/** `z-[1005]`, `!z-[1003]`, `z-1001` (Tailwind 4 bare values), `-z-[60]`; values of 50 or less pass. */
const Z_CLASS = /(?<![\w-])!?-?z-(?:\[(-?\d+)\]|(\d+))(?![\w-])/g;
/** `zIndex: 1000`, `zIndex: "999"`, `z-index: 1100` in TS objects and CSS. */
const Z_STYLE = /(?:zIndex|z-index)\s*:\s*["'`]?(-?\d+)/g;

function overScale(src: string, re: RegExp): string[] {
    const hits: string[] = [];
    for (const m of src.matchAll(re)) {
        const value = Number(m[1] ?? m[2]);
        if (Math.abs(value) > 50) hits.push(m[0]);
    }
    return hits;
}

describe("one layering scale (UI1)", () => {
    it("globals.css defines the scale in order", () => {
        const css = readFileSync(path.join(root, "app/globals.css"), "utf8");
        const token = (name: string) => {
            const m = css.match(new RegExp(`--z-index-${name}:\\s*(\\d+);`));
            expect(m, `--z-index-${name}`).not.toBeNull();
            return Number(m![1]);
        };
        const order = ["base", "raised", "sticky", "modal", "popover", "toast", "tooltip"].map(token);
        expect([...order].sort((a, b) => a - b)).toEqual(order);
        expect(new Set(order).size).toBe(order.length);
        // The whole scale stays inside the guard below.
        expect(Math.max(...order)).toBeLessThanOrEqual(100);
    });

    it("each overlay primitive sits on its layer", () => {
        const layer = (file: string, cls: string) =>
            expect(readFileSync(path.join(root, file), "utf8"), `${file} uses ${cls}`).toContain(cls);
        layer("components/ui/dialog.tsx", "z-modal");
        layer("components/ui/sheet.tsx", "z-modal");
        layer("components/ui/popover.tsx", "z-popover");
        layer("components/ui/dropdown-menu.tsx", "z-popover");
        layer("components/ui/tooltip.tsx", "z-tooltip");
        layer("app/globals.css", "z-index: var(--z-index-toast)");
    });

    it("no arbitrary or bare z-index class above 50 anywhere in the source", () => {
        const offenders = files
            .map((f) => ({ rel: f.rel, hits: overScale(f.src, Z_CLASS) }))
            .filter((f) => f.hits.length > 0)
            .map((f) => `${f.rel}: ${f.hits.join(", ")}`);
        expect(offenders).toEqual([]);
    });

    it("no inline or CSS z-index above 50 outside the allow-list", () => {
        const offenders = files
            .filter((f) => !Z_INDEX_ALLOWED.has(f.rel))
            .map((f) => ({ rel: f.rel, hits: overScale(f.src, Z_STYLE) }))
            .filter((f) => f.hits.length > 0)
            .map((f) => `${f.rel}: ${f.hits.join(", ")}`);
        expect(offenders).toEqual([]);
    });

    it("each Leaflet map is its own stacking context, so its 400-1000 panes stay under the scale", () => {
        const css = readFileSync(path.join(root, "app/globals.css"), "utf8");
        expect(css).toMatch(/\.leaflet-container\s*\{[^}]*isolation:\s*isolate/);
    });

    it("the guard patterns catch the old ladder", () => {
        expect(overScale(`className="z-[1005]" "!z-[1003]" "z-1001" "z-50" "z-10" "z-modal"`, Z_CLASS)).toEqual(["z-[1005]", "!z-[1003]", "z-1001"]);
        expect(overScale(`style={{ zIndex: 1000 }} .x { z-index: 1100; } { zIndex: "40" }`, Z_STYLE)).toEqual(["zIndex: 1000", "z-index: 1100"]);
    });
});

describe("shared components stay calm (UI1)", () => {
    const shared = files.filter((f) => f.rel.startsWith("components/") && /\.tsx$/.test(f.rel) && !/\.test\.tsx$/.test(f.rel));

    it.each([
        ["a gradient", /\bbg-gradient-|\bbg-linear-|\bbg-radial|\bbg-conic|\b(?:linear|radial|conic)-gradient\(/],
        ["glassmorphism (backdrop-blur)", /\bbackdrop-blur/],
        ["a glow (blur-xl and up)", /(?<![\w-])blur-(?:xl|2xl|3xl)\b/],
        ["an oversized shadow (shadow-xl, shadow-2xl)", /(?<![\w-])shadow-(?:xl|2xl)\b/],
    ])("no shared component uses %s", (_what, re) => {
        expect(shared.filter((f) => re.test(f.src)).map((f) => f.rel)).toEqual([]);
    });
});
