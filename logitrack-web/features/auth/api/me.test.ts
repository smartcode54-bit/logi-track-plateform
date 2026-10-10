// TW4 (developer-spec.md §10.6; Appendix C §C.6.3, §C.8): ['me'] over GET /v1/me, its selectors and
// the legacy claims useAuth() synthesises from it.
import { afterEach, describe, expect, it, vi } from "vitest";
import { CAPABILITY_KEYS } from "shared-docs/schemas/capabilities";
import { CAPABILITIES, toCatalogKey } from "@/lib/capabilities";
import { can, getRole, isAdmin } from "@/lib/permissions";
import { createQueryClient } from "@/lib/queryClient";
import {
    claimsFromMe,
    fetchMe,
    hasCapability,
    isCustomerPrincipal,
    legacyRoleOf,
    meQueryOptions,
    parseMe,
    type MeDTO,
} from "./me";
import { homeRouteOf } from "./homeRoute";

function me(over: Partial<MeDTO> = {}): MeDTO {
    return {
        id: "u1",
        email: "a@example.test",
        displayName: "Ann",
        photoUrl: null,
        tenant: { id: "t-own", nameTh: "บริษัท", nameEn: null, kind: "own_fleet", role: "manager" },
        tenants: [],
        platformRoles: [],
        dispatcher: false,
        steward: true,
        driver: null,
        customerScopes: [],
        capabilities: ["fleet:view_trucks", "users:manage"],
        mustChangePassword: false,
        ...over,
    };
}

afterEach(() => {
    vi.unstubAllGlobals();
});

describe("parseMe", () => {
    it("reads the Go body and fills safe defaults", () => {
        const parsed = parseMe({
            id: "u1",
            email: "a@example.test",
            displayName: null,
            tenant: { id: "t1", nameTh: "ท", nameEn: "T", kind: "carrier", role: "tenant_admin" },
            tenants: [{ id: "t1", nameTh: "ท", nameEn: "T", kind: "carrier", role: "tenant_admin" }, "junk"],
            platformRoles: ["support", 3],
            dispatcher: true,
            customerScopes: [{ billingPartyId: "bp1", name: "CJ" }, { name: "no id" }],
            capabilities: ["chat:view"],
            legacyAuthUid: "fb-uid",
        });
        expect(parsed.tenant).toEqual({ id: "t1", nameTh: "ท", nameEn: "T", kind: "carrier", role: "tenant_admin" });
        expect(parsed.tenants).toHaveLength(1);
        expect(parsed.platformRoles).toEqual(["support"]);
        expect(parsed.customerScopes).toEqual([{ billingPartyId: "bp1", name: "CJ" }]);
        expect(parsed.dispatcher).toBe(true);
        expect(parsed.mustChangePassword).toBe(false);
        expect(parsed.legacyAuthUid).toBe("fb-uid");
        expect(parsed.photoUrl).toBeNull();
    });

    it("refuses a body without id or capabilities", () => {
        expect(() => parseMe({ capabilities: [] })).toThrowError(expect.objectContaining({ code: "bad_response" }));
        expect(() => parseMe({ id: "u1" })).toThrowError(expect.objectContaining({ code: "bad_response" }));
    });
});

describe("['me'] query", () => {
    it("is GET /v1/me with 5 min stale / 30 min gc and focus refetch", () => {
        expect(meQueryOptions.queryKey).toEqual(["me"]);
        expect(meQueryOptions.staleTime).toBe(300_000);
        expect(meQueryOptions.gcTime).toBe(1_800_000);
        expect(meQueryOptions.refetchOnWindowFocus).toBe(true);
    });

    it("resolves to the principal, and to null for a signed-out visitor on a public page", async () => {
        const urls: string[] = [];
        let signedIn = true;
        vi.stubGlobal(
            "fetch",
            vi.fn(async (input: RequestInfo | URL) => {
                const url = String(input);
                urls.push(url);
                if (url === "/api/go/v1/me") {
                    return signedIn
                        ? new Response(JSON.stringify({ data: me() }), { status: 200 })
                        : new Response(JSON.stringify({ error: { code: "unauthenticated", message: "", details: {}, requestId: "r" } }), { status: 401 });
                }
                // The shared refresh finds no refresh cookie.
                if (url === "/api/auth/refresh") return new Response(JSON.stringify({ error: { code: "unauthenticated" } }), { status: 401 });
                throw new Error(`unexpected ${url}`);
            })
        );
        const client = createQueryClient();
        await expect(client.fetchQuery(meQueryOptions)).resolves.toMatchObject({ id: "u1" });
        signedIn = false;
        await expect(fetchMe({ signal: new AbortController().signal })).resolves.toBeNull();
        expect(urls).toEqual(["/api/go/v1/me", "/api/go/v1/me", "/api/auth/refresh"]);
    });

    it("lets any other error through", async () => {
        vi.stubGlobal("fetch", vi.fn(async () => new Response("<html>bad gateway</html>", { status: 502 })));
        await expect(fetchMe({ signal: new AbortController().signal })).rejects.toMatchObject({ status: 502, code: "unavailable" });
    });
});

