/**
 * Applies the realtime event map to the tab's query cache (developer-spec.md §10.8; Appendix E
 * §E.5.1 "Event -> cache" and "Backpressure"):
 *
 * - writes (`setQueryData`) apply at once;
 * - invalidations are coalesced per key: the first event for a quiet key opens a window
 *   (`windowMs`: 500 ms, 2 s for the monitor and income keys) and every event inside it joins the one
 *   invalidation at its end, so a burst costs one refetch per window and a steady stream of events
 *   still refetches once per window instead of never (a trailing debounce would starve);
 * - `invalidateQueries` marks every query of the key stale and refetches only the observed ones
 *   (an unobserved list is fetched on its next use);
 * - a hidden tab defers the refetch: the queries are marked stale at once and refetched when the tab
 *   is visible again (`flushDeferred`, from the provider's focus listener), joining the focus refetch
 *   TanStack starts itself;
 * - `resync` (the replay window was lost, or a stream reopened without a position) invalidates every
 *   query and refetches the active ones, on the same visibility terms.
 */
import { focusManager, hashKey, type QueryClient, type QueryKey } from "@tanstack/react-query";
import { effectsOf } from "./eventMap";
import type { RealtimeFrame } from "./realtimeStream";

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
    /** Keys invalidated while the tab was hidden, refetched when it is visible again. */
    private readonly deferred = new Map<string, QueryKey>();
    private deferredAll = false;
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
        for (const w of effects.write) this.client.setQueryData(w.queryKey, w.update);
        for (const i of effects.invalidate) this.schedule(i.queryKey, i.windowMs);
    }

    /** `event: resync`, or a stream reopened without a position: everything may have changed. */
    resync(): void {
        if (this.disposed) return;
        if (!this.isVisible()) {
            this.deferredAll = true;
            this.deferred.clear();
            void this.client.invalidateQueries({ refetchType: "none" });
            return;
        }
        void this.client.invalidateQueries({ refetchType: "active" });
    }

    /** The tab is visible again: the active queries made stale while it was hidden refetch now. */
    flushDeferred(): void {
        if (this.disposed) return;
        // cancelRefetch: false joins the focus refetch TanStack may have started for the same query.
        if (this.deferredAll) {
            this.deferredAll = false;
            void this.client.refetchQueries({ type: "active", stale: true }, { cancelRefetch: false });
        }
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
        this.deferred.clear();
        this.deferredAll = false;
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
        if (!this.isVisible()) {
            if (!this.deferredAll) this.deferred.set(hashKey(queryKey), queryKey);
            void this.client.invalidateQueries({ queryKey, refetchType: "none" });
            return;
        }
        // The default cancelRefetch restarts a fetch already in flight: it may predate the change.
        void this.client.invalidateQueries({ queryKey });
    }
}
