// @vitest-environment node
import { execFileSync } from "node:child_process";
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { describe, expect, it } from "vitest";
import {
    checkRatchet, countMetrics, METRIC_NAMES, readCleanManifests, scanMetrics, serializeBaseline,
} from "../scripts/ui-audit-metrics.mjs";

export { checkRatchet } from "../scripts/ui-audit-metrics.mjs";

describe("UI4 audit metrics", () => {
    it("strips comments without stripping strings, URLs, regexes or template expressions", () => {
        const source = `
            // text-red-500 text-[9px] uppercase #abc border-l-4 blur-xl hsl(var(--card))
            /* text-blue-600 rgb(0,0,0) animate-ping */
            const url = "https://example.test/photo";
            const re = /https?:\\/\\//;
            const el = <p className={cn(\`text-warning \${active ? "text-red-500" /* bg-red-500 */ : "text-primary"}\`)}>ok</p>;
        `;
        expect(countMetrics(source)).toEqual({
            paletteColors: 1, hexOrRgb: 0, tinyText: 0, uppercaseClass: 0, sideStripe: 0, blurGlow: 0, invalidHslVar: 0,
        });
        expect(countMetrics("/* #abc rgb(0,0,0) */ .map { color: #abc; }", "map.css").hexOrRgb).toBe(1);
    });

    it("counts every prohibited literal and token with boundaries", () => {
        const source = `const el = <p className="text-red-500 dark:bg-amber-900 text-[9px] text-[10px] text-[11px] text-[12px] border-l-4 md:border-t-4 border-l-40 backdrop-blur-sm blur-xl blur-2xl blur-5xl blur-10xl blur-lg shadow-primary/20 shadow-red-500/50 animate-ping animate-pulse" />;
            const colors = ["#abc", "#A1B2C3", "rgb(1, 2, 3)", "rgba(1, 2, 3, .5)", "hsl(var(--chart-1))", "var(--chart-2)"];
        `;
        expect(countMetrics(source)).toEqual({
            paletteColors: 2, hexOrRgb: 4, tinyText: 3, uppercaseClass: 0, sideStripe: 2, blurGlow: 9, invalidHslVar: 1,
        });
    });

    it("counts uppercase class strings only, without double-counting nested cn/cva calls", () => {
        const source = `
            const code = "UPPERCASE"; const lower = "uppercase";
            const id = code.toUpperCase(); const styles = { uppercase: true };
            const badge = cva("uppercase", { variants: { size: { small: "md:uppercase", large: "normal-case" } } });
            const a = <p className="uppercase uppercase-other">ABC</p>;
            const b = <p className={cn("uppercase", active && "lg:uppercase", cn("uppercase"))}>SOCE</p>;
        `;
        expect(countMetrics(source).uppercaseClass).toBe(6);
        expect(countMetrics(".uppercase { text-transform: uppercase; }", "test.css").uppercaseClass).toBe(0);
        expect(countMetrics('const identity = <T>(v: T) => v; const styles = cn("uppercase");', "styles.ts").uppercaseClass).toBe(1);
    });

    it("counts class-like module constants when used as className identifiers", () => {
        const source = `
            const HEAD = "text-xs font-semibold uppercase";
            const UNUSED = "text-xs tracking-wide";
            const short = "uppercase";
            const header = <th className={HEAD} />;
        `;
        expect(countMetrics(source).uppercaseClass).toBe(1);
    });

    it("keeps loading pulses separate from decorative status dots", () => {
        const source = `
            function Skeleton() { return <div className={cn("animate-pulse")} />; }
            function Cards({loading}) { if (loading) return <div className="animate-pulse" />; return <i className="animate-pulse" />; }
            const busy = <div aria-busy="true"><div className="animate-pulse" /></div>;
            const status = <span role="status" className="animate-pulse">Loading</span>;
            const fallback = dynamic(loadMap, { loading: () => <div className="animate-pulse">Loading map...</div> });
            const progress = calculating ? <span className="animate-pulse" /> : <i className="animate-pulse" />;
        `;
        expect(countMetrics(source).blurGlow).toBe(2);
    });

    it.each([
        [`function A({loading}) { return !loading ? <i className="animate-pulse" /> : null; }`, 1],
        [`function A({loading}) { return loading ? null : <i className="animate-pulse" />; }`, 1],
        [`function A({loading}) { return !loading ? null : <i className="animate-pulse" />; }`, 0],
        [`function A({loading}) { return loading && <i className="animate-pulse" />; }`, 0],
        [`function A({loading}) { return !loading && <i className="animate-pulse" />; }`, 1],
        [`function A({loading}) { return !loading || <i className="animate-pulse" />; }`, 0],
        [`function A({loading}) { return loading || <i className="animate-pulse" />; }`, 1],
        [`const label = "Loading"; const dot = <i className="animate-pulse">{label}</i>;`, 1],
    ])("classifies pulse branch structurally: %s", (source, expected) => {
        expect(countMetrics(source).blurGlow).toBe(expected);
    });

    it("counts alpha hex colours and skips hash-like JSX text", () => {
        const source = `<><div data-colors="#ff000080 #f008 #abc #AABBCC" />Order #123 &#169;</>`;
        expect(countMetrics(source).hexOrRgb).toBe(4);
    });
});

