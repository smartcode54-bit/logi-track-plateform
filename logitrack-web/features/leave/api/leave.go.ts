/**
 * Leave requests over Go (T46, issue #66; T45 routes, Appendix B §B.2.17; Appendix E §E.2.2, §E.5 row 10,
 * §E.6). Replaces the listener on the whole `leave_requests` collection (`leave-requests/page.tsx:61-66`)
 * and the browser status write (`:117-129`):
 *
 * - `['leave', {status, driverId}]`: `GET /v1/leave-requests` keyset pages of 50, newest first, filters on the
 *   server, polled every 60 s while the tab is visible (row 10; `leave.*` on `tenant:{tid}:hr` also makes it
 *   stale while the stream is open); the first page's `meta.statusCounts` feeds the summary cards;
 * - `POST /v1/leave-requests/{id}/approve|reject|cancel`: approve and reject from `pending`, cancel from
 *   `approved`; reject needs `{reason}`. While the installed 3.x apps still write leave requests in
 *   Firestore the decision is written back there first and answers `202 {pending: true, ...}` until the
 *   mirror acknowledges it (main spec §13.8 as built T45): the page says so and keeps polling.
 *
 * Statuses are Go's lower_snake vocabulary (R65); dates are Bangkok days.
 */
import { useInfiniteQuery, useMutation } from "@tanstack/react-query";

import { goFetch, goPath } from "@/lib/goFetch";
import { goInfiniteQueryFn, goNextPageParam, type GoPage } from "@/lib/goQuery";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import { pollWhileVisible } from "@/lib/realtime/polling";

export const LEAVE_PATH = "/v1/leave-requests";
export const LEAVE_PAGE_SIZE = 50;

export const LEAVE_STATUSES = ["pending", "approved", "rejected", "cancelled"] as const;
export type LeaveStatus = (typeof LEAVE_STATUSES)[number];

/** An evidence file: its object key and a 15-minute presigned GET (`url` null when it cannot be signed). */
export interface LeaveAttachmentDTO {
    key: string | null;
    url: string | null;
}

/** One request (Go `hr.LeaveRequest`). */
export interface LeaveRequestDTO {
    id: string;
    tenantId: string;
    driverId: string | null;
    driverName: string | null;
    type: "sick" | "business" | string;
    status: LeaveStatus;
    /** Bangkok days `yyyy-MM-dd`, inclusive. */
    startDate: string;
    endDate: string;
    days: number;
    reason: string;
    approverUserId: string | null;
    decidedAt: string | null;
    rejectionReason: string | null;
    attachments: LeaveAttachmentDTO[];
    createdAt: string;
    updatedAt: string;
}

export type LeaveFilters = {
    status?: LeaveStatus;
    driverId?: string;
};

export function leaveParams(f: LeaveFilters): LeaveFilters {
    const out: LeaveFilters = {};
    if (f.status) out.status = f.status;
    if (f.driverId) out.driverId = f.driverId;
    return out;
}

/** `['leave', params]`: keyset pages of 50, polled every 60 s while visible (Appendix E §E.5 row 10). */
export function useLeaveRequests(filters: LeaveFilters) {
    const params = leaveParams(filters);
    return useInfiniteQuery({
        queryKey: queryKeys.leave(params),
        queryFn: goInfiniteQueryFn<LeaveRequestDTO>(LEAVE_PATH, { query: { ...params, limit: LEAVE_PAGE_SIZE } }),
        initialPageParam: undefined as string | undefined,
        getNextPageParam: goNextPageParam,
        ...QUERY_POLICY.list,
        ...pollWhileVisible(),
    });
}

export function leaveRows(pages: readonly GoPage<LeaveRequestDTO>[] | undefined): LeaveRequestDTO[] {
    return (pages ?? []).flatMap((p) => p.data ?? []);
}

/** `meta.statusCounts` of the first page (every request matching the driver filter), or undefined. */
export function leaveStatusCounts(pages: readonly GoPage<LeaveRequestDTO>[] | undefined): Record<LeaveStatus, number> | undefined {
    const counts = pages?.[0]?.meta?.statusCounts;
    if (typeof counts !== "object" || counts === null) return undefined;
    const out = {} as Record<LeaveStatus, number>;
    for (const s of LEAVE_STATUSES) {
        const n = (counts as Record<string, unknown>)[s];
        out[s] = typeof n === "number" ? n : 0;
    }
    return out;
}

export type LeaveAction = "approve" | "reject" | "cancel";

/** The decisions a request in `status` allows (Go's `leaveMoves`). */
export function leaveActions(status: LeaveStatus): LeaveAction[] {
    if (status === "pending") return ["approve", "reject"];
    if (status === "approved") return ["cancel"];
    return [];
}

/** The 202 data of a decision written back to Firestore that the mirror has not acknowledged yet. */
export interface LeaveWriteBackPending {
    pending: true;
    id: string;
    status: LeaveStatus;
    collection: string;
    docId: string;
}

export type LeaveDecisionResult = LeaveRequestDTO | LeaveWriteBackPending;

export function isWriteBackPending(r: LeaveDecisionResult | undefined): r is LeaveWriteBackPending {
    return typeof r === "object" && r !== null && (r as { pending?: unknown }).pending === true;
}

export interface LeaveDecisionInput {
    id: string;
    action: LeaveAction;
    /** The rejection reason (required by reject, refused by the others). */
    reason?: string;
}

export async function decideLeave({ id, action, reason }: LeaveDecisionInput): Promise<LeaveDecisionResult> {
    const body = action === "reject" ? { reason: reason ?? "" } : {};
    return goFetch<LeaveDecisionResult>(goPath`/v1/leave-requests/${id}/${action}`, { method: "POST", body });
}

export const LEAVE_DECISION_INVALIDATES = [queryKeys.leave()] as const;

export function useDecideLeave() {
    return useMutation({ mutationFn: decideLeave, meta: { invalidates: LEAVE_DECISION_INVALIDATES } });
}
