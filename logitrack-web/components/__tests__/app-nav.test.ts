// UI3 (#143): the shell's navigation is defined once (components/app-nav.ts) and the header breadcrumb is built
// from its i18n keys, never from URL slugs. Every page under app/app gets a crumb whose text exists in the base
// dictionary of both languages (the header renders it on every route, before any split namespace loads).
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

import { activeNavUrl, breadcrumbTrail, MAIN_NAV, SECURITY_NAV, SYSTEM_NAV, isNavGroup } from "../app-nav";
import en from "@/context/locales/en";
import th from "@/context/locales/th";
import { WEB_ROOT } from "@/test-utils/importGraph";

/** Every route of the `/app` group, from its page files (`[id]` segments filled with a sample id). */
function appRoutes(): string[] {
    const out: string[] = [];
    const walk = (dir: string) => {
        for (const name of fs.readdirSync(dir)) {
            const p = path.join(dir, name);
            if (fs.statSync(p).isDirectory()) walk(p);
            else if (name === "page.tsx") {
                const rel = path.relative(path.join(WEB_ROOT, "app"), path.dirname(p)).split(path.sep).join("/");
                out.push(`/${rel}`.replace(/\[[^\]]+\]/g, "x1"));
            }
        }
    };
    walk(path.join(WEB_ROOT, "app", "app"));
    return out.sort();
}

describe("breadcrumbTrail", () => {
    it("names a page by its menu group and entry", () => {
        expect(breadcrumbTrail("/app/accounting/income")).toEqual({
            crumbs: [{ labelKey: "nav.accounting" }, { labelKey: "nav.income", href: "/app/accounting/income" }],
            detail: false,
        });
        expect(breadcrumbTrail("/app/dashboard/")).toEqual({ crumbs: [{ labelKey: "nav.dashboard", href: "/app/dashboard" }], detail: false });
    });

    it("gives a detail page its entry's label (the page may add its own name after it)", () => {
        expect(breadcrumbTrail("/app/trucks/view")).toEqual({
            crumbs: [{ labelKey: "nav.fleets" }, { labelKey: "nav.truckManagement", href: "/app/trucks" }],
            detail: true,
        });
        expect(breadcrumbTrail("/app/customers/abc/edit").crumbs.at(-1)).toEqual({ labelKey: "nav.customers", href: "/app/customers" });
        expect(breadcrumbTrail("/app/utilities/billing-impact")).toEqual({
            crumbs: [{ labelKey: "nav.utilities", href: "/app/utilities/backfill" }],
            detail: true,
        });
    });

    it("puts the Security Center pages under the Security Center", () => {
        expect(breadcrumbTrail("/app/security-center/users").crumbs).toEqual([
            { labelKey: "nav.securityCenter", href: "/app/security-center" },
            { labelKey: "securityCenter.userManagement", href: "/app/security-center/users" },
        ]);
        expect(breadcrumbTrail("/app/security-center").crumbs).toEqual([{ labelKey: "nav.securityCenter", href: "/app/security-center" }]);
    });

    it("has no crumbs outside the menu", () => {
        expect(breadcrumbTrail("/app/nowhere")).toEqual({ crumbs: [], detail: false });
        expect(breadcrumbTrail("/about")).toEqual({ crumbs: [], detail: false });
    });

    it("labels every app page from keys both base dictionaries hold, never from the URL", () => {
        const routes = appRoutes();
        expect(routes.length).toBeGreaterThan(50);
        for (const route of routes) {
            if (route === "/app") continue; // redirects to the principal's home
            const { crumbs } = breadcrumbTrail(route);
            expect(crumbs.length, route).toBeGreaterThan(0);
            for (const crumb of crumbs) {
                expect(en[crumb.labelKey], `${route} en ${crumb.labelKey}`).toBeTruthy();
                expect(th[crumb.labelKey], `${route} th ${crumb.labelKey}`).toBeTruthy();
            }
        }
    });
});

describe("menu entries", () => {
    it("translate in both languages", () => {
        const keys = [
            ...MAIN_NAV.flatMap((entry) => (isNavGroup(entry) ? [entry.labelKey, ...entry.items.map((i) => i.labelKey)] : [entry.labelKey])),
            ...SYSTEM_NAV.map((l) => l.labelKey),
            ...SECURITY_NAV.map((l) => l.labelKey),
        ];
        for (const key of keys) {
            expect(en[key], `en ${key}`).toBeTruthy();
            expect(th[key], `th ${key}`).toBeTruthy();
            // No shouting labels (DESIGN.md, The No Shouting Rule).
            expect(en[key], key).not.toMatch(/^[A-Z\s&]{4,}$/);
        }
    });

    it("keep a detail page's entry active", () => {
        expect(activeNavUrl("/app/customers/abc/edit")).toBe("/app/customers");
        expect(activeNavUrl("/app/accounting/income")).toBe("/app/accounting/income");
        expect(activeNavUrl("/app/nowhere")).toBeUndefined();
    });
});
