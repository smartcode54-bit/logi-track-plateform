// @vitest-environment node
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from "vitest";

import { resetJwksForTests } from "../accessToken";
import {
    firebaseToken,
    google,
    googleNonce,
    login,
    logout,
    refreshGet,
    refreshPost,
    resetRotationsForTests,
    ROTATION_SHARE_MS,
    safeNext,
    tenant,
} from "../authRoutes";
import { FakeGo, goError, newSigningKey, parseSetCookie, SAME_ORIGIN, setCookies, signAccess, WEB_ORIGIN, webRequest, type SigningKey } from "./fakeGo";

const go = new FakeGo();
let key: SigningKey;

beforeAll(async () => {
    await go.start();
    key = await newSigningKey();
    go.published = [key];
});
afterAll(async () => {
    await go.stop();
});
beforeEach(() => {
    resetJwksForTests();
    resetRotationsForTests();
});
afterEach(() => {
    go.requests.length = 0;
    go.routes.clear();
});

function cookiesOf(res: Response) {
    return new Map(setCookies(res).map((l) => {
        const c = parseSetCookie(l);
        return [c.name, c] as const;
    }));
}

const tokens = (n: number) => ({ accessToken: `access.token.v${n}`, refreshToken: `refresh-token-${n}`, expiresIn: 900 });

function json(body: unknown, headers: Record<string, string> = {}): RequestInit {
    return { method: "POST", headers: { ...SAME_ORIGIN, "Content-Type": "application/json", ...headers }, body: JSON.stringify(body) };
}

