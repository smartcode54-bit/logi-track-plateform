// TW5 (Appendix E §E.5 "Poll" disposition, §E.5.1 "Backpressure"; issue #85 M1 slice): the reusable
// "poll 60 s while the tab is visible + refetch on focus" options (drivers and trucks lists, rows 23
// and 30, wired by T25) and the SSE fallback poll while the tab's stream is not open.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import React from "react";
import { act, renderHook } from "@testing-library/react";
import { focusManager, InfiniteQueryObserver, QueryClient, QueryClientProvider, QueryObserver, useQuery } from "@tanstack/react-query";

import { QUERY_POLICY, queryKeys } from "../queryKeys";
import {
    loadedPages,
    MIN_POLL_TIMER_MS,
    POLL_INTERVAL_MS,
    pollFromLastAnswer,
    pollHeadWhileVisible,
    pollWhileVisible,
    refetchOnFocusOlderThan,
    streamFallbackPoll,
    useStreamFallbackPoll,
} from "./polling";
import { getRealtimeStatus, setRealtimeStatus, subscribeRealtimeStatus } from "./status";

/** An observed query counting its fetches, on a mounted client (the cache hears focus changes). */
function observe(options: Record<string, unknown>) {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    client.mount(); // what QueryClientProvider does
    let fetches = 0;
    const observer = new QueryObserver(client, {
        queryKey: queryKeys.trucks.list({ status: "active" }),
        queryFn: async () => ++fetches,
        ...options,
    });
    const unsubscribe = observer.subscribe(() => undefined);
    return {
        fetches: () => fetches,
        stop: () => {
            unsubscribe();
            client.unmount();
            client.clear();
        },
    };
}

beforeEach(() => {
    vi.useFakeTimers();
    focusManager.setFocused(true);
});

afterEach(() => {
    focusManager.setFocused(undefined);
    setRealtimeStatus("idle");
    vi.useRealTimers();
});

describe("pollWhileVisible", () => {
    it("is a 60 s interval, paused in a hidden tab, plus refetch on focus once the data is older than that", () => {
        expect(POLL_INTERVAL_MS).toBe(60_000);
        const poll = pollWhileVisible();
        expect(poll).toMatchObject({ refetchInterval: 60_000, refetchIntervalInBackground: false });
        expect(poll).not.toHaveProperty("staleTime"); // the caller's policy row keeps its staleTime
        expect(poll.refetchOnWindowFocus).toBe(refetchOnFocusOlderThan(60_000)); // one callback per interval
        expect(pollWhileVisible(30_000).refetchInterval).toBe(30_000);

        const focus = refetchOnFocusOlderThan(60_000);
        const at = (ageMs: number) => ({ state: { dataUpdatedAt: Date.now() - ageMs } });
        expect(focus(at(59_999))).toBe(true); // TanStack's rule: only when stale
        expect(focus(at(60_000))).toBe("always");
        expect(focus({ state: { dataUpdatedAt: 0 } })).toBe("always"); // never loaded
    });

    it("refetches every 60 s while visible, never while hidden, and once on focus", async () => {
        const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        client.mount(); // what QueryClientProvider does: the cache hears focus changes
        let fetches = 0;
        const observer = new QueryObserver(client, {
            queryKey: ["trucks", { status: "active" }],
            queryFn: async () => ++fetches,
            staleTime: 60_000,
            ...pollWhileVisible(),
        });
        const unsubscribe = observer.subscribe(() => undefined);
        await vi.advanceTimersByTimeAsync(0);
        expect(fetches).toBe(1);
        await vi.advanceTimersByTimeAsync(60_000);
        expect(fetches).toBe(2);
        await vi.advanceTimersByTimeAsync(60_000);
        expect(fetches).toBe(3);

        focusManager.setFocused(false); // the tab is hidden
        await vi.advanceTimersByTimeAsync(5 * 60_000);
        expect(fetches).toBe(3);

        focusManager.setFocused(true); // back: the data is stale, the focus refetch runs at once
        await vi.advanceTimersByTimeAsync(0);
        expect(fetches).toBe(4);
        unsubscribe();
        client.unmount();
        client.clear();
    });

    it("with the drivers/trucks policy (5 min staleTime), a hide longer than the interval refetches at once on focus", async () => {
        const q = observe({ ...QUERY_POLICY.entity, ...pollWhileVisible() });
        await vi.advanceTimersByTimeAsync(0);
        await vi.advanceTimersByTimeAsync(60_000);
        expect(q.fetches()).toBe(2); // t = 0 and 60 s

        await vi.advanceTimersByTimeAsync(10_000);
        focusManager.setFocused(false); // hidden at 70 s for 150 s: shorter than the staleTime
        await vi.advanceTimersByTimeAsync(150_000);
        expect(q.fetches()).toBe(2);

        focusManager.setFocused(true); // the data is 160 s old: refetched at once, not at the next tick
        await vi.advanceTimersByTimeAsync(0);
        expect(q.fetches()).toBe(3);
        q.stop();
    });

    it("a hide shorter than the interval costs no request; the next tick keeps the data under one interval old", async () => {
        const q = observe({ ...QUERY_POLICY.entity, ...pollWhileVisible() });
        await vi.advanceTimersByTimeAsync(0);
        expect(q.fetches()).toBe(1);
        await vi.advanceTimersByTimeAsync(5_000);
        focusManager.setFocused(false);
        await vi.advanceTimersByTimeAsync(30_000);
        focusManager.setFocused(true); // the data is 35 s old
        await vi.advanceTimersByTimeAsync(0);
        expect(q.fetches()).toBe(1);
        await vi.advanceTimersByTimeAsync(25_000); // the 60 s tick
        expect(q.fetches()).toBe(2);
        q.stop();
    });

    it("a caller's shorter staleTime still refetches on focus once stale (badges: 30 s)", async () => {
        const q = observe({ ...QUERY_POLICY.live, ...pollWhileVisible() });
        await vi.advanceTimersByTimeAsync(0);
        focusManager.setFocused(false);
        await vi.advanceTimersByTimeAsync(40_000);
        focusManager.setFocused(true); // 40 s: younger than the interval, older than the staleTime
        await vi.advanceTimersByTimeAsync(0);
        expect(q.fetches()).toBe(2);
        q.stop();
    });
});

