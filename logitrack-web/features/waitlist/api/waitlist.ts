/**
 * The waitlist over Go (T54R, issue #74; T53 routes, Appendix B §B.2.19; Appendix E §E.2.4, §E.5 row 18,
 * §E.6): `GET /v1/waitlist` (keyset, newest first, `meta.total` = every entry) and
 * `DELETE /v1/waitlist/{id}`, both `waitlist:view` (a steward key, R60). Replaces the listener on the
 * whole `waitlist` collection (`app/app/waitlist/page.tsx:24-40`) and its Firestore delete.
 *
 * `['waitlist']` (60 s / 5 min) polls every 60 s while the tab is visible and only while it holds its
 * first page (TanStack re-reads every loaded page on a refetch; after "load more" the Refresh button
 * updates the list); `waitlist.created` on `platform:security` makes it stale for a platform principal
 * (lib/realtime/eventMap.ts). A delete makes `['waitlist']` and the sidebar's `['badges']` stale.
 *
 * New entries come from the anonymous landing forms through the BFF (`POST /api/forms/waitlist`, R44,
 * R77); this module only reads and deletes.
 */
import { useInfiniteQuery, useMutation } from "@tanstack/react-query";

import { goFetch, goPath, isApiError } from "@/lib/goFetch";
import { goInfiniteQueryFn, goNextPageParam, type GoPage } from "@/lib/goQuery";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import { POLL_INTERVAL_MS, pollHeadWhileVisible } from "@/lib/realtime/polling";

export const WAITLIST_PATH = "/v1/waitlist";
export const WAITLIST_PAGE_SIZE = 50;
/** Appendix E §E.5 row 18. */
export const WAITLIST_POLL_MS = POLL_INTERVAL_MS;

/** One entry of `GET /v1/waitlist` (Go `forms.Entry`). */
export interface WaitlistEntryDTO {
    id: string;
    email: string;
    name: string;
    countryCode: string;
    phone: string | null;
    createdAt: string;
}

/** `['waitlist']`, keyset pages of 50, newest first. */
export function useWaitlist(enabled = true) {
    return useInfiniteQuery({
        queryKey: queryKeys.waitlist(),
        queryFn: goInfiniteQueryFn<WaitlistEntryDTO>(WAITLIST_PATH, { query: { limit: WAITLIST_PAGE_SIZE } }),
        initialPageParam: undefined as string | undefined,
        getNextPageParam: goNextPageParam,
        ...QUERY_POLICY.list,
        ...pollHeadWhileVisible(WAITLIST_POLL_MS),
        enabled,
    });
}

/** The entries of every loaded page, in order. */
export function waitlistEntries(pages: readonly GoPage<WaitlistEntryDTO>[] | undefined): WaitlistEntryDTO[] {
    return (pages ?? []).flatMap((p) => p.data ?? []);
}

/** `meta.total` of the newest page: every entry, not only the loaded ones. */
export function waitlistTotal(pages: readonly GoPage<WaitlistEntryDTO>[] | undefined): number | undefined {
    const total = pages?.[0]?.meta?.total;
    return typeof total === "number" ? total : undefined;
}

/** The keys a removed entry makes stale: the list and the sidebar count. */
export const WAITLIST_DELETE_INVALIDATES = [queryKeys.waitlist(), queryKeys.badges()] as const;

/**
 * `DELETE /v1/waitlist/{id}`: 204, or 404 for an entry someone else removed first, which ends the same
 * way (the entry is gone, the list and the count refetch).
 */
export async function deleteWaitlistEntry({ id }: { id: string }): Promise<void> {
    try {
        await goFetch<void>(goPath`/v1/waitlist/${id}`, { method: "DELETE" });
    } catch (error) {
        if (isApiError(error) && error.status === 404) return;
        throw error;
    }
}

export function useDeleteWaitlistEntry() {
    return useMutation({ mutationFn: deleteWaitlistEntry, meta: { invalidates: WAITLIST_DELETE_INVALIDATES } });
}
