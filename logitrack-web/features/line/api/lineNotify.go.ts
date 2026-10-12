
/**
 * The customer LINE card and its evidence link over Go (T49; developer-spec.md §7.6, §9.6; Appendix B §B.2.11-§B.2.13):
 * - `POST /v1/{tasks|trips|standby}/{id}/notify-line {force}` (`operations:edit_trip_details`) answers `202 {jobId}`:
 *   the worker's `notify.line` sends the card once (a job, `['jobs',id]`); `force` resends a card already sent and
 *   replaces a revoked evidence link (R47). A request while one waits for the same card is `409 already_exists` with
 *   `details.jobId`: that job is followed instead. It replaces the legacy page's `sendCustomerLineNotification` call
 *   (`EditTripDetailsDialog.tsx:857-880`, `LegacyStandbyRecordsPage.tsx:146`), which answered synchronously.
 * - `POST /v1/{trips|standby}/{id}/evidence/revoke` (`204`): the gallery link the cards carry answers 404 from then on,
 *   until the next forced send mints a new one (new; the legacy links were irrevocable).
 */
import { useState } from "react";

import { isJobFinished, useJob, type JobDTO } from "@/features/jobs/api/jobs";
import { goFetch, goPath, isApiError } from "@/lib/goFetch";

/** The record a card is about: the check-in card of a task, the delivered card of a trip, the standby card. */
export type LineRecordKind = "tasks" | "trips" | "standby";

/** The result of a finished `notify.line.force` job (the worker's `{sent, skipped?, reason?, photoCount?}`). */
export interface LineJobResult {
    sent?: boolean;
    skipped?: boolean;
    reason?: string;
    photoCount?: number;
}

function notifyPath(kind: LineRecordKind, id: string): string {
    switch (kind) {
        case "tasks":
            return goPath`/v1/tasks/${id}/notify-line`;
        case "trips":
            return goPath`/v1/trips/${id}/notify-line`;
        default:
            return goPath`/v1/standby/${id}/notify-line`;
    }
}

/**
 * Asks Go for the card; a request for the same card already waiting (`409 already_exists`, `details.jobId`) is that
 * job (`joined`: it may be another user's, which sends no `job.updated` to this tab).
 */
export async function startLineNotify(kind: LineRecordKind, id: string, force: boolean): Promise<{ jobId: string; joined: boolean }> {
    try {
        const out = await goFetch<{ jobId: string }>(notifyPath(kind, id), { method: "POST", body: { force } });
        return { jobId: out.jobId, joined: false };
    } catch (error) {
        if (isApiError(error) && error.code === "already_exists" && typeof error.details.jobId === "string" && error.details.jobId !== "") {
            return { jobId: error.details.jobId, joined: true };
        }
        throw error;
    }
}

/** Revokes the evidence link of a trip or a standby record (`204`). */
export async function revokeEvidence(kind: Exclude<LineRecordKind, "tasks">, id: string): Promise<void> {
    const path = kind === "trips" ? goPath`/v1/trips/${id}/evidence/revoke` : goPath`/v1/standby/${id}/evidence/revoke`;
    await goFetch<void>(path, { method: "POST", body: {} });
}

/** How a finished job settled the card, for the page's toast. */
export type LineOutcome =
    | { kind: "sent"; photoCount: number }
    | { kind: "skipped"; reason: string }
    | { kind: "failed"; reason: string };

/** The outcome of a finished job, `null` while it runs. */
export function lineOutcome(job: Pick<JobDTO<LineJobResult>, "status" | "result" | "error"> | null | undefined): LineOutcome | null {
    if (!job || !isJobFinished(job)) return null;
    const r = job.result ?? {};
    if (job.status === "succeeded" && r.sent) return { kind: "sent", photoCount: r.photoCount ?? 0 };
    if (job.status === "succeeded") return { kind: "skipped", reason: r.reason ?? "" };
    return { kind: "failed", reason: r.reason ?? job.error ?? "" };
}

/** The legacy page's reasons (`EditTripDetailsDialog.tsx:830-850`) as i18n keys; `null` = the generic skip text. */
export function lineReasonKey(reason: string): string | null {
    switch (reason) {
        case "already notified":
            return "lineNotify.skipAlready";
        case "no lineGroupId configured":
            return "lineNotify.skipNoGroup";
        case "LINE_NOTIFY_ENABLED is false":
            return "lineNotify.skipDisabled";
        default:
            return null;
    }
}

/** A card request as a Go job: start, then follow `['jobs',id]` until it settles. */
export function useLineNotify(kind: LineRecordKind, id: string) {
    const [jobId, setJobId] = useState<string | null>(null);
    const [joined, setJoined] = useState(false);
    const [starting, setStarting] = useState(false);
    const [startError, setStartError] = useState<unknown>(null);
    const job = useJob<LineJobResult>(jobId, { pollWhileStreamOpen: joined });
    const data = job.data as JobDTO<LineJobResult> | undefined;

    const start = async (force: boolean) => {
        setStarting(true);
        setStartError(null);
        setJobId(null);
        setJoined(false);
        try {
            const out = await startLineNotify(kind, id, force);
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
        running: starting || (jobId !== null && !isJobFinished(data)),
        outcome: jobId !== null ? lineOutcome(data) : null,
        error: startError ?? job.error ?? null,
    };
}
