// T17 (developer-spec.md §10.6, §10.7): TanStack queryFn / mutationFn factories over goFetch.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MutationObserver, QueryClient } from "@tanstack/react-query";
import { newIdempotencyKey } from "./goFetch";
import { goInfiniteQueryFn, goMutationFn, goNextPageParam, goQueryFn, idempotencyKeyFor } from "./goQuery";

interface Call {
    url: string;
    method: string;
    headers: Headers;
    body: string | undefined;
    signal: AbortSignal | undefined;
}

const calls: Call[] = [];
let handler: (call: Call) => Response | Promise<Response>;

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

beforeEach(() => {
    calls.length = 0;
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
            return handler(call);
        })
    );
});

afterEach(() => {
    vi.unstubAllGlobals();
});

const newClient = () => new QueryClient({ defaultOptions: { queries: { retry: false } } });

describe("goQueryFn", () => {
    it("GETs the path through the BFF with TanStack's signal and returns data", async () => {
        handler = () => json(200, { data: [{ sourceId: "SPK-GW" }] });
        const client = newClient();
        const data = await client.fetchQuery({ queryKey: ["hubs"], queryFn: goQueryFn<{ sourceId: string }[]>("/v1/hubs") });
        expect(data).toEqual([{ sourceId: "SPK-GW" }]);
        expect(calls[0].url).toBe("/api/go/v1/hubs");
        expect(calls[0].signal).toBeInstanceOf(AbortSignal);
    });

    it("derives path and query from the key, and cancelling the query aborts the request", async () => {
        let release!: () => void;
        handler = (c) =>
            new Promise((resolve, reject) => {
                c.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
                release = () => resolve(json(200, { data: [] }));
            });
        const client = newClient();
        const key = ["trips", "detail", "t 1", { fields: "minimal" }] as const;
        const pending = client.fetchQuery({
            queryKey: key,
            queryFn: goQueryFn<unknown[], typeof key>(([, , id]) => `/v1/trips/${encodeURIComponent(id)}`, { query: ([, , , p]) => p }),
        });
        await vi.waitFor(() => expect(calls).toHaveLength(1));
        expect(calls[0].url).toBe("/api/go/v1/trips/t%201?fields=minimal");
        await client.cancelQueries({ queryKey: ["trips"] });
        expect(calls[0].signal?.aborted).toBe(true);
        await expect(pending).rejects.toBeTruthy();
        release();
    });
});

describe("goInfiniteQueryFn", () => {
    it("pages by keyset cursor until nextCursor is absent", async () => {
        handler = (c) => {
            const cursor = new URL(c.url, "http://web").searchParams.get("cursor");
            return cursor === null
                ? json(200, { data: [1, 2], nextCursor: "c2", meta: { total: 3 } })
                : json(200, { data: [3] });
        };
        const client = newClient();
        const options = {
            queryKey: ["users", { q: "som" }] as const,
            queryFn: goInfiniteQueryFn<number, readonly ["users", { q: string }]>("/v1/users", { query: ([, p]) => p }),
            initialPageParam: undefined as string | undefined,
            getNextPageParam: goNextPageParam,
        };
        await client.fetchInfiniteQuery(options);
        const result = await client.fetchInfiniteQuery({ ...options, pages: 2 });
        expect(result.pages.map((p) => p.data)).toEqual([[1, 2], [3]]);
        expect(result.pages[0].meta).toEqual({ total: 3 });
        expect(calls.map((c) => c.url)).toContain("/api/go/v1/users?q=som&cursor=c2");
        expect(goNextPageParam(result.pages[1])).toBeUndefined();
    });

    it("sends the headers derived from the key on every page (X-Act-On-Tenant of a platform read)", async () => {
        handler = (c) =>
            new URL(c.url, "http://web").searchParams.get("cursor") === null ? json(200, { data: [1], nextCursor: "c2" }) : json(200, { data: [2] });
        const client = newClient();
        type Key = readonly ["users", { reach: "all" | "tenant" }];
        const options = {
            queryKey: ["users", { reach: "all" }] as Key,
            queryFn: goInfiniteQueryFn<number, Key>("/v1/users", {
                headers: ([, p]) => (p.reach === "all" ? { "X-Act-On-Tenant": "*" } : undefined),
            }),
            initialPageParam: undefined as string | undefined,
            getNextPageParam: goNextPageParam,
        };
        await client.fetchInfiniteQuery({ ...options, pages: 2 });
        expect(calls).toHaveLength(2);
        expect(calls.map((c) => c.headers.get("X-Act-On-Tenant"))).toEqual(["*", "*"]);

        calls.length = 0;
        await client.fetchInfiniteQuery({ ...options, queryKey: ["users", { reach: "tenant" }] as Key });
        expect(calls[0].headers.has("X-Act-On-Tenant")).toBe(false);
    });
});

