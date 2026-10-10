// T18 (R50; developer-spec.md §10.4 steps 3-4, §10.8; Appendix E §E.5 row 20): the session stream that
// replaces the users/{uid}.forceLogoutAt listener. EventSource is a fake that the tests drive frame by
// frame; timers are captured and run by hand.
import { describe, expect, it, vi } from "vitest";

import { ApiError } from "../apiError";
import { CLOSED, REOPEN_MAX_MS, RealtimeStream, type EventSourceLike } from "./realtimeStream";
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
});
