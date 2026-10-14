// @vitest-environment node
import fs from "node:fs";
import path from "node:path";

import { describe, expect, it } from "vitest";

import { canonicalAppPath, homeRouteFor, ROUTE_CAPABILITIES, routeAllowed, routeFor } from "./routeCapabilities";

const appDir = path.resolve(__dirname, "..", "app", "app");

/** A catch-all segment (`[...x]`, `[[...x]]`): not a page of its own (UI3D: the shell's not-found answers there). */
const isCatchAll = (segment: string) => /^\[\[?\.\.\./.test(segment);

/** A page or route-handler file in any of the extensions Next accepts. */
const PAGE_FILE = /^page\.(tsx|ts|jsx|js|mdx)$/;
const ROUTE_FILE = /^route\.(tsx|ts|jsx|js)$/;

/**
 * Every page under app/app as its URL path; dynamic segments stay as `[id]`. Only the root catch-all of the shell's
 * not-found (`[...missing]`) is skipped: any other catch-all is a page like the rest and needs its mapping.
 */
function pages(dir: string, prefix = "/app"): string[] {
    const out: string[] = [];
    for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
        if (e.isDirectory()) {
            if (dir === appDir && e.name === "[...missing]") continue;
            const seg = e.name.startsWith("(") ? "" : `/${e.name}`;
            out.push(...pages(path.join(dir, e.name), prefix + seg));
        } else if (PAGE_FILE.test(e.name)) {
            out.push(prefix);
        }
    }
    return out;
}

/** Every route handler under app/app (relative paths). */
function routeHandlers(dir: string): string[] {
    const out: string[] = [];
    for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
        const p = path.join(dir, e.name);
        if (e.isDirectory()) out.push(...routeHandlers(p));
        else if (ROUTE_FILE.test(e.name)) out.push(path.relative(appDir, p));
    }
    return out;
}

describe("routeFor / routeAllowed", () => {
    it("matches exactly, else by the longest mapped prefix at a segment boundary", () => {
        expect(routeFor("/app/trucks/new")?.matched).toBe("/app/trucks/new");
        expect(routeFor("/app/trucks/view/abc")?.matched).toBe("/app/trucks/view");
        expect(routeFor("/app/trucks/")?.matched).toBe("/app/trucks");
        expect(routeFor("/app/trucksx")).toBeUndefined();
        expect(routeFor("/app")).toBeUndefined();
        expect(routeFor("/app/constructor")).toBeUndefined();
    });

    it("denies unmapped paths, admits everybody on the open routes, needs one listed key elsewhere", () => {
        expect(routeAllowed(["fleet:view_trucks"], "/app/analytics")).toBe(false);
        expect(routeAllowed([], "/app/dashboard")).toBe(true);
        expect(routeAllowed([], "/app/unauthorized")).toBe(true);
        expect(routeAllowed([], "/app/support")).toBe(true); // UI3C: help for every signed-in principal
        expect(routeAllowed(["fleet:view_trucks"], "/app/trucks")).toBe(true);
        expect(routeAllowed(new Set(["fleet:view_trucks"]), "/app/trucks/new")).toBe(false);
        expect(routeAllowed(["users:view"], "/app/security-center/users")).toBe(true);
        expect(routeAllowed(["security:view_overview"], "/app/security-center/users")).toBe(false);
        expect(routeAllowed(["accounting:recompute_force"], "/app/utilities/backfill")).toBe(true);
    });

    it("uses the colon keys of the Go catalog (R5, R27)", () => {
        for (const [p, caps] of Object.entries(ROUTE_CAPABILITIES)) {
            expect(p.startsWith("/app/")).toBe(true);
            for (const c of caps) expect(c).toMatch(/^[a-z_]+:[a-z_]+$/);
        }
    });

    it("has no route handler under app/app: the gate lets unmapped paths through (404), so one would be reachable unchecked", () => {
        expect(routeHandlers(appDir)).toEqual([]);
    });

    it("maps every page under app/app, in any extension (the gate lets an unmapped path through with 404 and no capability check, so an unmapped page would be served unchecked)", () => {
        // `/app` goes to the role's home in proxy.ts; the other two are next.config redirects() that
        // run before proxy.ts (developer-spec.md §10.12). Mirrors TestEveryWebPageIsMapped in Go.
        const redirected = ["/app", "/app/users", "/app/operations/roles"];
        const all = pages(appDir);
        expect(all.length).toBeGreaterThan(50);
        expect(all.filter((p) => !redirected.includes(p) && !routeFor(p))).toEqual([]);
    });
});