describe("POST /api/auth/login", () => {
    it("forwards with platform web, sets both cookies (R36) and returns the body without tokens", async () => {
        go.on("POST", "/v1/auth/login", () => ({
            status: 200,
            body: { data: { ...tokens(1), tenants: [{ id: "t1", role: "manager" }], defaultTenantId: "t1" } },
        }));
        const res = await login(
            webRequest("/api/auth/login", {
                ...json({ email: "a@b.test", password: "pw", platform: "android", installId: "x", appVersion: "9" }, { "X-Forwarded-For": "203.0.113.9", "User-Agent": "UA/1" }),
            }),
            { config: go.config({ cookieDomain: "web.test" }) }
        );
        expect(res.status).toBe(200);
        expect(res.headers.get("cache-control")).toBe("no-store");
        const body = await res.json();
        expect(body).toEqual({ data: { expiresIn: 900, tenants: [{ id: "t1", role: "manager" }], defaultTenantId: "t1" } });
        expect(JSON.stringify(body)).not.toMatch(/access\.token|refresh-token/);

        const c = cookiesOf(res);
        const at = c.get("lt_at")!;
        expect(at.value).toBe("access.token.v1");
        expect(Object.fromEntries(at.attrs)).toEqual({ path: "/", "max-age": "900", domain: "web.test", httponly: "", samesite: "Lax", secure: "" });
        const rt = c.get("lt_rt")!;
        expect(rt.value).toBe("refresh-token-1");
        expect(Object.fromEntries(rt.attrs)).toEqual({ path: "/api/auth", "max-age": "604800", domain: "web.test", httponly: "", samesite: "Lax", secure: "" });

        const [seen] = go.calls("POST", "/v1/auth/login");
        expect(JSON.parse(seen.body)).toEqual({ platform: "web", email: "a@b.test", password: "pw" });
        expect(seen.headers["x-forwarded-for"]).toBe("203.0.113.9");
        expect(seen.headers["user-agent"]).toBe("UA/1");
        expect(seen.headers.authorization).toBeUndefined();
    });

    it("leaves out Secure and Domain when configured so (local http)", async () => {
        go.on("POST", "/v1/auth/login", () => ({ status: 200, body: { data: tokens(1) } }));
        const res = await login(webRequest("/api/auth/login", json({ email: "a", password: "b" })), { config: go.config({ cookieSecure: false }) });
        for (const c of cookiesOf(res).values()) {
            expect(c.attrs.has("secure")).toBe(false);
            expect(c.attrs.has("domain")).toBe(false);
        }
    });

    it("passes a must-change-password 403 through with its ticket and sets no cookie (R79)", async () => {
        go.on("POST", "/v1/auth/login", () => ({ status: 403, body: goError("password_change_required", { passwordChangeTicket: "tkt" }) }));
        const res = await login(webRequest("/api/auth/login", json({ email: "a", password: "b" })), { config: go.config() });
        expect(res.status).toBe(403);
        expect(setCookies(res)).toEqual([]);
        const body = await res.json();
        expect(body.error.code).toBe("password_change_required");
        expect(body.error.details).toEqual({ passwordChangeTicket: "tkt" });
    });

    it("passes 423 and 429 through with Retry-After", async () => {
        go.on("POST", "/v1/auth/login", () => ({ status: 429, headers: { "Retry-After": "17" }, body: goError("resource_exhausted") }));
        const res = await login(webRequest("/api/auth/login", json({ email: "a", password: "b" })), { config: go.config() });
        expect(res.status).toBe(429);
        expect(res.headers.get("retry-after")).toBe("17");
    });

    it("refuses a foreign Origin before calling Go", async () => {
        const res = await login(webRequest("/api/auth/login", json({ email: "a", password: "b" }, { Origin: "https://evil.test", "Sec-Fetch-Site": "cross-site" })), {
            config: go.config(),
        });
        expect(res.status).toBe(403);
        expect((await res.json()).error.details).toEqual({ reason: "origin" });
        expect(go.requests).toHaveLength(0);
    });

    it("refuses a body that is not a JSON object, or longer than 16 KiB, without calling Go", async () => {
        for (const body of ["[1]", "not json", JSON.stringify({ email: "a", password: "x".repeat(17 * 1024) })]) {
            const res = await login(webRequest("/api/auth/login", { method: "POST", headers: SAME_ORIGIN, body }), { config: go.config() });
            expect(res.status).toBe(400);
            expect((await res.json()).error.code).toBe("bad_request");
        }
        expect(go.requests).toHaveLength(0);
    });

    it("never sets a cookie from a malformed token", async () => {
        go.on("POST", "/v1/auth/login", () => ({ status: 200, body: { data: { accessToken: "a\r\nSet-Cookie: x=y", refreshToken: "r", expiresIn: 1 } } }));
        const res = await login(webRequest("/api/auth/login", json({ email: "a", password: "b" })), { config: go.config() });
        expect(res.status).toBe(502);
        expect(setCookies(res)).toEqual([]);
    });
});

describe("Google sign-in", () => {
    it("GET /api/auth/google/nonce passes through", async () => {
        go.on("GET", "/v1/auth/google/nonce", () => ({ status: 200, headers: { "Cache-Control": "no-store" }, body: { data: { nonce: "n1", expiresIn: 600 } } }));
        const res = await googleNonce(webRequest("/api/auth/google/nonce"), { config: go.config() });
        expect(res.status).toBe(200);
        expect(await res.json()).toEqual({ data: { nonce: "n1", expiresIn: 600 } });
    });

    it("POST /api/auth/google forwards idToken and nonce only, and sets both cookies", async () => {
        go.on("POST", "/v1/auth/google", () => ({ status: 200, body: { data: tokens(2) } }));
        const res = await google(webRequest("/api/auth/google", json({ idToken: "gis.id.token", nonce: "n1", platform: "ios" })), { config: go.config() });
        expect(res.status).toBe(200);
        expect(JSON.parse(go.calls("POST", "/v1/auth/google")[0].body)).toEqual({ platform: "web", idToken: "gis.id.token", nonce: "n1" });
        expect([...cookiesOf(res).keys()].sort()).toEqual(["lt_at", "lt_rt"]);
    });
});

