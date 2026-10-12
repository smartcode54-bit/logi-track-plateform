// T18 (R50; developer-spec.md §10.4 steps 3-4, §10.8; Appendix E §E.5 row 20): the session stream that
// replaces the users/{uid}.forceLogoutAt listener; TW5 (Appendix E §E.5.1): its state, resume after a
// network drop, and the domain events on the same stream. EventSource is a fake that the tests drive
// frame by frame; timers are captured and run by hand.
import { describe, expect, it, vi } from "vitest";

import { ApiError } from "../apiError";
import { CLOSED, CONNECTING, MAX_EXPLICIT_TOPICS, REOPEN_MAX_MS, RealtimeStream, type EventSourceLike } from "./realtimeStream";
import { handleSessionRevoked } from "./sessionRevoked";
import { createSessionStream, EVENTS_URL, EVENT_SESSION_REVOKED } from "./sessionStream";

class FakeEventSource implements EventSourceLike {
    readyState = 0;
    onopen: ((this: EventSource, ev: Event) => unknown) | null = null;
    onerror: ((this: EventSource, ev: Event) => unknown) | null = null;
    closed = false;
    private readonly listeners = new Map<string, ((ev: MessageEvent) => void)[]>();
    constructor(readonly url: string) {}
    addEventListener(type: string, listener: (ev: MessageEvent) => void): void {
        this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener]);
    }
    close(): void {
        this.closed = true;
        this.readyState = CLOSED;
    }
    open(): void {
        this.readyState = 1;
        this.onopen?.call(this as unknown as EventSource, new Event("open"));
    }
    emit(type: string, data: unknown, id = ""): void {
        const ev = { data: typeof data === "string" ? data : JSON.stringify(data), lastEventId: id } as MessageEvent;
        for (const l of this.listeners.get(type) ?? []) l(ev);
    }
    fail(): void {
        this.readyState = CLOSED;
        this.onerror?.call(this as unknown as EventSource, new Event("error"));
    }
}

function harness() {
    const sources: FakeEventSource[] = [];
    const timers: { fn: () => void; ms: number }[] = [];
    return {
        sources,
        timers,
        createEventSource: (url: string) => {
            const s = new FakeEventSource(url);
            sources.push(s);
            return s;
        },
        setTimer: (fn: () => void, ms: number) => {
            timers.push({ fn, ms });
            return timers.length;
        },
        clearTimer: () => undefined,
        random: () => 0,
        async runTimers() {
            const pending = timers.splice(0);
            for (const t of pending) t.fn();
            await new Promise((r) => setTimeout(r, 0));
        },
    };
}

const flush = () => new Promise((r) => setTimeout(r, 0));

describe("RealtimeStream", () => {
    it("reopens after event: reconnect with lastEventId and an unforced refresh", async () => {
        const h = harness();
        const refresh = vi.fn(async () => true);
        const stream = new RealtimeStream({ url: "/s", refresh, onSessionLost: vi.fn(), onResync: vi.fn(), onEvent: vi.fn(), ...h });
        stream.open();
        h.sources[0].open();
        h.sources[0].emit("reconnect", { reason: "token_expiring" }, "41");
        expect(h.sources[0].closed).toBe(true);
        await h.runTimers();
        expect(refresh).toHaveBeenCalledTimes(1);
        expect(h.sources[1].url).toBe("/s?lastEventId=41");
    });

    it("a failed open backs off and ends the session when the refresh is refused", async () => {
        const h = harness();
        const lost = vi.fn();
        const stream = new RealtimeStream({ url: "/s", refresh: async () => false, onSessionLost: lost, onResync: vi.fn(), onEvent: vi.fn(), ...h });
        stream.open();
        h.sources[0].fail();
        await h.runTimers();
        expect(lost).toHaveBeenCalledTimes(1);
        expect(stream.running).toBe(false);
        expect(h.sources).toHaveLength(1);
    });

    it("doubles the backoff per failure up to 60 s and ignores the browser's own reconnect (CONNECTING)", () => {
        const h = harness();
        const stream = new RealtimeStream({ url: "/s", refresh: async () => true, onSessionLost: vi.fn(), onResync: vi.fn(), onEvent: vi.fn(), ...h });
        expect(stream.delay()).toBe(1_000);
        stream.open();
        h.sources[0].readyState = 0;
        h.sources[0].onerror?.call(h.sources[0] as unknown as EventSource, new Event("error"));
        expect(h.timers).toHaveLength(0);
        for (let i = 0; i < 10; i++) {
            (stream as unknown as { failures: number }).failures = i;
            expect(stream.delay()).toBe(Math.min(REOPEN_MAX_MS, 1_000 * 2 ** Math.min(i, 6)));
        }
    });

    it("delivers named events with the envelope unwrapped and resync to its handler", () => {
        const h = harness();
        const onEvent = vi.fn();
        const onResync = vi.fn();
        const stream = new RealtimeStream({ url: "/s", refresh: async () => true, onSessionLost: vi.fn(), onResync, onEvent, ...h });
        stream.listen(["session.revoked"]);
        stream.open();
        h.sources[0].emit("session.revoked", { type: "session.revoked", topic: "user:u1", eventId: "e1", data: { reason: "admin_revoke" } }, "7");
        h.sources[0].emit("resync", { reason: "window" }, "9");
        expect(onEvent).toHaveBeenCalledWith(expect.objectContaining({ event: "session.revoked", topic: "user:u1", eventId: "e1", data: { reason: "admin_revoke" }, id: "7" }));
        expect(onResync).toHaveBeenCalledTimes(1);
        expect(stream.lastId).toBe("9");
    });
});

