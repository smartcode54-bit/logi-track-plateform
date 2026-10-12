/**
 * Driver penalties over Go (T46, issue #66; T44 routes, Appendix B §B.2.17; Appendix E §E.2.2, §E.6,
 * §E.10 row 11). Replaces the three-step waterfall of the legacy page (every driver, then the active
 * config, then the newest 200 penalties, `penalties/page.tsx:46-66`): the page reads three independent
 * queries in parallel, a server-searched driver picker (`['drivers',{fields:'minimal',q}]`), the active
 * config's penalty types (`['compConfig','active']`) and this list.
 *
 * - `['penalties', {driverId, status}]`: `GET /v1/penalties` keyset pages of 50, newest incurred first, no cap
 *   (5 min / 30 min, refetch on focus); each row carries `installmentThb`, what the next R2 run deducts at most;
 * - `POST /v1/penalties` (`201`): a pending penalty of a driver of the caller's reach, whole baht;
 * - `POST /v1/penalties/{id}/cancel`: open penalties only (`409 failed_precondition` `penalty_closed`).
 */
import { useInfiniteQuery, useMutation } from "@tanstack/react-query";

import { goFetch, goPath } from "@/lib/goFetch";
import { goInfiniteQueryFn, goNextPageParam, type GoPage } from "@/lib/goQuery";
import { queryKeys } from "@/lib/queryKeys";

export const PENALTIES_PATH = "/v1/penalties";
export const PENALTIES_PAGE_SIZE = 50;

export const PENALTY_STATUSES = ["pending", "partially_deducted", "cleared", "cancelled"] as const;
export type PenaltyStatus = (typeof PENALTY_STATUSES)[number];
/** The filter of the open penalties (what a run still deducts). */
export const OPEN_PENALTY_STATUSES: readonly PenaltyStatus[] = ["pending", "partially_deducted"];

/** One penalty (Go `hr.Penalty`). */
export interface PenaltyDTO {
    id: string;
    tenantId: string;
    driverId: string | null;
    driverName: string | null;
    typeCode: string;
    typeName: string | null;
    totalThb: number;
    remainingThb: number;
    installmentsTotal: number;
    installmentsPaid: number;
    /** The next R2 deduction at most (0 once closed). */
    installmentThb: number;
    reason: string | null;
    evidenceKey: string | null;
    status: PenaltyStatus;
    incurredAt: string;
    createdBy: string | null;
    createdAt: string;
    updatedAt: string;
}

/** The list filters: a driver, and a status set ("open" = pending and partially deducted). */
export type PenaltyFilters = {
    driverId?: string;
    status?: PenaltyStatus | "open";
};

export function penaltyParams(f: PenaltyFilters): PenaltyFilters {
    const out: PenaltyFilters = {};
    if (f.driverId) out.driverId = f.driverId;
    if (f.status) out.status = f.status;
    return out;
}

/** The `status` query value: one status, or the open pair comma-separated (Go accepts both forms). */
export function penaltyStatusQuery(status: PenaltyFilters["status"]): string | undefined {
    if (!status) return undefined;
    return status === "open" ? OPEN_PENALTY_STATUSES.join(",") : status;
}

/** `['penalties', params]`: keyset pages of 50 (5 min / 30 min, refetch on focus; Appendix E §E.6). */
export function usePenalties(filters: PenaltyFilters) {
    const params = penaltyParams(filters);
    return useInfiniteQuery({
        queryKey: queryKeys.penalties(params),
        queryFn: goInfiniteQueryFn<PenaltyDTO>(PENALTIES_PATH, {
            query: { driverId: params.driverId, status: penaltyStatusQuery(params.status), limit: PENALTIES_PAGE_SIZE },
        }),
        initialPageParam: undefined as string | undefined,
        getNextPageParam: goNextPageParam,
        staleTime: 5 * 60_000,
        gcTime: 30 * 60_000,
        refetchOnWindowFocus: true,
    });
}

export function penaltyRows(pages: readonly GoPage<PenaltyDTO>[] | undefined): PenaltyDTO[] {
    return (pages ?? []).flatMap((p) => p.data ?? []);
}

/** The body of `POST /v1/penalties` (whole baht; `incurredAt` a Bangkok day or an RFC 3339 instant). */
export interface PenaltyInput {
    driverId: string;
    typeCode: string;
    typeName?: string;
    totalThb: number;
    installmentsTotal: number;
    reason?: string;
    incurredAt: string;
}

export const PENALTY_WRITE_INVALIDATES = [queryKeys.penalties()] as const;

export async function createPenalty(input: PenaltyInput): Promise<PenaltyDTO> {
    return goFetch<PenaltyDTO>(PENALTIES_PATH, { method: "POST", body: input });
}

export function useCreatePenalty() {
    return useMutation({ mutationFn: createPenalty, meta: { invalidates: PENALTY_WRITE_INVALIDATES } });
}

export async function cancelPenalty({ id }: { id: string }): Promise<PenaltyDTO> {
    return goFetch<PenaltyDTO>(goPath`/v1/penalties/${id}/cancel`, { method: "POST", body: {} });
}

export function useCancelPenalty() {
    return useMutation({ mutationFn: cancelPenalty, meta: { invalidates: PENALTY_WRITE_INVALIDATES } });
}

export function isOpenPenalty(p: Pick<PenaltyDTO, "status">): boolean {
    return OPEN_PENALTY_STATUSES.includes(p.status);
}