describe("pollHeadWhileVisible (T54S: a keyset list whose every request is audited)", () => {
    /** An observed infinite query (3 pages available) counting its page requests. */
    function observeList(options: object) {
        const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        client.mount();
        let requests = 0;
        const observer = new InfiniteQueryObserver(client, {
            queryKey: queryKeys.security.events({ limit: 100, reach: "all" }),
            queryFn: async ({ pageParam }: { pageParam: number }) => {
                requests++;
                return { page: pageParam };
            },
            initialPageParam: 0,
            getNextPageParam: (last: { page: number }) => (last.page < 2 ? last.page + 1 : undefined),
            ...QUERY_POLICY.live,
            ...options,
        });
        const unsubscribe = observer.subscribe(() => undefined);
        return {
            observer,
            requests: () => requests,
            stop: () => {
                unsubscribe();
                client.unmount();
                client.clear();
            },
        };
    }

    it("counts the loaded pages and stops polling, and refetching on focus, past the first", () => {
        const poll = pollHeadWhileVisible(30_000);
        const q = (pages: unknown[] | undefined, ageMs = 0) => ({ state: { dataUpdatedAt: Date.now() - ageMs, data: pages && { pages, pageParams: [] } } });
        expect(loadedPages(q(undefined))).toBe(0);
        expect(loadedPages(q([1, 2, 3]))).toBe(3);
        expect(poll.refetchInterval(q(undefined))).toBe(30_000);
        expect(poll.refetchInterval(q([1]))).toBe(30_000);
        expect(poll.refetchInterval(q([1, 2]))).toBe(false);
        expect(poll.refetchIntervalInBackground).toBe(false);
        expect(poll.refetchOnWindowFocus(q([1], 30_000))).toBe("always");
        expect(poll.refetchOnWindowFocus(q([1], 1_000))).toBe(true);
        expect(poll.refetchOnWindowFocus(q([1, 2], 3_600_000))).toBe(false);
    });

    it("with pollWhileVisible a poll re-reads every loaded page; with pollHeadWhileVisible one page polls, more pages stop it", async () => {
        // The cost this bounds: TanStack refetches every loaded page in turn.
        const all = observeList(pollWhileVisible(30_000));
        await vi.advanceTimersByTimeAsync(0);
        await all.observer.fetchNextPage();
        await all.observer.fetchNextPage();
        expect(all.requests()).toBe(3);
        await vi.advanceTimersByTimeAsync(30_000);
        expect(all.requests()).toBe(6); // three requests for one poll
        all.stop();

        const head = observeList(pollHeadWhileVisible(30_000));
        await vi.advanceTimersByTimeAsync(0);
        expect(head.requests()).toBe(1);
        await vi.advanceTimersByTimeAsync(30_000);
        expect(head.requests()).toBe(2); // one page: one request per interval
        await head.observer.fetchNextPage();
        await head.observer.fetchNextPage();
        expect(head.requests()).toBe(4);
        await vi.advanceTimersByTimeAsync(10 * 30_000);
        expect(head.requests()).toBe(4); // "load more" used: no poll
        focusManager.setFocused(false);
        await vi.advanceTimersByTimeAsync(5 * 60_000);
        focusManager.setFocused(true);
        await vi.advanceTimersByTimeAsync(0);
        expect(head.requests()).toBe(4); // nor a focus refetch
        await head.observer.refetch(); // an explicit Refresh still re-reads every page
        expect(head.requests()).toBe(7);
        head.stop();
    });
});

