// T17 (developer-spec.md §10.4, §10.6; Appendix E §E.8.2, §E.8.3): the browser client of the BFF.
// A fake BFF stands in for /api/go and /api/auth (TW3); each test loads fresh modules, and the
// two-tab test loads two module graphs that share localStorage and one Web Locks manager, as two
// tabs of one origin do.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

type Modules = {
    goFetch: typeof import("./goFetch");
    session: typeof import("./sessionEnd");
    refresh: typeof import("./sharedRefresh");
};

async function loadTab(): Promise<Modules> {
    vi.resetModules();
    const [goFetch, session, refresh] = await Promise.all([import("./goFetch"), import("./sessionEnd"), import("./sharedRefresh")]);
    return { goFetch, session, refresh };
}

interface Call {
    url: string;
    method: string;
    headers: Headers;
    body: string | undefined;
    signal: AbortSignal | undefined;
}

const calls: Call[] = [];
let handler: (call: Call) => Response | Promise<Response>;

function json(status: number, body: unknown, headers: Record<string, string> = {}): Response {
    return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json", ...headers } });
}

function goError(status: number, code: string, details: Record<string, unknown> = {}): Response {
    return json(status, { error: { code, message: code.replace(/_/g, " "), details, requestId: "req-" + code } });
}

const dataCalls = () => calls.filter((c) => c.url.startsWith("/api/go/"));
const refreshCalls = () => calls.filter((c) => c.url === "/api/auth/refresh");
const logoutCalls = () => calls.filter((c) => c.url === "/api/auth/logout");

/** A Web Locks manager shared by every "tab" of the test: one exclusive FIFO queue per name. */
class FakeLocks {
    private queues = new Map<string, Promise<unknown>>();
    request(name: string, ...args: unknown[]): Promise<unknown> {
        const cb = args[args.length - 1] as (lock: { name: string }) => Promise<unknown>;
        const tail = this.queues.get(name) ?? Promise.resolve();
        const run = tail.then(() => cb({ name }));
        this.queues.set(name, run.catch(() => undefined));
        return run;
    }
}

const UUID_RX = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

beforeEach(() => {
    calls.length = 0;
    window.localStorage.clear();
    window.history.replaceState({}, "", "/app/driver-monitor?day=1");
    vi.stubGlobal(
        "fetch",
        vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
            const call: Call = {
                url: String(input),
                method: init.method ?? "GET",
                headers: new Headers(init.headers),
                body: typeof init.body === "string" ? init.body : undefined,
                signal: init.signal ?? undefined,
            };
            calls.push(call);
            if (call.signal?.aborted) throw new DOMException("aborted", "AbortError");
            if (call.url === "/api/auth/logout") return new Response(null, { status: 204 });
            return handler(call);
        })
    );
});

afterEach(() => {
    vi.unstubAllGlobals();
    Reflect.deleteProperty(navigator, "locks");
});

function useLocks(locks: FakeLocks) {
    Object.defineProperty(navigator, "locks", { value: locks, configurable: true });
}

