/**
 * A user's live sessions (T54S; `GET /v1/users/{id}/sessions`, `DELETE /v1/users/{id}/sessions/{sid}`,
 * T19, Appendix B §B.2.5; Appendix E §E.2.4 `useUserSessions(userId)` on expand). Needs
 * `users:revoke_sessions`; Go refuses the caller's own user and a user it does not outrank. A platform
 * principal reads with `X-Act-On-Tenant: *` (the reach is in the key); revokes never carry the header.
 */
import { useQuery } from "@tanstack/react-query";

import { goFetch, goPath } from "@/lib/goFetch";
import { queryKeys } from "@/lib/queryKeys";
import { reachHeaders, type SecurityReach } from "./reach";

/** One live session (Go `iam.Session`). */
export interface UserSessionDTO {
    id: string;
    /** `web` or `mobile`. */
    platform: string;
    /** How it signed in (`pwd`, `google`, ...). */
    amr: string;
    installId: string | null;
    deviceLabel: string | null;
    appVersion: string | null;
    ip: string | null;
    userAgent: string | null;
    createdAt: string;
    lastSeenAt: string;
}

export function userSessionsKey(userId: string, reach: SecurityReach) {
    return [...queryKeys.users.sessions(userId), { reach }] as const;
}

/** `['users','sessions',id,{reach}]` (30 s / 5 min), loaded when its dialog opens. */
export function useUserSessions(userId: string | null, reach: SecurityReach | undefined) {
    return useQuery({
        queryKey: userSessionsKey(userId ?? "", reach ?? "tenant"),
        queryFn: ({ signal }) =>
            goFetch<UserSessionDTO[]>(goPath`/v1/users/${userId ?? ""}/sessions`, { signal, headers: reachHeaders(reach ?? "tenant") }),
        staleTime: 30_000,
        gcTime: 5 * 60_000,
        enabled: !!userId && reach !== undefined,
    });
}

/** `DELETE /v1/users/{id}/sessions/{sid}`: ends one session (`admin_revoke`). */
export function revokeUserSession(userId: string, sessionId: string): Promise<void> {
    return goFetch<void>(goPath`/v1/users/${userId}/sessions/${sessionId}`, { method: "DELETE" });
}