describe("POST /api/auth/refresh (R37, R78)", () => {
    beforeEach(() => {
        go.on("POST", "/v1/auth/refresh", () => ({ status: 200, body: { data: tokens(3) } }));
    });

    it("is a 204 no-op while lt_at has more than 120 s left (acceptance criterion)", async () => {
        const at = await signAccess(key, { ttl: 600 });
        const res = await refreshPost(webRequest("/api/auth/refresh", { method: "POST", headers: SAME_ORIGIN, cookies: { lt_at: at, lt_rt: "rt-old" } }), {
            config: go.config(),
        });
        expect(res.status).toBe(204);
        expect(setCookies(res)).toEqual([]);
        expect(go.calls("POST", "/v1/auth/refresh")).toHaveLength(0);
    });

    it('rotates with {"force":true} although lt_at has more than 120 s left (acceptance criterion)', async () => {
        const at = await signAccess(key, { ttl: 600 });
        const res = await refreshPost(webRequest("/api/auth/refresh", { ...json({ force: true }), cookies: { lt_at: at, lt_rt: "rt-old" } }), {
            config: go.config(),
        });
        expect(res.status).toBe(204);
        expect(JSON.parse(go.calls("POST", "/v1/auth/refresh")[0].body)).toEqual({ refreshToken: "rt-old" });
        const c = cookiesOf(res);
        expect(c.get("lt_at")?.value).toBe("access.token.v3");
        expect(c.get("lt_rt")?.value).toBe("refresh-token-3");
        expect(c.get("lt_rt")?.attrs.get("path")).toBe("/api/auth");
    });

    it.each([
        ["120 s or less left", { ttl: 100 }],
        ["an expired lt_at", { ttl: -60 }],
        ["an lt_at of another issuer", { iss: "http://elsewhere.test" }],
    ])("rotates with %s", async (_label, claims) => {
        const at = await signAccess(key, claims);
        const res = await refreshPost(webRequest("/api/auth/refresh", { method: "POST", headers: SAME_ORIGIN, cookies: { lt_at: at, lt_rt: "rt-old" } }), {
            config: go.config(),
        });
        expect(res.status).toBe(204);
        expect(go.calls("POST", "/v1/auth/refresh")).toHaveLength(1);
    });

    it("rotates without lt_at (it outlived its Max-Age)", async () => {
        const res = await refreshPost(webRequest("/api/auth/refresh", { method: "POST", headers: SAME_ORIGIN, cookies: { lt_rt: "rt-old" } }), { config: go.config() });
        expect(res.status).toBe(204);
        expect(go.calls("POST", "/v1/auth/refresh")).toHaveLength(1);
    });

    it("answers 401 and expires both cookies without lt_rt", async () => {
        const res = await refreshPost(webRequest("/api/auth/refresh", { method: "POST", headers: SAME_ORIGIN }), { config: go.config() });
        expect(res.status).toBe(401);
        expect((await res.json()).error.code).toBe("unauthenticated");
        const c = cookiesOf(res);
        expect(c.get("lt_at")?.attrs.get("max-age")).toBe("0");
        expect(c.get("lt_rt")?.attrs.get("max-age")).toBe("0");
        expect(c.get("lt_rt")?.attrs.get("path")).toBe("/api/auth");
        expect(go.requests).toHaveLength(0);
    });

    it("expires both cookies when Go refuses the refresh token, passing its 401 through", async () => {
        go.on("POST", "/v1/auth/refresh", () => ({ status: 401, body: goError("session_revoked") }));
        const res = await refreshPost(webRequest("/api/auth/refresh", { method: "POST", headers: SAME_ORIGIN, cookies: { lt_rt: "rt-reused" } }), {
            config: go.config(),
        });
        expect(res.status).toBe(401);
        expect((await res.json()).error.code).toBe("session_revoked");
        expect([...cookiesOf(res).values()].map((c) => c.attrs.get("max-age"))).toEqual(["0", "0"]);
    });

    it("keeps the cookies when Go fails otherwise (the session may still be valid)", async () => {
        go.on("POST", "/v1/auth/refresh", () => ({ status: 503, body: goError("unavailable") }));
        const res = await refreshPost(webRequest("/api/auth/refresh", { method: "POST", headers: SAME_ORIGIN, cookies: { lt_rt: "rt" } }), { config: go.config() });
        expect(res.status).toBe(503);
        expect(setCookies(res)).toEqual([]);
    });

    it("refuses a foreign Origin", async () => {
        const res = await refreshPost(webRequest("/api/auth/refresh", { method: "POST", headers: { Origin: "https://evil.test" }, cookies: { lt_rt: "rt" } }), {
            config: go.config(),
        });
        expect(res.status).toBe(403);
        expect(go.requests).toHaveLength(0);
    });
});