describe("goFetch: same-origin BFF calls only", () => {
    it("prefixes /api/go, merges the query and sends no credential header", async () => {
        const { goFetch } = await loadTab();
        handler = () => json(200, { data: [{ id: 1 }], nextCursor: "c2", meta: { total: 3 } });
        const env = await goFetch.goFetchEnvelope<{ id: number }[]>("/v1/hubs?fields=minimal", {
            query: { q: "บางนา", tag: ["a", "b"], skip: undefined, none: null, n: 2, on: true },
        });
        expect(env).toEqual({ data: [{ id: 1 }], nextCursor: "c2", meta: { total: 3 } });
        const [call] = calls;
        expect(call.url).toBe("/api/go/v1/hubs?fields=minimal&q=%E0%B8%9A%E0%B8%B2%E0%B8%87%E0%B8%99%E0%B8%B2&tag=a&tag=b&n=2&on=true");
        expect(call.headers.get("Accept")).toBe("application/json");
        expect(call.headers.has("Authorization")).toBe(false);
        expect(call.headers.has("Idempotency-Key")).toBe(false);
    });

    it("refuses anything that is not a /v1 Go path, and encodes goPath values as one segment", async () => {
        const { goFetch } = await loadTab();
        for (const bad of ["https://api.example.com/v1/hubs", "//evil.example/v1/x", "/api/go/v1/hubs", "v1/hubs", "/v2/x", "/v1/a/../me", "/v1//x", "/v1/a%2Fb", "/v1/x#y"]) {
            expect(() => goFetch.goUrl(bad), bad).toThrow(TypeError);
        }
        expect(goFetch.goPath`/v1/trips/${"a/b ?"}/photos`).toBe("/v1/trips/a%2Fb%20%3F/photos");
        // An encoded slash from goPath is refused rather than sent as a path separator.
        expect(() => goFetch.goUrl(goFetch.goPath`/v1/trips/${"a/b"}`)).toThrow(TypeError);
        expect(calls).toHaveLength(0);
    });

    it("refuses dot segments in every spelling fetch would resolve, so a path cannot leave /api/go/v1", async () => {
        const { goFetch } = await loadTab();
        const origin = "http://web.test";
        // Each of these would reach another path once the URL parser normalised it (e.g. /api/auth/refresh).
        const escapes = [
            "/v1/trips/%2e%2e/%2e%2e/%2e%2e/auth/refresh",
            "/v1/%2e%2e/x",
            "/v1/a/.%2e/x",
            "/v1/a/%2E./x",
            "/v1/a/%2E%2E/x",
            "/v1/%2e/x",
            "/v1/a/.\t./x",
            "/v1/a/.\n./x",
            "/v1/a/.\r./x",
            "/v1/a\rb",
            "/v1/a\u0000b",
            "/v1/a%5cb",
            "/v1/a%5Cb",
            "/v1/a%zz",
            "/v1/100%",
        ];
        for (const bad of escapes) {
            expect(() => goFetch.goUrl(bad), JSON.stringify(bad)).toThrow(TypeError);
        }
        // Legitimate values pass, and every accepted URL resolves under /api/go/v1/ unchanged.
        const ok = [
            goFetch.goPath`/v1/trips/${"%2e%2e"}`,
            goFetch.goPath`/v1/hubs/${"บางนา 26"}`,
            "/v1/files/a.b/...",
            goFetch.goPath`/v1/users/${"a..b"}`,
            "/v1/search?q=..%2F..",
        ];
        for (const path of ok) {
            const url = goFetch.goUrl(path, { next: "../../auth/logout" });
            expect(new URL(url, origin).pathname.startsWith("/api/go/v1/"), url).toBe(true);
        }
        expect(goFetch.goUrl(goFetch.goPath`/v1/trips/${"%2e%2e"}`)).toBe("/api/go/v1/trips/%252e%252e");
        // goPath does not encode dots, so a literal ".." value is still refused.
        expect(() => goFetch.goUrl(goFetch.goPath`/v1/trips/${".."}`)).toThrow(TypeError);
        expect(calls).toHaveLength(0);
    });

    it("never lets the caller set credentials or ship files through the BFF", async () => {
        const { goFetch } = await loadTab();
        handler = () => json(200, { data: null });
        await expect(goFetch.goFetch("/v1/me", { headers: { Authorization: "Bearer x" } })).rejects.toThrow(TypeError);
        await expect(goFetch.goFetch("/v1/me", { headers: { cookie: "lt_at=x" } })).rejects.toThrow(TypeError);
        await expect(goFetch.goFetch("/v1/files", { method: "POST", body: new Blob(["x"]) })).rejects.toThrow(TypeError);
        await expect(goFetch.goFetch("/v1/hubs", { idempotencyKey: true })).rejects.toThrow(TypeError);
        await expect(goFetch.goFetch("/v1/tasks", { method: "POST", idempotencyKey: "not-a-uuid" })).rejects.toThrow(TypeError);
        expect(calls).toHaveLength(0);
    });

    it("returns data, undefined for 204, and sends JSON bodies", async () => {
        const { goFetch } = await loadTab();
        handler = (c) => (c.method === "DELETE" ? new Response(null, { status: 204 }) : json(201, { data: { id: "t1" } }));
        await expect(goFetch.goFetch("/v1/tasks", { method: "POST", body: { a: 1 } })).resolves.toEqual({ id: "t1" });
        expect(calls[0].body).toBe('{"a":1}');
        expect(calls[0].headers.get("Content-Type")).toBe("application/json");
        await expect(goFetch.goFetch("/v1/tasks/t1", { method: "DELETE" })).resolves.toBeUndefined();
    });
});

