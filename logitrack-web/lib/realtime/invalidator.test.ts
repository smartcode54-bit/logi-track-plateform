// TW5 (developer-spec.md §10.8; Appendix E §E.5.1 "Event -> cache", "Backpressure"): the event map
// applied to a real QueryClient. Coalescing windows run on captured timers; visibility is a flag.
import { afterEach, describe, expect, it, vi } from "vitest";
import { InfiniteQueryObserver, QueryClient, QueryObserver, type QueryKey } from "@tanstack/react-query";

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

describe("RealtimeInvalidator: the chat room (T50)", () => {
    it("chat.updated refetches the room's chat (exact), never its loaded message pages below it", async () => {
        const h = setup();
        const chat = await h.query(queryKeys.chat.detail("c1"));
        const messages = await h.query(queryKeys.chat.messages("c1"));
        const queued = await h.query(queryKeys.chats.queued());
        h.inv.apply({ event: "chat.updated", data: { entity: "chat", id: "c1", status: "closed" } });
        await h.runTimers();
        expect(chat()).toBe(2);
        expect(queued()).toBe(2);
        expect(messages()).toBe(1);
        h.cleanup();
    });

    it("chat.message_created refetches the room's messages only while the stream lacks its chat:{id}", async () => {
        let joined = false;
        const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 60_000 } } });
        const timers: Array<() => void> = [];
        const inv = new RealtimeInvalidator({
            client,
            isVisible: () => true,
            hasTopic: (t) => joined && t === "chat:c1",
            setTimer: (fn) => (timers.push(fn), timers.length),
            clearTimer: () => undefined,
        });
        let fetches = 0;
        const obs = new QueryObserver(client, { queryKey: queryKeys.chat.messages("c1"), queryFn: async () => ++fetches });
        const off = obs.subscribe(() => undefined);
        await flush();
        const thin = { entity: "chat_message", id: "m1", chatId: "c1", senderRole: "driver" };
        inv.apply({ event: "chat.message_created", data: thin });
        for (const fn of timers.splice(0)) fn();
        await flush();
        expect(fetches).toBe(2); // the fallback: no chat:{id} on the stream
        joined = true;
        inv.apply({ event: "chat.message_created", data: { ...thin, id: "m2" } });
        for (const fn of timers.splice(0)) fn();
        await flush();
        expect(fetches).toBe(2); // message.created on chat:{id} writes it instead
        off();
        inv.dispose();
    });
});

