/**
 * Broadcasts over Go (T50, issue #70; T48 routes, Appendix B §B.2.16; Appendix E §E.2.1 `?view=broadcast`,
 * §E.6, §E.10 row 15). Rendered while the `comms` web flag is on; replaces the composer's read of every
 * driver (`BroadcastComposer.tsx:58`, used to build the recipient lists in the browser), the history's
 * silent cap of 50 (`:140-144`), the `sendBroadcast` callable and the browser's `deleteDoc`:
 *
 * - `['broadcasts']`: `GET /v1/broadcasts?limit&cursor` keyset pages, newest first, read counts from
 *   `broadcast_reads`, voided ones left out (1 min / 5 min);
 * - `POST /v1/broadcasts`: a recipient group (`all_driver`, `own_fleet`, `subcontractor`; `all_tenants` for a
 *   steward, R60) that Go resolves under the caller's reach, or `recipientUserIds` (the logins of drivers
 *   picked through `GET /v1/drivers?fields=minimal&q`); Go refuses a selected send while installed 3.x apps
 *   may be the recipients (`422 recipientUserIds unsupported_until_p7b`, before P7b);
 * - `DELETE /v1/broadcasts/{id}`: the soft delete (`voided_at`), 204; the projection removes it from the
 *   3.x apps.
 */
import { infiniteQueryOptions, useMutation } from "@tanstack/react-query";

import { goFetch, goPath } from "@/lib/goFetch";
import { goInfiniteQueryFn, goNextPageParam, type GoPage } from "@/lib/goQuery";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";

export const BROADCASTS_PATH = "/v1/broadcasts";
export const BROADCAST_PAGE_SIZE = 50;
/** Go's bounds of a send (internal/broadcasts). */
export const MAX_BROADCAST_TITLE = 300;
export const MAX_BROADCAST_MESSAGE = 5000;

/** The groups a send names; `selected` is implied by `recipientUserIds`. */
export const BROADCAST_GROUPS = ["all_driver", "own_fleet", "subcontractor", "all_tenants"] as const;
export type BroadcastGroup = (typeof BROADCAST_GROUPS)[number];

/** `GET /v1/broadcasts` item (internal/broadcasts `Broadcast`). */
export interface BroadcastDTO {
    id: string;
    /** Null for a platform-wide broadcast. */
    tenantId: string | null;
    legacyDocId: string | null;
    createdByUserId: string | null;
    createdByName: string;
    title: string | null;
    messageText: string;
    /** A group, or `selected`. */
    recipientGroup: string;
    recipientCount: number;
    readCount: number;
    sentAt: string;
    voidedAt: string | null;
}

/** `['broadcasts']`: the history, keyset pages of 50. */
export const broadcastsQuery = infiniteQueryOptions({
    queryKey: queryKeys.broadcasts(),
    queryFn: goInfiniteQueryFn<BroadcastDTO>(BROADCASTS_PATH, { query: { limit: BROADCAST_PAGE_SIZE } }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: goNextPageParam,
    ...QUERY_POLICY.list,
});

export function broadcastRows(pages: readonly GoPage<BroadcastDTO>[] | undefined): BroadcastDTO[] {
    const seen = new Set<string>();
    return (pages ?? []).flatMap((p) => p.data ?? []).filter((b) => (seen.has(b.id) ? false : (seen.add(b.id), true)));
}

export type BroadcastRecipients = { group: BroadcastGroup } | { userIds: string[] };

export interface SendBroadcastInput {
    recipients: BroadcastRecipients;
    title: string;
    messageText: string;
}

export function broadcastBody({ recipients, title, messageText }: SendBroadcastInput): Record<string, unknown> {
    const to = "group" in recipients ? { recipientGroup: recipients.group } : { recipientUserIds: recipients.userIds };
    return { ...to, title: title.trim(), messageText: messageText.trim() };
}

/** `POST /v1/broadcasts`: 201 with the broadcast (its `recipientCount` the audience Go resolved). */
export async function sendBroadcast(input: SendBroadcastInput): Promise<BroadcastDTO> {
    return goFetch<BroadcastDTO>(BROADCASTS_PATH, { method: "POST", body: broadcastBody(input) });
}

/** `DELETE /v1/broadcasts/{id}`: voided (204), gone from the history and the 3.x apps. */
export async function voidBroadcast(id: string): Promise<void> {
    await goFetch<void>(goPath`/v1/broadcasts/${id}`, { method: "DELETE" });
}

export const BROADCAST_WRITE_INVALIDATES = [queryKeys.broadcasts()] as const;

export function useSendBroadcast() {
    return useMutation({ mutationFn: sendBroadcast, meta: { invalidates: BROADCAST_WRITE_INVALIDATES } });
}

export function useVoidBroadcast() {
    return useMutation({ mutationFn: voidBroadcast, meta: { invalidates: BROADCAST_WRITE_INVALIDATES } });
}
