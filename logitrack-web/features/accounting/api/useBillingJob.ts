"use client";

/** Billing jobs follow useJob's SSE/poll lifecycle. Restoration is opt-in for backfill only. */
import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { isJobFinished, useJob, type JobDTO } from "@/features/jobs/api/jobs";
import { isApiError } from "@/lib/goFetch";
import { startBillingJob, type BackfillInput, type BillingJobType } from "./billingWrites.go";

interface RestoreOptions {
    /** Includes viewer, tenant, job type and scope; only the job id is stored. */
    storageKey: string | null;
    onRestored?: () => void;
    onAlreadyRunning?: () => void;
}

function storedId(key: string): string | null {
    try { return sessionStorage.getItem(key); } catch { return null; }
}
function rememberId(key: string, id: string | null) {
    try {
        if (id) sessionStorage.setItem(key, id);
        else sessionStorage.removeItem(key);
    } catch { /* Storage may be disabled; the in-memory job still works. */ }
}

export function useBillingJob<TResult = Record<string, unknown>>(restore?: RestoreOptions) {
    const client = useQueryClient();
    const refreshedJobs = useRef(new Set<string>());
    const refreshOnSuccess = Boolean(restore);
    const storageKey = restore?.storageKey ?? null;
    const [tracked, setTracked] = useState<{ key: string | null; id: string | null }>({ key: storageKey, id: null });
    const jobId = tracked.key === storageKey ? tracked.id : null;
    const [startError, setStartError] = useState<unknown>(null);
    const [starting, setStarting] = useState(false);
    const job = useJob<TResult>(jobId, restore ? { pollWhileStreamOpen: true } : undefined);
    const data = job.data as JobDTO<TResult> | undefined;
    const finished = isJobFinished(data);

    // An attached job may have another owner, whose job.updated event never reaches this viewer.
    useEffect(() => {
        if (!refreshOnSuccess || !jobId || data?.status !== "succeeded" || refreshedJobs.current.has(jobId)) return;
        refreshedJobs.current.add(jobId);
        void client.invalidateQueries({ queryKey: ["income"] });
        void client.invalidateQueries({ queryKey: ["billing"] });
    }, [refreshOnSuccess, jobId, data?.status, client]);

    useEffect(() => {
        const id = storageKey ? storedId(storageKey) : null;
        setTracked({ key: storageKey, id });
        setStartError(null);
        if (id) restore?.onRestored?.();
        // Callbacks report a restoration, but do not change which job is followed.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [storageKey]);

    useEffect(() => {
        if (!storageKey || !jobId) return;
        if (isApiError(job.error) && (job.error.status === 403 || job.error.status === 404)) {
            rememberId(storageKey, null);
            setTracked({ key: storageKey, id: null });
            setStartError(job.error);
        } else if (finished) rememberId(storageKey, null);
    }, [storageKey, jobId, job.error, finished]);

    const start = async (type: BillingJobType, input: BackfillInput | { fromDateStr: string; toDateStr: string; maxScan?: number }) => {
        setStarting(true);
        setStartError(null);
        setTracked({ key: storageKey, id: null });
        if (storageKey) rememberId(storageKey, null);
        try {
            const out = await startBillingJob(type, input);
            setTracked({ key: storageKey, id: out.jobId });
            if (storageKey) rememberId(storageKey, out.jobId);
        } catch (e) {
            if (restore && isApiError(e) && e.code === "already_exists" && typeof e.details.jobId === "string") {
                setTracked({ key: storageKey, id: e.details.jobId });
                if (storageKey) rememberId(storageKey, e.details.jobId);
                restore.onAlreadyRunning?.();
            } else setStartError(e);
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