describe("GET /api/auth/refresh?next= (the proxy.ts bounce)", () => {
    const nav = { "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Site": "none" };

    it("rotates both cookies and 303s to next", async () => {
        go.on("POST", "/v1/auth/refresh", () => ({ status: 200, body: { data: tokens(4) } }));
        const res = await refreshGet(webRequest("/api/auth/refresh?next=%2Fapp%2Fcustomers%3Ftab%3D2", { headers: nav, cookies: { lt_rt: "rt" } }), {
            config: go.config(),
        });
        expect(res.status).toBe(303);
        expect(res.headers.get("location")).toBe("/app/customers?tab=2");
        expect([...cookiesOf(res).keys()].sort()).toEqual(["lt_at", "lt_rt"]);
    });

    it("rotates even while lt_at is still valid: the gate only sends unusable tokens here", async () => {
        go.on("POST", "/v1/auth/refresh", () => ({ status: 200, body: { data: tokens(5) } }));
        const at = await signAccess(key, { ttl: 600 });
        const res = await refreshGet(webRequest("/api/auth/refresh?next=/app/dashboard", { headers: nav, cookies: { lt_at: at, lt_rt: "rt" } }), {
            config: go.config(),
        });
        expect(res.status).toBe(303);
        expect(go.calls("POST", "/v1/auth/refresh")).toHaveLength(1);
    });

    it("303s to /login?next= without lt_rt (acceptance criterion: /app without cookie ends on the login page)", async () => {
        const res = await refreshGet(webRequest("/api/auth/refresh?next=%2Fapp%2Fdrivers", { headers: nav }), { config: go.config() });
        expect(res.status).toBe(303);
        expect(res.headers.get("location")).toBe("/login?next=%2Fapp%2Fdrivers");
        expect(go.requests).toHaveLength(0);
    });

    it("clears both cookies and 303s to /login with reason=revoked when the session was revoked", async () => {
        go.on("POST", "/v1/auth/refresh", () => ({ status: 401, body: goError("session_revoked") }));
        const res = await refreshGet(webRequest("/api/auth/refresh?next=/app/drivers", { headers: nav, cookies: { lt_rt: "rt" } }), { config: go.config() });
        expect(res.headers.get("location")).toBe("/login?next=%2Fapp%2Fdrivers&reason=revoked");
        expect([...cookiesOf(res).values()].map((c) => c.attrs.get("max-age"))).toEqual(["0", "0"]);
    });

    it("refuses a non-navigation (prefetch, RSC fetch) without rotating", async () => {
        const res = await refreshGet(webRequest("/api/auth/refresh?next=/app", { headers: { "Sec-Fetch-Mode": "cors" }, cookies: { lt_rt: "rt" } }), {
            config: go.config(),
        });
        expect(res.status).toBe(400);
        expect(go.requests).toHaveLength(0);
    });

    it("keeps the cookies and answers 503 when Go is unreachable", async () => {
        const res = await refreshGet(webRequest("/api/auth/refresh?next=/app", { headers: nav, cookies: { lt_rt: "rt" } }), {
            config: go.config({ goApiInternalUrl: "http://127.0.0.1:1" }),
        });
        expect(res.status).toBe(503);
        expect(setCookies(res)).toEqual([]);
    });
});

