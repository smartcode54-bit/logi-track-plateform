/**
 * Admin jobs (developer-spec.md §7.5; Appendix B §B.2.20 `GET /v1/jobs/{id}`; §10.7 `['jobs',id]`).
 * A long action answers `202 {jobId}`; the page follows `['jobs',id]`, which the session stream
 * keeps current (`job.updated` writes it, lib/realtime/eventMap.ts) and which polls every 5 s only
 * while the job runs and the stream is not open.
 */
import { useQuery } from "@tanstack/react-query";
import { goFetch, goPath } from "@/lib/goFetch";
import { queryKeys } from "@/lib/queryKeys";
import { useRealtimeStatus } from "@/lib/realtime/status";

export type JobStatus = "queued" | "running" | "succeeded" | "failed";

export interface JobDTO<TResult = Record<string, unknown>> {
    id: string;
    type: string;
    status: JobStatus | string;
    progress?: { done?: number; total?: number } | null;
    result?: TResult;
    error?: string | null;
    createdAt?: string;
    startedAt?: string | null;
    finishedAt?: string | null;
}

/** The 202 body of a job start. */
export interface JobStarted {
    jobId: string;
}

export const JOB_POLL_MS = 5_000;

export function isJobFinished(job: Pick<JobDTO, "status"> | null | undefined): boolean {
    return job?.status === "succeeded" || job?.status === "failed";
}

/** `['jobs', id]`: `GET /v1/jobs/{id}`; polled 5 s while running and the stream is down. */
export function useJob<TResult = Record<string, unknown>>(id: string | null | undefined) {
    const streamOpen = useRealtimeStatus() === "open";
    return useQuery({
        queryKey: queryKeys.jobs(id ?? ""),
        queryFn: ({ signal }) => goFetch<JobDTO<TResult>>(goPath`/v1/jobs/${id ?? ""}`, { signal }),
        enabled: Boolean(id),
        staleTime: 0,
        gcTime: 10 * 60_000,
        refetchInterval: (query) => (streamOpen || isJobFinished(query.state.data) ? false : JOB_POLL_MS),
    });
}
