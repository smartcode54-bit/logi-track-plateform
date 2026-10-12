/**
 * Payroll runs over Go (T46, issue #66; T44 routes, Appendix B §B.2.17; Appendix E §E.2.2, §E.5 row 12, §E.6,
 * §E.10 row 10). Replaces the 100-row listener on `payroll` (`app/app/payroll/page.tsx:89-116`), the
 * `generateDriverPayoutRun` and `approveDriverPayout` callables and the browser status write:
 *
 * - `['payroll', {period, round, status, driverId}]`: `GET /v1/payroll` keyset pages of 50 (`period_end DESC`,
 *   no cap), filters on the server, polled every 60 s while the tab is visible (row 12); the first page's
 *   `meta.statusCounts` (every status, the other filters applied) feeds the summary cards;
 * - `['payroll','detail',id]`: `GET /v1/payroll/{id}` with its line items (ADR 0013) and penalty applications;
 * - `POST /v1/jobs/payroll.run` (`202 {jobId}`) followed through `['jobs',id]`; a run of the same period and
 *   round in progress (`409 already_exists`, `details.jobId`) is followed instead of failing; a succeeded
 *   job makes `['payroll']` stale (`JOB_SUCCESS_KEYS` when the stream is open, `useRunPayroll` otherwise);
 * - `POST /v1/payroll/{id}/approve` (one transaction with the penalty installments and the ledger row: the
 *   penalties are stale too) and `POST /v1/payroll/{id}/status` (validated moves).
 *
 * Statuses are Go's lower_snake vocabulary (R65). Money is whole baht.
 */
import { useEffect, useRef, useState } from "react";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { isJobFinished, useJob, type JobDTO } from "@/features/jobs/api/jobs";
import { goFetch, goPath, isApiError } from "@/lib/goFetch";
import { goInfiniteQueryFn, goNextPageParam, type GoPage } from "@/lib/goQuery";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import { pollWhileVisible } from "@/lib/realtime/polling";

export const PAYROLL_PATH = "/v1/payroll";
export const PAYROLL_RUN_JOB_PATH = "/v1/jobs/payroll.run";
export const PAYROLL_PAGE_SIZE = 50;

export const PAYROLL_STATUSES = ["draft", "pending_approval", "approved", "paid", "cancelled"] as const;
export type PayrollStatus = (typeof PAYROLL_STATUSES)[number];
export const PAYROLL_ROUNDS = ["R1", "R2"] as const;
export type PayrollRound = (typeof PAYROLL_ROUNDS)[number];

/** One run of `GET /v1/payroll` (Go `hr.Run`): one driver's pay of one round. */
export interface PayrollRunDTO {
    id: string;
    tenantId: string;
    driverId: string | null;
    driverName: string | null;
    /** `yyyy-MM`. */
    payPeriod: string;
    round: PayrollRound;
    /** Bangkok midnight bounds of the round (RFC 3339). */
    periodStart: string;
    periodEnd: string;
    status: PayrollStatus;
    totalEarningsThb: number;
    totalDeductionsThb: number;
    netPayThb: number;
    currency: string;
    paymentDate: string | null;
    paymentMethod: string | null;
    remarks: string | null;
    approvedBy: string | null;
    approvedAt: string | null;
    ledgerTransactionId: string | null;
    createdAt: string;
    updatedAt: string;
}

/** One line of a run (ADR 0013). */
export interface PayrollLineItemDTO {
    lineNo: number;
    type: "earning" | "deduction";
    category: string;
    name: string;
    amountThb: number;
    quantity: number | null;
    unitRateThb: number | null;
    description: string | null;
    referenceId: string | null;
    meta: Record<string, unknown> | null;
}

/** `GET /v1/payroll/{id}`. */
export interface PayrollRunDetailDTO extends PayrollRunDTO {
    lineItems: PayrollLineItemDTO[];
    penaltyApplications: { penaltyId: string; appliedThb: number }[];
}