describe("RealtimeStream: state and resume (TW5)", () => {
    function stream(h: ReturnType<typeof harness>, over: Partial<ConstructorParameters<typeof RealtimeStream>[0]> = {}) {
        const statuses: string[] = [];
        const s = new RealtimeStream({
            url: "/s",
            refresh: async () => true,
            onSessionLost: vi.fn(),
            onResync: vi.fn(),
            onEvent: vi.fn(),
            onStatus: (st) => statuses.push(st),
            ...h,
            ...over,
        });
        return { s, statuses };
    }

    it("reports connecting, open, connecting while the browser retries, open again, idle when closed", () => {
        const h = harness();
        const { s, statuses } = stream(h);
        s.open();
        h.sources[0].open();
        // A network drop the browser retries itself: readyState stays CONNECTING, nothing is recreated.
        h.sources[0].readyState = CONNECTING;
        h.sources[0].onerror?.call(h.sources[0] as unknown as EventSource, new Event("error"));
        expect(s.status).toBe("connecting");
        h.sources[0].open();
        s.close();
        expect(statuses).toEqual(["connecting", "open", "connecting", "open", "idle"]);
        expect(h.sources).toHaveLength(1);
        expect(h.timers).toHaveLength(0);
    });

    it("a stream that closed is down until it reopens from the last id it saw", async () => {
        const h = harness();
        const onEvent = vi.fn();
        const { s, statuses } = stream(h, { onEvent });
        s.listen(["truck.updated"]);
        s.open();
        h.sources[0].open();
        h.sources[0].emit("truck.updated", { type: "truck.updated", topic: "tenant:t:fleet", eventId: "e5", data: {} }, "5");
        h.sources[0].fail();
        expect(s.status).toBe("down");
        await h.runTimers();
        expect(h.sources[1].url).toBe("/s?lastEventId=5");
        h.sources[1].open();
        expect(statuses).toEqual(["connecting", "open", "down", "connecting", "open"]);
    });

    it("resume() cuts the backoff short (back online); it does nothing while open or refreshing", async () => {
        const h = harness();
        let release: (ok: boolean) => void = () => undefined;
        const refresh = vi.fn(() => new Promise<boolean>((r) => (release = r)));
        const { s } = stream(h, { refresh });
        s.open();
        s.resume(); // connecting: nothing to cut short
        h.sources[0].open();
        s.resume(); // open
        expect(refresh).not.toHaveBeenCalled();
        h.sources[0].fail();
        expect(h.timers).toHaveLength(1);
        s.resume();
        expect(refresh).toHaveBeenCalledTimes(1);
        s.resume(); // the refresh is in flight
        expect(refresh).toHaveBeenCalledTimes(1);
        release(true);
        await flush();
        expect(h.sources).toHaveLength(2);
    });

    it("a reopen without a position (closed before its first event) resyncs once the new stream is open", async () => {
        const h = harness();
        const onResync = vi.fn();
        const { s } = stream(h, { onResync });
        s.open();
        h.sources[0].open();
        h.sources[0].fail();
        await h.runTimers();
        expect(h.sources[1].url).toBe("/s");
        expect(onResync).not.toHaveBeenCalled();
        h.sources[1].open();
        expect(onResync).toHaveBeenCalledTimes(1);
        // The browser's own reconnect of that stream replays: no second resync.
        h.sources[1].readyState = CONNECTING;
        h.sources[1].onerror?.call(h.sources[1] as unknown as EventSource, new Event("error"));
        h.sources[1].open();
        expect(onResync).toHaveBeenCalledTimes(1);
    });

    it("event: reconnect carries the id of the frame Go writes at connect, so the reopen replays", async () => {
        const h = harness();
        const onResync = vi.fn();
        const { s } = stream(h, { onResync });
        s.open();
        h.sources[0].open();
        // No event yet: the reconnect frame has no id line, its lastEventId is the id-only frame's.
        h.sources[0].emit("reconnect", { reason: "token_expiring" }, "120");
        await h.runTimers();
        expect(h.sources[1].url).toBe("/s?lastEventId=120");
        h.sources[1].open();
        expect(onResync).not.toHaveBeenCalled();
    });

    it("reopen() during a pending refresh wins: the refresh does not open a second stream", async () => {
        const h = harness();
        let release: (ok: boolean) => void = () => undefined;
        const { s } = stream(h, { refresh: () => new Promise<boolean>((r) => (release = r)) });
        s.open();
        h.sources[0].fail();
        await h.runTimers(); // the refresh starts
        s.reopen(); // claims changed meanwhile
        release(true);
        await flush();
        expect(h.sources).toHaveLength(2);
        expect(h.sources[1].closed).toBe(false);
    });

    it("network drop: offline refreshes fail and back off; back online it reopens with lastEventId and delivers the replay", async () => {
        const h = harness();
        const frames: string[] = [];
        let online = false;
        const refresh = vi.fn(async () => {
            if (!online) throw new ApiError({ status: 0, code: "network_error", message: "offline" });
            return true;
        });
        const { s } = stream(h, { refresh, onEvent: (f) => frames.push(`${f.id}:${f.event}`) });
        s.listen(["hubs.changed"]);
        s.open();
        h.sources[0].open();
        h.sources[0].emit("hubs.changed", { type: "hubs.changed", topic: "global", eventId: "a", data: { ids: ["h1"] } }, "41");
        // The BFF cannot be reached: the browser gives up on the stream (CLOSED).
        h.sources[0].fail();
        await h.runTimers(); // refresh rejects -> another backoff
        await h.runTimers();
        expect(refresh).toHaveBeenCalledTimes(2);
        expect(h.sources).toHaveLength(1);
        expect(s.status).toBe("down");
        online = true;
        s.resume(); // window 'online'
        await flush();
        expect(h.sources[1].url).toBe("/s?lastEventId=41");
        h.sources[1].open();
        h.sources[1].emit("hubs.changed", { type: "hubs.changed", topic: "global", eventId: "b", data: { ids: ["h2"] } }, "42");
        expect(frames).toEqual(["41:hubs.changed", "42:hubs.changed"]);
        expect(s.status).toBe("open");
    });
});

