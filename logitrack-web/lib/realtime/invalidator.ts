/**
 * Applies the realtime event map to the tab's query cache (developer-spec.md §10.8; Appendix E
 * §E.5.1 "Event -> cache" and "Backpressure"):
 *
 * - writes (`setQueryData`) apply at once; a partial one (`keepUpdatedAt`) keeps the query's
 *   `dataUpdatedAt`, and one whose payload names what the cache lacks (`refetchWhen`) also makes its
 *   key stale through the window below;
 * - invalidations are coalesced per key: the first event for a quiet key opens a window
 *   (`windowMs`: 500 ms, 2 s for the monitor and income keys) and every event inside it joins the one
 *   flush at its end, so a burst costs one refetch per window (a trailing debounce would never fire
 *   under a steady stream);
 * - a flush marks every query of the key stale and refetches the observed ones (an unobserved list is
 *   fetched on its next use);
 * - at most one flush refetch per key is in flight: a window that closes while it runs only marks the
 *   key stale, and one trailing flush follows when it lands. Restarting it instead would starve a key
 *   whose fetch is slower than the window under a steady stream (no refetch would ever land); this way
 *   the data advances once per fetch and the last event is never lost;
 * - a fetch already running when a key is flushed (a focus, poll or mutation refetch) may predate the
 *   change, and landing it would make the query fresh without it. When the tab is visible, TanStack
 *   restarts one that has data (`cancelRefetch`); one it joins instead (a first load), and any running
 *   fetch in a hidden tab, is awaited and its query is made stale again (and refetched) once it lands;
 * - a hidden tab starts no fetch: the queries are marked stale and refetched when the tab is visible
 *   again (`flushDeferred`, from the provider's focus listener), joining the focus refetch TanStack
 *   starts itself;
 * - `resync` (the replay window was lost, or a stream reopened without a position) is the flush of
 *   every query (the root key), on the same terms.
 */
import { focusManager, hashKey, type Query, type QueryClient, type QueryKey } from "@tanstack/react-query";
import { effectsOf, LIST_WINDOW_MS } from "./eventMap";
import type { RealtimeFrame } from "./realtimeStream";

/** The prefix of every query: what `resync` flushes. */
const ROOT_KEY: QueryKey = [];

export interface InvalidatorDeps {
    client: QueryClient;
    /** Whether the tab is visible (TanStack's focusManager by default). */
    isVisible?: () => boolean;
    setTimer?: (fn: () => void, ms: number) => unknown;
    clearTimer?: (handle: unknown) => void;
}

export class RealtimeInvalidator {
    private readonly client: QueryClient;
    private readonly isVisible: () => boolean;
    private readonly setTimer: (fn: () => void, ms: number) => unknown;
    private readonly clearTimer: (handle: unknown) => void;
    /** Open coalescing windows by key hash. */
    private readonly windows = new Map<string, unknown>();
    /** Keys whose flush refetch (or awaited running fetch) has not landed yet. */
    private readonly inflight = new Set<string>();
    /** Keys flushed again while their refetch ran: one trailing flush each when it lands. */
    private readonly dirty = new Map<string, QueryKey>();
    /** Keys (and single queries) made stale while the tab was hidden, refetched when it is visible again. */
    private readonly deferred = new Map<string, QueryKey>();
    private disposed = false;

    constructor(deps: InvalidatorDeps) {
        this.client = deps.client;
        this.isVisible = deps.isVisible ?? (() => focusManager.isFocused());
        this.setTimer = deps.setTimer ?? ((fn, ms) => setTimeout(fn, ms));
        this.clearTimer = deps.clearTimer ?? ((h) => clearTimeout(h as ReturnType<typeof setTimeout>));
    }

