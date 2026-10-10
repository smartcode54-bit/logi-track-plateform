// @vitest-environment node
/**
 * T18 end to end in one process: the browser side of the web login (lib/authClient.ts, the users and
 * tenants API modules over lib/goFetch.ts) talks to the real BFF handlers through a same-origin fetch
 * with a cookie jar that honours Path, and the handlers talk to a fake Go over HTTP. Covers the issue's
 * acceptance criteria that do not need a browser: the login sets `lt_at` (`Path=/`) and `lt_rt`
 * (`Path=/api/auth`) and returns no token; a must-change-password user holds no cookie until the ticket
 * is redeemed, then signs in with the new password (R79); the bridge token, the tenant switch and the
 * logout go through their BFF routes; the users page actions reach only Go routes.
 */
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import {
    changePasswordWithTicket,
    firebaseCustomToken,
    passwordChangeTicket,
    signInWithPassword,
    signOutSession,
    switchTenant,
} from "../../authClient";
import { goFetch } from "../../goFetch";
import { createUser, revokeUserSessions, setMemberRole, setUserDisabled } from "@/features/users/api/users";
import { createTenant } from "@/features/tenants/api/tenants";
import { resetJwksForTests } from "../accessToken";
import { firebaseToken, login, logout, resetRotationsForTests, tenant } from "../authRoutes";
import { proxyToGo } from "../goProxy";
import { FakeGo, goError, parseSetCookie, WEB_ORIGIN } from "./fakeGo";

const go = new FakeGo();
const realFetch = globalThis.fetch;
const jar = new Map<string, { value: string; path: string; attrs: Map<string, string> }>();
const browserRequests: { method: string; url: string }[] = [];

function cookieHeader(path: string): string {
    return [...jar]
        .filter(([, c]) => path === c.path || path.startsWith(c.path.endsWith("/") ? c.path : `${c.path}/`))
        .map(([n, c]) => `${n}=${c.value}`)
        .join("; ");
}

function store(res: Response): void {
    for (const line of res.headers.getSetCookie()) {
        const c = parseSetCookie(line);
        if (c.attrs.get("max-age") === "0") jar.delete(c.name);
        else jar.set(c.name, { value: c.value, path: c.attrs.get("path") ?? "/", attrs: c.attrs });
    }
}

async function browserFetch(input: RequestInfo | URL, init: RequestInit = {}): Promise<Response> {
    const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    if (/^[a-z]+:/i.test(url)) {
        if (url.startsWith(go.url)) return realFetch(input, init);
        throw new TypeError(`cross-origin request from the browser: ${url}`);
    }
    const method = (init.method ?? "GET").toUpperCase();
    browserRequests.push({ method, url });
    const headers = new Headers(init.headers);
    const path = url.split("?")[0];
    const cookies = cookieHeader(path);
    if (cookies) headers.set("cookie", cookies);
    if (method !== "GET" && method !== "HEAD") {
        headers.set("Origin", WEB_ORIGIN);
        headers.set("Sec-Fetch-Site", "same-origin");
    }
    const body = typeof init.body === "string" ? init.body : undefined;
    const req = new Request(`${WEB_ORIGIN}${url}`, { method, headers, body });
    const deps = { config: go.config(), fetchImpl: realFetch };
    let res: Response;
    if (path.startsWith("/api/go/")) res = await proxyToGo(req, deps);
    else if (path === "/api/auth/login") res = await login(req, deps);
    else if (path === "/api/auth/logout") res = await logout(req, deps);
    else if (path === "/api/auth/tenant") res = await tenant(req, deps);
    else if (path === "/api/auth/firebase-token") res = await firebaseToken(req, deps);
    else res = new Response(null, { status: 404 });
    store(res);
    return res;
}

const storage = new Map<string, string>();

beforeAll(async () => {
    await go.start();
    globalThis.fetch = browserFetch as typeof fetch;
    vi.stubGlobal("window", {
        location: { pathname: "/login", search: "", assign: () => undefined },
        localStorage: {
            getItem: (k: string) => storage.get(k) ?? null,
            setItem: (k: string, v: string) => void storage.set(k, v),
            removeItem: (k: string) => void storage.delete(k),
        },
    });
});
afterAll(async () => {
    globalThis.fetch = realFetch;
    vi.unstubAllGlobals();
    await go.stop();
});
beforeEach(() => {
    resetJwksForTests();
    resetRotationsForTests();
});
afterEach(() => {
    go.requests.length = 0;
    go.routes.clear();
    jar.clear();
    browserRequests.length = 0;
});

const tokens = (n: number) => ({ accessToken: `access.token.${n}`, refreshToken: `refresh-${n}`, expiresIn: 900 });

