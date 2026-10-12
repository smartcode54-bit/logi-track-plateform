// TW5 (developer-spec.md §10.8; Appendix E §E.5.1 "Event -> cache", "Backpressure"): the event map
// applied to a real QueryClient. Coalescing windows run on captured timers; visibility is a flag.
import { afterEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryObserver, type QueryKey } from "@tanstack/react-query";

import { queryKeys } from "../queryKeys";
import { LIST_WINDOW_MS } from "./eventMap";
import { RealtimeInvalidator } from "./invalidator";

const flush = () => new Promise((r) => setTimeout(r, 0));

function setup() {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 60_000 } } });
    const timers: { fn: () => void; ms: number; cleared: boolean }[] = [];
    let visible = true;
    const inv = new RealtimeInvalidator({
        client,
        isVisible: () => visible,
        setTimer: (fn, ms) => {
            const t = { fn, ms, cleared: false };
            timers.push(t);
            return t;
        },
        clearTimer: (h) => {
            (h as { cleared: boolean }).cleared = true;
        },
    });
    const fetches = new Map<string, number>();
    const unsubs: Array<() => void> = [];
    /** A query with `n` observers (0 = cached but unobserved); counts its fetches. */
    async function query(key: QueryKey, observed = true) {
        const name = JSON.stringify(key);
        fetches.set(name, 0);
        const queryFn = async () => {
            fetches.set(name, (fetches.get(name) ?? 0) + 1);
            return fetches.get(name);
        };
        if (observed) {
            const obs = new QueryObserver(client, { queryKey: key, queryFn });
            unsubs.push(obs.subscribe(() => undefined));
        } else {
            await client.prefetchQuery({ queryKey: key, queryFn });
        }
        await flush();
        return () => fetches.get(name) ?? 0;
    }
    async function runTimers() {
        const due = timers.splice(0).filter((t) => !t.cleared);
        for (const t of due) t.fn();
        await flush();
    }
    return {
        client,
        inv,
        timers,
        query,
        runTimers,
        setVisible: (v: boolean) => {
            visible = v;
        },
        cleanup: () => unsubs.forEach((u) => u()),
    };
}

/**
 * A server whose rows change by `server.version++`, and a query on it whose fetches the test settles
 * by hand (`land()`): a fetch reads the version when it starts, as a database read would.
 */
function slowQuery(client: QueryClient, key: QueryKey) {
    const server = { version: 0 };
    const stats = { started: 0, aborted: 0 };
    const pending: Array<() => void> = [];
    const observer = new QueryObserver(client, {
        queryKey: key,
        queryFn: ({ signal }) =>
            new Promise<number>((resolve, reject) => {
                const version = server.version;
                stats.started++;
                signal.addEventListener("abort", () => {
                    stats.aborted++;
                    reject(new DOMException("aborted", "AbortError"));
                });
                pending.push(() => resolve(version));
            }),
    });
    const unsubscribe = observer.subscribe(() => undefined);
    return {
        server,
        stats,
        /** Settles the oldest fetch still waiting (an aborted one settles to nothing). */
        land: async () => {
            pending.shift()?.();
            await flush();
        },
        pendingCount: () => pending.length,
        data: () => client.getQueryData<number>(key),
        state: () => client.getQueryState(key),
        unsubscribe,
    };
}

let cleanup: () => void = () => undefined;
afterEach(() => {
    cleanup();
    cleanup = () => undefined;
    vi.useRealTimers();
});

const truckUpdated = { event: "truck.updated", data: { entity: "truck", id: "t1", changed: ["status"] } };