/** The list filters, every one sent to the server; an absent filter is every value. */
export type PayrollFilters = {
    period?: string;
    round?: PayrollRound;
    status?: PayrollStatus;
    driverId?: string;
};

/** The key's params: only the filters that are set (TanStack hashes a missing and an `undefined` field alike). */
export function payrollParams(f: PayrollFilters): PayrollFilters {
    const out: PayrollFilters = {};
    if (f.period) out.period = f.period;
    if (f.round) out.round = f.round;
    if (f.status) out.status = f.status;
    if (f.driverId) out.driverId = f.driverId;
    return out;
}

/** `['payroll', params]`: keyset pages of 50, polled every 60 s while visible (Appendix E §E.5 row 12). */
export function usePayrollRuns(filters: PayrollFilters) {
    const params = payrollParams(filters);
    return useInfiniteQuery({
        queryKey: queryKeys.payroll.list(params),
        queryFn: goInfiniteQueryFn<PayrollRunDTO>(PAYROLL_PATH, { query: { ...params, limit: PAYROLL_PAGE_SIZE } }),
        initialPageParam: undefined as string | undefined,
        getNextPageParam: goNextPageParam,
        ...QUERY_POLICY.list,
        ...pollWhileVisible(),
    });
}

/** The runs of every loaded page, in order. */
export function payrollRuns(pages: readonly GoPage<PayrollRunDTO>[] | undefined): PayrollRunDTO[] {
    return (pages ?? []).flatMap((p) => p.data ?? []);
}

/** `meta.statusCounts` of the first page (every run matching the other filters), or undefined. */
export function payrollStatusCounts(pages: readonly GoPage<PayrollRunDTO>[] | undefined): Record<PayrollStatus, number> | undefined {
    const counts = pages?.[0]?.meta?.statusCounts;
    if (typeof counts !== "object" || counts === null) return undefined;
    const out = {} as Record<PayrollStatus, number>;
    for (const s of PAYROLL_STATUSES) {
        const n = (counts as Record<string, unknown>)[s];
        out[s] = typeof n === "number" ? n : 0;
    }
    return out;
}

/** `['payroll','detail',id]`: one run with its lines and penalty applications. */
export function usePayrollRun(id: string | null) {
    return useQuery({
        queryKey: queryKeys.payroll.detail(id ?? ""),
        queryFn: ({ signal }) => goFetch<PayrollRunDetailDTO>(goPath`/v1/payroll/${id ?? ""}`, { signal }),
        enabled: Boolean(id),
        ...QUERY_POLICY.list,
    });
}

/** What every write of a run makes stale: the lists and the details. */
export const PAYROLL_WRITE_INVALIDATES = [queryKeys.payroll.all()] as const;
/** An approval also commits the penalty installments (one transaction): the penalties are stale too. */
export const PAYROLL_APPROVE_INVALIDATES = [queryKeys.payroll.all(), queryKeys.penalties()] as const;

/** `POST /v1/payroll/{id}/approve`: from draft or pending_approval; `409 failed_precondition` `stale_run` asks for a regeneration. */
export async function approvePayrollRun({ id }: { id: string }): Promise<PayrollRunDetailDTO> {
    return goFetch<PayrollRunDetailDTO>(goPath`/v1/payroll/${id}/approve`, { method: "POST", body: {} });
}

export function useApprovePayrollRun() {
    return useMutation({ mutationFn: approvePayrollRun, meta: { invalidates: PAYROLL_APPROVE_INVALIDATES } });
}

/** The moves of `POST /v1/payroll/{id}/status` (approved only through approve). */
export type PayrollStatusMove = "pending_approval" | "paid" | "cancelled" | "draft";

export interface PayrollStatusInput {
    id: string;
    status: PayrollStatusMove;
    /** A Bangkok day or an RFC 3339 instant of a paid run; absent = now. */
    paymentDate?: string;
    paymentMethod?: string;
    remarks?: string;
}

