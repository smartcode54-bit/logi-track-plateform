import { describe, expect, it } from "vitest";
import { menuFor, menuLinksFor } from "@/components/app-nav";
import { makeMe } from "@/test-utils/fakeWeb";
import { routeAllowed } from "@/lib/routeCapabilities";
import { canViewSupportAccess, SUPPORT_ACCESS_PATH } from "./access";

describe("Support access navigation", () => {
    it("adds one Settings entry for a real active tenant admin, including a carrier admin", () => {
        for (const kind of ["own_fleet", "carrier"]) {
            const base = makeMe();
            const tenant = { ...base.tenant!, kind };
            const me = makeMe({ tenant, tenants: [tenant] });
            const links = menuLinksFor(menuFor(new Set(me.capabilities), "/app/dashboard", kind === "carrier" ? "partner" : "staff", me));
            expect(links.filter((link) => link.href === SUPPORT_ACCESS_PATH)).toEqual([{ href: SUPPORT_ACCESS_PATH, labelKey: "nav.supportAccess", groupLabelKey: "nav.settings" }]);
        }
    });

    it("requires both membership context and route capabilities, never inferring admin from capabilities", () => {
        const admin = makeMe();
        const hrefs = (me?: typeof admin | null, held = new Set(admin.capabilities)) => menuLinksFor(menuFor(held, "/app/dashboard", "staff", me)).map((link) => link.href);
        expect(hrefs()).not.toContain(SUPPORT_ACCESS_PATH);
        expect(hrefs(null)).not.toContain(SUPPORT_ACCESS_PATH);
        expect(hrefs(admin, new Set())).not.toContain(SUPPORT_ACCESS_PATH);
        expect(hrefs(makeMe({ tenant: null, tenants: [], platformRoles: ["platform_admin"] }))).not.toContain(SUPPORT_ACCESS_PATH);
        expect(hrefs(makeMe({ tenant: { ...admin.tenant!, role: "manager" } }))).not.toContain(SUPPORT_ACCESS_PATH);
        expect(canViewSupportAccess({ ...admin, tenant: { ...admin.tenant!, role: "support_readonly" } })).toBe(false);
        expect(canViewSupportAccess({ ...admin, tenant: { ...admin.tenant!, kind: "quarantine" } })).toBe(false);
        expect(canViewSupportAccess({ ...admin, tenants: [{ ...admin.tenant!, status: "suspended" }] })).toBe(false);
        expect(routeAllowed(["users:assign_role"], SUPPORT_ACCESS_PATH)).toBe(true);
        expect(routeAllowed(["company:manage"], SUPPORT_ACCESS_PATH)).toBe(false);
    });
});
