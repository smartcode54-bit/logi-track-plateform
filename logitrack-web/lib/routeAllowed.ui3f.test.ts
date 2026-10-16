// @vitest-environment node
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

import { ROLE_DEFAULT_CAPABILITIES, STEWARD_CAPABILITIES } from "shared-docs/schemas/capabilities";
import { makeMe } from "@/test-utils/fakeWeb";
import { routeAllowed } from "./routeCapabilities";
import fixture from "./routeAllowed.ui3f.fixture.json";

const appDir = path.resolve(__dirname, "..", "app", "app");
const PAGE_FILE = /^page\.(tsx|ts|jsx|js|mdx)$/;

/** Same discovery as routeCapabilities.test.ts's every-page-is-mapped invariant; retain dynamic segments. */
function pages(dir: string, prefix = "/app"): string[] {
    const out: string[] = [];
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
        if (entry.isDirectory()) {
            if (dir === appDir && entry.name === "[...missing]") continue;
            const segment = entry.name.startsWith("(") ? "" : `/${entry.name}`;
            out.push(...pages(path.join(dir, entry.name), prefix + segment));
        } else if (PAGE_FILE.test(entry.name)) {
            out.push(prefix);
        }
    }
    return out.sort();
}

const roleCaps = (role: string) => ROLE_DEFAULT_CAPABILITIES.find((row) => row.role === role)?.capabilities ?? [];
const withoutSteward = (caps: readonly string[]) => caps.filter((capability) => !(STEWARD_CAPABILITIES as readonly string[]).includes(capability));

// The eight BEFORE principals of components/__tests__/app-nav.test.ts, with the same effective capability sets.
const before: Record<string, readonly string[]> = {
    admin: roleCaps("tenant_admin"),
    manager: roleCaps("manager"),
    operationStaff: roleCaps("operation_staff"),
    operator: roleCaps("operator"),
    dispatcher: roleCaps("dispatcher"),
    customer: roleCaps("customer"),
    partner: withoutSteward(roleCaps("tenant_admin")),
    platformAdmin: roleCaps("platform_admin"),
};

const customer = makeMe({
    tenant: null, tenants: [], steward: false,
    customerScopes: [{ billingPartyId: "customer-1", name: "CJSF" }],
    capabilities: [...roleCaps("customer")],
});
const carrierAdmin = makeMe({
    tenant: { id: "carrier-1", nameTh: "รถร่วม", nameEn: "Partner", kind: "carrier", role: "tenant_admin" },
    steward: false,
    capabilities: withoutSteward(roleCaps("tenant_admin")),
});
const ownFleetStaff = makeMe({
    tenant: { id: "own-1", nameTh: "กองรถ", nameEn: "Own fleet", kind: "own_fleet", role: "operation_staff" },
    capabilities: [...roleCaps("operation_staff")],
});

const principals = { ...before, customerScope: customer.capabilities, carrierAdmin: carrierAdmin.capabilities, ownFleetStaff: ownFleetStaff.capabilities };
const expected: Record<string, readonly boolean[]> = fixture.routes;

describe("UI3F access baseline (dd2de92a, before navigation presentation changes)", () => {
    it("covers every discovered app page and every BEFORE/additional principal", () => {
        expect(fixture.principals).toEqual(Object.keys(principals));
        // This post-baseline route has its own role/eligibility tests; the frozen decisions remain intact.
        expect(Object.keys(expected).sort()).toEqual(pages(appDir).filter((route) => route !== "/app/settings/support-access"));
        for (const decisions of Object.values(expected)) expect(decisions).toHaveLength(fixture.principals.length);
    });

    for (const [index, [who, capabilities]] of Object.entries(principals).entries()) {
        it(`${who}: preserves every frozen allow/deny decision`, () => {
            for (const route of Object.keys(expected)) {
                expect(routeAllowed(capabilities, route), `${who} -> ${route}`).toBe(expected[route][index]);
            }
        });
    }

    it("keeps customer standby records reachable even when absent from the short menu", () => {
        expect(routeAllowed(customer.capabilities, "/app/standby-records")).toBe(true);
    });

    it("keeps carrier-admin payroll reachable even when absent from the short menu", () => {
        expect(routeAllowed(carrierAdmin.capabilities, "/app/payroll")).toBe(true);
    });
});
