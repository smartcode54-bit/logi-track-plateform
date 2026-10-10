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