describe("RealtimeInvalidator", () => {
    it("coalesces a burst into one refetch per key per window, and refetches only observed queries", async () => {
        const h = setup();
        cleanup = h.cleanup;
        const trucks = await h.query(queryKeys.trucks.list({ status: "active" }));
        const drivers = await h.query(queryKeys.drivers.list({ status: "active", q: "" }));
        const hubs = await h.query(queryKeys.hubs.all());
        const renewals = await h.query(queryKeys.renewals({ due: "30d" }), false);
        expect([trucks(), drivers(), hubs(), renewals()]).toEqual([1, 1, 1, 1]);

        for (let i = 0; i < 50; i++) h.inv.apply(truckUpdated);
        await flush();
        expect(trucks()).toBe(1); // nothing before the window ends
        expect(h.timers.map((t) => t.ms)).toEqual([500, 500, 500, 500, 500]); // one window per fleet key
        await h.runTimers();
        expect([trucks(), drivers(), hubs(), renewals()]).toEqual([2, 2, 1, 1]);
        // The unobserved list is stale and fetched on its next use.
        expect(h.client.getQueryState(queryKeys.renewals({ due: "30d" }))?.isInvalidated).toBe(true);
    });

    it("a steady stream of events still refetches once per window (no starvation)", async () => {
        const h = setup();
        cleanup = h.cleanup;
        const hubs = await h.query(queryKeys.hubs.maps());
        for (let window = 1; window <= 3; window++) {
            for (let i = 0; i < 5; i++) h.inv.apply({ event: "hubs.changed", data: { ids: [`h${i}`] } });
            await h.runTimers();
            expect(hubs()).toBe(1 + window);
        }
    });

    it("a hidden tab marks the keys stale without refetching, then refetches them once when visible", async () => {
        const h = setup();
        cleanup = h.cleanup;
        const customers = await h.query(queryKeys.customers.all());
        h.setVisible(false);
        h.inv.apply({ event: "customers.changed", data: { ids: ["c1"] } });
        h.inv.apply({ event: "customers.changed", data: { ids: ["c2"] } });
        await h.runTimers();
        expect(customers()).toBe(1);
        expect(h.client.getQueryState(queryKeys.customers.all())?.isInvalidated).toBe(true);
        h.setVisible(true);
        h.inv.flushDeferred();
        await flush();
        expect(customers()).toBe(2);
        h.inv.flushDeferred(); // nothing left
        await flush();
        expect(customers()).toBe(2);
    });

    it("the deferred refetch joins a focus refetch already running instead of restarting it", async () => {
        const h = setup();
        cleanup = h.cleanup;
        const companies = await h.query(queryKeys.companies.owner());
        h.setVisible(false);
        h.inv.apply({ event: "companies.changed", data: {} });
        await h.runTimers();
        h.setVisible(true);
        // TanStack's own focus refetch starts first...
        void h.client.refetchQueries({ queryKey: queryKeys.companies.all(), type: "active" });
        h.inv.flushDeferred();
        await flush();
        expect(companies()).toBe(2);
    });

    it("job.updated writes ['jobs', id] at once, even in a hidden tab; success invalidates the type's keys", async () => {
        const h = setup();
        cleanup = h.cleanup;
        const distances = await h.query(queryKeys.distances.meta());
        h.inv.apply({ event: "job.updated", data: { id: "j1", type: "distances.compute", status: "running", progress: { done: 1, total: 2 } } });
        expect(h.client.getQueryData(queryKeys.jobs("j1"))).toMatchObject({ status: "running" });
        h.inv.apply({ event: "job.updated", data: { id: "j1", type: "distances.compute", status: "succeeded", progress: { done: 2, total: 2 } } });
        h.inv.apply({ event: "job.updated", data: { id: "j1", type: "distances.compute", status: "running", progress: { done: 1, total: 2 } } });
        expect(h.client.getQueryData(queryKeys.jobs("j1"))).toMatchObject({ status: "succeeded" });
        await h.runTimers();
        expect(distances()).toBe(2);
    });

    it("a vehicle_locations.updated batch keeps the list's dataUpdatedAt and refetches it when it names an unlisted truck", async () => {
        const h = setup();
        cleanup = h.cleanup;
        let fetches = 0;
        const row = (truckId: string) => ({ truckId, tenantId: "t", licensePlate: `p-${truckId}`, lat: 13, lng: 100, speedKmh: 0, heading: null, lastGpsAt: "2026-10-12T01:00:00Z", updatedAt: "2026-10-12T01:00:05Z" });
        const observer = new QueryObserver(h.client, {
            queryKey: queryKeys.vehicleLocations(),
            queryFn: async () => (++fetches === 1 ? [row("a")] : [row("a"), row("b")]),
        });
        const unsub = observer.subscribe(() => undefined);
        await flush();
        const answeredAt = h.client.getQueryState(queryKeys.vehicleLocations())!.dataUpdatedAt;
        expect(answeredAt).toBeGreaterThan(0);
        await new Promise((r) => setTimeout(r, 5));

        const at = "2026-10-12T01:03:00Z";
        h.inv.apply({ event: "vehicle_locations.updated", data: { positions: [{ truckId: "a", lat: 14, lng: 101, speed: 30, heading: 0, at }] } });
        const after = h.client.getQueryState(queryKeys.vehicleLocations())!;
        expect((after.data as { lat: number }[])[0].lat).toBe(14);
        expect(after.dataUpdatedAt).toBe(answeredAt); // a partial write is not an answer
        expect(h.timers).toHaveLength(0); // every truck was listed: no refetch
        await h.runTimers();
        expect(fetches).toBe(1);

        h.inv.apply({ event: "vehicle_locations.updated", data: { positions: [{ truckId: "b", lat: 15, lng: 102, speed: 0, heading: null, at }] } });
        expect(h.timers.map((t) => t.ms)).toEqual([LIST_WINDOW_MS]);
        await h.runTimers();
        await flush();
        expect(fetches).toBe(2);
        expect((h.client.getQueryData(queryKeys.vehicleLocations()) as { truckId: string }[]).map((r) => r.truckId)).toEqual(["a", "b"]);
        unsub();
    });

    it("resync refetches every active query; hidden, it waits for the tab to be visible", async () => {
        const h = setup();
        cleanup = h.cleanup;
        const trucks = await h.query(queryKeys.trucks.all());
        const me = await h.query(queryKeys.me());
        const idle = await h.query(queryKeys.statements.all(), false);
        h.inv.resync();
        await flush();
        expect([trucks(), me(), idle()]).toEqual([2, 2, 1]);
        h.setVisible(false);
        h.inv.resync();
        await flush();
        expect([trucks(), me()]).toEqual([2, 2]);
        h.setVisible(true);
        h.inv.flushDeferred();
        await flush();
        expect([trucks(), me(), idle()]).toEqual([3, 3, 1]);
    });

    it("dispose drops the open windows and ignores later frames", async () => {
        const h = setup();
        cleanup = h.cleanup;
        const trucks = await h.query(queryKeys.trucks.all());
        h.inv.apply(truckUpdated);
        h.inv.dispose();
        expect(h.timers.every((t) => t.cleared)).toBe(true);
        await h.runTimers();
        h.inv.apply(truckUpdated);
        h.inv.resync();
        await flush();
        expect(trucks()).toBe(1);
        expect(h.timers).toHaveLength(0);
    });

    it("uses TanStack's focus state by default", () => {
        const client = new QueryClient();
        const spy = vi.spyOn(client, "invalidateQueries");
        const inv = new RealtimeInvalidator({ client });
        inv.resync();
        expect(spy).toHaveBeenCalledWith({ queryKey: [] }); // visible: invalidate and refetch every active query
        inv.dispose();
    });
});