describe("SSE fallback poll", () => {
    it("polls only while the stream is not open", () => {
        expect(streamFallbackPoll(true)).toEqual({ refetchInterval: false, refetchIntervalInBackground: false, refetchOnWindowFocus: true });
        expect(streamFallbackPoll(false)).toEqual(pollWhileVisible());
    });

    it("with the stream open, a fresh SSE row is not refetched on focus, however long the tab was hidden", async () => {
        const q = observe({ ...QUERY_POLICY.masterData, ...streamFallbackPoll(true) });
        await vi.advanceTimersByTimeAsync(0);
        focusManager.setFocused(false);
        await vi.advanceTimersByTimeAsync(5 * 60_000); // older than the poll interval, fresh for 10 min
        focusManager.setFocused(true);
        await vi.advanceTimersByTimeAsync(0);
        expect(q.fetches()).toBe(1);
        q.stop();
    });

    it("follows the tab's stream state (a 429 past the cap, the api down, opening)", () => {
        const { result, unmount } = renderHook(() => useStreamFallbackPoll());
        expect(result.current.refetchInterval).toBe(60_000); // idle
        act(() => setRealtimeStatus("open"));
        expect(result.current.refetchInterval).toBe(false);
        act(() => setRealtimeStatus("down"));
        expect(result.current.refetchInterval).toBe(60_000);
        act(() => setRealtimeStatus("open"));
        expect(result.current.refetchInterval).toBe(false);
        unmount();
    });

    it("an SSE-backed query starts polling when the stream goes down and stops when it is open again", async () => {
        const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        let fetches = 0;
        setRealtimeStatus("open");
        const wrapper = ({ children }: { children: React.ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
        const view = renderHook(
            () =>
                useQuery({
                    queryKey: ["tasks", { date: "2026-10-11", type: "FIRST_MILE" }],
                    queryFn: async () => ++fetches,
                    staleTime: 30_000,
                    ...useStreamFallbackPoll(),
                }),
            { wrapper }
        );
        await act(() => vi.advanceTimersByTimeAsync(0));
        expect(fetches).toBe(1);
        await act(() => vi.advanceTimersByTimeAsync(3 * 60_000));
        expect(fetches).toBe(1); // the stream invalidates it; no poll

        act(() => setRealtimeStatus("down"));
        await act(() => vi.advanceTimersByTimeAsync(60_000));
        expect(fetches).toBe(2);
        await act(() => vi.advanceTimersByTimeAsync(60_000));
        expect(fetches).toBe(3);

        act(() => setRealtimeStatus("open"));
        await act(() => vi.advanceTimersByTimeAsync(3 * 60_000));
        expect(fetches).toBe(3);
        view.unmount();
        client.clear();
    });

    it("the status store notifies on change only", () => {
        const seen: string[] = [];
        const off = subscribeRealtimeStatus(() => seen.push(getRealtimeStatus()));
        setRealtimeStatus("connecting");
        setRealtimeStatus("connecting");
        setRealtimeStatus("open");
        off();
        setRealtimeStatus("down");
        expect(seen).toEqual(["connecting", "open"]);
    });
});

describe("pollFromLastAnswer (a key the stream also writes into, T54R)", () => {
    const at = (dataUpdatedAt: number, errorUpdatedAt = 0) => ({ state: { dataUpdatedAt, errorUpdatedAt } });

    it("is the time left until the last answer (data or error) is one interval old", () => {
        const poll = pollFromLastAnswer(5 * 60_000);
        expect(poll).toMatchObject({ refetchIntervalInBackground: false });
        expect(poll.refetchOnWindowFocus).toBe(refetchOnFocusOlderThan(5 * 60_000));
        const now = Date.now();
        expect(poll.refetchInterval(at(0))).toBe(5 * 60_000); // nothing answered yet
        expect(poll.refetchInterval(at(now - 3 * 60_000))).toBe(2 * 60_000);
        expect(poll.refetchInterval(at(now - 3 * 60_000, now - 60_000))).toBe(4 * 60_000); // an error is an answer
        expect(poll.refetchInterval(at(now - 5 * 60_000 + 10))).toBe(MIN_POLL_TIMER_MS); // never a spinning timer
        expect(poll.refetchInterval(at(now - 6 * 60_000))).toBe(5 * 60_000); // due: running, or the focus rule refetches
        expect(pollFromLastAnswer().refetchInterval(at(now))).toBe(POLL_INTERVAL_MS);
    });

    it("refetches one interval after the last answer although writes that keep dataUpdatedAt arrive more often (a constant interval never fires)", async () => {
        const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        client.mount();
        const key = ["vehicleLocations"];
        const counts = { fromAnswer: 0, constant: 0 };
        const fromAnswer = new QueryObserver(client, { queryKey: key, queryFn: async () => ++counts.fromAnswer, ...pollFromLastAnswer(5 * 60_000) });
        const constantKey = ["vehicleLocations", "constant"];
        const constant = new QueryObserver(client, { queryKey: constantKey, queryFn: async () => ++counts.constant, ...pollWhileVisible(5 * 60_000) });
        const stops = [fromAnswer.subscribe(() => undefined), constant.subscribe(() => undefined)];
        await vi.advanceTimersByTimeAsync(0);
        const answered = client.getQueryState(key)!.dataUpdatedAt;
        // A batch every 3 min: TanStack restarts an observer's interval on every update of its query.
        for (let i = 0; i < 4; i++) {
            await vi.advanceTimersByTimeAsync(3 * 60_000);
            client.setQueryData(key, (n: number | undefined) => n, { updatedAt: client.getQueryState(key)!.dataUpdatedAt });
            client.setQueryData(constantKey, (n: number | undefined) => n);
            if (i === 0) expect(client.getQueryState(key)!.dataUpdatedAt).toBe(answered);
        }
        // 12 minutes: two refetches 5 min apart; the constant 5-min interval was restarted every 3 min.
        expect(counts.fromAnswer).toBe(3);
        expect(counts.constant).toBe(1);
        stops.forEach((s) => s());
        client.unmount();
        client.clear();
    });

    it("refetches nothing in a hidden tab, and at once on focus once the last answer is older than the interval", async () => {
        const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        client.mount();
        let fetches = 0;
        const observer = new QueryObserver(client, { queryKey: ["vehicleLocations"], queryFn: async () => ++fetches, ...pollFromLastAnswer(60_000) });
        const stop = observer.subscribe(() => undefined);
        await vi.advanceTimersByTimeAsync(0);
        focusManager.setFocused(false);
        await vi.advanceTimersByTimeAsync(10 * 60_000);
        expect(fetches).toBe(1);
        focusManager.setFocused(true);
        await vi.advanceTimersByTimeAsync(0);
        expect(fetches).toBe(2);
        stop();
        client.unmount();
        client.clear();
    });
});