describe("handleSessionRevoked", () => {
    it("claims_changed: a forced refresh, and the user stays signed in", async () => {
        const deps = { forcedRefresh: vi.fn(async () => true), checkSession: vi.fn(), endSession: vi.fn() };
        await handleSessionRevoked({ reason: "claims_changed", sessionIds: [] }, 1_000, deps);
        expect(deps.forcedRefresh).toHaveBeenCalledWith(1_000);
        expect(deps.endSession).not.toHaveBeenCalled();
        expect(deps.checkSession).not.toHaveBeenCalled();
    });

    it("claims_changed whose refresh is refused ends the session", async () => {
        const deps = { forcedRefresh: vi.fn(async () => false), checkSession: vi.fn(), endSession: vi.fn() };
        await handleSessionRevoked({ reason: "claims_changed" }, 1, deps);
        expect(deps.endSession).toHaveBeenCalledWith(expect.objectContaining({ code: "session_revoked" }));
    });

    it("any other reason asks Go about this session (its 401 ends it through goFetch)", async () => {
        for (const reason of ["admin_revoke", "disabled", "password_reset", "logout"]) {
            const deps = { forcedRefresh: vi.fn(), checkSession: vi.fn(async () => Promise.reject(new ApiError({ status: 401, code: "session_revoked", message: "" }))), endSession: vi.fn() };
            await handleSessionRevoked({ reason, sessionIds: ["s1"] }, 1, deps);
            expect(deps.checkSession).toHaveBeenCalledTimes(1);
            expect(deps.forcedRefresh).not.toHaveBeenCalled();
        }
    });
});

