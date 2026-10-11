// TW5 (Appendix E §E.5 "Poll" disposition, §E.5.1 "Backpressure"; issue #85 M1 slice): the reusable
// "poll 60 s while the tab is visible + refetch on focus" options (drivers and trucks lists, rows 23
// and 30, wired by T25) and the SSE fallback poll while the tab's stream is not open.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import React from "react";
import { act, renderHook } from "@testing-library/react";
import { focusManager, QueryClient, QueryClientProvider, QueryObserver, useQuery } from "@tanstack/react-query";

import { QUERY_POLICY, queryKeys } from "../queryKeys";
import { POLL_INTERVAL_MS, pollWhileVisible, refetchOnFocusOlderThan, streamFallbackPoll, useStreamFallbackPoll } from "./polling";
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
