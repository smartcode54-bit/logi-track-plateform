// @vitest-environment node
// UI3D (#143): the shell owns the page frame. Every `/app` page names itself through one PageHeader (the only `<h1>` of
// the console, Headline 24/600), pads nothing itself (the shell's `p-gutter` is the one left edge), names its icon-only
// buttons, and never hides a missing translation behind an English fallback.
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

import { WEB_ROOT, resolveImport } from "@/test-utils/importGraph";

const APP_DIR = path.join(WEB_ROOT, "app", "app");
const PAGE_HEADER = path.join(WEB_ROOT, "components", "page-header.tsx");

/** Source without comments (doc comments quote markup they do not render); line numbers are kept. */
function code(file: string): string {
    const blank = (comment: string) => comment.replace(/[^\n]/g, "");
    return fs
        .readFileSync(file, "utf8")
        .replace(/\{\/\*[\s\S]*?\*\/\}/g, blank)
        .replace(/\/\*[\s\S]*?\*\//g, blank)
        .replace(/^\s*\/\/.*$/gm, "");
}

/** Every page of the `/app` group, its not-found and its error boundary. */
function entries(): string[] {
    const out: string[] = [];
    const walk = (dir: string) => {
        for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
            const p = path.join(dir, e.name);
            if (e.isDirectory()) walk(p);
            else if (["page.tsx", "not-found.tsx", "error.tsx"].includes(e.name)) out.push(p);
        }
    };
    walk(APP_DIR);
    return out.sort();
}

const IMPORT_RX = /(?:import|export)\s+(?:type\s+)?(?:[^'";]*?\s+from\s+)?["']([^"']+)["']|import\(\s*["']([^"']+)["']\s*\)/g;

/** Every module `entry` reaches through static and lazy imports, inside the web app (tsx only). */
function reachable(entry: string, seen = new Set<string>()): Set<string> {
    if (seen.has(entry)) return seen;
    seen.add(entry);
    for (const m of code(entry).matchAll(IMPORT_RX)) {
        const next = resolveImport(entry, m[1] ?? m[2]);
        if (next && next.endsWith(".tsx") && !next.includes(`${path.sep}node_modules${path.sep}`)) reachable(next, seen);
    }
    return seen;
}

const rel = (file: string) => path.relative(WEB_ROOT, file);

describe("one page header (UI3D)", () => {
    const pages = entries();
    const graph = new Set<string>();
    for (const page of pages) for (const file of reachable(page)) graph.add(file);

    it("walks the whole console", () => {
        expect(pages.length).toBeGreaterThan(60);
        expect(graph.size).toBeGreaterThan(200);
        expect(graph.has(PAGE_HEADER)).toBe(true);
    });

    it("renders no <h1> outside PageHeader, and none in the old 30 px / 700 style", () => {
        const offenders = [...graph].filter((file) => file !== PAGE_HEADER && /<h1\b/.test(code(file))).map(rel);
        expect(offenders).toEqual([]);
        expect(code(PAGE_HEADER)).toMatch(/<h1 className="[^"]*\btext-2xl font-semibold tracking-tight\b/);
        expect(code(PAGE_HEADER)).not.toMatch(/text-3xl|font-bold/);
    });

    it("gives every page a PageHeader (redirects and the not-found catch-all aside)", () => {
        // Pages that render no page of their own: redirects, the catch-all (notFound()), and the chat opener, which
        // replaces itself with the room as soon as Go answers (its error state keeps the way back).
        const noHeader = new Set(["page.tsx", "users/page.tsx", "operations/roles/page.tsx", "[...missing]/page.tsx", "chat/with-driver/page.tsx"]);
        const missing = pages
            .filter((page) => !noHeader.has(path.relative(APP_DIR, page).split(path.sep).join("/")))
            .filter((page) => ![...reachable(page)].some((file) => /<PageHeader\b/.test(code(file))))
            .map(rel);
        expect(missing).toEqual([]);
    });

    it("leaves the page's outer frame to the shell: the element around a PageHeader adds no padding or centring", () => {
        // The chat console is a three-column app whose columns pad themselves; the wizards' sticky step bar keeps a
        // bottom padding above its border.
        const columns = new Set(["features/chat/components/ChatHomeGo.tsx", "features/chat/components/ChatRoomGo.tsx", "features/chat/components/BroadcastComposerGo.tsx"]);
        const problems: string[] = [];
        for (const file of graph) {
            if (columns.has(rel(file))) continue;
            const lines = code(file).split("\n");
            lines.forEach((line, i) => {
                if (!/<PageHeader\b/.test(line)) return;
                for (let j = i - 1; j >= Math.max(0, i - 4); j--) {
                    const m = /<(?:div|main|section)\b[^>]*className="([^"]*)"/.exec(lines[j]);
                    if (!m) continue;
                    const classes = m[1].split(/\s+/).filter((c) => c !== "pb-4");
                    const bad = classes.filter((c) => /^(container|mx-auto|(md:|lg:)?p[xyt]?-\d+|min-h-screen)$/.test(c));
                    if (bad.length > 0) problems.push(`${rel(file)}:${j + 1} ${bad.join(" ")}`);
                    break;
                }
            });
        }
        expect(problems).toEqual([]);
        // The shell's own gutter is the DESIGN.md token.
        expect(code(path.join(APP_DIR, "layout.tsx"))).toMatch(/<main[^>]*className="[^"]*\bp-gutter\b/);
    });
});