describe("RealtimeInvalidator with fetches in flight", () => {
    it("an event during its own refetch causes one trailing refetch after it lands, never a restart", async () => {
        const h = setup();
        const q = slowQuery(h.client, queryKeys.trucks.list({ status: "active" }));
        cleanup = () => {
            q.unsubscribe();
            h.cleanup();
        };
        await q.land(); // first load
        expect(q.data()).toBe(0);

        q.server.version = 1;
        h.inv.apply(truckUpdated);
        await h.runTimers(); // the window closes: one refetch starts
        expect(q.stats.started).toBe(2);
        for (let v = 2; v <= 4; v++) {
            q.server.version = v;
            h.inv.apply(truckUpdated);
            await h.runTimers(); // more windows close while it runs
        }
        expect(q.stats).toEqual({ started: 2, aborted: 0 });
        expect(h.client.getQueryState(queryKeys.trucks.list({ status: "active" }))?.isInvalidated).toBe(true);

        await q.land(); // version 1 lands; the trailing refetch reads version 4
        expect(q.data()).toBe(1);
        expect(q.stats.started).toBe(3);
        await q.land();
        expect(q.data()).toBe(4);
        expect(q.stats).toEqual({ started: 3, aborted: 0 });
        expect(q.pendingCount()).toBe(0);
    });

    it.each([
        { fetchMs: 800, everyMs: 300 },
        { fetchMs: 1_200, everyMs: 100 },
        { fetchMs: 3_000, everyMs: 500 },
    ])("a steady stream against a $fetchMs ms fetch (an event every $everyMs ms) advances the data every fetch, aborts nothing and ends on the last version", async ({ fetchMs, everyMs }) => {
        vi.useFakeTimers();
        const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 5 * 60_000 } } });
        const inv = new RealtimeInvalidator({ client, isVisible: () => true });
        let server = 0;
        const stats = { started: 0, completed: 0, aborted: 0 };
        const key = queryKeys.trucks.list({ status: "active" });
        const observer = new QueryObserver(client, {
            queryKey: key,
            queryFn: ({ signal }) =>
                new Promise<number>((resolve, reject) => {
                    const version = server;
                    stats.started++;
                    const timer = setTimeout(() => {
                        stats.completed++;
                        resolve(version);
                    }, fetchMs);
                    signal.addEventListener("abort", () => {
                        clearTimeout(timer);
                        stats.aborted++;
                        reject(new DOMException("aborted", "AbortError"));
                    });
                }),
        });
        const seen: number[] = [];
        const unsubscribe = observer.subscribe((result) => {
            if (result.data !== undefined && result.data !== seen[seen.length - 1]) seen.push(result.data);
        });
        cleanup = () => {
            unsubscribe();
            inv.dispose();
            client.clear();
        };
        await vi.advanceTimersByTimeAsync(fetchMs);
        expect(seen).toEqual([0]);

        const streamMs = 12_000;
        for (let t = 0; t < streamMs; t += everyMs) {
            server++;
            inv.apply(truckUpdated);
            await vi.advanceTimersByTimeAsync(everyMs);
        }
        const updatesDuringStream = seen.length - 1;
        // One refetch lands about every fetch: far from the zero a restart per window gives.
        expect(updatesDuringStream).toBeGreaterThanOrEqual(Math.floor(streamMs / (fetchMs + LIST_WINDOW_MS + everyMs)));
        expect(stats.aborted).toBe(0);

        await vi.advanceTimersByTimeAsync(LIST_WINDOW_MS + 2 * fetchMs); // the stream stopped
        expect(observer.getCurrentResult().data).toBe(server); // the last event is not lost
        expect(stats).toEqual({ started: stats.completed, completed: stats.completed, aborted: 0 });
    });

    it("a first load running when the key is flushed is refetched once more after it lands", async () => {
        const h = setup();
        const q = slowQuery(h.client, queryKeys.trucks.list({ status: "active" }));
        cleanup = () => {
            q.unsubscribe();
            h.cleanup();
        };
        expect(q.stats.started).toBe(1); // reads version 0
        q.server.version = 1;
        h.inv.apply(truckUpdated);
        await h.runTimers();
        expect(q.stats.started).toBe(1); // TanStack joins a first load instead of restarting it
        await q.land();
        expect(q.data()).toBe(0);
        expect(q.stats.started).toBe(2); // ... so it is refetched once it has landed
        await q.land();
        expect(q.data()).toBe(1);
        expect(q.stats).toEqual({ started: 2, aborted: 0 });
    });

    it("a hidden tab that flushes a key while an older fetch runs still shows the change when visible", async () => {
        const h = setup();
        const key = queryKeys.trucks.list({ status: "active" });
        const q = slowQuery(h.client, key);
        cleanup = () => {
            q.unsubscribe();
            h.cleanup();
        };
        await q.land();
        // A mutation's own invalidation starts a refetch, which reads the server before the change.
        void h.client.invalidateQueries({ queryKey: queryKeys.trucks.all() });
        expect(q.stats.started).toBe(2);
        h.setVisible(false);
        q.server.version = 1;
        h.inv.apply(truckUpdated);
        await h.runTimers();
        expect(q.stats.started).toBe(2); // a hidden tab starts no fetch

        await q.land(); // the older response lands...
        expect(q.data()).toBe(0);
        expect(q.state()?.isInvalidated).toBe(true); // ...and the query stays stale

        h.setVisible(true);
        h.inv.flushDeferred();
        await flush();
        expect(q.stats.started).toBe(3);
        await q.land();
        expect(q.data()).toBe(1);
        expect(q.stats.aborted).toBe(0);
    });

    it("a hidden resync waits for a running fetch and keeps its query stale", async () => {
        const h = setup();
        const key = queryKeys.trucks.list({ status: "active" });
        const q = slowQuery(h.client, key);
        cleanup = () => {
            q.unsubscribe();
            h.cleanup();
        };
        await q.land();
        void h.client.refetchQueries({ queryKey: key }); // e.g. a poll, before the lost events
        h.setVisible(false);
        q.server.version = 1;
        h.inv.resync();
        await flush();
        await q.land();
        expect(q.state()?.isInvalidated).toBe(true);
        h.setVisible(true);
        h.inv.flushDeferred();
        await flush();
        await q.land();
        expect(q.data()).toBe(1);
        expect(q.stats).toEqual({ started: 3, aborted: 0 });
    });

    it("dispose drops the trailing flushes of the refetches still running", async () => {
        const h = setup();
        const q = slowQuery(h.client, queryKeys.trucks.list({ status: "active" }));
        cleanup = () => {
            q.unsubscribe();
            h.cleanup();
        };
        await q.land();
        h.inv.apply(truckUpdated);
        await h.runTimers();
        h.inv.apply(truckUpdated);
        await h.runTimers(); // dirty: a trailing flush is due when the refetch lands
        h.inv.dispose();
        await q.land();
        expect(q.stats.started).toBe(2);
    });
});
