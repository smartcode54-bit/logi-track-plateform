// TW5 (issue #85; developer-spec.md §10.8; Appendix E §E.5.1): the RealtimeProvider holds one
// EventSource per tab once ['me'] holds a principal and applies the event map to the cache. The
// acceptance criterion "network drop then reconnect resumes without reload" is driven end to end
// here: a mounted list keeps its component instance while the stream drops, waits offline, reopens
// with ?lastEventId= when the browser is back online, and the replayed event refetches the list.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import React, { useEffect } from "react";
import { act, render, screen } from "@testing-library/react";
import { focusManager, QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";

const refresh = vi.hoisted(() => ({ fn: vi.fn<(o: { force?: boolean; since: number }) => Promise<boolean>>() }));
vi.mock("@/lib/sharedRefresh", async (importOriginal) => ({
    ...(await importOriginal<typeof import("@/lib/sharedRefresh")>()),
    sharedRefresh: (o: { force?: boolean; since: number }) => refresh.fn(o),
}));

import { ApiError } from "@/lib/apiError";
import { queryKeys } from "@/lib/queryKeys";
import { makeMe } from "@/test-utils/fakeWeb";
import { RealtimeProvider } from "./RealtimeProvider";
import { CLOSED, CONNECTING, OPEN, type EventSourceLike } from "./realtimeStream";
import { getRealtimeStatus, setRealtimeStatus } from "./status";
import { chatTopic, resetRealtimeTopicsForTests, useRealtimeTopic } from "./topics";
import { useInfiniteQuery } from "@tanstack/react-query";

class FakeEventSource implements EventSourceLike {
    readyState = CONNECTING;
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
        this.readyState = OPEN;
        this.onopen?.call(this as unknown as EventSource, new Event("open"));
    }
    emit(type: string, data: unknown, id: string): void {
        const ev = { data: JSON.stringify({ type, topic: "tenant:t-own:fleet", eventId: `e${id}`, data }), lastEventId: id } as MessageEvent;
        for (const l of this.listeners.get(type) ?? []) l(ev);
    }
    /** The connection broke; `retrying`: the browser reconnects by itself (CONNECTING), else it gave up (CLOSED). */
    drop(retrying: boolean): void {
        this.readyState = retrying ? CONNECTING : CLOSED;
        this.onerror?.call(this as unknown as EventSource, new Event("error"));
    }
}

let sources: FakeEventSource[] = [];
const createEventSource = (url: string) => {
    const s = new FakeEventSource(url);
    sources.push(s);
    return s;
};

const TRUCKS = queryKeys.trucks.list({ status: "active" });
let fetches = 0;
let mounts = 0;

function TrucksList() {
    useEffect(() => {
        mounts++;
    }, []);
    const { data } = useQuery({ queryKey: TRUCKS, queryFn: async () => ++fetches, staleTime: 5 * 60_000 });
    return <p data-testid="trucks">{String(data ?? "")}</p>;
}

function renderTab(client: QueryClient) {
    return render(
        <QueryClientProvider client={client}>
            <RealtimeProvider createEventSource={createEventSource}>
                <TrucksList />
            </RealtimeProvider>
        </QueryClientProvider>
    );
}

function newClient(me: unknown = makeMe()) {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    // ['me'] is resolved (fresh for its 5 min): the provider opens the stream without a request.
    client.setQueryData(queryKeys.me(), me);
    client.setQueryDefaults(queryKeys.me(), { staleTime: Infinity, queryFn: async () => me });
    return client;
}

const advance = (ms: number) => act(() => vi.advanceTimersByTimeAsync(ms));

beforeEach(() => {
    vi.useFakeTimers();
    focusManager.setFocused(true);
    sources = [];
    fetches = 0;
    mounts = 0;
    refresh.fn.mockReset();
    refresh.fn.mockResolvedValue(true);
});

afterEach(() => {
    focusManager.setFocused(undefined);
    setRealtimeStatus("idle");
    vi.useRealTimers();
});

