// T18 helpers: legacy claims synthesised from ['me'] (Appendix C §C.6.3), the header's role line, the
// 422 field texts (Appendix C §C.4.8), the Go user behind a legacy users/{uid} document, page walking.
import { afterEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "@/lib/apiError";
import { fetchAllPages } from "@/lib/goPages";
import { safeNext } from "@/lib/safeNext";
import { findUserForLegacyAccount, usersReach } from "@/features/users/api/users";
import { grantableRoles, userActions } from "@/features/users/utils/roles";
import { legacyClaimsFromMe } from "../api/me";
import { makeMe } from "@/test-utils/fakeWeb";
import { fieldViolations, passwordErrorText, violationFor } from "./fieldErrors";
import { principalRoleLabel } from "./principalLabel";

const t = (key: string, fallbackOrParams?: string | Record<string, string | number>) => {
    const texts: Record<string, string> = {
        "auth.passwordPolicy.too_short": "Use at least {min} characters.",
        "apiError.invalid_argument": "Some fields are not valid.",
        "apiError.invalid_credentials": "Incorrect email or password.",
        "users.tenantRole.manager": "Manager",
        "users.platformRole.platform_admin": "Platform admin",
        "users.scopeKind.customer": "Customer",
    };
    const text = texts[key] ?? (typeof fallbackOrParams === "string" ? fallbackOrParams : key);
    if (typeof fallbackOrParams === "object") return Object.entries(fallbackOrParams).reduce((s, [k, v]) => s.replace(`{${k}}`, String(v)), text);
    return text;
};

afterEach(() => vi.unstubAllGlobals());

describe("legacyClaimsFromMe (the claims of unbridged principals, Appendix C §C.6.3)", () => {
    it("maps platform_admin and an own-fleet tenant_admin to admin, a carrier tenant_admin to partner", () => {
        expect(legacyClaimsFromMe(makeMe({ platformRoles: ["platform_admin"], tenant: null }))).toEqual({ admin: true, role: "admin" });
        expect(legacyClaimsFromMe(makeMe())).toEqual({ admin: true, role: "admin" });
        expect(legacyClaimsFromMe(makeMe({ tenant: { id: "c", nameTh: "c", nameEn: null, kind: "carrier", role: "tenant_admin" } }))).toEqual({ admin: false, role: "partner" });
        expect(legacyClaimsFromMe(makeMe({ tenant: { id: "o", nameTh: "o", nameEn: null, kind: "own_fleet", role: "operator" } }))).toEqual({ admin: false, role: "operator" });
        expect(legacyClaimsFromMe(makeMe({ tenant: null, customerScopes: [{ billingPartyId: "bp", name: "CJ" }] }))).toEqual({ admin: false, role: "customer" });
    });
});

describe("principalRoleLabel", () => {
    it("names the platform role, else the tenant role and tenant, else the scope", () => {
        expect(principalRoleLabel(makeMe({ platformRoles: ["platform_admin"] }), t, "en")).toBe("Platform admin");
        expect(principalRoleLabel(makeMe({ tenant: { id: "o", nameTh: "กอง", nameEn: "Fleet", kind: "own_fleet", role: "manager" } }), t, "en")).toBe("Manager · Fleet");
        expect(principalRoleLabel(makeMe({ tenant: null, customerScopes: [{ billingPartyId: "b", name: "x" }] }), t, "en")).toBe("Customer");
        expect(principalRoleLabel(null, t, "en")).toBe("");
    });
});

describe("field errors", () => {
    const tooShort = new ApiError({ status: 422, code: "invalid_argument", message: "", details: { fields: [{ field: "newPassword", reason: "too_short", params: { min: 10, max: 128 } }] } });
    it("reads details.fields and renders the policy text with its params", () => {
        expect(fieldViolations(tooShort)).toEqual([{ field: "newPassword", reason: "too_short", params: { min: 10, max: 128 } }]);
        expect(violationFor(tooShort, "newPassword")?.reason).toBe("too_short");
        expect(passwordErrorText(tooShort, "newPassword", t)).toBe("Use at least 10 characters.");
    });
    it("falls back to the error code's text", () => {
        expect(passwordErrorText(new ApiError({ status: 401, code: "invalid_credentials", message: "" }), "newPassword", t)).toBe("Incorrect email or password.");
        expect(fieldViolations(new Error("x"))).toEqual([]);
    });
});

describe("safeNext", () => {
    it("keeps same-origin /app paths only", () => {
        expect(safeNext("/app/security-center/users?x=1")).toBe("/app/security-center/users?x=1");
        expect(safeNext("https://evil.test/app")).toBe("/app");
        expect(safeNext("//evil.test/app")).toBe("/app");
        expect(safeNext("/login")).toBe("/app");
        expect(safeNext(null)).toBe("/app");
    });
});

describe("user actions from ['me']", () => {
    it("derives the page controls and who may grant tenant_admin", () => {
        const ta = makeMe();
        expect(userActions(ta)).toMatchObject({ manage: true, assignRole: true, revokeSessions: true, linkDriver: false, platformRoles: false, platform: false });
        expect(grantableRoles(ta, "t-own")).toContain("tenant_admin");
        const manager = makeMe({ tenant: { id: "t-own", nameTh: "", nameEn: null, kind: "own_fleet", role: "manager" } });
        expect(grantableRoles(manager, "t-own")).not.toContain("tenant_admin");
        expect(grantableRoles(makeMe({ platformRoles: ["platform_admin"], tenant: null }), "any")).toContain("tenant_admin");
    });
});

describe("findUserForLegacyAccount", () => {
    function stub(users: unknown[]) {
        const requests: { url: string; headers: Headers }[] = [];
        vi.stubGlobal(
            "fetch",
            vi.fn(async (u: string, init: RequestInit = {}) => {
                requests.push({ url: u, headers: new Headers(init.headers) });
                return new Response(JSON.stringify({ data: users }), { status: 200 });
            })
        );
        return requests;
    }
    it("matches only the Firebase uid of the document (the email only narrows the search)", async () => {
        const requests = stub([
            { id: "a", email: "x@y.test", legacyAuthUid: "other" },
            { id: "b", email: "X@y.test", legacyAuthUid: "fb-b" },
        ]);
        expect((await findUserForLegacyAccount({ uid: "fb-b", email: "x@y.test" }))?.id).toBe("b");
        expect(requests[0].url).toBe("/api/go/v1/users?q=x%40y.test&limit=50");
        expect(requests[0].headers.has("X-Act-On-Tenant")).toBe(false);
        stub([]);
        expect(await findUserForLegacyAccount({ uid: "fb-z", email: "z@y.test" })).toBeNull();
        expect(await findUserForLegacyAccount({ uid: "fb-z", email: "" })).toBeNull();
    });

    it("never resolves a row that names someone else's email (the row's owner can rewrite users/{uid})", async () => {
        // X wrote users/{fb-x}.email = boss@company: the search finds the boss, whose uid is not the row's.
        stub([{ id: "go-boss", email: "boss@company.test", legacyAuthUid: "fb-boss" }]);
        expect(await findUserForLegacyAccount({ uid: "fb-x", email: "boss@company.test" })).toBeNull();
        // Nor a unique email match of a user without a legacy uid.
        stub([{ id: "go-c", email: "c@y.test", legacyAuthUid: null }]);
        expect(await findUserForLegacyAccount({ uid: "fb-c", email: "c@y.test" })).toBeNull();
    });

    it("a platform principal searches every tenant with X-Act-On-Tenant: *", async () => {
        const requests = stub([{ id: "b", email: "x@y.test", legacyAuthUid: "fb-b" }]);
        expect((await findUserForLegacyAccount({ uid: "fb-b", email: "x@y.test" }, usersReach(makeMe({ platformRoles: ["platform_admin"] }))))?.id).toBe("b");
        expect(requests[0].headers.get("X-Act-On-Tenant")).toBe("*");
        expect(usersReach(makeMe({ platformRoles: ["support"] }))).toBe("all");
        expect(usersReach(makeMe())).toBe("tenant");
    });
});

describe("fetchAllPages", () => {
    it("follows nextCursor and stops at the last page or the page bound", async () => {
        const urls: string[] = [];
        vi.stubGlobal(
            "fetch",
            vi.fn(async (u: string) => {
                urls.push(u);
                const n = urls.length;
                return new Response(JSON.stringify({ data: [n], ...(n < 3 ? { nextCursor: `c${n}` } : {}) }), { status: 200 });
            })
        );
        expect(await fetchAllPages<number>("/v1/tenants", { kind: "carrier" })).toEqual([1, 2, 3]);
        expect(urls).toEqual([
            "/api/go/v1/tenants?kind=carrier&limit=100",
            "/api/go/v1/tenants?kind=carrier&limit=100&cursor=c1",
            "/api/go/v1/tenants?kind=carrier&limit=100&cursor=c2",
        ]);
        urls.length = 0;
        expect(await fetchAllPages<number>("/v1/tenants", {}, { maxPages: 2 })).toEqual([1, 2]);
    });
});
