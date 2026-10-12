/**
 * `['badges']`: the counters of the shell and the dashboard (`GET /v1/badges`, Appendix B §B.2.19, W8;
 * developer-spec.md §10.7, §10.9; Appendix E §E.5 rows 19, 21, 22).
 *
 * With the `dashboard` web flag on, `GET /v1/badges` (T53) answers every counter the principal holds a
 * capability for: the sidebar's waitlist count, the dashboard's pending-expense and chat widgets. With
 * the flag off the Firestore source (`badges.firestore.ts`, loaded lazily so the shell's chunk carries
 * no Firestore code) answers the waitlist counter with one `getCountFromServer` per fetch instead of the
 * sidebar's old listener on the whole `waitlist` collection (`components/app-sidebar.tsx:63-74`).
 * Polled every 60 s while the tab is visible, 30 s stale; SSE `expense.*`, `chat.*` and
 * `waitlist.created` make it stale (lib/realtime/eventMap.ts), as do the waitlist delete and the
 * expense status mutations.
 */
import { queryOptions, type QueryFunctionContext } from "@tanstack/react-query";
import { goQueryFn } from "@/lib/goQuery";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import { POLL_INTERVAL_MS, pollWhileVisible } from "@/lib/realtime/polling";
import { domainQueryFn } from "@/features/platform/api/webFlags";

export const BADGES_PATH = "/v1/badges";
/** Poll interval while the tab is visible (Appendix E §E.5 row 19). */
export const BADGES_POLL_MS = POLL_INTERVAL_MS;

/** The body of `GET /v1/badges`: each counter only when the principal holds its capability. */
export interface BadgesDTO {
    /** `waitlist:view` */
    waitlist?: number;
    /** `accounting:audit_expense` */
    pendingExpenses?: number;
    /** `chat:view` */
    unassignedChats?: number;
    /** `chat:view` */
    urgentChats?: number;
}

/** The flag-off source (TW4 interim), imported only when a fetch needs it. */
export async function fetchBadgesFromFirestore(context: Pick<QueryFunctionContext, "client">): Promise<BadgesDTO> {
    const { fetchBadgesFromFirestore: fetchFromFirestore } = await import("./badges.firestore");
    return fetchFromFirestore(context);
}

export const badgesQueryOptions = queryOptions({
    queryKey: queryKeys.badges(),
    queryFn: domainQueryFn<BadgesDTO>("dashboard", { firestore: fetchBadgesFromFirestore, go: goQueryFn<BadgesDTO>(BADGES_PATH) }),
    ...QUERY_POLICY.live,
    ...pollWhileVisible(BADGES_POLL_MS),
});
