// TW4 (developer-spec.md §10.6, Appendix E §E.6): the one QueryClient, its W5 defaults, the global
// meta.invalidates, the 403 notifier and the session binding.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MutationObserver, QueryObserver } from "@tanstack/react-query";
import { ApiError } from "./apiError";
import {
    bindQueryClientToSession,
    createQueryClient,
    getQueryClient,
    isRetryableError,
    MAX_QUERY_RETRIES,
    QUERY_DEFAULTS,
    resetQueryClientForTests,
    setForbiddenNotifier,
    shouldRetryQuery,
} from "./queryClient";
import { configureSessionEnd, endSession } from "./sessionEnd";
import { sharedRefresh } from "./sharedRefresh";
import { meQueryOptions } from "@/features/auth/api/me";

const apiError = (status: number, code = "x") => new ApiError({ status, code, message: code });

beforeEach(() => {
    resetQueryClientForTests();
});

afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
});

describe("defaults (W5)", () => {
    it("30 s stale, 5 min gc, focus refetch, mutations never retried", () => {
        const client = createQueryClient();
        const q = client.getDefaultOptions().queries!;
        expect(QUERY_DEFAULTS).toEqual({ staleTime: 30_000, gcTime: 300_000 });
        expect(q.staleTime).toBe(30_000);
        expect(q.gcTime).toBe(300_000);
        expect(q.refetchOnWindowFocus).toBe(true);
        expect(client.getDefaultOptions().mutations!.retry).toBe(false);
    });

    it("retries at most twice, and only a network failure or a 5xx", () => {
        expect(MAX_QUERY_RETRIES).toBe(2);
        expect(isRetryableError(apiError(0, "network_error"))).toBe(true);
        expect(isRetryableError(apiError(502, "unavailable"))).toBe(true);
        expect(isRetryableError(apiError(503))).toBe(true);
        for (const status of [400, 401, 403, 404, 409, 422, 429]) expect(isRetryableError(apiError(status))).toBe(false);
        // Firestore SDK transient codes (P0 query functions), nothing else.
        expect(isRetryableError({ code: "unavailable" })).toBe(true);
        expect(isRetryableError({ code: "permission-denied" })).toBe(false);
        expect(isRetryableError(new Error("boom"))).toBe(false);
        expect(shouldRetryQuery(0, apiError(500))).toBe(true);
        expect(shouldRetryQuery(1, apiError(500))).toBe(true);
        expect(shouldRetryQuery(2, apiError(500))).toBe(false);
        expect(shouldRetryQuery(0, apiError(401))).toBe(false);
    });

    it("tries a failing 5xx query three times in all, a 403 once", async () => {
        const client = createQueryClient();
        client.setDefaultOptions({ queries: { ...client.getDefaultOptions().queries, retryDelay: 0 } });
        const fiveHundred = vi.fn(async () => {
            throw apiError(503, "unavailable");
        });
        await expect(client.fetchQuery({ queryKey: ["a"], queryFn: fiveHundred, retry: shouldRetryQuery })).rejects.toThrow();
        expect(fiveHundred).toHaveBeenCalledTimes(3);
        const forbidden = vi.fn(async () => {
            throw apiError(403, "permission_denied");
        });
        await expect(client.fetchQuery({ queryKey: ["b"], queryFn: forbidden, retry: shouldRetryQuery })).rejects.toThrow();
        expect(forbidden).toHaveBeenCalledTimes(1);
    });
});

describe("meta.invalidates", () => {
    it("invalidates the declared keys (prefixes) after a successful mutation, and nothing on failure", async () => {
        const client = createQueryClient();
        client.setQueryData(["hubs"], [1]);
        client.setQueryData(["hubs", "maps"], {});
        client.setQueryData(["customers"], [2]);
        const ok = new MutationObserver(client, {
            mutationFn: async () => "done",
            meta: { invalidates: [["hubs"]] },
        });
        await ok.mutate();
        expect(client.getQueryState(["hubs"])?.isInvalidated).toBe(true);
        expect(client.getQueryState(["hubs", "maps"])?.isInvalidated).toBe(true);
        expect(client.getQueryState(["customers"])?.isInvalidated).toBe(false);

        client.setQueryData(["customers"], [3]);
        const failing = new MutationObserver(client, {
            mutationFn: async () => {
                throw apiError(409, "already_exists");
            },
            meta: { invalidates: [["customers"]] },
        });
        await expect(failing.mutate()).rejects.toThrow();
        expect(client.getQueryState(["customers"])?.isInvalidated).toBe(false);
    });
});