describe("goFetch: typed errors from the envelope (R48, R76)", () => {
    it("throws ApiError with the envelope fields", async () => {
        const { goFetch } = await loadTab();
        handler = () => goError(409, "billing_period_locked", { blockedInvoiceNumber: "INV-1" });
        const err = await goFetch.goFetch("/v1/trips/t1/billing/compute", { method: "POST" }).catch((e) => e);
        expect(err).toBeInstanceOf(goFetch.ApiError);
        expect(goFetch.isApiError(err)).toBe(true);
        expect(err).toMatchObject({
            status: 409,
            code: "billing_period_locked",
            message: "billing period locked",
            details: { blockedInvoiceNumber: "INV-1" },
            requestId: "req-billing_period_locked",
        });
    });

    it("maps a proxy page, a non-envelope body and a network failure to client codes", async () => {
        const { goFetch } = await loadTab();
        handler = () => new Response("<html>502</html>", { status: 502, headers: { "X-Request-Id": "rid-1" } });
        await expect(goFetch.goFetch("/v1/hubs")).rejects.toMatchObject({ status: 502, code: "unavailable", requestId: "rid-1" });
        handler = () => new Response("not json", { status: 200 });
        await expect(goFetch.goFetch("/v1/hubs")).rejects.toMatchObject({ code: "bad_response" });
        handler = () => {
            throw new TypeError("Failed to fetch");
        };
        await expect(goFetch.goFetch("/v1/hubs")).rejects.toMatchObject({ status: 0, code: "network_error" });
    });

    it("passes the abort signal and rethrows the abort as is", async () => {
        const { goFetch } = await loadTab();
        handler = () => json(200, { data: 1 });
        const ac = new AbortController();
        ac.abort();
        const err = await goFetch.goFetch<never>("/v1/hubs", { signal: ac.signal }).catch((e: unknown) => e as DOMException);
        expect(err).toBeInstanceOf(DOMException);
        expect(err.name).toBe("AbortError");
        expect(calls[0].signal).toBe(ac.signal);
    });
});

/**
 * A fake BFF + Go session: the access cookie is valid or expired, carries an auth version, and the
 * user's current version may be newer (a role change, R50). POST /api/auth/refresh behaves as
 * developer-spec.md §10.4 says: a no-op 204 while the access cookie is still valid unless forced.
 */
function fakeSession(init: { accessValid: boolean; cookieVer?: number; userVer?: number; refreshOk?: boolean }) {
    const s = { cookieVer: 1, userVer: 1, refreshOk: true, ...init };
    handler = async (c) => {
        if (c.url === "/api/auth/refresh") {
            await new Promise((r) => setTimeout(r, 5));
            if (!s.refreshOk) return goError(401, "session_revoked");
            const force = c.body ? JSON.parse(c.body).force === true : false;
            if (!s.accessValid || force) {
                s.accessValid = true;
                s.cookieVer = s.userVer;
            }
            return new Response(null, { status: 204 });
        }
        if (!s.accessValid) return goError(401, "token_expired", { reason: "expired" });
        if (s.cookieVer < s.userVer) return goError(401, "token_expired", { reason: "claims_changed" });
        return json(200, { data: { ver: s.cookieVer, path: c.url } });
    };
    return s;
}