describe("rotation sharing: one Go rotation per refresh token in this process", () => {
    // Go answers a token presented again within its 30 s grace with a sibling and revokes the first
    // successor (Appendix C §C.4.4): two rotations of one token keep the browser signed in only if it
    // applies the later Set-Cookie last. Navigations run outside the browser's refresh lock.
    const nav = { "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Site": "none" };
    let clock = 0;
    const deps = () => ({ config: go.config(), now: () => clock });
    let issued = 0;

    beforeEach(() => {
        clock = Date.now();
        issued = 0;
        go.on("POST", "/v1/auth/refresh", () => {
            issued += 1;
            return { status: 200, delayMs: 30, body: { data: tokens(100 + issued) } };
        });
    });

    const bounce = (rt: string) => refreshGet(webRequest("/api/auth/refresh?next=/app/drivers", { headers: nav, cookies: { lt_rt: rt } }), deps());
    const post = (rt: string, force = false) =>
        refreshPost(webRequest("/api/auth/refresh", { ...json(force ? { force: true } : {}), cookies: { lt_rt: rt } }), deps());
    const newRt = (res: Response) => cookiesOf(res).get("lt_rt")?.value;

    it("makes one Go call for two concurrent bounces, and both set the same pair", async () => {
        const [a, b] = await Promise.all([bounce("rt0"), bounce("rt0")]);
        expect(go.calls("POST", "/v1/auth/refresh")).toHaveLength(1);
        for (const res of [a, b]) {
            expect(res.status).toBe(303);
            expect(res.headers.get("location")).toBe("/app/drivers");
            expect(newRt(res)).toBe("refresh-token-101");
            expect(cookiesOf(res).get("lt_at")?.value).toBe("access.token.v101");
        }
    });

    it("makes one Go call for a bounce racing a tab's POST", async () => {
        const [a, b] = await Promise.all([bounce("rt0"), post("rt0")]);
        expect(go.calls("POST", "/v1/auth/refresh")).toHaveLength(1);
        expect([a.status, b.status]).toEqual([303, 204]);
        expect(newRt(a)).toBe("refresh-token-101");
        expect(newRt(b)).toBe("refresh-token-101");
    });

    it("answers a request sent before the browser applied the first Set-Cookie with the same pair, for 10 s", async () => {
        expect(newRt(await bounce("rt0"))).toBe("refresh-token-101");
        clock += ROTATION_SHARE_MS - 1;
        expect(newRt(await post("rt0"))).toBe("refresh-token-101");
        expect(go.calls("POST", "/v1/auth/refresh")).toHaveLength(1);
        clock += 1;
        expect(newRt(await bounce("rt0"))).toBe("refresh-token-102"); // Go decides (its grace, or reuse)
        expect(go.calls("POST", "/v1/auth/refresh")).toHaveLength(2);
    });

    it("sends a late copy of the old token to Go once the browser presents the successor", async () => {
        expect(newRt(await bounce("rt0"))).toBe("refresh-token-101");
        expect(newRt(await post("refresh-token-101"))).toBe("refresh-token-102");
        expect(newRt(await bounce("rt0"))).toBe("refresh-token-103");
        expect(go.calls("POST", "/v1/auth/refresh").map((r) => JSON.parse(r.body).refreshToken)).toEqual(["rt0", "refresh-token-101", "rt0"]);
    });

    it("lets a forced refresh wait for a rotation in flight but never take a finished one (R78)", async () => {
        const [a, b] = await Promise.all([bounce("rt0"), post("rt0", true)]);
        expect(newRt(a)).toBe("refresh-token-101");
        expect(newRt(b)).toBe("refresh-token-101");
        expect(newRt(await post("rt0", true))).toBe("refresh-token-102");
        expect(go.calls("POST", "/v1/auth/refresh")).toHaveLength(2);
    });

    it("gives a refusal to every waiting request, clearing both cookies, and keeps none", async () => {
        go.on("POST", "/v1/auth/refresh", () => ({ status: 401, delayMs: 30, body: goError("session_revoked") }));
        const [a, b] = await Promise.all([bounce("rt0"), post("rt0")]);
        expect(go.calls("POST", "/v1/auth/refresh")).toHaveLength(1);
        expect(a.status).toBe(303);
        expect(a.headers.get("location")).toBe("/login?next=%2Fapp%2Fdrivers&reason=revoked");
        expect(b.status).toBe(401);
        expect((await b.json()).error.code).toBe("session_revoked");
        for (const res of [a, b]) expect([...cookiesOf(res).values()].map((c) => `${c.name}:${c.attrs.get("max-age")}`).sort()).toEqual(["lt_at:0", "lt_rt:0"]);
        await post("rt0");
        expect(go.calls("POST", "/v1/auth/refresh")).toHaveLength(2);
    });

    it("gives a Go failure to every waiting request, keeping the cookies, and keeps none", async () => {
        go.on("POST", "/v1/auth/refresh", () => ({ status: 503, delayMs: 30, headers: { "Retry-After": "7" }, body: goError("unavailable") }));
        const [a, b] = await Promise.all([bounce("rt0"), post("rt0")]);
        expect(go.calls("POST", "/v1/auth/refresh")).toHaveLength(1);
        expect(a.status).toBe(503);
        expect(b.status).toBe(503);
        expect(b.headers.get("retry-after")).toBe("7");
        expect((await b.json()).error.code).toBe("unavailable");
        expect([...setCookies(a), ...setCookies(b)]).toEqual([]);
        await bounce("rt0");
        expect(go.calls("POST", "/v1/auth/refresh")).toHaveLength(2);
    });
});