describe("legacy UI audit reporter", () => {
    it("keeps old columns on raw TSX and excludes .ts routes from its source set", () => {
        const root = mkdtempSync(path.join(tmpdir(), "ui4-reporter-"));
        try {
            mkdirSync(path.join(root, "components/ui"), { recursive: true });
            mkdirSync(path.join(root, "app/api"), { recursive: true });
            mkdirSync(path.join(root, "features"), { recursive: true });
            writeFileSync(path.join(root, "components/ui/sample.tsx"), `export const Sample = () => <div style={\n{ color: "red" }\n}><button>Go</button></div>;`);
            writeFileSync(path.join(root, "app/api/route.ts"), `export const marker = "<button>";`);

            const script = path.join(__dirname, "../scripts/ui-audit-counts.mjs");
            const output = execFileSync(process.execPath, [script, root], { encoding: "utf8" });
            const [headerLine, ui1Line] = output.trimEnd().split("\n");
            const header = headerLine.split("\t");
            const row = ui1Line.split("\t");
            expect(row[header.indexOf("files")]).toBe("1");
            expect(row[header.indexOf("inlineStyles")]).toBe("0");
            expect(row[header.indexOf("rawButton")]).toBe("1");
            expect(output).not.toContain("app/api/route.ts");
        } finally {
            rmSync(root, { recursive: true, force: true });
        }
    });
});

describe("UI4 per-file ratchet", () => {
    it.each(METRIC_NAMES)("rejects an increase in %s", (metric) => {
        expect(checkRatchet({ "app/a.tsx": { [metric]: 2 } }, { "app/a.tsx": { [metric]: 1 } })).toHaveLength(1);
    });

    it.each(METRIC_NAMES)("rejects a new offending file for %s", (metric) => {
        expect(checkRatchet({ "app/new.tsx": { [metric]: 1 } }, {})).toHaveLength(1);
    });

    it("permits decreases and new all-zero files", () => {
        expect(checkRatchet({ "app/a.tsx": { tinyText: 1 }, "app/new.tsx": {} }, { "app/a.tsx": { tinyText: 2 } })).toEqual([]);
    });

    it.each(METRIC_NAMES)("a manifest overrides the baseline for %s", (metric) => {
        expect(checkRatchet({ "app/a.tsx": { [metric]: 1 } }, { "app/a.tsx": { [metric]: 1 } }, [["app/a.tsx"]])).toHaveLength(1);
        expect(checkRatchet({ "app/a.tsx": {} }, { "app/a.tsx": { [metric]: 1 } }, [["app/a.tsx"]])).toEqual([]);
    });

    it("a missing manifest path fails, even with a baseline entry", () => {
        expect(checkRatchet({}, { "app/gone.tsx": { tinyText: 1 } }, [["app/gone.tsx"]])).toEqual([
            "app/gone.tsx: manifest path does not exist in the source tree",
        ]);
    });

    it("ignores deleted baseline files", () => {
        expect(checkRatchet({}, { "app/gone.tsx": { tinyText: 4 } })).toEqual([]);
    });

    it("only exempts photo overlay colours, and clean manifests still override them", () => {
        const file = "components/ui/file-viewer.tsx";
        expect(checkRatchet({ [file]: { paletteColors: 5, hexOrRgb: 2 } }, {})).toEqual([]);
        expect(checkRatchet({ [file]: { tinyText: 1 } }, {})).toHaveLength(1);
        expect(checkRatchet({ [file]: { hexOrRgb: 1 } }, {}, [[file]])).toHaveLength(1);
        expect(checkRatchet({ "components/new-viewer.tsx": { hexOrRgb: 1 } }, {})).toHaveLength(1);
    });

    it("caps sidebar tiny text at the existing two even if a baseline is widened", () => {
        const file = "components/app-sidebar.tsx";
        expect(checkRatchet({ [file]: { tinyText: 2 } }, { [file]: { tinyText: 2 } })).toEqual([]);
        expect(checkRatchet({ [file]: { tinyText: 3 } }, { [file]: { tinyText: 3 } })).toHaveLength(1);
    });

    it("discovers optional manifests deterministically and rejects malformed paths", () => {
        const root = mkdtempSync(path.join(tmpdir(), "ui4-manifests-"));
        try {
            expect(readCleanManifests(root)).toEqual([]);
            const dir = path.join(root, "lib/ui4-clean");
            mkdirSync(dir, { recursive: true });
            writeFileSync(path.join(dir, "UI4C.json"), '["features/c.tsx"]');
            writeFileSync(path.join(dir, "UI4B.json"), '["app/b.tsx"]');
            expect(readCleanManifests(root)).toEqual([["app/b.tsx"], ["features/c.tsx"]]);
            writeFileSync(path.join(dir, "UI4B.json"), '["../outside.tsx"]');
            expect(() => readCleanManifests(root)).toThrow("production source paths");
        } finally {
            rmSync(root, { recursive: true, force: true });
        }
    });
});

describe("UI4 real-tree ratchet", () => {
    const root = path.resolve(__dirname, "..");
    const counts = scanMetrics(root);
    const baselineText = readFileSync(path.join(root, "lib/ui4Palette.baseline.json"), "utf8");

    it("ratchets production TS, TSX and CSS with every discovered clean manifest", () => {
        expect(checkRatchet(counts, JSON.parse(baselineText), readCleanManifests(root))).toEqual([]);
        expect(counts["components/filter-combobox.tsx"].paletteColors).toBe(0);
        expect(counts["components/app-sidebar.tsx"].tinyText).toBe(2);
        expect(Object.keys(counts).some((file) => /\.(test|spec)\./.test(file) || file.includes("__tests__/"))).toBe(false);
    });

    it("serializes a deterministic baseline with sorted paths and metrics", () => {
        expect(serializeBaseline(JSON.parse(baselineText))).toBe(baselineText);
        const sample = serializeBaseline({ "app/z.tsx": { tinyText: 1, paletteColors: 2 }, "app/a.tsx": { tinyText: 0 } });
        expect(sample).toBe('{\n  "app/z.tsx": {\n    "paletteColors": 2,\n    "tinyText": 1\n  }\n}\n');
    });
});