describe("goFetch: 401 refresh and retry (R37, R78)", () => {
    it("refreshes once on token_expired and retries once with the same Idempotency-Key", async () => {
        const { goFetch } = await loadTab();
        fakeSession({ accessValid: false });
        const res = await goFetch.goFetch<{ ver: number }>("/v1/tasks", { method: "POST", body: { a: 1 }, idempotencyKey: true });
        expect(res.ver).toBe(1);
        expect(refreshCalls()).toHaveLength(1);
        expect(refreshCalls()[0].body).toBeUndefined(); // unforced: no {"force":true}
        const [first, retry] = dataCalls();
        expect(dataCalls()).toHaveLength(2);
        expect(first.headers.get("Idempotency-Key")).toMatch(UUID_RX);
        expect(retry.headers.get("Idempotency-Key")).toBe(first.headers.get("Idempotency-Key"));
        expect(retry.body).toBe(first.body);
    });

    it("treats a 401 unauthenticated (access cookie past its Max-Age) as refreshable", async () => {
        const { goFetch } = await loadTab();
        let sent = 0;
        handler = (c) => {
            if (c.url === "/api/auth/refresh") return new Response(null, { status: 204 });
            return sent++ === 0 ? goError(401, "unauthenticated") : json(200, { data: "ok" });
        };
        await expect(goFetch.goFetch("/v1/me")).resolves.toBe("ok");
        expect(refreshCalls()).toHaveLength(1);
    });

    it("two tabs receiving token_expired at the same time trigger exactly one POST /api/auth/refresh", async () => {
        useLocks(new FakeLocks());
        const tabA = await loadTab();
        const tabB = await loadTab();
        expect(tabA.goFetch).not.toBe(tabB.goFetch);
        fakeSession({ accessValid: false });
        const [a, b] = await Promise.all([
            tabA.goFetch.goFetch<{ ver: number }>("/v1/trips/monitor"),
            tabB.goFetch.goFetch<{ ver: number }>("/v1/hubs"),
        ]);
        expect(a.ver).toBe(1);
        expect(b.ver).toBe(1);
        expect(refreshCalls()).toHaveLength(1);
        expect(dataCalls()).toHaveLength(4); // two 401s, two retries
        const marker = Number(window.localStorage.getItem(tabA.refresh.LAST_REFRESH_KEY));
        expect(marker).toBeGreaterThan(0); // a timestamp, no token material
    });

    it("concurrent 401s in one tab share one refresh without Web Locks or localStorage", async () => {
        const { goFetch } = await loadTab();
        const getItem = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
            throw new Error("storage blocked");
        });
        try {
            fakeSession({ accessValid: false });
            await Promise.all([goFetch.goFetch("/v1/a"), goFetch.goFetch("/v1/b"), goFetch.goFetch("/v1/c")]);
            expect(refreshCalls()).toHaveLength(1);
        } finally {
            getItem.mockRestore();
        }
    });

    it("claims_changed sends {\"force\":true} and the retry carries the new claims", async () => {
        const { goFetch } = await loadTab();
        // The access cookie is still valid (an unforced refresh would be the BFF's no-op), but a role
        // change bumped the user's auth version.
        fakeSession({ accessValid: true, cookieVer: 1, userVer: 2 });
        const res = await goFetch.goFetch<{ ver: number }>("/v1/me");
        expect(refreshCalls()).toHaveLength(1);
        expect(JSON.parse(refreshCalls()[0].body ?? "{}")).toEqual({ force: true });
        expect(refreshCalls()[0].headers.get("Content-Type")).toBe("application/json");
        expect(res.ver).toBe(2);
        expect(dataCalls()).toHaveLength(2);
    });

    it("an unforced refresh of another tab does not satisfy a claims_changed caller", async () => {
        useLocks(new FakeLocks());
        const tabA = await loadTab();
        const tabB = await loadTab();
        // Tab A's access token expired; its unforced refresh reaches Go just before a role change, so
        // the rotated token still carries the old version. Tab B's request, sent while A refreshes,
        // meets the new version and gets claims_changed: A's refresh must not count for B.
        let accessValid = false;
        let cookieVer = 1;
        handler = async (c) => {
            if (c.url === "/api/auth/refresh") {
                await new Promise((r) => setTimeout(r, 5));
                if (c.body && JSON.parse(c.body).force === true) cookieVer = 2;
                accessValid = true;
                return new Response(null, { status: 204 });
            }
            if (c.url === "/api/go/v1/a") {
                return accessValid ? json(200, { data: { ver: cookieVer } }) : goError(401, "token_expired", { reason: "expired" });
            }
            return cookieVer < 2 ? goError(401, "token_expired", { reason: "claims_changed" }) : json(200, { data: { ver: cookieVer } });
        };
        const since = Date.now() - 1;
        const [a, b] = await Promise.all([
            tabA.goFetch.goFetch<{ ver: number }>("/v1/a"),
            tabB.goFetch.goFetch<{ ver: number }>("/v1/b"),
        ]);
        expect(a.ver).toBe(1);
        expect(b.ver).toBe(2);
        expect(refreshCalls().map((c) => c.body)).toEqual([undefined, '{"force":true}']);
        expect(tabA.refresh.refreshedSince(since, false)).toBe(true);
        expect(Number(window.localStorage.getItem(tabA.refresh.LAST_FORCED_REFRESH_KEY))).toBeGreaterThan(since);
    });

    it("a 401 after the single refresh-and-retry ends the session and redirects to /login?next=", async () => {
        const { goFetch, session } = await loadTab();
        const navigate = vi.fn();
        const ended = vi.fn();
        session.configureSessionEnd({ navigate });
        session.onSessionEnd(ended);
        handler = (c) => (c.url === "/api/auth/refresh" ? new Response(null, { status: 204 }) : goError(401, "token_expired", { reason: "expired" }));
        const err = await goFetch.goFetch("/v1/hubs").catch((e) => e);
        expect(err).toMatchObject({ status: 401, code: "token_expired" });
        expect(dataCalls()).toHaveLength(2); // exactly one retry
        expect(refreshCalls()).toHaveLength(1);
        expect(navigate).toHaveBeenCalledTimes(1);
        expect(navigate).toHaveBeenCalledWith("/login?next=%2Fapp%2Fdriver-monitor%3Fday%3D1");
        expect(ended).toHaveBeenCalledWith(err);
        expect(logoutCalls()).toHaveLength(1);
    });

    it("session_revoked and invalid_token end the session without a refresh", async () => {
        for (const code of ["session_revoked", "invalid_token"]) {
            calls.length = 0;
            const { goFetch, session } = await loadTab();
            const navigate = vi.fn();
            session.configureSessionEnd({ navigate });
            handler = () => goError(401, code);
            await expect(goFetch.goFetch("/v1/hubs")).rejects.toMatchObject({ code });
            expect(refreshCalls()).toHaveLength(0);
            expect(dataCalls()).toHaveLength(1);
            const reason = code === "session_revoked" ? "&reason=revoked" : "";
            expect(navigate).toHaveBeenCalledWith(`/login?next=%2Fapp%2Fdriver-monitor%3Fday%3D1${reason}`);
        }
    });

    it("a refused refresh ends the session once, however many requests failed", async () => {
        const { goFetch, session } = await loadTab();
        const navigate = vi.fn();
        session.configureSessionEnd({ navigate });
        fakeSession({ accessValid: false, refreshOk: false });
        const results = await Promise.allSettled([goFetch.goFetch("/v1/a"), goFetch.goFetch("/v1/b")]);
        expect(results.every((r) => r.status === "rejected")).toBe(true);
        expect(dataCalls()).toHaveLength(2); // no retry
        expect(navigate).toHaveBeenCalledTimes(1);
        expect(logoutCalls()).toHaveLength(1);
    });

    it("a refresh that cannot reach the server keeps the session", async () => {
        const { goFetch, session } = await loadTab();
        const navigate = vi.fn();
        session.configureSessionEnd({ navigate });
        handler = (c) => {
            if (c.url === "/api/auth/refresh") throw new TypeError("Failed to fetch");
            return goError(401, "token_expired", { reason: "expired" });
        };
        await expect(goFetch.goFetch("/v1/hubs")).rejects.toMatchObject({ code: "network_error" });
        expect(navigate).not.toHaveBeenCalled();
        expect(dataCalls()).toHaveLength(1);
    });

    it("an anonymous call (password forms) returns its 401 without a refresh or sign-out", async () => {
        const { goFetch, session } = await loadTab();
        const navigate = vi.fn();
        session.configureSessionEnd({ navigate });
        handler = () => goError(401, "invalid_token");
        await expect(
            goFetch.goFetch("/v1/auth/password/change", { method: "POST", body: { passwordChangeTicket: "t", newPassword: "x" }, anonymous: true })
        ).rejects.toMatchObject({ status: 401, code: "invalid_token" });
        expect(refreshCalls()).toHaveLength(0);
        expect(navigate).not.toHaveBeenCalled();
    });

    it("a 304 to the caller's If-None-Match yields no data", async () => {
        const { goFetch } = await loadTab();
        handler = () => new Response(null, { status: 304 });
        await expect(goFetch.goFetch("/v1/hubs", { headers: { "If-None-Match": '"v1"' } })).resolves.toBeUndefined();
        expect(calls[0].headers.get("If-None-Match")).toBe('"v1"');
    });

    it("leaves next out on the login page itself", async () => {
        const { session } = await loadTab();
        window.history.replaceState({}, "", "/login");
        expect(session.loginUrl()).toBe("/login");
        window.history.replaceState({}, "", "/app/users");
        expect(session.loginUrl({ code: "session_revoked" })).toBe("/login?next=%2Fapp%2Fusers&reason=revoked");
    });
});

