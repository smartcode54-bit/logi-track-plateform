/**
 * Query options for the two refetch dispositions of Appendix E §E.5 that are not plain fetches
 * (developer-spec.md §8.4, §10.8):
 *
 * - **Poll**: `pollWhileVisible(ms)` refetches every `ms` while the tab is visible (TanStack pauses the
 *   interval in a hidden tab, `refetchIntervalInBackground: false`) and, when the tab is visible again,
 *   at once if the data is older than `ms` (or stale by the query's `staleTime`). The data on screen is
 *   therefore never older than one interval, whatever the `staleTime` (5 min for drivers and trucks),
 *   and a tab switch shorter than that costs no request. It leaves `staleTime` alone, so it can be
 *   spread in any order: `useInfiniteQuery({ ...QUERY_POLICY.entity, ...listOptions, ...pollWhileVisible() })`.
 *   Rows 23 and 30 (drivers and trucks lists, M1) use the 60 s default; security events use 30 s.
 *   A keyset list whose every request is audited uses `pollHeadWhileVisible(ms)`, which stops once
 *   more than one page is loaded (TanStack refetches each loaded page).
 * - **SSE**: the query refetches when an event of the map invalidates it (lib/realtime/eventMap.ts) and
 *   on focus when stale. While the tab's stream is not open (opening, a 429 past
 *   `SSE_MAX_CONN_PER_USER`, the api down) it polls instead: `useStreamFallbackPoll()` returns the poll
 *   options then, and while the stream is open no interval and TanStack's own focus refetch (only when
 *   stale: the events keep the data current); it re-renders when the stream's state changes.
 * - **SSE data plus a poll**: a key the stream writes into but cannot keep whole (the vehicle-location
 *   batches carry part of each row and never add or remove one) uses `pollFromLastAnswer(ms)`, an
 *   interval counted from the server's last answer, which the stream's writes do not move.
 */
import { useRealtimeStatus } from "./status";

/** The interval of the poll rows of Appendix E §E.5 (and of the SSE fallback). */
export const POLL_INTERVAL_MS = 60_000;

/**
 * A `refetchOnWindowFocus` callback: TanStack calls it with the query when the tab is visible again,
 * and refetches on `'always'`, or on `true` when the data is stale.
 */
export type FocusRefetch = (query: { state: { dataUpdatedAt: number } }) => boolean | "always";

export interface PollOptions {
    refetchInterval: number | false;
    refetchIntervalInBackground: false;
    refetchOnWindowFocus: true | FocusRefetch;
}

const focusRules = new Map<number, FocusRefetch>();

/** On focus: at once when the data is older than `maxAgeMs`, else only when stale (one callback per age). */
export function refetchOnFocusOlderThan(maxAgeMs: number): FocusRefetch {
    let rule = focusRules.get(maxAgeMs);
    if (!rule) {
        rule = (query) => (Date.now() - query.state.dataUpdatedAt >= maxAgeMs ? "always" : true);
        focusRules.set(maxAgeMs, rule);
    }
    return rule;
}

/** Refetch every `intervalMs` while the tab is visible, and on focus once the data is older than that. */
export function pollWhileVisible(intervalMs: number = POLL_INTERVAL_MS): PollOptions {
    return {
        refetchInterval: intervalMs,
        refetchIntervalInBackground: false,
        refetchOnWindowFocus: refetchOnFocusOlderThan(intervalMs),
    };
}

/** What the poll rules read of a query: an infinite query's `data` holds its `pages`. */
export interface PolledQuery {
    state: { dataUpdatedAt: number; data?: unknown };
}

/** How many pages an infinite query holds (0 before its first one). */
export function loadedPages(query: PolledQuery): number {
    const data = query.state.data as { pages?: unknown } | undefined;
    return Array.isArray(data?.pages) ? data.pages.length : 0;
}