describe("canonicalAppPath (UI3D review: decide on the path Next routes)", () => {
    it("decodes each segment once and spells it one way", () => {
        expect(canonicalAppPath("/app/%70ayroll")).toEqual({ path: "/app/payroll", href: "/app/payroll" });
        expect(canonicalAppPath("/app/drivers/%6eew/")).toEqual({ path: "/app/drivers/new", href: "/app/drivers/new" });
        expect(canonicalAppPath("/app//payroll")).toEqual({ path: "/app/payroll", href: "/app/payroll" });
        // Decoded once only: %2570 is a literal "%70", which no route holds.
        expect(canonicalAppPath("/app/%2570ayroll")).toEqual({ path: "/app/%70ayroll", href: "/app/%2570ayroll" });
        // Stable for a segment that must stay escaped (no redirect loop).
        const thai = canonicalAppPath("/app/x/%E0%B8%81")!;
        expect(canonicalAppPath(thai.href)).toEqual(thai);
    });

    it("refuses what cannot be one route: malformed escapes, encoded slashes and backslashes, dot segments, controls", () => {
        for (const raw of ["/app/%E0%A4%A", "/app/%zz", "/app/a%2Fb", "/app/a%5cb", "/app/%2e%2e", "/app/%2E", "/app/a%00", "/app/a%0Ab"]) {
            expect(canonicalAppPath(raw), raw).toBeNull();
        }
    });
});

describe("not found is not forbidden (UI3D)", () => {
    it("has exactly one catch-all under app/app, at its root, and it only calls notFound()", () => {
        const catchAlls: string[] = [];
        const walk = (dir: string) => {
            for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
                if (!e.isDirectory()) continue;
                if (isCatchAll(e.name)) catchAlls.push(path.relative(appDir, path.join(dir, e.name)));
                walk(path.join(dir, e.name));
            }
        };
        walk(appDir);
        expect(catchAlls).toEqual(["[...missing]"]);
        const source = fs.readFileSync(path.join(appDir, "[...missing]", "page.tsx"), "utf8");
        expect(source).toMatch(/notFound\(\);/);
        expect(source).not.toMatch(/<[A-Za-z]/); // renders nothing of its own
        expect(fs.existsSync(path.join(appDir, "not-found.tsx"))).toBe(true);
        expect(fs.existsSync(path.join(appDir, "error.tsx"))).toBe(true);
    });
});

describe("homeRouteFor (R89)", () => {
    const monitor = ["operations:view_driver_monitor"];
    it.each([
        [{ tenantRole: "manager", tenantKind: "own_fleet" }, monitor, "/app/dashboard"],
        [{ tenantRole: "operation_staff", tenantKind: "own_fleet" }, monitor, "/app/dashboard"],
        [{ tenantRole: "operator", tenantKind: "own_fleet" }, monitor, "/app/driver-monitor"],
        [{ tenantRole: "tenant_admin", tenantKind: "carrier" }, monitor, "/app/driver-monitor"],
        [{ tenantRole: "tenant_admin", tenantKind: "own_fleet" }, monitor, "/app/dashboard"],
        [{ customerScope: true }, monitor, "/app/driver-monitor"],
        [{ dispatcher: true, tenantRole: "operation_staff" }, monitor, "/app/driver-monitor"],
        [{ tenantRole: "operator" }, [], "/app/dashboard"],
        [{}, [], "/app/dashboard"],
    ])("%o -> %s", (p, caps, want) => {
        expect(homeRouteFor({ customerScope: false, dispatcher: false, capabilities: caps, ...p })).toBe(want);
    });
});