describe("web login through the BFF (T18)", () => {
    it("sets lt_at on / and lt_rt on /api/auth (HttpOnly, Secure, SameSite=Lax, Max-Age from the TTLs) and returns no token", async () => {
        go.on("POST", "/v1/auth/login", () => ({
            status: 200,
            body: { data: { ...tokens(1), tenants: [{ id: "t1", nameTh: "ก", nameEn: "A", kind: "own_fleet", role: "manager" }], defaultTenantId: "t1" } },
        }));
        const result = await signInWithPassword("a@b.test", "pw");
        expect(result).toEqual({ expiresIn: 900, tenants: [{ id: "t1", nameTh: "ก", nameEn: "A", kind: "own_fleet", role: "manager" }], defaultTenantId: "t1" });
        expect(JSON.stringify(result)).not.toContain("access.token");

        const at = jar.get("lt_at")!;
        expect(at.value).toBe("access.token.1");
        expect(Object.fromEntries(at.attrs)).toMatchObject({ path: "/", "max-age": "900", httponly: "", samesite: "Lax", secure: "" });
        const rt = jar.get("lt_rt")!;
        expect(rt.value).toBe("refresh-1");
        expect(Object.fromEntries(rt.attrs)).toMatchObject({ path: "/api/auth", "max-age": "604800", httponly: "", samesite: "Lax", secure: "" });
        expect(JSON.parse(go.calls("POST", "/v1/auth/login")[0].body)).toEqual({ platform: "web", email: "a@b.test", password: "pw" });
    });

    it("must change password: no cookie until the ticket is redeemed, then a normal login with the new password (R79)", async () => {
        let mustChange = true;
        go.on("POST", "/v1/auth/login", (r) => {
            const body = JSON.parse(r.body) as { password: string };
            if (mustChange) return { status: 403, body: goError("password_change_required", { passwordChangeTicket: "tkt-1", expiresIn: 600 }) };
            return body.password === "New-Password-2026" ? { status: 200, body: { data: { ...tokens(2), tenants: [], defaultTenantId: null } } } : { status: 401, body: goError("invalid_credentials") };
        });
        go.on("POST", "/v1/auth/password/change", (r) => {
            const body = JSON.parse(r.body) as { passwordChangeTicket?: string; newPassword?: string };
            if (body.passwordChangeTicket !== "tkt-1") return { status: 422, body: goError("invalid_argument", { fields: [{ field: "passwordChangeTicket", reason: "invalid_or_expired" }] }) };
            mustChange = false;
            return { status: 204 };
        });

        const first = await signInWithPassword("driver@b.test", "Temp-Password").catch((e: unknown) => e);
        expect(passwordChangeTicket(first)).toBe("tkt-1");
        expect(jar.size).toBe(0);

        await changePasswordWithTicket("tkt-1", "New-Password-2026");
        expect(jar.size).toBe(0);
        const [change] = go.calls("POST", "/v1/auth/password/change");
        expect(JSON.parse(change.body)).toEqual({ passwordChangeTicket: "tkt-1", newPassword: "New-Password-2026" });

        await signInWithPassword("driver@b.test", "New-Password-2026");
        expect(jar.get("lt_at")?.value).toBe("access.token.2");
        expect(jar.get("lt_rt")?.value).toBe("refresh-2");
    });

    it("an expired ticket is a 422 on the ticket field, not a 401 that would end anything", async () => {
        go.on("POST", "/v1/auth/password/change", () => ({ status: 422, body: goError("invalid_argument", { fields: [{ field: "passwordChangeTicket", reason: "invalid_or_expired" }] }) }));
        const err = await changePasswordWithTicket("old", "New-Password-2026").catch((e: unknown) => e);
        expect(err).toMatchObject({ status: 422, code: "invalid_argument" });
        expect(browserRequests.map((r) => r.url)).toEqual(["/api/go/v1/auth/password/change"]);
    });

    it("mints the bridge token, switches tenant and logs out through the BFF; logout leaves no cookie", async () => {
        go.on("POST", "/v1/auth/login", () => ({ status: 200, body: { data: { ...tokens(3), tenants: [], defaultTenantId: null } } }));
        go.on("POST", "/v1/bridge/firebase-token", (r) =>
            r.headers.authorization === "Bearer access.token.3" ? { status: 200, body: { data: { customToken: "ct", expiresIn: 3600 } } } : { status: 401, body: goError("unauthenticated") }
        );
        go.on("POST", "/v1/auth/tenant", (r) => {
            const body = JSON.parse(r.body) as { tenantId: string };
            return body.tenantId === "t2" ? { status: 200, body: { data: { accessToken: "access.token.t2", expiresIn: 900 } } } : { status: 403, body: goError("permission_denied") };
        });
        go.on("POST", "/v1/auth/logout", () => ({ status: 204 }));

        await signInWithPassword("a@b.test", "pw");
        expect(await firebaseCustomToken()).toEqual({ customToken: "ct", expiresIn: 3600 });

        await switchTenant("t2");
        expect(jar.get("lt_at")?.value).toBe("access.token.t2");
        expect(jar.get("lt_rt")?.value).toBe("refresh-3");

        await signOutSession();
        expect(jar.size).toBe(0);
        const [out] = go.calls("POST", "/v1/auth/logout");
        expect(out.headers.authorization).toBe("Bearer access.token.t2");
        expect(JSON.parse(out.body)).toEqual({ refreshToken: "refresh-3" });
    });

    it("a user Go refuses a Firestore session for gets 403 from the bridge (a dispatcher created in Go)", async () => {
        go.on("POST", "/v1/auth/login", () => ({ status: 200, body: { data: { ...tokens(4), tenants: [], defaultTenantId: null } } }));
        go.on("POST", "/v1/bridge/firebase-token", () => ({ status: 403, body: goError("permission_denied") }));
        await signInWithPassword("dispatcher@b.test", "pw");
        await expect(firebaseCustomToken()).rejects.toMatchObject({ status: 403, code: "permission_denied" });
        // The session itself stays: a 403 is not a session end.
        expect(jar.get("lt_at")?.value).toBe("access.token.4");
    });

    it("the users and tenants pages reach Go routes only, with the access cookie as the bearer", async () => {
        go.on("POST", "/v1/auth/login", () => ({ status: 200, body: { data: { ...tokens(5), tenants: [], defaultTenantId: null } } }));
        go.on("POST", "/v1/users", () => ({ status: 201, body: { data: { user: { id: "u9", email: "n@b.test" }, temporaryPassword: "ABCD-EFGH-JKLM" } } }));
        go.on("PUT", "/v1/tenants/t9/members/u9", () => ({ status: 200, body: { data: { role: "tenant_admin" } } }));
        go.on("POST", "/v1/users/u9/disable", () => ({ status: 204 }));
        go.on("DELETE", "/v1/users/u9/sessions", () => ({ status: 204 }));
        go.on("POST", "/v1/tenants", () => ({ status: 201, body: { data: { id: "t9", kind: "carrier", code: "WRT", nameTh: "ดับบลิว", nameEn: null, status: "active" } } }));
        await signInWithPassword("admin@b.test", "pw");

        const t9 = await createTenant({ code: "WRT", nameTh: "ดับบลิว", legalType: "company" });
        const created = await createUser({ email: "n@b.test", displayName: "N", role: "tenant_admin", tenantId: t9.id });
        expect(created.temporaryPassword).toBe("ABCD-EFGH-JKLM");
        await setMemberRole("t9", "u9", "tenant_admin");
        await setUserDisabled("u9", true, "left");
        await revokeUserSessions("u9");

        const seen = go.requests.filter((r) => r.url !== "/v1/auth/login").map((r) => `${r.method} ${r.url}`);
        expect(seen).toEqual([
            "POST /v1/tenants",
            "POST /v1/users",
            "PUT /v1/tenants/t9/members/u9",
            "POST /v1/users/u9/disable",
            "DELETE /v1/users/u9/sessions",
        ]);
        for (const r of go.requests.filter((x) => x.url !== "/v1/auth/login")) expect(r.headers.authorization).toBe("Bearer access.token.5");
        expect(JSON.parse(go.calls("POST", "/v1/tenants")[0].body)).toEqual({ kind: "carrier", code: "WRT", nameTh: "ดับบลิว", legalType: "company" });
        expect(JSON.parse(go.calls("POST", "/v1/users")[0].body)).toEqual({ email: "n@b.test", displayName: "N", role: "tenant_admin", tenantId: "t9" });
        expect(JSON.parse(go.calls("POST", "/v1/users/u9/disable")[0].body)).toEqual({ reason: "left" });
        expect(browserRequests.every((r) => r.url.startsWith("/api/"))).toBe(true);
    });

    it("GET /v1/me with a revoked session is a 401 the browser sees (the tab then ends its session)", async () => {
        go.on("GET", "/v1/me", () => ({ status: 401, body: goError("session_revoked") }));
        jar.set("lt_at", { value: "access.token.x", path: "/", attrs: new Map() });
        await expect(goFetch("/v1/me", { anonymous: true })).rejects.toMatchObject({ status: 401, code: "session_revoked" });
    });
});