describe("onClaimsRefreshed: the follow-up of a claims_changed refresh (§10.4 step 3, Appendix E §E.8.3)", () => {
    it("a 401 claims_changed runs the listener exactly once, before goFetch resolves", async () => {
        const { goFetch, refresh } = await loadTab();
        fakeSession({ accessValid: true, cookieVer: 1, userVer: 2 });
        const order: string[] = [];
        refresh.onClaimsRefreshed(() => order.push(`listener after ${refreshCalls().length} refresh, ${dataCalls().length} data calls`));
        const res = await goFetch.goFetch<{ ver: number }>("/v1/me");
        order.push("resolved");
        expect(res.ver).toBe(2);
        // After the forced refresh, before the retry and before the caller sees the result.
        expect(order).toEqual(["listener after 1 refresh, 1 data calls", "resolved"]);
    });

    it("concurrent claims_changed 401s in one tab notify once", async () => {
        const { goFetch, refresh } = await loadTab();
        fakeSession({ accessValid: true, cookieVer: 1, userVer: 2 });
        const listener = vi.fn();
        refresh.onClaimsRefreshed(listener);
        await Promise.all([goFetch.goFetch("/v1/me"), goFetch.goFetch("/v1/hubs"), goFetch.goFetch("/v1/trips/monitor")]);
        expect(refreshCalls()).toHaveLength(1);
        expect(listener).toHaveBeenCalledTimes(1);
    });

    it("a tab served by another tab's forced refresh still runs its own listener once", async () => {
        useLocks(new FakeLocks());
        const tabA = await loadTab();
        const tabB = await loadTab();
        fakeSession({ accessValid: true, cookieVer: 1, userVer: 2 });
        const inA = vi.fn();
        const inB = vi.fn();
        tabA.refresh.onClaimsRefreshed(inA);
        tabB.refresh.onClaimsRefreshed(inB);
        const [a, b] = await Promise.all([
            tabA.goFetch.goFetch<{ ver: number }>("/v1/me"),
            tabB.goFetch.goFetch<{ ver: number }>("/v1/me"),
        ]);
        expect([a.ver, b.ver]).toEqual([2, 2]);
        expect(refreshCalls()).toHaveLength(1); // one tab refreshed, the other skipped on the forced marker
        expect(inA).toHaveBeenCalledTimes(1);
        expect(inB).toHaveBeenCalledTimes(1);
    });

    it("an unforced token_expired refresh does not notify", async () => {
        const { goFetch, refresh } = await loadTab();
        fakeSession({ accessValid: false });
        const listener = vi.fn();
        refresh.onClaimsRefreshed(listener);
        await goFetch.goFetch("/v1/hubs");
        expect(refreshCalls()).toHaveLength(1);
        expect(listener).not.toHaveBeenCalled();
    });

    it("a refused forced refresh ends the session and does not notify", async () => {
        const { goFetch, refresh, session } = await loadTab();
        const navigate = vi.fn();
        const ended = vi.fn();
        const claims = vi.fn();
        session.configureSessionEnd({ navigate });
        session.onSessionEnd(ended);
        refresh.onClaimsRefreshed(claims);
        fakeSession({ accessValid: true, cookieVer: 1, userVer: 2, refreshOk: false });
        await expect(goFetch.goFetch("/v1/me")).rejects.toMatchObject({ code: "token_expired" });
        expect(ended).toHaveBeenCalledTimes(1);
        expect(navigate).toHaveBeenCalledTimes(1);
        expect(claims).not.toHaveBeenCalled();
    });

    it("a failing listener does not stop the others or the retry; unsubscribe stops it", async () => {
        const { goFetch, refresh } = await loadTab();
        const s = fakeSession({ accessValid: true, cookieVer: 1, userVer: 2 });
        const second = vi.fn();
        const off = refresh.onClaimsRefreshed(() => {
            throw new Error("boom");
        });
        refresh.onClaimsRefreshed(second);
        await expect(goFetch.goFetch<{ ver: number }>("/v1/me")).resolves.toMatchObject({ ver: 2 });
        expect(second).toHaveBeenCalledTimes(1);
        off();
        s.userVer = 3; // another role change
        await new Promise((r) => setTimeout(r, 2)); // the next forced refresh completes at a later instant
        await goFetch.goFetch("/v1/me");
        expect(second).toHaveBeenCalledTimes(2);
    });
});

