/**
 * The state of the tab's realtime stream (Appendix E §E.5.1 "Backpressure"), kept outside React so a
 * query option can read it: the RealtimeProvider writes it, `useRealtimeStatus` re-renders on change.
 * While it is not `open` (signed out, opening, the api or BFF down, or a 429 past
 * `SSE_MAX_CONN_PER_USER`) a list whose disposition is SSE falls back to the 60 s poll
 * (`useStreamFallbackPoll`, lib/realtime/polling.ts).
 */
import { useSyncExternalStore } from "react";
import type { RealtimeStatus } from "./realtimeStream";

let current: RealtimeStatus = "idle";
const listeners = new Set<() => void>();

export function getRealtimeStatus(): RealtimeStatus {
    return current;
}

export function setRealtimeStatus(next: RealtimeStatus): void {
    if (next === current) return;
    current = next;
    for (const listener of [...listeners]) listener();
}

export function subscribeRealtimeStatus(listener: () => void): () => void {
    listeners.add(listener);
    return () => {
        listeners.delete(listener);
    };
}

/** The stream's state; `idle` on the server. */
export function useRealtimeStatus(): RealtimeStatus {
    return useSyncExternalStore(subscribeRealtimeStatus, getRealtimeStatus, () => "idle");
}