describe("createSessionStream", () => {
    it("opens /api/go/v1/events and logs the tab out after an admin revoke (within one GET /v1/me)", async () => {
        const h = harness();
        const ended: ApiError[] = [];
        const checks: string[] = [];
        const handled: string[] = [];
        const stream = createSessionStream({
            onResync: vi.fn(),
            // goFetch ends a revoked session itself; this stands in for it.
            checkSession: async () => {
                checks.push("GET /v1/me");
                const e = new ApiError({ status: 401, code: "session_revoked", message: "" });
                ended.push(e);
                throw e;
            },
            refresh: async () => true,
            createEventSource: h.createEventSource,
            onHandled: (f) => handled.push(f.event),
            setTimer: h.setTimer,
            clearTimer: h.clearTimer,
            random: h.random,
        });
        stream.open();
        expect(h.sources[0].url).toBe(EVENTS_URL);
        const started = Date.now();
        h.sources[0].emit(EVENT_SESSION_REVOKED, { type: EVENT_SESSION_REVOKED, topic: "user:u1", eventId: "e", data: { userId: "u1", sessionIds: ["s1"], reason: "admin_revoke" } }, "3");
        await flush();
        expect(checks).toEqual(["GET /v1/me"]);
        expect(ended).toHaveLength(1);
        expect(handled).toEqual([EVENT_SESSION_REVOKED]);
        expect(Date.now() - started).toBeLessThan(5_000);
    });

    it("claims_changed runs one forced refresh and never ends the session", async () => {
        const h = harness();
        const refresh = vi.fn(async (o: { force?: boolean; since: number }) => Boolean(o));
        const end = vi.fn();
        const stream = createSessionStream({
            onResync: vi.fn(),
            checkSession: vi.fn(),
            refresh,
            endSession: end,
            createEventSource: h.createEventSource,
            setTimer: h.setTimer,
            clearTimer: h.clearTimer,
            random: h.random,
        });
        stream.open();
        h.sources[0].emit(EVENT_SESSION_REVOKED, { type: EVENT_SESSION_REVOKED, topic: "user:u1", eventId: "e", data: { userId: "u1", sessionIds: [], reason: "claims_changed" } }, "4");
        await flush();
        expect(refresh).toHaveBeenCalledTimes(1);
        expect(refresh.mock.calls[0][0]).toMatchObject({ force: true });
        expect(end).not.toHaveBeenCalled();
    });

    it("a refused refresh after a stream error ends the session (not session_revoked: no 'revoked' notice)", async () => {
        const h = harness();
        const end = vi.fn();
        const stream = createSessionStream({
            onResync: vi.fn(),
            checkSession: vi.fn(),
            refresh: async () => false,
            endSession: end,
            createEventSource: h.createEventSource,
            setTimer: h.setTimer,
            clearTimer: h.clearTimer,
            random: h.random,
        });
        stream.open();
        h.sources[0].fail();
        await h.runTimers();
        expect(end).toHaveBeenCalledWith(expect.objectContaining({ code: "unauthenticated" }));
    });

    it("delivers the domain events it listens to on the same stream; session.revoked never reaches onEvent", async () => {
        const h = harness();
        const seen: string[] = [];
        const statuses: string[] = [];
        const stream = createSessionStream({
            onResync: vi.fn(),
            checkSession: vi.fn(async () => null),
            refresh: async () => true,
            events: ["truck.updated", "job.updated"],
            onEvent: (f) => seen.push(f.event),
            onStatus: (st) => statuses.push(st),
            createEventSource: h.createEventSource,
            setTimer: h.setTimer,
            clearTimer: h.clearTimer,
            random: h.random,
        });
        stream.open();
        h.sources[0].open();
        h.sources[0].emit("truck.updated", { type: "truck.updated", topic: "tenant:t:fleet", eventId: "e1", data: { entity: "truck", id: "x" } }, "1");
        h.sources[0].emit(EVENT_SESSION_REVOKED, { type: EVENT_SESSION_REVOKED, topic: "user:u", eventId: "e2", data: { reason: "logout", sessionIds: ["other"] } }, "2");
        h.sources[0].emit("job.updated", { type: "job.updated", topic: "user:u", eventId: "e3", data: { id: "j", status: "running" } }, "3");
        h.sources[0].emit("not.listened", { type: "not.listened", topic: "global", eventId: "e4", data: {} }, "4");
        await flush();
        expect(seen).toEqual(["truck.updated", "job.updated"]);
        expect(statuses).toEqual(["connecting", "open"]);
        expect(stream.lastId).toBe("3");
    });
});