describe("RealtimeProvider", () => {
    it("network drop, offline, back online: the stream resumes from its last id and the list refetches, without a reload", async () => {
        const client = newClient();
        const view = renderTab(client);
        await advance(0);
        expect(sources.map((s) => s.url)).toEqual(["/api/go/v1/events"]);
        expect(fetches).toBe(1);
        sources[0].open();
        expect(getRealtimeStatus()).toBe("open");

        // A live event: one refetch after the 500 ms window.
        act(() => sources[0].emit("truck.updated", { entity: "truck", id: "tr-1", changed: ["status"] }, "10"));
        await advance(500);
        expect(fetches).toBe(2);

        // The network drops and the browser gives up on the stream; the refresh cannot reach the BFF.
        refresh.fn.mockRejectedValue(new ApiError({ status: 0, code: "network_error", message: "offline" }));
        act(() => sources[0].drop(false));
        expect(getRealtimeStatus()).toBe("down");
        await advance(3_000); // first reopen attempt: refresh rejected, longer backoff
        expect(refresh.fn).toHaveBeenCalledTimes(1);
        expect(sources).toHaveLength(1);

        // Back online: the provider reopens at once (no wait for the backoff) from the last id.
        refresh.fn.mockResolvedValue(true);
        await act(async () => {
            window.dispatchEvent(new Event("online"));
            await vi.advanceTimersByTimeAsync(0);
        });
        expect(sources.map((s) => s.url)).toEqual(["/api/go/v1/events", "/api/go/v1/events?lastEventId=10"]);
        expect(sources[0].closed).toBe(true);
        sources[1].open();
        expect(getRealtimeStatus()).toBe("open");

        // Go replays what was published while the tab was offline.
        act(() => sources[1].emit("truck.updated", { entity: "truck", id: "tr-2", changed: ["plate"] }, "11"));
        await advance(500);
        expect(fetches).toBe(3);
        await advance(10); // the observer is notified on a later tick, then React renders
        expect(screen.getByTestId("trucks").textContent).toBe("3");
        expect(mounts).toBe(1); // the same component instance throughout: no reload, no remount
        view.unmount();
        expect(sources[1].closed).toBe(true);
        expect(getRealtimeStatus()).toBe("idle");
    });

    it("a drop the browser retries itself needs nothing from the provider; events keep arriving", async () => {
        const client = newClient();
        const view = renderTab(client);
        await advance(0);
        sources[0].open();
        act(() => sources[0].drop(true));
        expect(getRealtimeStatus()).toBe("connecting");
        await advance(60_000);
        expect(sources).toHaveLength(1);
        expect(refresh.fn).not.toHaveBeenCalled();
        sources[0].open(); // reconnected with the browser's own Last-Event-ID
        act(() => sources[0].emit("driver.updated", { entity: "driver", id: "d-1" }, "12"));
        await advance(500);
        expect(fetches).toBe(2);
        view.unmount();
    });

    it("opens no stream before ['me'] holds a principal, and one per principal and tenant", async () => {
        const client = newClient(null);
        const view = renderTab(client);
        await advance(0);
        expect(sources).toHaveLength(0);

        act(() => client.setQueryData(queryKeys.me(), makeMe()));
        await advance(0);
        expect(sources).toHaveLength(1);
        view.rerender(
            <QueryClientProvider client={client}>
                <RealtimeProvider createEventSource={createEventSource}>
                    <TrucksList />
                    <p>more</p>
                </RealtimeProvider>
            </QueryClientProvider>
        );
        await advance(0);
        expect(sources).toHaveLength(1); // a re-render keeps the stream

        // A tenant switch: the implicit topics change, so the stream is recreated.
        act(() => client.setQueryData(queryKeys.me(), makeMe({ tenant: { id: "t-2", nameTh: "อื่น", nameEn: "Other", kind: "carrier", role: "tenant_admin" } })));
        await advance(0);
        expect(sources).toHaveLength(2);
        expect(sources[0].closed).toBe(true);

        act(() => client.setQueryData(queryKeys.me(), null)); // signed out
        await advance(0);
        expect(sources[1].closed).toBe(true);
        expect(getRealtimeStatus()).toBe("idle");
        view.unmount();
    });

    it("a hidden tab refetches on its return, once", async () => {
        const client = newClient();
        const view = renderTab(client);
        await advance(0);
        sources[0].open();
        act(() => focusManager.setFocused(false));
        act(() => sources[0].emit("truck.renewed", { entity: "truck", id: "tr-1" }, "20"));
        act(() => sources[0].emit("assignment.created", { entity: "assignment", id: "as-1" }, "21"));
        await advance(5_000);
        expect(fetches).toBe(1);
        act(() => focusManager.setFocused(true));
        await advance(0);
        expect(fetches).toBe(2);
        view.unmount();
    });

    it("job.updated writes ['jobs', id] for a page that follows a job", async () => {
        const client = newClient();
        const view = renderTab(client);
        await advance(0);
        sources[0].open();
        act(() => sources[0].emit("job.updated", { id: "job-1", type: "distances.compute", status: "running", progress: { done: 3, total: 9 } }, "30"));
        expect(client.getQueryData(queryKeys.jobs("job-1"))).toMatchObject({ status: "running", progress: { done: 3, total: 9 } });
        view.unmount();
    });

    it("an open chat room joins chat:{id} within 500 ms, from the last id; message.created shows the message without a refetch (T50)", async () => {
        resetRealtimeTopicsForTests();
        const client = newClient();
        let messageFetches = 0;
        function Room() {
            useRealtimeTopic(chatTopic("c1"));
            const q = useInfiniteQuery({
                queryKey: queryKeys.chat.messages("c1"),
                queryFn: async () => {
                    messageFetches++;
                    return { data: [{ id: "m1", chatId: "c1", senderRole: "driver", text: "first", clientMessageId: null, createdAt: "2026-10-12T01:00:01Z" }] };
                },
                initialPageParam: undefined as string | undefined,
                getNextPageParam: () => undefined,
                staleTime: Infinity,
            });
            const texts = (q.data?.pages ?? []).flatMap((p) => p.data.map((m) => (m as { text: string }).text));
            return <p data-testid="room">{texts.join("|")}</p>;
        }
        const tab = (room: boolean) => (
            <QueryClientProvider client={client}>
                <RealtimeProvider createEventSource={createEventSource}>{room ? <Room /> : <p>console</p>}</RealtimeProvider>
            </QueryClientProvider>
        );
        // The tab's stream is open before the room is (the console, then a click on a chat).
        const view = render(tab(false));
        await advance(0);
        sources[0].open();
        act(() => sources[0].emit("hubs.changed", { ids: [] }, "40"));
        view.rerender(tab(true));
        await advance(499);
        expect(sources).toHaveLength(1); // at most one recreate per 500 ms
        await advance(1);
        expect(sources).toHaveLength(2);
        expect(sources[0].closed).toBe(true);
        const joined = sources[1];
        expect(joined.url).toBe(`/api/go/v1/events?topics=${encodeURIComponent("chat:c1")}&lastEventId=40`);
        joined.open();
        expect(messageFetches).toBe(1);
        act(() =>
            joined.emit(
                "message.created",
                { entity: "chat_message", id: "m2", chatId: "c1", senderRole: "driver", senderName: "S", text: "second", imageFileId: null, messageType: "normal", clientMessageId: "x", createdAt: "2026-10-12T01:00:02Z" },
                "41"
            )
        );
        await advance(10);
        expect(screen.getByTestId("room").textContent).toBe("second|first");
        // The tenant topic's thin copy of the same message: the room has its topic, so no refetch.
        act(() => joined.emit("chat.message_created", { entity: "chat_message", id: "m2", chatId: "c1", senderRole: "driver" }, "41"));
        await advance(600);
        expect(messageFetches).toBe(1);
        view.unmount(); // the room's unmount drops its registration
    });
});