describe("capabilities", () => {
    it("every legacy capability id is a catalog key or has an alias (usePermission keeps CAPABILITIES.x)", () => {
        const catalog = new Set<string>(CAPABILITY_KEYS);
        const unknown = Object.values(CAPABILITIES).filter((c) => !catalog.has(toCatalogKey(c)));
        expect(unknown).toEqual([]);
        expect(toCatalogKey(CAPABILITIES.security_manage_users)).toBe("users:manage");
    });

    it("hasCapability reads the Go set, with legacy ids translated", () => {
        expect(hasCapability(me(), "fleet:view_trucks")).toBe(true);
        expect(hasCapability(me(), CAPABILITIES.security_manage_users)).toBe(true);
        expect(hasCapability(me(), "chat:view")).toBe(false);
        expect(hasCapability(null, "fleet:view_trucks")).toBe(false);
    });
});

describe("legacy role and claims (Appendix C §C.6.3)", () => {
    const tenant = (kind: string, role: string) => ({ id: "t", nameTh: "", nameEn: null, kind, role });

    it.each([
        ["own-fleet tenant_admin", me({ tenant: tenant("own_fleet", "tenant_admin") }), true, "admin"],
        ["platform_admin without a tenant", me({ tenant: null, platformRoles: ["platform_admin"] }), true, "admin"],
        ["carrier tenant_admin (legacy partner)", me({ tenant: tenant("carrier", "tenant_admin") }), false, "partner"],
        ["own-fleet manager", me(), false, "manager"],
        ["own-fleet operation_staff", me({ tenant: tenant("own_fleet", "operation_staff") }), false, "operation_staff"],
        ["operator", me({ tenant: tenant("own_fleet", "operator") }), false, "operator"],
        ["driver", me({ tenant: tenant("own_fleet", "driver") }), false, "driver"],
        ["scope-only customer", me({ tenant: null, customerScopes: [{ billingPartyId: "bp", name: "CJ" }] }), false, "customer"],
        ["support only", me({ tenant: null, platformRoles: ["support"] }), false, "user"],
    ])("%s", (_name, principal, admin, role) => {
        expect(legacyRoleOf(principal)).toEqual({ admin, role });
        const claims = claimsFromMe(principal);
        expect(getRole(claims)).toBe(role);
        expect(isAdmin(claims)).toBe(admin);
    });

    it("can() answers from the Go capabilities, not the role defaults", () => {
        const claims = claimsFromMe(me({ capabilities: ["chat:view"] }));
        // A manager's default set has fleet:view_trucks; this principal's overrides removed it.
        expect(can(claims, CAPABILITIES.fleet_view_trucks)).toBe(false);
        expect(can(claims, CAPABILITIES.chat_view)).toBe(true);
        // Legacy claims without a capability list keep the old defaults (tests, other callers).
        expect(can({ role: "manager" }, CAPABILITIES.fleet_view_trucks)).toBe(true);
    });

    it("takes only the legacy Firestore ids from the bridge token, never the role", () => {
        const claims = claimsFromMe(me({ tenant: null, customerScopes: [{ billingPartyId: "bp", name: "CJ" }] }), {
            role: "admin",
            admin: true,
            customerScopeId: "cust-doc-1",
            partnerScopeId: " ",
        });
        expect(claims).toMatchObject({ role: "customer", admin: false, customerScopeId: "cust-doc-1" });
        expect(claims).not.toHaveProperty("partnerScopeId");
        expect(claimsFromMe(null, { admin: true })).toBeNull();
    });

    it("isCustomerPrincipal: a customer scope without a membership", () => {
        expect(isCustomerPrincipal(me({ tenant: null, customerScopes: [{ billingPartyId: "bp", name: "CJ" }] }))).toBe(true);
        expect(isCustomerPrincipal(me({ customerScopes: [{ billingPartyId: "bp", name: "CJ" }] }))).toBe(false);
        expect(isCustomerPrincipal(null)).toBe(false);
    });
});

describe("homeRouteOf (R89, the proxy.ts rule)", () => {
    it("sends operators, dispatchers, carrier admins and scope-only customers to the monitor when allowed", () => {
        const monitor = ["operations:view_driver_monitor"];
        expect(homeRouteOf(me({ tenant: { id: "t", nameTh: "", nameEn: null, kind: "own_fleet", role: "operator" }, capabilities: monitor }))).toBe(
            "/app/driver-monitor"
        );
        expect(homeRouteOf(me({ tenant: null, customerScopes: [{ billingPartyId: "b", name: "" }], capabilities: monitor }))).toBe("/app/driver-monitor");
        expect(homeRouteOf(me({ dispatcher: true, capabilities: [] }))).toBe("/app/dashboard");
        expect(homeRouteOf(me())).toBe("/app/dashboard");
    });
});
