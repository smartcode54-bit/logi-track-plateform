import { describe, expect, it } from "vitest";

import { TENANT_ROLES } from "../api/me";
import { makeMe } from "@/test-utils/fakeWeb";
import { isCustomerView as customerView } from "./principalLabel";
import { CUSTOMER_MENU_HREFS, isCustomerView, navigationProfileOf, PARTNER_MENU, PARTNER_MENU_HREFS, profileRouteLabelKey, type NavigationProfile } from "./navigationProfile";

const scopes = [{ billingPartyId: "customer-1", name: "CJSF" }];
const carrier = { id: "carrier-1", nameTh: "รถร่วม", nameEn: "Partner", kind: "carrier", role: "tenant_admin" };
const ownFleet = { id: "own-1", nameTh: "กองรถ", nameEn: "Own fleet", kind: "own_fleet", role: "operation_staff" };

describe("navigationProfileOf", () => {
    it("reuses the customer-view predicate and selects a customer-only principal", () => {
        expect(isCustomerView).toBe(customerView);
        const me = makeMe({ tenant: null, tenants: [], steward: false, customerScopes: scopes });
        expect(isCustomerView(me)).toBe(true);
        expect(navigationProfileOf(me)).toBe("customer");
    });

    it.each(TENANT_ROLES)("selects partner for a carrier's %s", (role) => {
        expect(navigationProfileOf(makeMe({ tenant: { ...carrier, role }, steward: false }))).toBe("partner");
    });

    it.each(TENANT_ROLES)("keeps own-fleet %s staff, including with customer scopes", (role) => {
        expect(navigationProfileOf(makeMe({ tenant: { ...ownFleet, role }, customerScopes: scopes }))).toBe("staff");
    });

    it("keeps a dispatcher with customer scopes staff", () => {
        expect(navigationProfileOf(makeMe({ tenant: null, tenants: [], dispatcher: true, customerScopes: scopes }))).toBe("staff");
    });

    it.each(["platform_admin", "support"])("gives %s precedence over carrier membership and customer scopes", (role) => {
        expect(navigationProfileOf(makeMe({ tenant: carrier, platformRoles: [role], customerScopes: scopes }))).toBe("staff");
        expect(navigationProfileOf(makeMe({ tenant: null, tenants: [], platformRoles: [role], customerScopes: scopes }))).toBe("staff");
    });

    it("falls back to staff without a principal or a qualifying membership/scope", () => {
        expect(navigationProfileOf(null)).toBe("staff");
        expect(navigationProfileOf(undefined)).toBe("staff");
        expect(navigationProfileOf(makeMe({ tenant: null, tenants: [] }))).toBe("staff");
        expect(navigationProfileOf(makeMe({ tenant: { ...ownFleet, kind: "quarantine" } }))).toBe("staff");
    });

    it("uses the current tenant after a switch, ignoring other available memberships", () => {
        const me = makeMe({ tenant: ownFleet, tenants: [ownFleet, carrier], customerScopes: scopes });
        expect(navigationProfileOf(me)).toBe("staff");
        expect(navigationProfileOf({ ...me, tenant: carrier })).toBe("partner");
        expect(navigationProfileOf({ ...me, tenant: null })).toBe("customer");
        expect(navigationProfileOf(me)).toBe("staff");
    });

    it("does not infer presentation from capabilities, steward status or driver identity", () => {
        expect(navigationProfileOf(makeMe({ tenant: carrier, capabilities: [], steward: true, dispatcher: true, driver: { id: "d1" } }))).toBe("partner");
        expect(navigationProfileOf(makeMe({ tenant: null, customerScopes: scopes, capabilities: ["users:manage"], steward: true }))).toBe("customer");
    });
});

describe("profileRouteLabelKey", () => {
    const labels = [
        ["/app/driver-monitor", "nav.customerTrips", "nav.partnerTrips"],
        ["/app/job-assign", "nav.scopedTasks", "nav.scopedTasks"],
        ["/app/incident-reports", "nav.deliveryIssues", "nav.deliveryIssues"],
    ] as const;

    it.each(labels)("relabels %s for customer/partner and retains staff labels", (href, customer, partner) => {
        expect(profileRouteLabelKey(href, "customer")).toBe(customer);
        expect(profileRouteLabelKey(href, "partner")).toBe(partner);
        expect(profileRouteLabelKey(href, "staff")).toBeUndefined();
    });

    it.each<NavigationProfile>(["staff", "customer", "partner"])("has no override for other hrefs with %s", (profile) => {
        for (const href of ["/app/support", "/app/trucks", "/app/dashboard", "/app/first-mile", "/app/driver-monitor/", "/app/job-assign/x1", "/app/nowhere"]) {
            expect(profileRouteLabelKey(href, profile), href).toBeUndefined();
        }
    });
});

describe("presentation allowlists", () => {
    it("orders the customer's four destinations", () => {
        expect(CUSTOMER_MENU_HREFS).toEqual(["/app/driver-monitor", "/app/job-assign", "/app/incident-reports", "/app/support"]);
    });

    it("orders the partner's nine destinations and groups only the three fleet pages", () => {
        expect(PARTNER_MENU_HREFS).toEqual([
            "/app/driver-monitor", "/app/job-assign", "/app/incident-reports", "/app/chat",
            "/app/trucks", "/app/drivers", "/app/maintenance", "/app/accounting/billing-document", "/app/support",
        ]);
        expect(PARTNER_MENU.filter((entry) => "items" in entry)).toEqual([
            { labelKey: "nav.partnerFleet", items: [{ href: "/app/trucks" }, { href: "/app/drivers" }, { href: "/app/maintenance" }] },
        ]);
        expect(new Set(PARTNER_MENU_HREFS).size).toBe(9);
    });
});
