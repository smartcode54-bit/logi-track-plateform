/**
 * Admin jobs (developer-spec.md §7.5; Appendix B §B.2.20 `GET /v1/jobs/{id}`; §10.7 `['jobs',id]`).
 * A long action answers `202 {jobId}`; the page follows `['jobs',id]`, which the session stream
 * keeps current (`job.updated` writes it, lib/realtime/eventMap.ts) and which polls every 5 s only
 * while the job runs and the stream is not open. A job owned by another user (no `job.updated` reaches this
 * tab) polls while it runs whatever the stream state (`pollWhileStreamOpen`).
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

export interface UseJobOptions {
    /**
     * Poll every 5 s while the job runs even when the stream is open. Go sends `job.updated` only on the
     * owner's `user:{id}` topic, so a job this user follows but does not own (a 409 `already_exists` join of
     * another user's job) gets no event: it must poll until it finishes.
     */
    pollWhileStreamOpen?: boolean;
}

/**
 * `['jobs', id]`: `GET /v1/jobs/{id}`; polled 5 s while running and the stream is down (or, with
 * `pollWhileStreamOpen`, while running whatever the stream state: a job owned by another user).
 */
export function useJob<TResult = Record<string, unknown>>(id: string | null | undefined, opts?: UseJobOptions) {
    const streamOpen = useRealtimeStatus() === "open";
    const pollAlways = opts?.pollWhileStreamOpen === true;
    return useQuery({
        queryKey: queryKeys.jobs(id ?? ""),
        queryFn: ({ signal }) => goFetch<JobDTO<TResult>>(goPath`/v1/jobs/${id ?? ""}`, { signal }),
        enabled: Boolean(id),
        staleTime: 0,
        gcTime: 10 * 60_000,
        refetchInterval: (query) => (isJobFinished(query.state.data) || (streamOpen && !pollAlways) ? false : JOB_POLL_MS),
    });
}
