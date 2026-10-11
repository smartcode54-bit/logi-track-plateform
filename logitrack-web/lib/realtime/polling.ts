/**
 * Query options for the two refetch dispositions of Appendix E §E.5 that are not plain fetches
 * (developer-spec.md §8.4, §10.8):
 *
 * - **Poll**: `pollWhileVisible(ms)` refetches every `ms` while the tab is visible (TanStack pauses the
 *   interval in a hidden tab, `refetchIntervalInBackground: false`) and on focus. Spread it into the
 *   query's options: `useInfiniteQuery({ ...listOptions, ...pollWhileVisible() })`. Rows 23 and 30
 *   (drivers and trucks lists, M1) use the 60 s default; security events use 30 s.
 * - **SSE**: the query refetches when an event of the map invalidates it (lib/realtime/eventMap.ts) and
 *   on focus. While the tab's stream is not open (opening, a 429 past `SSE_MAX_CONN_PER_USER`, the api
 *   down) it polls instead: `useStreamFallbackPoll()` returns the poll options then and no interval
 *   while the stream is open, and re-renders when the stream's state changes.
 */
import { useRealtimeStatus } from "./status";

/** The interval of the poll rows of Appendix E §E.5 (and of the SSE fallback). */
export const POLL_INTERVAL_MS = 60_000;

export interface PollOptions {
    refetchInterval: number | false;
    refetchIntervalInBackground: false;
    refetchOnWindowFocus: true;
}

/** Refetch every `intervalMs` while the tab is visible, and when it regains focus. */
export function pollWhileVisible(intervalMs: number = POLL_INTERVAL_MS): PollOptions {
    return { refetchInterval: intervalMs, refetchIntervalInBackground: false, refetchOnWindowFocus: true };
}

/** For an SSE-backed query: no interval while the stream is open, `pollWhileVisible(intervalMs)` otherwise. */
export function streamFallbackPoll(streamOpen: boolean, intervalMs: number = POLL_INTERVAL_MS): PollOptions {
    return streamOpen ? { ...pollWhileVisible(intervalMs), refetchInterval: false } : pollWhileVisible(intervalMs);
}

/** `streamFallbackPoll` over the tab's stream state (re-renders when it changes). */
export function useStreamFallbackPoll(intervalMs: number = POLL_INTERVAL_MS): PollOptions {
    return streamFallbackPoll(useRealtimeStatus() === "open", intervalMs);
}