describe("named controls and real translations (UI3D)", () => {
    const SOURCES = ["app", "features", "components"].map((d) => path.join(WEB_ROOT, d));
    const files: string[] = [];
    const walk = (dir: string) => {
        for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
            const p = path.join(dir, e.name);
            if (e.isDirectory()) {
                if (e.name !== "node_modules" && e.name !== "__tests__") walk(p);
            } else if (e.name.endsWith(".tsx") && !e.name.includes(".test.")) files.push(p);
        }
    };
    SOURCES.forEach(walk);

    it("never covers a missing key with an English fallback (`t(\"x\") || \"…\"` showed the raw key: customers.form.uploadLogo)", () => {
        const hits = files.filter((file) => /\bt\(\s*["'`][^"'`]+["'`]\s*\)\s*\|\|\s*["'`]/.test(code(file))).map(rel);
        expect(hits).toEqual([]);
    });

    /** The `<Button …>…</Button>` and `<button …>…</button>` elements of a source, with their opening tag and body. */
    function buttons(src: string): { line: number; opening: string; body: string }[] {
        const out: { line: number; opening: string; body: string }[] = [];
        for (const tag of ["Button", "button"]) {
            for (const m of src.matchAll(new RegExp(`<${tag}\\b`, "g"))) {
                let j = (m.index ?? 0) + m[0].length;
                let depth = 0;
                for (; j < src.length; j++) {
                    if (src[j] === "{") depth++;
                    else if (src[j] === "}") depth--;
                    else if (src[j] === ">" && depth === 0) break;
                }
                const opening = src.slice(m.index, j + 1);
                const line = src.slice(0, m.index).split("\n").length;
                if (opening.endsWith("/>")) {
                    out.push({ line, opening, body: "" });
                    continue;
                }
                const close = src.indexOf(`</${tag}>`, j);
                if (close > 0) out.push({ line, opening, body: src.slice(j + 1, close) });
            }
        }
        return out;
    }

    /** Text a screen reader would read: anything but self-closing components (icons) and tags. */
    const readable = (body: string) =>
        body
            .replace(/<[A-Z]\w*[^>]*\/>/g, "")
            .replace(/<[^>]+>/g, "")
            .trim();

    it("names every icon-only button (aria-label, sr-only text or a title), in the page components", () => {
        const skip = (file: string) => /components[\\/](ui|landing)[\\/]/.test(file);
        const unnamed: string[] = [];
        for (const file of files) {
            if (skip(file)) continue;
            const src = code(file);
            for (const { line, opening, body } of buttons(src)) {
                const iconOnly = /size="icon/.test(opening) || (body.trim() !== "" && readable(body) === "");
                if (!iconOnly) continue;
                const named = /aria-label(ledby)?=|\btitle=/.test(opening) || /sr-only|aria-label=/.test(body) || readable(body) !== "";
                if (!named) unnamed.push(`${rel(file)}:${line}`);
            }
        }
        expect(unnamed).toEqual([]);
    });
});
