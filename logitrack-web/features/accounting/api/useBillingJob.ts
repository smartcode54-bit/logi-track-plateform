"use client";

/**
 * A long billing action as a Go job (T42; developer-spec.md §7.5): `POST /v1/jobs/{type}` answers
 * `202 {jobId}` and the page follows `['jobs',id]` (`useJob`: SSE `job.updated`, a 5 s poll while the
 * stream is down) instead of holding a 540 s callable open. The result is the finished job's `result`;
 * a succeeded job of a billing type also makes its keys stale (`JOB_SUCCESS_KEYS`, lib/realtime/eventMap.ts).
 */
import { useState } from "react";
import { isJobFinished, useJob, type JobDTO } from "@/features/jobs/api/jobs";
import { startBillingJob, type BackfillInput, type BillingJobType } from "./billingWrites.go";

export function useBillingJob<TResult = Record<string, unknown>>() {
    const [jobId, setJobId] = useState<string | null>(null);
    const [startError, setStartError] = useState<unknown>(null);
    const [starting, setStarting] = useState(false);
    const job = useJob<TResult>(jobId);
    const data = job.data as JobDTO<TResult> | undefined;
    const finished = isJobFinished(data);

    const start = async (type: BillingJobType, input: BackfillInput | { fromDateStr: string; toDateStr: string; maxScan?: number }) => {
        setStarting(true);
        setStartError(null);
        setJobId(null);
        try {
            const out = await startBillingJob(type, input);
            setJobId(out.jobId);
        } catch (e) {
            setStartError(e);
        } finally {
            setStarting(false);
        }
    };

    return {
        start,
        jobId,
        job: data,
        running: starting || (jobId !== null && !finished),
        succeeded: data?.status === "succeeded",
        result: data?.status === "succeeded" ? (data.result as TResult | undefined) : undefined,
        error: startError ?? job.error ?? (data?.status === "failed" ? new Error(data.error ?? "job failed") : null),
        progress: data?.progress ?? null,
    };
}
