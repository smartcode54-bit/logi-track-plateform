/**
 * `['badges']`: the counters of the shell and the dashboard (`GET /v1/badges`, Appendix B §B.2.19, W8;
 * developer-spec.md §10.7, §10.9; Appendix E §E.5 rows 19, 21, 22).
 *
 * `GET /v1/badges` (T53) answers every counter the principal holds a capability for: the sidebar's
 * waitlist count, the dashboard's pending-expense and chat widgets (TW7 removed the Firestore source
 * that served the waitlist count while the `dashboard` web flag was off). Polled every 60 s while the
 * tab is visible, 30 s stale; SSE `expense.*`, `chat.*` and `waitlist.created` make it stale
 * (lib/realtime/eventMap.ts), as do the waitlist delete and the expense status mutations.
 */
import { queryOptions } from "@tanstack/react-query";
import { goQueryFn } from "@/lib/goQuery";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import { POLL_INTERVAL_MS, pollWhileVisible } from "@/lib/realtime/polling";

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

export const badgesQueryOptions = queryOptions({
    queryKey: queryKeys.badges(),
    queryFn: goQueryFn<BadgesDTO>(BADGES_PATH),
    ...QUERY_POLICY.live,
    ...pollWhileVisible(BADGES_POLL_MS),
});