describe("session end on public pages (no reload loop on /login)", () => {
    /** A browser with no cookies: Go says unauthenticated, the BFF refuses the refresh (no lt_rt). */
    function signedOut() {
        handler = (c) => (c.url === "/api/auth/refresh" ? goError(401, "unauthenticated") : goError(401, "unauthenticated"));
    }

    for (const page of ["/login", "/login?next=%2Fapp%2Fusers", "/about", "/"]) {
        it(`a signed-out visitor on ${page} gets a plain 401: no navigation, no logout, no listeners`, async () => {
            window.history.replaceState({}, "", page);
            const { goFetch, session } = await loadTab();
            const navigate = vi.fn();
            const ended = vi.fn();
            session.configureSessionEnd({ navigate });
            session.onSessionEnd(ended);
            signedOut();
            await expect(goFetch.goFetch("/v1/me")).rejects.toMatchObject({ status: 401, code: "unauthenticated" });
            // Each "page load" of the old loop: the second load behaves the same, still without leaving.
            await expect(goFetch.goFetch("/v1/me")).rejects.toMatchObject({ code: "unauthenticated" });
            expect(refreshCalls()).toHaveLength(2);
            expect(navigate).not.toHaveBeenCalled();
            expect(logoutCalls()).toHaveLength(0);
            expect(ended).not.toHaveBeenCalled();
        });
    }

    it("on /app the same signed-out 401 still goes to /login?next=", async () => {
        window.history.replaceState({}, "", "/app/x");
        const { goFetch, session } = await loadTab();
        const navigate = vi.fn();
        session.configureSessionEnd({ navigate });
        signedOut();
        await expect(goFetch.goFetch("/v1/me")).rejects.toMatchObject({ code: "unauthenticated" });
        expect(navigate).toHaveBeenCalledTimes(1);
        expect(navigate).toHaveBeenCalledWith("/login?next=%2Fapp%2Fx");
        expect(logoutCalls()).toHaveLength(1);
    });

    it("a revoked session on /login is cleaned up without leaving the page, once for concurrent failures", async () => {
        window.history.replaceState({}, "", "/login");
        const { goFetch, session } = await loadTab();
        const navigate = vi.fn();
        const ended = vi.fn();
        session.configureSessionEnd({ navigate });
        session.onSessionEnd(ended);
        handler = () => goError(401, "session_revoked");
        await Promise.allSettled([goFetch.goFetch("/v1/me"), goFetch.goFetch("/v1/me/tenants")]);
        expect(ended).toHaveBeenCalledTimes(1);
        expect(logoutCalls()).toHaveLength(1);
        expect(navigate).not.toHaveBeenCalled();
    });

    it("after a session end on /login, a later session end on /app still navigates", async () => {
        window.history.replaceState({}, "", "/login");
        const { goFetch, session } = await loadTab();
        const navigate = vi.fn();
        const ended = vi.fn();
        session.configureSessionEnd({ navigate });
        session.onSessionEnd(ended);
        handler = () => goError(401, "session_revoked");
        await expect(goFetch.goFetch("/v1/me")).rejects.toMatchObject({ code: "session_revoked" });
        expect(navigate).not.toHaveBeenCalled();
        await vi.waitFor(() => expect(logoutCalls()).toHaveLength(1));
        await new Promise((r) => setTimeout(r, 0)); // the logout settled
        // The user signs in again and the login page moves to /app on the client (no page load).
        window.history.replaceState({}, "", "/app/users");
        await expect(goFetch.goFetch("/v1/users")).rejects.toMatchObject({ code: "session_revoked" });
        expect(navigate).toHaveBeenCalledTimes(1);
        expect(navigate).toHaveBeenCalledWith("/login?next=%2Fapp%2Fusers&reason=revoked");
        expect(ended).toHaveBeenCalledTimes(2);
        expect(logoutCalls()).toHaveLength(2);
    });
});