describe("safeNext", () => {
    it.each([
        ["/app/x?y=1", "/app/x?y=1"],
        ["/app", "/app"],
        ["/app/", "/app/"],
        [null, "/app"],
        ["", "/app"],
        ["https://evil.test/app", "/app"],
        ["//evil.test/app", "/app"],
        ["/\\evil.test", "/app"],
        ["/login", "/app"],
        ["/apple", "/app"],
        ["/app/../login", "/app"],
        ["/app/%2e%2e/login", "/app"],
        ["/app\n/x", "/app"],
    ])("%s -> %s", (input, want) => {
        expect(safeNext(input)).toBe(want);
    });
});

describe("POST /api/auth/logout", () => {
    it("revokes at Go with the bearer and the refresh token, then expires both cookies", async () => {
        go.on("POST", "/v1/auth/logout", () => ({ status: 204 }));
        const res = await logout(webRequest("/api/auth/logout", { method: "POST", headers: SAME_ORIGIN, cookies: { lt_at: "a.b.c", lt_rt: "rt" } }), {
            config: go.config(),
        });
        expect(res.status).toBe(204);
        const [seen] = go.calls("POST", "/v1/auth/logout");
        expect(seen.headers.authorization).toBe("Bearer a.b.c");
        expect(JSON.parse(seen.body)).toEqual({ refreshToken: "rt" });
        expect([...cookiesOf(res).values()].map((c) => `${c.name}:${c.attrs.get("path")}:${c.attrs.get("max-age")}`).sort()).toEqual([
            "lt_at:/:0",
            "lt_rt:/api/auth:0",
        ]);
    });

    it("still clears the cookies when Go refuses the credentials", async () => {
        go.on("POST", "/v1/auth/logout", () => ({ status: 401, body: goError("invalid_token") }));
        const res = await logout(webRequest("/api/auth/logout", { method: "POST", headers: SAME_ORIGIN, cookies: { lt_at: "a.b.c" } }), { config: go.config() });
        expect(res.status).toBe(204);
        expect(setCookies(res)).toHaveLength(2);
    });

    it("does not call Go without a cookie", async () => {
        const res = await logout(webRequest("/api/auth/logout", { method: "POST", headers: SAME_ORIGIN }), { config: go.config() });
        expect(res.status).toBe(204);
        expect(go.requests).toHaveLength(0);
    });
});

