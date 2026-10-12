/**
 * A cache write that survives a fetch already running for its key (T50; developer-spec.md §10.8; Appendix E
 * §E.5.1 "Event -> cache", §E.6 `['chat', id, 'messages']`).
 *
 * `setQueryData` while a fetch runs is overwritten when that fetch lands: TanStack replaces the data with the
 * fetched value (an infinite query refetches its pages from the server, it does not merge), and the fetch's
 * server read may predate the change the write carries. For a key the stream keeps fresh (never stale, so
 * nothing refetches it later) the change would then be gone until a reload: a driver's message pushed on
 * `chat:{id}` while the room's image refetch runs, or the staff member's own send while the fallback poll
 * runs. `writeThroughFetch` writes at once and, when the key is fetching (or paused), applies the same
 * update again on the fetched value once that fetch succeeds. A failed or cancelled fetch keeps (or reverts
 * to) the data with the write in it, so nothing is re-applied then. A fetch started after the write read
 * the server after the change and needs nothing.
 *
 * The update must be idempotent and never move data back (an upsert by id, a forward-only read state): it
 * may run on a value that already holds its change.
 */
import type { Query, QueryClient, QueryKey } from "@tanstack/react-query";

export type CacheUpdate = (cached: unknown) => unknown;

export interface WriteThroughFetchOptions {
    /** Keep the query's `dataUpdatedAt` (a partial write, not a fresh answer; lib/realtime/polling.ts). */
    keepUpdatedAt?: boolean;
    /** Called with the fetched value right before the update is applied again on it. */
    beforeReapply?: (fetched: unknown) => void;
}

interface Pending {
    update: CacheUpdate;
    options: WriteThroughFetchOptions;
}

/** The updates waiting for each query's running fetch to land (one cache subscription per query). */
const waiting = new WeakMap<QueryClient, Map<Query, Pending[]>>();

function write(client: QueryClient, queryKey: QueryKey, update: CacheUpdate, keepUpdatedAt: boolean | undefined) {
    const state = client.getQueryState(queryKey);
    const kept = keepUpdatedAt && state && state.dataUpdatedAt > 0 ? { updatedAt: state.dataUpdatedAt } : undefined;
    client.setQueryData(queryKey, update, kept);
}

/** `setQueryData(queryKey, update)`, applied again on the result of a fetch that was running meanwhile. */
export function writeThroughFetch(client: QueryClient, queryKey: QueryKey, update: CacheUpdate, options: WriteThroughFetchOptions = {}): void {
    write(client, queryKey, update, options.keepUpdatedAt);
    const query = client.getQueryCache().find({ queryKey, exact: true });
    if (!query || query.state.fetchStatus === "idle") return;
    let byQuery = waiting.get(client);
    if (!byQuery) {
        byQuery = new Map();
        waiting.set(client, byQuery);
    }
    const queue = byQuery.get(query);
    if (queue) {
        queue.push({ update, options });
        return;
    }
    const updates: Pending[] = [{ update, options }];
    byQuery.set(query, updates);
    const done = () => {
        off();
        byQuery.delete(query);
    };
    const off = client.getQueryCache().subscribe((event) => {
        if (event.query !== query) return;
        if (event.type === "removed") return done();
        if (event.type !== "updated") return;
        const action = event.action;
        if (action.type === "success" && !action.manual) {
            // The fetch landed (another write's re-apply below is a manual success: not this one).
            done();
            for (const u of updates) {
                u.options.beforeReapply?.(query.state.data);
                write(client, query.queryKey, u.update, u.options.keepUpdatedAt);
            }
            return;
        }
        // Failed, or cancelled and reverted: the data TanStack kept holds the writes.
        if (query.state.fetchStatus === "idle" && (action.type === "error" || action.type === "setState")) done();
    });
}
