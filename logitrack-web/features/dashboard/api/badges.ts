/**
 * `['badges']`: the counters of the shell and the dashboard (`GET /v1/badges`, Appendix B, W8;
 * developer-spec.md §10.7, §10.9; Appendix E §E.5 rows 19, 21, 22).
 *
 * Until `GET /v1/badges` ships (T53, P6) the Firestore source answers the waitlist counter with one
 * `getCountFromServer` per fetch instead of the sidebar's listener on the whole `waitlist` collection
 * (`components/app-sidebar.tsx:63-74`): polled every 60 s while the tab is visible, 30 s stale. The
 * dashboard's chat and expense widgets keep their listeners until P6. Each counter is present only for
 * a principal holding its capability, as Go will gate it.
 */
import { queryOptions, type QueryFunctionContext } from "@tanstack/react-query";
import { collection, getCountFromServer } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { goQueryFn } from "@/lib/goQuery";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import { domainQueryFn } from "@/features/platform/api/webFlags";
import { hasCapability, meQueryOptions } from "@/features/auth/api/me";

export const BADGES_PATH = "/v1/badges";
/** Poll interval while the tab is visible (Appendix E §E.5 row 19). */
export const BADGES_POLL_MS = 60_000;

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

/** P0 source: one count query for the waitlist, no listener. */
export async function fetchBadgesFromFirestore({ client }: Pick<QueryFunctionContext, "client">): Promise<BadgesDTO> {
    const me = await client.ensureQueryData(meQueryOptions);
    const badges: BadgesDTO = {};
    if (hasCapability(me, "waitlist:view")) {
        const snapshot = await getCountFromServer(collection(db, COLLECTIONS.WAITLIST));
        badges.waitlist = snapshot.data().count;
    }
    return badges;
}

export const badgesQueryOptions = queryOptions({
    queryKey: queryKeys.badges(),
    queryFn: domainQueryFn<BadgesDTO>("dashboard", { firestore: fetchBadgesFromFirestore, go: goQueryFn<BadgesDTO>(BADGES_PATH) }),
    ...QUERY_POLICY.live,
    refetchInterval: BADGES_POLL_MS,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: true,
});