describe("POST /api/auth/tenant", () => {
    it("forwards tenantId with the bearer and replaces lt_at only", async () => {
        go.on("POST", "/v1/auth/tenant", () => ({ status: 200, body: { data: { accessToken: "new.access.tok", expiresIn: 900 } } }));
        const res = await tenant(webRequest("/api/auth/tenant", { ...json({ tenantId: "t2", other: 1 }), cookies: { lt_at: "a.b.c", lt_rt: "rt" } }), {
            config: go.config(),
        });
        expect(res.status).toBe(204);
        const [seen] = go.calls("POST", "/v1/auth/tenant");
        expect(seen.headers.authorization).toBe("Bearer a.b.c");
        expect(JSON.parse(seen.body)).toEqual({ tenantId: "t2" });
        expect([...cookiesOf(res).keys()]).toEqual(["lt_at"]);
        expect(cookiesOf(res).get("lt_at")?.value).toBe("new.access.tok");
    });

    it("passes Go's refusal through", async () => {
        go.on("POST", "/v1/auth/tenant", () => ({ status: 403, body: goError("permission_denied") }));
        const res = await tenant(webRequest("/api/auth/tenant", { ...json({ tenantId: "t9" }), cookies: { lt_at: "a.b.c" } }), { config: go.config() });
        expect(res.status).toBe(403);
        expect(setCookies(res)).toEqual([]);
    });
});

describe("POST /api/auth/firebase-token (P0 bridge, R80)", () => {
    it("passes the custom token through, called with the bearer", async () => {
        go.on("POST", "/v1/bridge/firebase-token", () => ({ status: 200, body: { data: { customToken: "fb.custom.token" } } }));
        const res = await firebaseToken(webRequest("/api/auth/firebase-token", { method: "POST", headers: SAME_ORIGIN, cookies: { lt_at: "a.b.c" } }), {
            config: go.config(),
        });
        expect(res.status).toBe(200);
        expect(await res.json()).toEqual({ data: { customToken: "fb.custom.token" } });
        expect(go.calls("POST", "/v1/bridge/firebase-token")[0].headers.authorization).toBe("Bearer a.b.c");
    });

    it("passes 404 through when the bridge mode excludes web", async () => {
        go.on("POST", "/v1/bridge/firebase-token", () => ({ status: 404, body: goError("not_found") }));
        const res = await firebaseToken(webRequest("/api/auth/firebase-token", { method: "POST", headers: SAME_ORIGIN, cookies: { lt_at: "a.b.c" } }), {
            config: go.config(),
        });
        expect(res.status).toBe(404);
    });

    it("refuses a foreign Origin", async () => {
        const res = await firebaseToken(webRequest("/api/auth/firebase-token", { method: "POST", headers: { Origin: "https://evil.test" } }), { config: go.config() });
        expect(res.status).toBe(403);
    });
});

it("every BFF answer carries X-Request-Id and Cache-Control: no-store", async () => {
    go.on("POST", "/v1/auth/login", () => ({ status: 200, body: { data: tokens(1) } }));
    const res = await login(webRequest("/api/auth/login", { ...json({ email: "a", password: "b" }), headers: { ...SAME_ORIGIN, "X-Request-Id": "rid-12345678" } }), {
        config: go.config(),
    });
    expect(res.headers.get("x-request-id")).toBe("rid-12345678");
    expect(res.headers.get("cache-control")).toBe("no-store");
    expect(WEB_ORIGIN).toBe("https://web.test");
});
