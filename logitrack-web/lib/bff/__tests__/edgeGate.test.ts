// @vitest-environment node
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import { JWKS_FORCED_RELOAD_INTERVAL_MS, resetJwksForTests } from "../accessToken";
import { edgeGate, ME_CACHE_TTL_MS, resetGateCacheForTests } from "../edgeGate";
import { FakeGo, goError, newSigningKey, parseSetCookie, signAccess, WEB_ORIGIN, webRequest, type SigningKey, type TokenClaims } from "./fakeGo";

const go = new FakeGo();
let key: SigningKey;
let previous: SigningKey;
let stranger: SigningKey;
let rotated: SigningKey;
let clock = Date.now();
const now = () => clock;

beforeAll(async () => {
    await go.start();
    [key, previous, stranger, rotated] = await Promise.all([newSigningKey(), newSigningKey(), newSigningKey(), newSigningKey()]);
});
afterAll(async () => {
    await go.stop();
});
beforeEach(() => {
    clock = Date.now();
    go.published = [key];
    go.jwksStatus = 200;
    go.jwksFetches = 0;
    resetJwksForTests();
    resetGateCacheForTests();
});
afterEach(() => {
    vi.useRealTimers();
    go.requests.length = 0;
    go.routes.clear();
});

const MANAGER_CAPS = ["fleet:view_trucks", "drivers:view", "operations:view_driver_monitor", "accounting:view_income", "users:view"];

function me(over: Record<string, unknown> = {}) {
    return {
        id: "user-1",
        tenant: { id: "t1", nameTh: "Own", kind: "own_fleet", role: "manager" },
        tenants: [],
        platformRoles: [],
        dispatcher: false,
        steward: true,
        driver: null,
        customerScopes: [],
        capabilities: MANAGER_CAPS,
        mustChangePassword: false,
        ...over,
    };
}

function serveMe(body: () => unknown) {
    go.on("GET", "/v1/me", () => ({ status: 200, body: { data: body() } }));
}

async function gate(path: string, claims: TokenClaims | null = {}, signer?: SigningKey, headers?: Record<string, string>) {
    const cookies = claims === null ? undefined : { lt_at: await signAccess(signer ?? key, claims) };
    return edgeGate(webRequest(path, { cookies, headers }), { config: go.config(), now });
}

function location(res: Response | undefined): string | null {
    expect(res).toBeDefined();
    expect(res!.status).toBe(307);
    return res!.headers.get("location");
}