export interface HeadPollOptions {
    refetchInterval: (query: PolledQuery) => number | false;
    refetchIntervalInBackground: false;
    refetchOnWindowFocus: (query: PolledQuery) => boolean | "always";
}

/**
 * `pollWhileVisible` for a keyset list (an infinite query) that polls only while it holds its first
 * page (T54S). TanStack refetches every loaded page in turn on each interval and focus refetch, one
 * request per page; once "load more" was used, the list stops refreshing by itself (the page's Refresh
 * button and an invalidation still refetch). That bounds what a poll costs where each request is
 * audited (a platform principal's `X-Act-On-Tenant`, developer-spec.md §19.2 question 20) to one
 * request per interval. Not `maxPages`: with forward paging it drops the pages "load more" added.
 */
export function pollHeadWhileVisible(intervalMs: number = POLL_INTERVAL_MS): HeadPollOptions {
    const focus = refetchOnFocusOlderThan(intervalMs);
    return {
        refetchInterval: (query) => (loadedPages(query) > 1 ? false : intervalMs),
        refetchIntervalInBackground: false,
        refetchOnWindowFocus: (query) => (loadedPages(query) > 1 ? false : focus(query)),
    };
}

/**
 * For an SSE-backed query: while the stream is open no interval, and on focus only when stale (not
 * the poll's age rule, which would refetch every SSE row with a long `staleTime` on a tab switch);
 * otherwise `pollWhileVisible(intervalMs)`.
 */
export function streamFallbackPoll(streamOpen: boolean, intervalMs: number = POLL_INTERVAL_MS): PollOptions {
    return streamOpen
        ? { refetchInterval: false, refetchIntervalInBackground: false, refetchOnWindowFocus: true }
        : pollWhileVisible(intervalMs);
}

/** `streamFallbackPoll` over the tab's stream state (re-renders when it changes). */
export function useStreamFallbackPoll(intervalMs: number = POLL_INTERVAL_MS): PollOptions {
    return streamFallbackPoll(useRealtimeStatus() === "open", intervalMs);
}

/** What `pollFromLastAnswer` reads of a query: when the server last answered, with data or an error. */
export interface AnsweredQuery {
    state: { dataUpdatedAt: number; errorUpdatedAt: number };
}

export interface AnswerPollOptions {
    refetchInterval: (query: AnsweredQuery) => number;
    refetchIntervalInBackground: false;
    refetchOnWindowFocus: FocusRefetch;
}

/** The shortest timer `pollFromLastAnswer` sets (a hidden tab never spins on a due refetch). */
export const MIN_POLL_TIMER_MS = 1_000;

/**
 * `pollWhileVisible(ms)` for a query the stream also writes into (a `keepUpdatedAt` cache write of
 * lib/realtime/eventMap.ts: the vehicle-location batches, T54R). TanStack restarts a numeric
 * `refetchInterval` on every update of the query, `setQueryData` included, so a constant interval
 * longer than the stream's period would never fire (a 5-minute one under the 3-minute Cartrack
 * batches). This interval is the time left until the server's last answer (`dataUpdatedAt`, which
 * those writes keep, or the last error) is `ms` old, recomputed on each update: the query refetches
 * `ms` after its last answer whatever the stream wrote in between, only while the tab is visible,
 * and on focus once that answer is older than `ms`.
 */
export function pollFromLastAnswer(intervalMs: number = POLL_INTERVAL_MS): AnswerPollOptions {
    return {
        refetchInterval: (query) => {
            const last = Math.max(query.state.dataUpdatedAt, query.state.errorUpdatedAt);
            const left = last + intervalMs - Date.now();
            // Nothing answered yet, or the refetch is due (it is running, or the tab was hidden: the
            // focus rule refetches): one interval from now, recomputed when the answer lands.
            return last === 0 || left <= 0 ? intervalMs : Math.max(left, MIN_POLL_TIMER_MS);
        },
        refetchIntervalInBackground: false,
        refetchOnWindowFocus: refetchOnFocusOlderThan(intervalMs),
    };
}
