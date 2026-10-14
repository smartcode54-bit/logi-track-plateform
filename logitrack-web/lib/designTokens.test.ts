// UI1 (issue #141) AC: DESIGN.md matches the tokens in app/globals.css. The frontmatter colours, radius scale,
// spacing steps and the layering table of DESIGN.md are read back and compared with the CSS, so a token change
// that skips DESIGN.md (or the reverse) fails here.
import { readFileSync } from "fs";
import path from "path";
import { describe, expect, it } from "vitest";

const root = path.resolve(__dirname, "..");
const css = readFileSync(path.join(root, "app/globals.css"), "utf8");
const design = readFileSync(path.join(root, "DESIGN.md"), "utf8");
const frontmatter = design.split(/^---$/m)[1] ?? "";

/** `key: "value"` pairs of one top-level frontmatter block (`colors:`, `rounded:`, `spacing:`). */
function block(name: string): Record<string, string> {
    const m = frontmatter.match(new RegExp(`^${name}:\\n((?:  .*\\n)+)`, "m"));
    expect(m, `${name}: in DESIGN.md`).not.toBeNull();
    const out: Record<string, string> = {};
    for (const line of m![1].split("\n")) {
        const kv = line.match(/^ {2}([\w-]+):\s*"([^"]+)"/);
        if (kv) out[kv[1]] = kv[2];
    }
    return out;
}

/** The value of a custom property in one of the globals.css blocks (`:root`, `.dark`, `@theme static`). */
function cssVar(scope: ":root" | ".dark" | "@theme static", name: string): string {
    const start = css.indexOf(`${scope} {`);
    expect(start, scope).toBeGreaterThanOrEqual(0);
    const body = css.slice(start, css.indexOf("\n}", start));
    const m = body.match(new RegExp(`--${name}:\\s*([^;]+);`));
    expect(m, `--${name} in ${scope}`).not.toBeNull();
    return m![1].trim();
}

describe("DESIGN.md matches app/globals.css (UI1)", () => {
    it("every frontmatter colour is a token value of the light or dark theme", () => {
        const light: Record<string, string> = {
            "route-blue": "primary",
            "on-route-blue": "primary-foreground",
            paper: "background",
            ink: "foreground",
            "ink-soft": "secondary-foreground",
            mist: "muted",
            graphite: "muted-foreground",
            hairline: "border",
            "field-edge": "input",
            "control-edge": "control-edge",
            "sidebar-paper": "sidebar",
            "signal-red": "destructive",
            "delivered-green": "success",
            "caution-amber": "warning",
        };
        const dark: Record<string, string> = {
            "route-blue-night": "primary",
            "on-route-blue-night": "primary-foreground",
            night: "background",
            "night-surface": "card",
            "night-mist": "muted",
            "night-graphite": "muted-foreground",
            "night-hairline": "border",
            "night-field-edge": "input",
            "night-control-edge": "control-edge",
            "signal-red-night": "destructive",
            "delivered-green-night": "success",
            "caution-amber-night": "warning",
        };
        const colors = block("colors");
        expect(Object.keys(colors).sort()).toEqual([...Object.keys(light), ...Object.keys(dark)].sort());
        for (const [key, token] of Object.entries(light)) expect(colors[key], key).toBe(cssVar(":root", token));
        for (const [key, token] of Object.entries(dark)) expect(colors[key], key).toBe(cssVar(".dark", token));
        // The ring follows the accent in both themes.
        expect(cssVar(":root", "ring")).toBe(cssVar(":root", "primary"));
        expect(cssVar(".dark", "ring")).toBe(cssVar(".dark", "primary"));
    });

    it("the radius scale is the one --radius produces", () => {
        const base = Number(cssVar(":root", "radius").replace("rem", "")) * 16;
        const rounded = block("rounded");
        expect(rounded).toMatchObject({ sm: `${base - 4}px`, md: `${base - 2}px`, lg: `${base}px`, xl: `${base + 4}px` });
    });

    it("the named spacing steps are the --spacing tokens", () => {
        const spacing = block("spacing");
        for (const step of ["gutter", "stack", "section"]) {
            const rem = Number(cssVar("@theme static", `spacing-${step}`).replace("rem", ""));
            expect(spacing[step], step).toBe(`${rem * 16}px`);
        }
    });

    it("the layering table lists the z-index tokens with their values", () => {
        const rows = [...design.matchAll(/^\| `z-([a-z]+)` \| (\d+) \|/gm)].map((m) => [m[1], Number(m[2])] as const);
        const tokens = [...css.matchAll(/--z-index-([a-z]+):\s*(\d+);/g)].map((m) => [m[1], Number(m[2])] as const);
        expect(rows).toEqual(tokens);
    });

    it("the type pairing is Geist + Sarabun with Geist Mono", () => {
        expect(css).toMatch(/--font-sans:\s*var\(--font-geist-sans\),\s*var\(--font-sarabun\)/);
        expect(css).toMatch(/--font-mono:\s*var\(--font-geist-mono\)/);
        expect(css).not.toMatch(/--font-display/);
        expect(design).toContain("Geist (Latin) with Sarabun for Thai");
    });

    it("carries the picker rule of UI2", () => {
        expect(design).toContain("Every pick-from-a-list control uses the shared SearchSelect");
        expect(design).toContain("A fixed list of 5 options or fewer (e.g. a status filter) may use a");
    });
});
