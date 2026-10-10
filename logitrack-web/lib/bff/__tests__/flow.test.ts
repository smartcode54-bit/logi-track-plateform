// @vitest-environment node
/**
 * End to end in one process: the browser client (lib/goFetch.ts, lib/sharedRefresh.ts,
 * lib/sessionEnd.ts) talks to the real BFF handlers through a same-origin fetch with a cookie jar
 * that honours Path, and the handlers talk to a fake Go over HTTP. Covers the TW3 acceptance criteria
 * "only same-origin requests" and "an expired access token is refreshed once by the browser and the
 * request retried; the generic proxy itself never calls Go refresh" (R37, R78). It runs in the node
 * environment (the handlers need Node's fetch and AbortSignal) with a minimal browser `window`.
 */
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import { goFetch } from "../../goFetch";
import { configureSessionEnd, resetSessionEndForTests } from "../../sessionEnd";
import { LAST_FORCED_REFRESH_KEY, LAST_REFRESH_KEY } from "../../sharedRefresh";
import { resetJwksForTests } from "../accessToken";
import { logout, refreshPost, resetRotationsForTests } from "../authRoutes";
import { proxyToGo } from "../goProxy";
import { FakeGo, goError, newSigningKey, parseSetCookie, signAccess, WEB_ORIGIN, type SigningKey } from "./fakeGo";

const go = new FakeGo();
const realFetch = globalThis.fetch;
let key: SigningKey;

/** The browser's cookie jar for WEB_ORIGIN: name -> value and path. */
const jar = new Map<string, { value: string; path: string }>();
/** Every request the browser code made. */
const browserRequests: { method: string; url: string; body: string }[] = [];
/** Absolute URLs some browser code tried to reach (must stay empty). */
const crossOrigin: string[] = [];
let accessForGo = "";
let refreshForGo = "";
let generation = 0;

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
        else jar.set(c.name, { value: c.value, path: c.attrs.get("path") ?? "/" });
    }
}

/** The browser's fetch: same-origin only, cookies by path, Origin + Sec-Fetch-Site on mutations. */
async function browserFetch(input: RequestInfo | URL, init: RequestInit = {}): Promise<Response> {
    const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    if (/^[a-z]+:/i.test(url)) {
        // Server-side code in this process (jose fetching the JWKS) reaches Go directly.
        if (url.startsWith(go.url)) return realFetch(input, init);
        crossOrigin.push(url);
        throw new TypeError("cross-origin request from the browser");
    }
    const method = (init.method ?? "GET").toUpperCase();
    const body = typeof init.body === "string" ? init.body : "";
    browserRequests.push({ method, url, body });
    const headers = new Headers(init.headers);
    const path = url.split("?")[0];
    const cookies = cookieHeader(path);
    if (cookies) headers.set("cookie", cookies);
    if (method !== "GET" && method !== "HEAD") {
        headers.set("Origin", WEB_ORIGIN);
        headers.set("Sec-Fetch-Site", "same-origin");
    }
    const req = new Request(`${WEB_ORIGIN}${url}`, { method, headers, body: body || undefined, signal: init.signal ?? undefined });
    const deps = { config: go.config(), fetchImpl: realFetch };
    let res: Response;
    if (path.startsWith("/api/go/")) res = await proxyToGo(req, deps);
    else if (path === "/api/auth/refresh" && method === "POST") res = await refreshPost(req, deps);
    else if (path === "/api/auth/logout" && method === "POST") res = await logout(req, deps);
    else res = new Response(null, { status: 404 });
    store(res);
    return res;
}

/** The page the browser shows, and its localStorage. */
const page = { pathname: "/app/driver-monitor", search: "" };
const storage = new Map<string, string>();