describe("403 notifier", () => {
    it("is called once per failed query or mutation with a 403, never for other errors", async () => {
        const client = createQueryClient();
        const seen: string[] = [];
        const off = setForbiddenNotifier((e) => seen.push(e.code));
        await expect(
            client.fetchQuery({
                queryKey: ["x"],
                queryFn: async () => {
                    throw apiError(403, "permission_denied");
                },
            })
        ).rejects.toThrow();
        await expect(
            client.fetchQuery({
                queryKey: ["y"],
                queryFn: async () => {
                    throw apiError(404, "not_found");
                },
            })
        ).rejects.toThrow();
        await expect(
            new MutationObserver(client, {
                mutationFn: async () => {
                    throw apiError(403, "tenant_required");
                },
            }).mutate()
        ).rejects.toThrow();
        expect(seen).toEqual(["permission_denied", "tenant_required"]);
        off();
        await expect(
            client.fetchQuery({
                queryKey: ["z"],
                queryFn: async () => {
                    throw apiError(403, "permission_denied");
                },
            })
        ).rejects.toThrow();
        expect(seen).toHaveLength(2);
    });
});

describe("getQueryClient", () => {
    it("is one client per tab", () => {
        expect(getQueryClient()).toBe(getQueryClient());
        resetQueryClientForTests();
        const next = getQueryClient();
        expect(next).toBe(getQueryClient());
    });
});

describe("bindQueryClientToSession (developer-spec.md §10.4 steps 3-4)", () => {
    it("empties the cache when the session ends, leaving ['me'] signed out", () => {
        const client = createQueryClient();
        const off = bindQueryClientToSession(client);
        client.setQueryData(["me"], { id: "u1" });
        client.setQueryData(["hubs"], [1]);
        const navigate = vi.fn();
        configureSessionEnd({ navigate });
        // A public page (jsdom's "/"): the listeners run, the visitor stays.
        vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status: 204 })));
        endSession(apiError(401, "session_revoked"));
        expect(client.getQueryCache().getAll().map((q) => q.queryKey)).toEqual([["me"]]);
        expect(client.getQueryData(["me"])).toBeNull();
        off();
    });

    it("refetches ['me'] and the active queries after a forced refresh, and stays signed in", async () => {
        const client = createQueryClient();
        const off = bindQueryClientToSession(client);
        let meCalls = 0;
        const me = new QueryObserver(client, { queryKey: ["me"], queryFn: async () => ++meCalls, staleTime: Infinity });
        const unsub = me.subscribe(() => undefined);
        await vi.waitFor(() => expect(meCalls).toBe(1));
        client.setQueryData(["inactive"], 1);

        vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status: 204 })));
        await expect(sharedRefresh({ force: true, since: Date.now() - 1 })).resolves.toBe(true);
        await vi.waitFor(() => expect(meCalls).toBe(2));
        expect(client.getQueryState(["inactive"])?.isInvalidated).toBe(true);
        unsub();
        off();
    });
});

// A `GET /v1/me` body (Appendix C §C.8) for user `id` in tenant `tid`.
function principal(id: string, tid: string | null = "t1") {
    return {
        id,
        email: `${id}@example.test`,
        displayName: id,
        photoUrl: null,
        tenant: tid ? { id: tid, nameTh: tid, nameEn: null, kind: "own_fleet", role: "manager" } : null,
        tenants: [],
        platformRoles: [],
        dispatcher: false,
        steward: true,
        driver: null,
        customerScopes: [],
        capabilities: ["masterdata:view_hubs"],
        mustChangePassword: false,
    };
}

function envelope(status: number, code: string): Response {
    return new Response(JSON.stringify({ error: { code, message: code, details: {}, requestId: "r" } }), {
        status,
        headers: { "Content-Type": "application/json" },
    });
}

