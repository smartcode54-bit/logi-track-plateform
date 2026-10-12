"use client";

/**
 * RealtimeProvider (developer-spec.md §10.6, §10.8; Appendix E §E.5.1; T18, TW5): once `['me']` holds
 * a principal, the tab keeps one same-origin `EventSource('/api/go/v1/events')`
 * (lib/realtime/sessionStream.ts) and maps its events onto the query cache:
 *
 * - `session.revoked` ends or refreshes the session (T18, Appendix E §E.5 row 20);
 * - the events of lib/realtime/eventMap.ts invalidate or write their keys (lib/realtime/invalidator.ts);
 * - `resync` refetches the active queries;
 * - the stream's state feeds lib/realtime/status.ts (the SSE fallback poll).
 *
 * A network drop needs no reload: the browser reconnects by itself and replays from its
 * `Last-Event-ID`; a stream that closed is reopened with `?lastEventId=` after a refresh and a
 * backoff, at once when the browser reports it is back online or the tab becomes visible.
 *
 * The stream is recreated when the user or the active tenant changes and after every forced refresh
 * (`onClaimsRefreshed`): its implicit topics come from the access token. It closes on sign-out and on
 * a session end. Explicit topics (`useRealtimeTopic`, lib/realtime/topics.ts: the open chat room's
 * `chat:{id}`, T50) recreate it with `?topics=` at most once per 500 ms, from the last id seen.
 */
import { useEffect } from "react";
import { focusManager, useQueryClient, type QueryClient } from "@tanstack/react-query";

import { refetchMe, type MeDTO } from "@/features/auth/api/me";
import { useMe } from "@/features/auth/api/useMe";
import { onSessionEnd } from "@/lib/sessionEnd";
import { onClaimsRefreshed } from "@/lib/sharedRefresh";
import { REALTIME_EVENTS } from "./eventMap";
import { RealtimeInvalidator } from "./invalidator";
import type { EventSourceLike, RealtimeStream } from "./realtimeStream";
import { createSessionStream } from "./sessionStream";
import { setRealtimeStatus } from "./status";
import { realtimeTopics, subscribeRealtimeTopics, TOPICS_RECREATE_MS } from "./topics";

export interface RealtimeOptions {
    /** Replaced in tests (defaults to the browser's `EventSource`). */
    createEventSource?: (url: string) => EventSourceLike;
}

/**
 * Holds the tab's stream for the principal `userId` in `tenantId` (none while `userId` is empty) and
 * applies its events to `client`.
 */
export function useRealtimeInvalidation(client: QueryClient, userId: string | undefined, tenantId: string, options: RealtimeOptions = {}) {
    const { createEventSource } = options;
    useEffect(() => {
        if (!userId || (!createEventSource && typeof EventSource === "undefined")) return;
        let current: RealtimeStream | null = null;
        // The event map reads which explicit topics the open connection carries (chat:{id} or its fallback).
        const cache = new RealtimeInvalidator({ client, hasTopic: (topic) => current?.hasTopic(topic) ?? false });
        const stream = createSessionStream({
            onResync: () => cache.resync(),
            checkSession: () => refetchMe(client),
            events: REALTIME_EVENTS,
            onEvent: (frame) => cache.apply(frame),
            onStatus: setRealtimeStatus,
            createEventSource,
        });
        current = stream;
        stream.setTopics(realtimeTopics());
        stream.open();
        // A changed set of explicit topics recreates the stream at most once per TOPICS_RECREATE_MS.
        let topicsTimer: ReturnType<typeof setTimeout> | null = null;
        const offTopics = subscribeRealtimeTopics(() => {
            if (topicsTimer !== null) return;
            topicsTimer = setTimeout(() => {
                topicsTimer = null;
                stream.setTopics(realtimeTopics());
            }, TOPICS_RECREATE_MS);
        });
        const offClaims = onClaimsRefreshed(() => stream.reopen());
        const offEnd = onSessionEnd(() => {
            cache.dispose();
            stream.close();
        });
        const offFocus = focusManager.subscribe((focused) => {
            if (!focused) return;
            cache.flushDeferred();
            stream.resume();
        });
        const onOnline = () => stream.resume();
        window.addEventListener("online", onOnline);
        return () => {
            if (topicsTimer !== null) clearTimeout(topicsTimer);
            offTopics();
            window.removeEventListener("online", onOnline);
            offFocus();
            offClaims();
            offEnd();
            cache.dispose();
            stream.close();
        };
    }, [client, userId, tenantId, createEventSource]);
}

const streamPrincipal = (me: MeDTO | null) => (me ? { id: me.id, tenantId: me.tenant?.id ?? "" } : null);

/** Opens the tab's stream once `['me']` holds a principal (mounted in app/providers.tsx). */
export function RealtimeProvider({ children, createEventSource }: { children: React.ReactNode } & RealtimeOptions) {
    const client = useQueryClient();
    const { data: principal } = useMe(streamPrincipal);
    useRealtimeInvalidation(client, principal?.id, principal?.tenantId ?? "", { createEventSource });
    return <>{children}</>;
}