describe("edge gate (proxy.ts, R39)", () => {
    it("bounces a request without lt_at to /api/auth/refresh?next=<path+query> before any page code (acceptance criterion)", async () => {
        const res = await gate("/app/customers/c1?tab=2&_rsc=abc", null);
        expect(location(res)).toBe(`${WEB_ORIGIN}/api/auth/refresh?next=%2Fapp%2Fcustomers%2Fc1%3Ftab%3D2`);
        expect(res!.headers.get("cache-control")).toBe("no-store");
        expect(go.requests).toHaveLength(0);
    });

    it("bounces an expired lt_at the same way", async () => {
        const res = await gate("/app/dashboard", { ttl: -120 });
        expect(location(res)).toBe(`${WEB_ORIGIN}/api/auth/refresh?next=%2Fapp%2Fdashboard`);
    });

    it.each([
        ["a key Go never published", {}, "stranger"],
        ["another issuer", { iss: "http://elsewhere.test" }, "key"],
        ["another audience", { aud: "someone-else" }, "key"],
    ] as const)("sends a token signed with %s to /login and expires lt_at", async (_label, claims, signer) => {
        const res = await gate("/app/dashboard", claims, signer === "stranger" ? stranger : key);
        expect(location(res)).toBe(`${WEB_ORIGIN}/login?next=%2Fapp%2Fdashboard`);
        const cleared = parseSetCookie(res!.headers.get("set-cookie")!);
        expect(cleared.name).toBe("lt_at");
        expect(cleared.attrs.get("max-age")).toBe("0");
        expect(go.calls("GET", "/v1/me")).toHaveLength(0);
    });

    it("answers 503 (and signs nobody out) while the JWKS cannot be read", async () => {
        go.jwksStatus = 500;
        const res = await gate("/app/dashboard");
        expect(res!.status).toBe(503);
        expect(res!.headers.get("set-cookie")).toBeNull();
    });

    it("verifies a token signed by the previous key during a rotation (acceptance criterion)", async () => {
        go.published = [key, previous];
        serveMe(() => me());
        expect(await gate("/app/dashboard", {}, previous)).toBeUndefined();
        expect(await gate("/app/dashboard", {}, key)).toBeUndefined();
    });

    describe("a key Go publishes while the web holds a key set fetched less than 30 s ago (jose's cooldown)", () => {
        // The rotation runbook (Appendix C §C.4.2) deploys the new key as active at once: right after
        // the api restarts, logins and refreshes carry a kid the cached set does not have yet.
        function expectLogin(res: Response | undefined) {
            expect(location(res)).toBe(`${WEB_ORIGIN}/login?next=%2Fapp%2Fdashboard`);
            expect(parseSetCookie(res!.headers.get("set-cookie")!).attrs.get("max-age")).toBe("0");
        }

        /** Moves the clock jose and the gate read. */
        function advance(ms: number) {
            vi.setSystemTime(Date.now() + ms);
            clock += ms;
        }

        it("lets a token of the new key through with one more fetch, and keeps verifying the previous key", async () => {
            serveMe(() => me());
            expect(await gate("/app/dashboard")).toBeUndefined();
            expect(go.jwksFetches).toBe(1);
            go.published = [rotated, key]; // the api restarted with a new active key
            const res = await gate("/app/dashboard", {}, rotated);
            expect(res).toBeUndefined();
            expect(go.jwksFetches).toBe(2);
            expect(await gate("/app/dashboard", {}, key)).toBeUndefined();
            expect(await gate("/app/dashboard", {}, rotated)).toBeUndefined();
            expect(go.jwksFetches).toBe(2);
        });

        it("still sends a kid Go never published to /login, forcing at most one fetch every 5 s", async () => {
            vi.useFakeTimers({ toFake: ["Date"] });
            serveMe(() => me());
            expect(await gate("/app/dashboard")).toBeUndefined();
            expectLogin(await gate("/app/dashboard", {}, stranger));
            expect(go.jwksFetches).toBe(2);
            advance(JWKS_FORCED_RELOAD_INTERVAL_MS - 1_000);
            expectLogin(await gate("/app/dashboard", {}, stranger)); // the last fetch is that recent: its answer stands
            expect(go.jwksFetches).toBe(2);
            advance(1_000);
            expectLogin(await gate("/app/dashboard", {}, stranger));
            expect(go.jwksFetches).toBe(3);
        });

        it("answers 503 and signs nobody out while that fetch fails, then lets the token through", async () => {
            vi.useFakeTimers({ toFake: ["Date"] });
            serveMe(() => me());
            expect(await gate("/app/dashboard")).toBeUndefined();
            go.published = [rotated, key];
            go.jwksStatus = 500;
            for (const res of [await gate("/app/dashboard", {}, rotated), await gate("/app/dashboard", {}, rotated)]) {
                expect(res!.status).toBe(503);
                expect(res!.headers.get("set-cookie")).toBeNull();
            }
            expect(go.jwksFetches).toBe(2); // the second request inside the 5 s interval did not fetch
            go.jwksStatus = 200;
            advance(JWKS_FORCED_RELOAD_INTERVAL_MS);
            expect(await gate("/app/dashboard", {}, rotated)).toBeUndefined();
            expect(go.jwksFetches).toBe(3);
        });

        it("shares one forced fetch between concurrent requests", async () => {
            serveMe(() => me());
            expect(await gate("/app/dashboard")).toBeUndefined();
            go.published = [rotated, key];
            const results = await Promise.all([1, 2, 3].map(() => gate("/app/dashboard", {}, rotated)));
            expect(results).toEqual([undefined, undefined, undefined]);
            expect(go.jwksFetches).toBe(2);
        });
    });

    it("sends a driver-only principal to /app/unauthorized without asking Go", async () => {
        expect(location(await gate("/app/dashboard", { rol: "driver", drv: "d1", tid: "t1" }))).toBe(`${WEB_ORIGIN}/app/unauthorized?from=%2Fapp%2Fdashboard`);
        expect(await gate("/app/unauthorized", { rol: "driver", drv: "d1", tid: "t1" })).toBeUndefined();
        expect(go.calls("GET", "/v1/me")).toHaveLength(0);
    });

    it("lets an allowed route through, asking GET /v1/me with the same bearer", async () => {
        serveMe(() => me());
        expect(await gate("/app/accounting/income")).toBeUndefined();
        expect(await gate("/app/drivers/view/d1")).toBeUndefined();
        const [seen] = go.calls("GET", "/v1/me");
        expect(seen.headers.authorization).toMatch(/^Bearer ey/);
    });

    it("sends a route the role lacks, and an unmapped route, to /app/unauthorized?from=", async () => {
        serveMe(() => me());
        expect(location(await gate("/app/payroll"))).toBe(`${WEB_ORIGIN}/app/unauthorized?from=%2Fapp%2Fpayroll`);
        expect(location(await gate("/app/not-a-page"))).toBe(`${WEB_ORIGIN}/app/unauthorized?from=%2Fapp%2Fnot-a-page`);
        expect(location(await gate("/app/security-center"))).toBe(`${WEB_ORIGIN}/app/unauthorized?from=%2Fapp%2Fsecurity-center`);
        expect(await gate("/app/security-center/users")).toBeUndefined();
    });

    it("keeps /app/dashboard and /app/unauthorized open to every signed-in principal", async () => {
        serveMe(() => me({ capabilities: [] }));
        expect(await gate("/app/dashboard")).toBeUndefined();
        expect(await gate("/app/unauthorized?from=/app/x")).toBeUndefined();
    });

    describe("/app exactly goes to the role's home (R89)", () => {
        it.each([
            ["own-fleet manager", me(), "/app/dashboard"],
            ["operator", me({ tenant: { kind: "own_fleet", role: "operator" } }), "/app/driver-monitor"],
            ["carrier tenant_admin (legacy partner)", me({ tenant: { kind: "carrier", role: "tenant_admin" } }), "/app/driver-monitor"],
            ["own-fleet tenant_admin (legacy admin)", me({ tenant: { kind: "own_fleet", role: "tenant_admin" } }), "/app/dashboard"],
            ["customer scope without a membership", me({ tenant: null, customerScopes: [{ billingPartyId: "p", name: "X" }] }), "/app/driver-monitor"],
            ["dispatcher", me({ dispatcher: true }), "/app/driver-monitor"],
            ["operator whose override removed the monitor", me({ tenant: { kind: "own_fleet", role: "operator" }, capabilities: ["fleet:view_trucks"] }), "/app/dashboard"],
        ])("%s", async (_label, body, home) => {
            serveMe(() => body);
            expect(location(await gate("/app"))).toBe(`${WEB_ORIGIN}${home}`);
        });
    });

    it("sees a capability that an override removed within 60 s, through the (sid, ver, tid) cache (acceptance criterion)", async () => {
        let caps = MANAGER_CAPS;
        serveMe(() => me({ capabilities: caps }));
        expect(await gate("/app/accounting/income")).toBeUndefined();
        caps = MANAGER_CAPS.filter((c) => c !== "accounting:view_income"); // tenant override: same ver
        clock += 30_000;
        expect(await gate("/app/accounting/income")).toBeUndefined(); // still cached
        expect(go.calls("GET", "/v1/me")).toHaveLength(1);
        clock += ME_CACHE_TTL_MS - 30_000 + 1;
        expect(location(await gate("/app/accounting/income"))).toBe(`${WEB_ORIGIN}/app/unauthorized?from=%2Fapp%2Faccounting%2Fincome`);
        expect(go.calls("GET", "/v1/me")).toHaveLength(2);
    });

    it("misses the cache at once on a new ver (role change) or tid (tenant switch)", async () => {
        serveMe(() => me());
        await gate("/app/dashboard", { ver: 1, tid: "t1" });
        await gate("/app/dashboard", { ver: 2, tid: "t1" });
        await gate("/app/dashboard", { ver: 2, tid: "t2" });
        await gate("/app/dashboard", { ver: 2, tid: "t2" });
        expect(go.calls("GET", "/v1/me")).toHaveLength(3);
    });

    it("shares one GET /v1/me between concurrent navigations", async () => {
        go.on("GET", "/v1/me", () => ({ status: 200, delayMs: 50, body: { data: me() } }));
        const at = await signAccess(key);
        const results = await Promise.all(
            ["/app/dashboard", "/app/drivers", "/app/trucks"].map((p) => edgeGate(webRequest(p, { cookies: { lt_at: at } }), { config: go.config(), now }))
        );
        expect(results).toEqual([undefined, undefined, undefined]);
        expect(go.calls("GET", "/v1/me")).toHaveLength(1);
    });

    it("bounces a stale ver (401 token_expired claims_changed) to the refresh route", async () => {
        go.on("GET", "/v1/me", () => ({ status: 401, body: goError("token_expired", { reason: "claims_changed" }) }));
        expect(location(await gate("/app/drivers"))).toBe(`${WEB_ORIGIN}/api/auth/refresh?next=%2Fapp%2Fdrivers`);
    });

    it("sends a revoked session to /login?reason=revoked and expires lt_at", async () => {
        go.on("GET", "/v1/me", () => ({ status: 401, body: goError("session_revoked") }));
        const res = await gate("/app/drivers");
        expect(location(res)).toBe(`${WEB_ORIGIN}/login?next=%2Fapp%2Fdrivers&reason=revoked`);
        expect(parseSetCookie(res!.headers.get("set-cookie")!).attrs.get("max-age")).toBe("0");
    });

    it("answers 503 when GET /v1/me fails, and caches no failure", async () => {
        go.on("GET", "/v1/me", () => ({ status: 503, body: goError("unavailable") }));
        expect((await gate("/app/drivers"))!.status).toBe(503);
        serveMe(() => me());
        expect(await gate("/app/drivers")).toBeUndefined();
    });

    it("sends a 403 from GET /v1/me to /app/unauthorized", async () => {
        go.on("GET", "/v1/me", () => ({ status: 403, body: goError("driver_profile_required") }));
        expect(location(await gate("/app/drivers"))).toBe(`${WEB_ORIGIN}/app/unauthorized?from=%2Fapp%2Fdrivers`);
    });

    it("leaves paths outside /app alone", async () => {
        expect(await edgeGate(webRequest("/login"), { config: go.config(), now })).toBeUndefined();
        expect(await edgeGate(webRequest("/apple"), { config: go.config(), now })).toBeUndefined();
    });
});
