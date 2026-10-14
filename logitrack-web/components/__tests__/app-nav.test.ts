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

    it("keeps a detail page's entry as a link and names the page generically until it names itself", () => {
        expect(breadcrumbTrail("/app/trucks/view")).toEqual({
            crumbs: [{ labelKey: "nav.fleets" }, { labelKey: "nav.truckManagement", href: "/app/trucks" }],
            detail: true,
            detailKey: "shell.crumb.detail",
        });
        expect(breadcrumbTrail("/app/customers/abc/edit")).toMatchObject({
            crumbs: [{ labelKey: "nav.customers", href: "/app/customers" }],
            detailKey: "shell.crumb.edit",
        });
        expect(breadcrumbTrail("/app/drivers/new").detailKey).toBe("shell.crumb.new");
    });

    it("names the pages opened from another page under their parent", () => {
        expect(breadcrumbTrail("/app/utilities/billing-impact")).toEqual({
            crumbs: [{ labelKey: "nav.utilities", href: "/app/utilities/backfill" }, { labelKey: "nav.billingImpact", href: "/app/utilities/billing-impact" }],
            detail: false,
        });
        expect(breadcrumbTrail("/app/payroll/config").crumbs).toEqual([
            { labelKey: "nav.hr" },
            { labelKey: "nav.payroll", href: "/app/payroll" },
            { labelKey: "driverComp.config.title", href: "/app/payroll/config" },
        ]);
        expect(activeNavUrl("/app/payroll/penalties")).toBe("/app/payroll");
        expect(activeNavUrl("/app/utilities/billing-impact")).toBe("/app/utilities/backfill");
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
            const { crumbs, detailKey } = breadcrumbTrail(route);
            expect(crumbs.length, route).toBeGreaterThan(0);
            for (const key of [...crumbs.map((c) => c.labelKey), ...(detailKey ? [detailKey] : [])]) {
                expect(en[key], `${route} en ${key}`).toBeTruthy();
                expect(th[key], `${route} th ${key}`).toBeTruthy();
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