describe("RealtimeStream: explicit topics (T50)", () => {
    const make = (h: ReturnType<typeof harness>) =>
        new RealtimeStream({ url: "/s", refresh: async () => true, onSessionLost: vi.fn(), onResync: vi.fn(), onEvent: vi.fn(), ...h });

    it("opens with ?topics= and recreates the stream from the last id only when a topic is added (Go replays the new topic, R52)", async () => {
        const h = harness();
        const s = make(h);
        s.listen(["hubs.changed"]);
        s.setTopics(["chat:b", "chat:a", "chat:a"]); // before open: only remembered
        expect(h.sources).toHaveLength(0);
        s.open();
        expect(h.sources[0].url).toBe(`/s?topics=${encodeURIComponent("chat:a,chat:b")}`);
        expect(s.hasTopic("chat:a")).toBe(false); // not open yet
        h.sources[0].open();
        expect(s.hasTopic("chat:a")).toBe(true);
        h.sources[0].emit("hubs.changed", { type: "hubs.changed", topic: "global", eventId: "e", data: {} }, "77");
        s.setTopics(["chat:a", "chat:b"]); // same set: nothing
        expect(h.sources).toHaveLength(1);
        // A new topic: one new connection, the topics no longer wanted carried along.
        s.setTopics(["chat:c"]);
        expect(h.sources[0].closed).toBe(true);
        expect(h.sources[1].url).toBe(`/s?topics=${encodeURIComponent("chat:a,chat:b,chat:c")}&lastEventId=77`);
        h.sources[1].open();
        expect(s.topics).toEqual(["chat:c"]);
        expect(s.hasTopic("chat:a")).toBe(true);
        // Leaving the room, or coming back to one the connection carries: no new connection (no new lease).
        s.setTopics([]);
        s.setTopics(["chat:a"]);
        expect(h.sources).toHaveLength(2);
        s.setTopics(["chat:d"]);
        expect(h.sources[2].url).toBe(`/s?topics=${encodeURIComponent("chat:a,chat:b,chat:c,chat:d")}&lastEventId=77`);
        h.sources[2].open();
        h.sources[2].emit("hubs.changed", { type: "hubs.changed", topic: "global", eventId: "e", data: {} }, "80");
        // A reopen for another reason asks for the wanted topics only.
        h.sources[2].fail();
        await h.runTimers();
        expect(h.sources[3].url).toBe(`/s?topics=${encodeURIComponent("chat:d")}&lastEventId=80`);
    });

    it("carries at most MAX_EXPLICIT_TOPICS, the least recently wanted dropped first", () => {
        const h = harness();
        const s = make(h);
        s.open();
        h.sources[0].open();
        h.sources[0].emit("hubs.changed", {}, "5");
        const room = (i: number) => `chat:${String(i).padStart(2, "0")}`;
        for (let i = 0; i < MAX_EXPLICIT_TOPICS; i++) s.setTopics([room(i)]);
        s.setTopics([room(0)]); // wanted again: now the most recent
        expect(h.sources).toHaveLength(MAX_EXPLICIT_TOPICS + 1);
        s.setTopics([room(MAX_EXPLICIT_TOPICS)]);
        const topics = new URL(h.sources.at(-1)!.url, "http://x").searchParams.get("topics")!.split(",");
        expect(topics).toHaveLength(MAX_EXPLICIT_TOPICS);
        expect(topics).toContain(room(0));
        expect(topics).toContain(room(MAX_EXPLICIT_TOPICS));
        expect(topics).not.toContain(room(1));
    });

    it("a stream recreated for a topic before any event (no position) resyncs once open: Go cannot replay the gap", () => {
        const h = harness();
        const onResync = vi.fn();
        const s = new RealtimeStream({ url: "/s", refresh: async () => true, onSessionLost: vi.fn(), onResync, onEvent: vi.fn(), ...h });
        s.open();
        h.sources[0].open(); // Go's id-only frame moved the browser's Last-Event-ID, but dispatched nothing
        s.setTopics(["chat:x"]);
        expect(h.sources[1].url).toBe(`/s?topics=${encodeURIComponent("chat:x")}`);
        expect(onResync).not.toHaveBeenCalled();
        h.sources[1].open();
        expect(onResync).toHaveBeenCalledTimes(1);
    });

    it("a stream recreated for a topic after an event resumes from it, with no resync", () => {
        const h = harness();
        const onResync = vi.fn();
        const s = new RealtimeStream({ url: "/s", refresh: async () => true, onSessionLost: vi.fn(), onResync, onEvent: vi.fn(), ...h });
        s.listen(["hubs.changed"]);
        s.open();
        h.sources[0].open();
        h.sources[0].emit("hubs.changed", {}, "77");
        s.setTopics(["chat:x"]);
        expect(h.sources[1].url).toBe(`/s?topics=${encodeURIComponent("chat:x")}&lastEventId=77`);
        h.sources[1].open();
        expect(onResync).not.toHaveBeenCalled();
    });

    it("a topic set during the backoff of a stream that never opened waits for the attempt, which resyncs once open", async () => {
        const h = harness();
        const onResync = vi.fn();
        const s = new RealtimeStream({ url: "/s", refresh: async () => true, onSessionLost: vi.fn(), onResync, onEvent: vi.fn(), ...h });
        s.open();
        h.sources[0].fail(); // e.g. a 429 past SSE_MAX_CONN_PER_USER: wait out the backoff
        s.setTopics(["chat:x"]);
        expect(h.sources).toHaveLength(1);
        await h.runTimers();
        expect(h.sources[1].url).toBe(`/s?topics=${encodeURIComponent("chat:x")}`);
        h.sources[1].open();
        expect(onResync).toHaveBeenCalledTimes(1);
    });

    it("drops the explicit topics after two opens that failed with them (Go's 403), keeping the implicit stream", async () => {
        const h = harness();
        const s = make(h);
        s.setTopics(["chat:x"]);
        s.open();
        h.sources[0].fail();
        await h.runTimers();
        expect(h.sources[1].url).toBe(`/s?topics=${encodeURIComponent("chat:x")}`);
        h.sources[1].fail();
        await h.runTimers();
        expect(h.sources[2].url).toBe("/s");
        h.sources[2].open();
        expect(s.status).toBe("open");
        expect(s.hasTopic("chat:x")).toBe(false);
        expect(s.topics).toEqual(["chat:x"]);
        // A new set is tried afresh.
        s.setTopics(["chat:y"]);
        expect(h.sources[3].url).toBe(`/s?topics=${encodeURIComponent("chat:y")}`);
    });

    it("a stream that fails without its dropped topics too was an outage: the next open carries them again", async () => {
        const h = harness();
        const s = make(h);
        s.setTopics(["chat:x"]);
        s.open();
        h.sources[0].fail();
        await h.runTimers();
        h.sources[1].fail();
        await h.runTimers();
        expect(h.sources[2].url).toBe("/s");
        h.sources[2].fail();
        await h.runTimers();
        expect(h.sources[3].url).toBe(`/s?topics=${encodeURIComponent("chat:x")}`);
        h.sources[3].open();
        expect(s.hasTopic("chat:x")).toBe(true);
    });

    it("a stream that opened and later closed does not count against its topics", async () => {
        const h = harness();
        const s = make(h);
        s.setTopics(["chat:x"]);
        s.open();
        for (let i = 0; i < 3; i++) {
            h.sources[i].open();
            h.sources[i].fail();
            await h.runTimers();
            expect(h.sources[i + 1].url).toBe(`/s?topics=${encodeURIComponent("chat:x")}`);
        }
    });
});

describe("explicit topic registry (T50)", () => {
    it("counts registrations per topic and notifies only when the set changes", async () => {
        const { addRealtimeTopic, realtimeTopics, resetRealtimeTopicsForTests, subscribeRealtimeTopics } = await import("./topics");
        resetRealtimeTopicsForTests();
        const changes = vi.fn();
        const off = subscribeRealtimeTopics(changes);
        const a1 = addRealtimeTopic("chat:a");
        const a2 = addRealtimeTopic("chat:a");
        expect(realtimeTopics()).toEqual(["chat:a"]);
        expect(changes).toHaveBeenCalledTimes(1);
        a1();
        a1(); // once
        expect(realtimeTopics()).toEqual(["chat:a"]);
        a2();
        expect(realtimeTopics()).toEqual([]);
        expect(changes).toHaveBeenCalledTimes(2);
        const many = Array.from({ length: MAX_EXPLICIT_TOPICS + 3 }, (_, i) => addRealtimeTopic(`chat:${String(i).padStart(2, "0")}`));
        expect(realtimeTopics()).toHaveLength(MAX_EXPLICIT_TOPICS);
        for (const remove of many) remove();
        off();
    });
});
