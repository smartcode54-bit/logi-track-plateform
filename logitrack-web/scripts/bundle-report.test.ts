import { describe, expect, it } from "vitest";
import {
    buildReport,
    entrypointChain,
    gateViolations,
    isForbiddenPackage,
    localeOfModulePath,
    packageOfModulePath,
    routeOfEntrypoint,
    toMarkdown,
} from "./bundle-report.mjs";

/** A leaf module as webpack-bundle-analyzer writes it. */
function mod(path: string, gzipSize: number) {
    return { label: path.split("/").pop(), path, statSize: gzipSize * 4, parsedSize: gzipSize * 3, gzipSize };
}

function chunk(label: string, gzipSize: number, initial: string[], modules: ReturnType<typeof mod>[]) {
    return {
        label,
        isAsset: true,
        statSize: gzipSize * 4,
        parsedSize: gzipSize * 3,
        gzipSize,
        isInitialByEntrypoint: Object.fromEntries(initial.map((e) => [e, true])),
        groups: [{ label: "node_modules", path: "./node_modules", groups: modules }],
    };
}

const XLSX = "./node_modules/.pnpm/xlsx@https+++cdn.sheetjs.com+xlsx-0.20.3+xlsx-0.20.3.tgz/node_modules/xlsx/xlsx.mjs";

describe("module path classification", () => {
    it("names the package of a pnpm path, scoped or not", () => {
        expect(packageOfModulePath(XLSX)).toBe("xlsx");
        expect(packageOfModulePath("./node_modules/.pnpm/@fullcalendar+core@6.1.20/node_modules/@fullcalendar/core/index.js")).toBe(
            "@fullcalendar/core",
        );
        expect(packageOfModulePath("./features/accounting/api/billing.ts")).toBeUndefined();
    });

    it("finds the language of a dictionary module, also inside a concatenated module", () => {
        expect(localeOfModulePath("./context/locales/th/accounting.ts")).toBe("th");
        expect(localeOfModulePath("./context/language.tsx + 43 modules (concatenated)/context/locales/en/common.ts")).toBe("en");
        expect(localeOfModulePath("./context/language.tsx")).toBeUndefined();
    });

    it("matches forbidden packages by name and by scope", () => {
        expect(isForbiddenPackage("xlsx")).toBe(true);
        expect(isForbiddenPackage("@fullcalendar/daygrid")).toBe(true);
        expect(isForbiddenPackage("xlsx-utils")).toBe(false);
        expect(isForbiddenPackage("react")).toBe(false);
    });
});

describe("routes and entrypoint chains", () => {
    it("maps page entrypoints to URL paths and drops route groups", () => {
        expect(routeOfEntrypoint("app/page")).toBe("/");
        expect(routeOfEntrypoint("app/(auth)/login/page")).toBe("/login");
        expect(routeOfEntrypoint("app/app/customers/[id]/page")).toBe("/app/customers/[id]");
        expect(routeOfEntrypoint("app/app/layout")).toBeUndefined();
        expect(routeOfEntrypoint("main-app")).toBeUndefined();
    });

    it("includes main-app and only the layouts that exist", () => {
        const known = new Set(["main-app", "app/layout", "app/app/layout", "app/app/chat/layout"]);
        expect(entrypointChain("app/app/chat/room/page", known)).toEqual([
            "main-app",
            "app/layout",
            "app/app/layout",
            "app/app/chat/layout",
            "app/app/chat/room/page",
        ]);
        expect(entrypointChain("app/page", known)).toEqual(["main-app", "app/layout", "app/page"]);
    });
});

describe("buildReport and the gate", () => {
    const client = [
        chunk("static/chunks/main-app.js", 100, ["main-app"], [mod("./node_modules/.pnpm/react@19/node_modules/react/index.js", 100)]),
        chunk("static/chunks/layout.js", 40, ["app/layout"], [mod("./context/language.tsx", 40)]),
        chunk("static/chunks/xlsx.js", 150, ["app/app/income/page"], [mod(XLSX, 150)]),
        chunk("static/chunks/income.js", 20, ["app/app/income/page"], [mod("./app/app/income/page.tsx", 20)]),
        // Loaded with import(): initial for no entrypoint, so never counted.
        chunk("static/chunks/locale-th.js", 56, [], [mod("./context/locales/th/common.ts", 56)]),
        chunk("static/chunks/home.js", 10, ["app/page"], [mod("./context/locales/en/common.ts", 10)]),
        chunk("static/css/app.css", 5, ["app/layout"], []),
    ];
    const report = buildReport(client);

    it("sums the chunks of main-app, the layouts and the page", () => {
        expect(report.routes["/app/income"]).toEqual({
            chunks: 4,
            parsedBytes: 930,
            gzipBytes: 310,
            forbidden: { xlsx: 150 },
            locales: {},
        });
        expect(report.routes["/"].gzipBytes).toBe(150);
        expect(report.routes["/"].locales).toEqual({ en: 10 });
    });

    it("flags forbidden libraries and dictionaries in initial JS", () => {
        expect(gateViolations(report, undefined)).toEqual([
            "/: locale dictionary en in initial JS",
            "/app/income: xlsx in initial JS",
        ]);
    });

    it("flags growth over the baseline beyond the limit only", () => {
        const clean = buildReport(client.filter((c) => !c.label.includes("xlsx") && !c.label.includes("home")));
        const baseline = { routes: { "/app/income": { ...clean.routes["/app/income"], gzipBytes: 150 } } };
        expect(gateViolations(clean, baseline, 5)).toEqual([
            "/app/income: initial JS 0.2 KB gzip is 6.7% over the baseline 0.1 KB (limit 5%)",
        ]);
        expect(gateViolations(clean, baseline, 10)).toEqual([]);
    });

    it("renders a Markdown row per route", () => {
        const md = toMarkdown(report, undefined);
        expect(md).toContain("| /app/income | 0.3 KB | 0.9 KB | 4 | xlsx 0.1 KB | none |");
    });
});
