/**
 * Explicit realtime topics (developer-spec.md §10.8; Appendix B §B.4.1, §B.4.2; Appendix E §E.5.1): a page
 * that needs a topic outside the implicit set the token gives (today only `chat:{id}`, the open chat room,
 * T50) registers it with `useRealtimeTopic`. The RealtimeProvider follows the registry at most once per
 * `TOPICS_RECREATE_MS` (`RealtimeStream.setTopics`): a topic the tab's one stream lacks recreates it with
 * `?topics=`, resuming from the last id it saw, so Go replays what the new topic missed (one global
 * sequence, R52); a topic only removed stays on the stream until it reopens for another reason.
 *
 * Registrations are counted per topic, so two observers of one room keep it until both unmount; at most
 * `MAX_EXPLICIT_TOPICS` go on the stream (Go refuses more, `422 topics too_many`).
 */
import { useEffect } from "react";
import { MAX_EXPLICIT_TOPICS } from "./realtimeStream";

/** Go's cap on `?topics=` (internal/sse `MaxExplicitTopics`). */
export { MAX_EXPLICIT_TOPICS };
/** The provider recreates the stream at most this often when the registry changes (§10.8). */
export const TOPICS_RECREATE_MS = 500;

const counts = new Map<string, number>();
const listeners = new Set<() => void>();

function notify(): void {
    for (const listener of [...listeners]) listener();
}

/** The `chat:{id}` topic of one chat (its full messages and read state, Appendix B §B.4.2). */
export function chatTopic(chatId: string): string {
    return `chat:${chatId}`;
}

/** The registered topics, sorted, at most `MAX_EXPLICIT_TOPICS`. */
export function realtimeTopics(): string[] {
    return [...counts.keys()].sort().slice(0, MAX_EXPLICIT_TOPICS);
}

/** Registers `topic`; the returned function removes this registration (once). */
export function addRealtimeTopic(topic: string): () => void {
    const before = counts.get(topic) ?? 0;
    counts.set(topic, before + 1);
    if (before === 0) notify();
    let removed = false;
    return () => {
        if (removed) return;
        removed = true;
        const n = (counts.get(topic) ?? 1) - 1;
        if (n > 0) {
            counts.set(topic, n);
            return;
        }
        counts.delete(topic);
        notify();
    };
}

/** Calls `listener` whenever the set of registered topics changes. */
export function subscribeRealtimeTopics(listener: () => void): () => void {
    listeners.add(listener);
    return () => {
        listeners.delete(listener);
    };
}

/** Keeps `topic` on the tab's stream while the component is mounted (nothing for an empty topic). */
export function useRealtimeTopic(topic: string | null | undefined): void {
    useEffect(() => (topic ? addRealtimeTopic(topic) : undefined), [topic]);
}

/** Tests: forget every registration. */
export function resetRealtimeTopicsForTests(): void {
    counts.clear();
    notify();
}