describe("a change of principal empties the cache (no data of one user reaches the next)", () => {
    afterEach(() => {
        window.history.replaceState({}, "", "/");
        window.localStorage.clear();
    });

    it("a session that lapses quietly on a public page drops the previous user's data", async () => {
        // On /support, a 401 `unauthenticated` whose refresh is refused is a signed-out visitor, not a
        // session end: no listener runs and no logout is sent, yet ['me'] turns null.
        window.history.replaceState({}, "", "/support");
        const client = createQueryClient();
        const off = bindQueryClientToSession(client);
        client.setQueryData(["me"], principal("ann"));
        client.setQueryData(["hubs"], [{ id: "h1" }]);
        client.setQueryData(["customers"], [{ id: "c1" }]);
        client.setQueryData(["companies", { owner: true }], { bank: "of ann's tenant" });
        const calls: string[] = [];
        vi.stubGlobal(
            "fetch",
            vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
                const url = String(input);
                calls.push(`${init?.method ?? "GET"} ${url}`);
                if (url === "/api/go/v1/me") return envelope(401, "unauthenticated");
                if (url === "/api/auth/refresh") return envelope(401, "session_revoked");
                return new Response(null, { status: 204 });
            })
        );
        await expect(client.fetchQuery({ ...meQueryOptions, staleTime: 0 })).resolves.toBeNull();
        expect(calls).toEqual(["GET /api/go/v1/me", "POST /api/auth/refresh"]);
        expect(client.getQueryData(["me"])).toBeNull();
        expect(client.getQueryCache().getAll().map((q) => q.queryKey)).toEqual([["me"]]);
        off();
    });

    it("another user or another tenant never sees the previous principal's data; the same one keeps it", async () => {
        const client = createQueryClient();
        const off = bindQueryClientToSession(client);
        // The first principal of the tab (loading -> signed in) clears nothing.
        client.setQueryData(["webFlags"], { domains: {} });
        client.setQueryData(["me"], principal("ann", "t1"));
        expect(client.getQueryData(["webFlags"])).toEqual({ domains: {} });

        let hubsFetches = 0;
        const hubs = new QueryObserver(client, { queryKey: ["hubs"], queryFn: async () => `hubs-${++hubsFetches}`, staleTime: Infinity });
        const unsub = hubs.subscribe(() => undefined);
        await vi.waitFor(() => expect(hubs.getCurrentResult().data).toBe("hubs-1"));
        client.setQueryData(["customers"], ["of ann"]);
        new MutationObserver(client, { mutationFn: async () => "x" }).mutate().catch(() => undefined);

        // The same principal again (a claims_changed refetch): nothing is dropped.
        client.setQueryData(["me"], { ...principal("ann", "t1"), capabilities: [] });
        expect(client.getQueryData(["customers"])).toEqual(["of ann"]);
        expect(client.getQueryData(["hubs"])).toBe("hubs-1");

        // Another user in this tab: inactive data is gone at once, the mounted page refetches.
        client.setQueryData(["me"], principal("bob", "t1"));
        expect(client.getQueryData(["customers"])).toBeUndefined();
        expect(client.getQueryData(["hubs"])).toBeUndefined();
        expect(client.getMutationCache().getAll()).toHaveLength(0);
        await vi.waitFor(() => expect(hubs.getCurrentResult().data).toBe("hubs-2"));
        expect(client.getQueryData(["me"])).toMatchObject({ id: "bob" });

        // A tenant switch of the same user is a change of principal too (keys carry no tenant).
        client.setQueryData(["customers"], ["of bob in t1"]);
        client.setQueryData(["me"], principal("bob", "t2"));
        expect(client.getQueryData(["customers"])).toBeUndefined();
        await vi.waitFor(() => expect(hubs.getCurrentResult().data).toBe("hubs-3"));

        // Signed out, then someone signs in: the signed-out state already dropped everything.
        client.setQueryData(["customers"], ["of bob in t2"]);
        client.setQueryData(["me"], null);
        expect(client.getQueryData(["customers"])).toBeUndefined();
        client.setQueryData(["customers"], ["fetched while signed out"]);
        client.setQueryData(["me"], principal("cat", "t1"));
        expect(client.getQueryData(["customers"])).toEqual(["fetched while signed out"]);

        unsub();
        off();
        // Unbound: a change of principal is no longer watched.
        client.setQueryData(["me"], principal("dan", "t1"));
        expect(client.getQueryData(["customers"])).toEqual(["fetched while signed out"]);
    });
});
