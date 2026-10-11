// TW5 (developer-spec.md §10.8; Appendix E §E.5.1 "Event -> cache", "Backpressure"): the event map
// applied to a real QueryClient. Coalescing windows run on captured timers; visibility is a flag.
import { afterEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryObserver, type QueryKey } from "@tanstack/react-query";

import { queryKeys } from "../queryKeys";
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

let cleanup: () => void = () => undefined;
afterEach(() => cleanup());

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
        expect(spy).toHaveBeenCalledWith({ refetchType: "active" });
        inv.dispose();
    });
});