describe("goMutationFn", () => {
    it("a retried mutation reuses the same Idempotency-Key; a new action gets a new one", async () => {
        let attempt = 0;
        handler = () =>
            attempt++ === 0
                ? json(503, { error: { code: "unavailable", message: "busy", details: {}, requestId: "r1" } })
                : json(201, { data: { id: "task-1" } });
        const client = newClient();
        const observer = new MutationObserver(client, {
            mutationFn: goMutationFn<{ id: string }, { date: string }>({ method: "POST", path: "/v1/tasks", idempotent: true }),
            retry: 1,
            retryDelay: 0,
        });
        await expect(observer.mutate({ date: "2026-10-10" })).resolves.toEqual({ id: "task-1" });
        expect(calls).toHaveLength(2);
        const key = calls[0].headers.get("Idempotency-Key");
        expect(key).toMatch(/^[0-9a-f-]{36}$/);
        expect(calls[1].headers.get("Idempotency-Key")).toBe(key);
        expect(calls[1].body).toBe('{"date":"2026-10-10"}');

        await observer.mutate({ date: "2026-10-10" });
        expect(calls[2].headers.get("Idempotency-Key")).not.toBe(key);
    });

    it("builds path, body and query from the variables; DELETE sends no body; plain writes send no key", async () => {
        handler = (c) => (c.method === "DELETE" ? new Response(null, { status: 204 }) : json(200, { data: { ok: true } }));
        const patch = goMutationFn<{ ok: boolean }, { id: string; name: string }>({
            method: "PATCH",
            path: (v) => `/v1/hubs/${v.id}`,
            body: (v) => ({ name: v.name }),
            query: () => ({ notify: false }),
        });
        const client = newClient();
        await patch({ id: "h1", name: "บางปู" }, { client, meta: undefined });
        expect(calls[0]).toMatchObject({ url: "/api/go/v1/hubs/h1?notify=false", method: "PATCH", body: '{"name":"บางปู"}' });
        expect(calls[0].headers.has("Idempotency-Key")).toBe(false);
        const del = goMutationFn<void, string>({ method: "DELETE", path: (id) => `/v1/hubs/${id}` });
        await expect(del("h1", { client, meta: undefined })).resolves.toBeUndefined();
        expect(calls[1].body).toBeUndefined();
    });

    it("keys one action per variables object and refuses primitives it cannot remember", () => {
        const vars = { a: 1 };
        expect(idempotencyKeyFor(vars)).toBe(idempotencyKeyFor(vars));
        expect(idempotencyKeyFor({ a: 1 })).not.toBe(idempotencyKeyFor(vars));
        for (const primitive of ["x", 1, undefined, null]) {
            expect(() => idempotencyKeyFor(primitive), String(primitive)).toThrow(TypeError);
        }
    });

    const busyThenOk = () => {
        let attempt = 0;
        handler = () =>
            attempt++ === 0
                ? json(503, { error: { code: "unavailable", message: "busy", details: {}, requestId: "r1" } })
                : json(200, { data: { ok: true } });
    };

    it("an idempotent mutate(id) is refused before any request instead of sending a new key per attempt", async () => {
        busyThenOk();
        const client = newClient();
        const observer = new MutationObserver(client, {
            mutationFn: goMutationFn<{ ok: boolean }, string>({
                method: "POST",
                path: (id) => `/v1/trips/${id}/billing/compute`,
                body: () => ({}),
                idempotent: true,
            }),
            retry: 1,
            retryDelay: 0,
        });
        await expect(observer.mutate("trip-1")).rejects.toThrow(/need object variables or an idempotencyKey/);
        expect(calls).toHaveLength(0);
    });

    it("an explicit idempotencyKey from the variables is reused across a 503 and its retry", async () => {
        busyThenOk();
        const client = newClient();
        const observer = new MutationObserver(client, {
            mutationFn: goMutationFn<{ ok: boolean }, { id: string; idempotencyKey: string }>({
                method: "POST",
                path: (v) => `/v1/trips/${v.id}/billing/compute`,
                body: () => ({}),
                idempotencyKey: (v) => v.idempotencyKey,
            }),
            retry: 1,
            retryDelay: 0,
        });
        const key = newIdempotencyKey();
        await expect(observer.mutate({ id: "trip-1", idempotencyKey: key })).resolves.toEqual({ ok: true });
        expect(calls).toHaveLength(2);
        expect(calls.map((c) => c.headers.get("Idempotency-Key"))).toEqual([key, key]);
        expect(calls.map((c) => c.body)).toEqual(["{}", "{}"]);
        expect(calls[1].url).toBe("/api/go/v1/trips/trip-1/billing/compute");
        handler = () => json(200, { data: { ok: true } });
        await observer.mutate({ id: "trip-1", idempotencyKey: newIdempotencyKey() });
        expect(calls[2].headers.get("Idempotency-Key")).not.toBe(key);
    });

    it("the same variables object passed again after a success is a new action with a new key", async () => {
        handler = () => json(200, { data: { ok: true } });
        const client = newClient();
        client.setQueryData(["users"], [{ id: "u1", role: "admin" }]);
        const row = client.getQueryData<{ id: string; role: string }[]>(["users"])![0];
        const observer = new MutationObserver(client, {
            mutationFn: goMutationFn<{ ok: boolean }, { id: string; role: string }>({
                method: "POST",
                path: (v) => `/v1/users/${v.id}/revoke-sessions`,
                body: () => ({}),
                idempotent: true,
            }),
        });
        await observer.mutate(row);
        // An equal refetch keeps the same reference (structural sharing).
        client.setQueryData(["users"], [{ id: "u1", role: "admin" }]);
        expect(client.getQueryData<{ id: string }[]>(["users"])![0]).toBe(row);
        await observer.mutate(row);
        expect(calls).toHaveLength(2);
        expect(calls[1].headers.get("Idempotency-Key")).not.toBe(calls[0].headers.get("Idempotency-Key"));
    });
});