describe("RealtimeInvalidator: a message pushed while the room's messages are fetching (T50)", () => {
    type Msg = { id: string; chatId: string; senderRole: string; text: string | null; imageFileId: string | null; imageUrl: string | null; clientMessageId: string | null; createdAt: string };
    const msg = (n: number, over: Partial<Msg> = {}): Msg => ({
        id: `m${n}`,
        chatId: "c1",
        senderRole: "driver",
        text: `text ${n}`,
        imageFileId: null,
        imageUrl: null,
        clientMessageId: null,
        createdAt: `2026-10-12T01:00:${String(n).padStart(2, "0")}Z`,
        ...over,
    });

    /** The room's messages over a server whose rows the test commits; each fetch reads them when it starts and lands by hand. */
    function room(client: QueryClient) {
        const server: Msg[] = [msg(1)];
        const pending: Array<() => void> = [];
        let started = 0;
        const observer = new InfiniteQueryObserver(client, {
            queryKey: queryKeys.chat.messages("c1"),
            queryFn: () =>
                new Promise<{ data: Msg[] }>((resolve) => {
                    started++;
                    const read = [...server].reverse().map((m) => (m.imageFileId ? { ...m, imageUrl: `https://files.test/${m.imageFileId}` } : m));
                    pending.push(() => resolve({ data: read }));
                }),
            initialPageParam: undefined as string | undefined,
            getNextPageParam: () => undefined,
            staleTime: Infinity,
            refetchOnWindowFocus: false,
        });
        const off = observer.subscribe(() => undefined);
        return {
            server,
            started: () => started,
            texts: () => (observer.getCurrentResult().data?.pages ?? []).flatMap((p) => p.data.map((m) => m.text ?? `[${m.imageFileId}]`)),
            async land() {
                pending.splice(0).forEach((resolve) => resolve());
                await flush();
            },
            off,
        };
    }

    const created = (m: Msg) => ({ event: "message.created", data: { entity: "chat_message", senderName: null, legacyDocId: null, messageType: "normal", ...m } });

    it("a caption committed after the photo's refetch read the server is kept when that refetch lands", async () => {
        const h = setup();
        // The room's chat:{id} is on the stream: the thin tenant copy refetches nothing.
        const inv = new RealtimeInvalidator({ client: h.client, isVisible: () => true, hasTopic: () => true, setTimer: (fn, ms) => (h.timers.push({ fn, ms, cleared: false }), h.timers.length), clearTimer: () => undefined });
        const r = room(h.client);
        await flush();
        await r.land(); // the first load: [text 1]
        // A photo: written in, and its key refetched once the window closes (the event has no signed URL).
        const photo = msg(2, { text: null, imageFileId: "f2" });
        r.server.push(photo);
        inv.apply(created(photo));
        await h.runTimers();
        expect(r.started()).toBe(2); // the refetch read [1, photo]
        // The caption commits after that read; its event arrives before the refetch lands.
        const caption = msg(3, { text: "the box is broken" });
        r.server.push(caption);
        inv.apply(created(caption));
        inv.apply({ event: "chat.message_created", data: { entity: "chat_message", id: caption.id, chatId: "c1", senderRole: "driver" } });
        expect(r.texts()).toContain("the box is broken");
        await r.land();
        expect(r.texts()).toEqual(["the box is broken", "[f2]", "text 1"]);
        await h.runTimers();
        expect(r.started()).toBe(2); // nothing else was needed: the photo's URL came with the refetch
        r.off();
        inv.dispose();
    });

    it("a message pushed during the first load is in the loaded page", async () => {
        const h = setup();
        const inv = new RealtimeInvalidator({ client: h.client, isVisible: () => true, hasTopic: () => true, setTimer: () => 0, clearTimer: () => undefined });
        const r = room(h.client);
        await flush(); // the first load read [text 1]
        const late = msg(2, { text: "sent during the load" });
        r.server.push(late);
        inv.apply(created(late)); // no cache yet: nothing to write into
        await r.land();
        expect(r.texts()).toEqual(["sent during the load", "text 1"]);
        r.off();
        inv.dispose();
    });

    it("an image pushed during a refetch that predates it makes the key stale again (its URL)", async () => {
        const h = setup();
        const inv = new RealtimeInvalidator({ client: h.client, isVisible: () => true, hasTopic: () => true, setTimer: (fn, ms) => (h.timers.push({ fn, ms, cleared: false }), h.timers.length), clearTimer: () => undefined });
        const r = room(h.client);
        await flush();
        await r.land();
        void h.client.invalidateQueries({ queryKey: queryKeys.chat.messages("c1") }); // a poll or resync: reads [1]
        await flush();
        expect(r.started()).toBe(2);
        const photo = msg(2, { text: null, imageFileId: "f2" });
        r.server.push(photo);
        inv.apply(created(photo)); // its own window refetch is scheduled too
        await r.land();
        expect(r.texts()).toEqual(["[f2]", "text 1"]);
        await h.runTimers(); // the image's refetch: now with its URL
        expect(r.started()).toBe(3);
        await r.land();
        const cached = h.client.getQueryData<{ pages: { data: Msg[] }[] }>(queryKeys.chat.messages("c1"))!;
        expect(cached.pages[0].data[0].imageUrl).toBe("https://files.test/f2");
        r.off();
        inv.dispose();
    });
});
