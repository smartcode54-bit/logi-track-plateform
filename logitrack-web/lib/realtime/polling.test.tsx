// TW5 (Appendix E §E.5 "Poll" disposition, §E.5.1 "Backpressure"; issue #85 M1 slice): the reusable
// "poll 60 s while the tab is visible + refetch on focus" options (drivers and trucks lists, rows 23
// and 30, wired by T25) and the SSE fallback poll while the tab's stream is not open.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import React from "react";
import { act, renderHook } from "@testing-library/react";
import { focusManager, QueryClient, QueryClientProvider, QueryObserver, useQuery } from "@tanstack/react-query";

import { POLL_INTERVAL_MS, pollWhileVisible, streamFallbackPoll, useStreamFallbackPoll } from "./polling";
import { getRealtimeStatus, setRealtimeStatus, subscribeRealtimeStatus } from "./status";

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
    it("is a 60 s interval, paused in a hidden tab, plus refetch on focus", () => {
        expect(POLL_INTERVAL_MS).toBe(60_000);
        expect(pollWhileVisible()).toEqual({ refetchInterval: 60_000, refetchIntervalInBackground: false, refetchOnWindowFocus: true });
        expect(pollWhileVisible(30_000).refetchInterval).toBe(30_000);
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
});

describe("SSE fallback poll", () => {
    it("polls only while the stream is not open", () => {
        expect(streamFallbackPoll(true)).toEqual({ refetchInterval: false, refetchIntervalInBackground: false, refetchOnWindowFocus: true });
        expect(streamFallbackPoll(false)).toEqual(pollWhileVisible());
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