    /** One frame of the stream. */
    apply(frame: Pick<RealtimeFrame, "event" | "data">): void {
        if (this.disposed) return;
        const effects = effectsOf(frame);
        for (const w of effects.write) {
            const state = this.client.getQueryState(w.queryKey);
            if (w.refetchWhen?.(state?.data)) this.schedule(w.queryKey, LIST_WINDOW_MS);
            // A partial write keeps the time of the server's last answer (a `pollFromLastAnswer` key).
            const kept = w.keepUpdatedAt && state && state.dataUpdatedAt > 0 ? { updatedAt: state.dataUpdatedAt } : undefined;
            this.client.setQueryData(w.queryKey, w.update, kept);
        }
        for (const i of effects.invalidate) this.schedule(i.queryKey, i.windowMs);
    }

    /** `event: resync`, or a stream reopened without a position: everything may have changed. */
    resync(): void {
        this.flush(ROOT_KEY);
    }

    /** The tab is visible again: the active queries made stale while it was hidden refetch now. */
    flushDeferred(): void {
        if (this.disposed) return;
        // cancelRefetch: false joins the focus refetch TanStack may have started for the same query
        // (a fetch that predates a change is awaited by the flush, which makes its query stale again).
        for (const queryKey of this.deferred.values()) {
            void this.client.refetchQueries({ queryKey, type: "active", stale: true }, { cancelRefetch: false });
        }
        this.deferred.clear();
    }

    /** Drops the open windows (the stream closed: sign-out, another principal, unmount). */
    dispose(): void {
        this.disposed = true;
        for (const handle of this.windows.values()) this.clearTimer(handle);
        this.windows.clear();
        this.inflight.clear();
        this.dirty.clear();
        this.deferred.clear();
    }

    private schedule(queryKey: QueryKey, windowMs: number): void {
        const hash = hashKey(queryKey);
        if (this.windows.has(hash)) return;
        this.windows.set(
            hash,
            this.setTimer(() => {
                this.windows.delete(hash);
                this.flush(queryKey);
            }, windowMs)
        );
    }

    private flush(queryKey: QueryKey): void {
        if (this.disposed) return;
        const hash = hashKey(queryKey);
        if (this.inflight.has(hash)) {
            // Let the running refetch land (restarting it again and again would starve the key);
            // the trailing flush then covers the events it may predate.
            this.dirty.set(hash, queryKey);
            void this.client.invalidateQueries({ queryKey, refetchType: "none" });
            return;
        }
        const running = this.client.getQueryCache().findAll({ queryKey, type: "active", fetchStatus: "fetching" });
        let refetched: Promise<void>;
        let awaited: Query[];
        if (this.isVisible()) {
            // The default cancelRefetch restarts a running fetch that has data; TanStack joins a first load.
            refetched = this.client.invalidateQueries({ queryKey });
            awaited = running.filter((query) => query.state.data === undefined);
        } else {
            this.deferred.set(hash, queryKey);
            refetched = this.client.invalidateQueries({ queryKey, refetchType: "none" });
            awaited = running;
        }
        this.track(hash, queryKey, Promise.allSettled([refetched, ...awaited.map((query) => query.promise)]), awaited);
    }

    private track(hash: string, queryKey: QueryKey, landed: Promise<unknown>, awaited: readonly Query[]): void {
        this.inflight.add(hash);
        void landed.then(() => this.settle(hash, queryKey, awaited));
    }

    private settle(hash: string, queryKey: QueryKey, awaited: readonly Query[]): void {
        if (this.disposed) return;
        this.inflight.delete(hash);
        const again = this.dirty.get(hash);
        if (again !== undefined) {
            this.dirty.delete(hash);
            this.flush(again); // the whole key again, the awaited queries included
            return;
        }
        if (awaited.length === 0) return;
        // The awaited fetches started before the flush: their data may predate the change.
        const queries = new Set<Query>(awaited);
        const predicate = (query: Query) => queries.has(query);
        if (!this.isVisible()) {
            void this.client.invalidateQueries({ predicate, refetchType: "none" });
            for (const query of awaited) this.deferred.set(query.queryHash, query.queryKey);
            return;
        }
        // cancelRefetch: false joins a fetch that started after they landed (it follows the change).
        this.track(hash, queryKey, this.client.invalidateQueries({ predicate, refetchType: "active" }, { cancelRefetch: false }), []);
    }
}
