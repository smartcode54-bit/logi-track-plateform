"use client";

/**
 * RealtimeProvider (developer-spec.md §10.6, §10.8; T18 part): while a principal is signed in, the tab
 * holds one same-origin `EventSource('/api/go/v1/events')` (lib/realtime/sessionStream.ts). T18 uses
 * it for `session.revoked`, which replaces the `users/{uid}.forceLogoutAt` listener (Appendix E §E.5
 * row 20); TW5 adds explicit topics and the domain-event invalidation map on the same stream.
 *
 * The stream is recreated when the user or the active tenant changes and after every forced refresh
 * (`onClaimsRefreshed`): its implicit topics come from the access token. It closes on sign-out and on
 * a session end.
 */
import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { useAuth } from "@/context/auth";
import { refetchMe } from "@/features/auth/api/me";
import { createSessionStream } from "@/lib/realtime/sessionStream";
import { onSessionEnd } from "@/lib/sessionEnd";
import { onClaimsRefreshed } from "@/lib/sharedRefresh";

export function RealtimeProvider({ children }: { children: React.ReactNode }) {
    const client = useQueryClient();
    const auth = useAuth();
    const userId = auth?.me?.id;
    const tenantId = auth?.me?.tenant?.id ?? "";

    useEffect(() => {
        if (!userId || typeof EventSource === "undefined") return;
        const stream = createSessionStream({
            onResync: () => void client.invalidateQueries({ refetchType: "active" }),
            checkSession: () => refetchMe(client),
        });
        stream.open();
        const offClaims = onClaimsRefreshed(() => stream.reopen());
        const offEnd = onSessionEnd(() => stream.close());
        return () => {
            offClaims();
            offEnd();
            stream.close();
        };
    }, [client, userId, tenantId]);

    return <>{children}</>;
}