export async function setPayrollStatus({ id, ...body }: PayrollStatusInput): Promise<PayrollRunDetailDTO> {
    return goFetch<PayrollRunDetailDTO>(goPath`/v1/payroll/${id}/status`, { method: "POST", body });
}

export function useSetPayrollStatus() {
    return useMutation({ mutationFn: setPayrollStatus, meta: { invalidates: PAYROLL_WRITE_INVALIDATES } });
}

/** What the UI offers on a run: approve, and the status moves (submit = pending_approval, pay, cancel, reopen = draft). */
export type PayrollAction = "submit" | "approve" | "pay" | "cancel" | "reopen";

/** The actions the UI offers from each status (Go's `hr.transitions`, plus approve from draft or pending_approval). */
export function payrollActions(status: PayrollStatus): PayrollAction[] {
    switch (status) {
        case "draft":
            return ["submit", "approve", "cancel"];
        case "pending_approval":
            return ["approve", "reopen", "cancel"];
        case "approved":
            return ["pay"];
        case "cancelled":
            return ["reopen"];
        default:
            return [];
    }
}

/** The result of a succeeded payroll.run job (Go `hr.RunResult`). */
export interface PayrollRunResult {
    period: string;
    round: PayrollRound;
    written: number;
    skippedApproved: number;
    drivers: number;
    configId: string;
}

/**
 * `POST /v1/jobs/payroll.run {period, round}`: the job's id. A generation of the same period and round
 * already in progress (`409 already_exists` with `details.jobId`) is that job: the page follows it.
 */
export async function startPayrollRun(input: { period: string; round: PayrollRound }): Promise<{ jobId: string; joined: boolean }> {
    try {
        const out = await goFetch<{ jobId: string }>(PAYROLL_RUN_JOB_PATH, { method: "POST", body: input });
        return { jobId: out.jobId, joined: false };
    } catch (error) {
        if (isApiError(error) && error.code === "already_exists" && typeof error.details.jobId === "string" && error.details.jobId !== "") {
            return { jobId: error.details.jobId, joined: true };
        }
        throw error;
    }
}

/**
 * A payroll generation as a Go job (developer-spec.md §7.5): start, then follow `['jobs',id]` (SSE
 * `job.updated`, a 5 s poll while the stream is down). When the job succeeds the runs are stale: the event
 * map does it for an open stream (`JOB_SUCCESS_KEYS`), this hook does it once more for a polled job.
 */
export function usePayrollRunJob() {
    const client = useQueryClient();
    const [jobId, setJobId] = useState<string | null>(null);
    const [joined, setJoined] = useState(false);
    const [starting, setStarting] = useState(false);
    const [startError, setStartError] = useState<unknown>(null);
    const job = useJob<PayrollRunResult>(jobId);
    const data = job.data as JobDTO<PayrollRunResult> | undefined;
    const finished = isJobFinished(data);
    const settled = useRef<string | null>(null);

    useEffect(() => {
        if (!jobId || data?.status !== "succeeded" || settled.current === jobId) return;
        settled.current = jobId;
        void client.invalidateQueries({ queryKey: queryKeys.payroll.all() });
    }, [client, jobId, data?.status]);

    const start = async (input: { period: string; round: PayrollRound }) => {
        setStarting(true);
        setStartError(null);
        setJobId(null);
        setJoined(false);
        try {
            const out = await startPayrollRun(input);
            setJobId(out.jobId);
            setJoined(out.joined);
        } catch (e) {
            setStartError(e);
        } finally {
            setStarting(false);
        }
    };

    return {
        start,
        jobId,
        joined,
        job: data,
        running: starting || (jobId !== null && !finished),
        succeeded: data?.status === "succeeded",
        result: data?.status === "succeeded" ? data.result : undefined,
        error: startError ?? job.error ?? (data?.status === "failed" ? new Error(data.error ?? "job failed") : null),
    };
}