beforeAll(async () => {
    await go.start();
    key = await newSigningKey();
    go.published = [key];
    globalThis.fetch = browserFetch as typeof fetch;
    vi.stubGlobal("window", {
        location: {
            get pathname() {
                return page.pathname;
            },
            get search() {
                return page.search;
            },
            assign: () => undefined,
        },
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
    // A session ended on the protected page of an earlier test must not turn endSession into a no-op.
    resetSessionEndForTests();
    storage.delete(LAST_REFRESH_KEY);
    storage.delete(LAST_FORCED_REFRESH_KEY);
    go.on("POST", "/v1/auth/refresh", (r) => {
        const presented = (JSON.parse(r.body) as { refreshToken?: string }).refreshToken;
        if (presented !== refreshForGo) return { status: 401, body: goError("invalid_token") };
        generation += 1;
        refreshForGo = `rt-${generation}`;
        return { status: 200, body: { data: { accessToken: accessForGo, refreshToken: refreshForGo, expiresIn: 900 } } };
    });
    go.on("POST", "/v1/auth/logout", () => ({ status: 204 }));
});
afterEach(() => {
    go.requests.length = 0;
    go.routes.clear();
    jar.clear();
    browserRequests.length = 0;
});

/** Go's monitor route: 200 for the current access token, else the given 401. */
function monitor(stale: () => { code: string; details: Record<string, unknown> }) {
    go.on("GET", "/v1/trips/monitor", (r) =>
        r.headers.authorization === `Bearer ${accessForGo}` ? { status: 200, body: { data: { trips: 3 } } } : { status: 401, body: goError(stale().code, stale().details) }
    );
}

async function startSession(accessTtl: number) {
    const expired = await signAccess(key, { ttl: accessTtl });
    accessForGo = await signAccess(key, { ttl: 900, ver: 2 });
    generation = 0;
    refreshForGo = "rt-0";
    jar.set("lt_at", { value: expired, path: "/" });
    jar.set("lt_rt", { value: "rt-0", path: "/api/auth" });
}

describe("browser -> BFF -> Go", () => {
    it("refreshes an expired access token once through /api/auth/refresh and retries; the proxy never calls Go refresh", async () => {
        await startSession(-120);
        monitor(() => ({ code: "token_expired", details: { reason: "expired" } }));

        await expect(goFetch("/v1/trips/monitor")).resolves.toEqual({ trips: 3 });

        expect(browserRequests.map((r) => `${r.method} ${r.url}`)).toEqual([
            "GET /api/go/v1/trips/monitor",
            "POST /api/auth/refresh",
            "GET /api/go/v1/trips/monitor",
        ]);
        expect(crossOrigin).toEqual([]);
        expect(go.requests.map((r) => `${r.method} ${r.url}`)).toEqual(["GET /v1/trips/monitor", "POST /v1/auth/refresh", "GET /v1/trips/monitor"]);
        // The refresh token reached Go only through the auth route, never with a page or /api/go request.
        for (const r of go.calls("GET", "/v1/trips/monitor")) {
            expect(r.headers.cookie).toBeUndefined();
            expect(JSON.stringify(r.headers)).not.toContain("rt-");
        }
        expect(jar.get("lt_rt")?.value).toBe("rt-1");
        expect(jar.get("lt_at")?.value).toBe(accessForGo);
    });

    it("serves concurrent 401s with a single refresh", async () => {
        await startSession(-120);
        monitor(() => ({ code: "token_expired", details: { reason: "expired" } }));
        const results = await Promise.all([goFetch("/v1/trips/monitor"), goFetch("/v1/trips/monitor"), goFetch("/v1/trips/monitor")]);
        expect(results).toEqual([{ trips: 3 }, { trips: 3 }, { trips: 3 }]);
        expect(go.calls("POST", "/v1/auth/refresh")).toHaveLength(1);
    });

    it("refreshes with an access cookie that outlived its Max-Age (no lt_at at all)", async () => {
        await startSession(-120);
        jar.delete("lt_at");
        monitor(() => ({ code: "unauthenticated", details: {} }));
        await expect(goFetch("/v1/trips/monitor")).resolves.toEqual({ trips: 3 });
        expect(go.calls("POST", "/v1/auth/refresh")).toHaveLength(1);
    });

    it('forces the refresh for claims_changed although lt_at has more than 120 s left (R78)', async () => {
        await startSession(600);
        monitor(() => ({ code: "token_expired", details: { reason: "claims_changed" } }));
        await expect(goFetch("/v1/trips/monitor")).resolves.toEqual({ trips: 3 });
        const refresh = browserRequests.find((r) => r.url === "/api/auth/refresh");
        expect(JSON.parse(refresh!.body)).toEqual({ force: true });
        expect(go.calls("POST", "/v1/auth/refresh")).toHaveLength(1);
    });

    it("does not rotate for an unforced caller while lt_at has more than 120 s left", async () => {
        await startSession(600);
        // Another tab already rotated: this tab's stale 401 retries with the cookie as it is.
        accessForGo = jar.get("lt_at")!.value;
        let first = true;
        go.on("GET", "/v1/trips/monitor", () => {
            if (first) {
                first = false;
                return { status: 401, body: goError("token_expired", { reason: "expired" }) };
            }
            return { status: 200, body: { data: { trips: 3 } } };
        });
        await expect(goFetch("/v1/trips/monitor")).resolves.toEqual({ trips: 3 });
        expect(browserRequests.map((r) => r.url)).toContain("/api/auth/refresh");
        expect(go.calls("POST", "/v1/auth/refresh")).toHaveLength(0);
    });

    it("ends the session when Go refuses the refresh token: cookies expired, logout, /login?next=", async () => {
        await startSession(-120);
        refreshForGo = "rt-other"; // the cookie's token was reused elsewhere
        monitor(() => ({ code: "token_expired", details: { reason: "expired" } }));
        const navigated: string[] = [];
        configureSessionEnd({ navigate: (u) => navigated.push(u) });

        await expect(goFetch("/v1/trips/monitor")).rejects.toMatchObject({ status: 401, code: "token_expired" });
        await new Promise((r) => setTimeout(r, 20));

        expect(navigated).toEqual(["/login?next=%2Fapp%2Fdriver-monitor"]);
        expect(browserRequests.map((r) => `${r.method} ${r.url}`)).toEqual([
            "GET /api/go/v1/trips/monitor",
            "POST /api/auth/refresh",
            "POST /api/auth/logout",
        ]);
        expect(jar.size).toBe(0);
        expect(crossOrigin).toEqual([]);
    });
});
