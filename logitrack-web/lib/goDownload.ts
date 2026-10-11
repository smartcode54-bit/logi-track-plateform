/**
 * Downloads of server-rendered files (T42; developer-spec.md §9.5, §9.9; R69): the Go route answers
 * `302` to a short-lived signed GET of the stored object (`Content-Disposition: attachment` with the real
 * file name), `202 {jobId}` while a render is still running (the Shopee report), or an error envelope
 * (`409` a document not ready, `403`, ...). The BFF passes the redirect to the browser (`redirect:
 * 'manual'`, developer-spec.md §10.3).
 *
 * A plain link would show a JSON error page for a 409, and an expired access cookie would show Go's 401
 * (the BFF never refreshes, R37). So the page first asks the route itself with `redirect: 'manual'`:
 * a redirect means the file is there, and the tab then navigates to the same route, which the browser
 * saves as a download without leaving the page; a 401 refreshes the session once through `goFetch`
 * (`GET /v1/me`) and asks again; any other status is thrown as the Go error.
 */
import { apiErrorFromResponse } from "./apiError";
import { goFetch, goUrl, type QueryParams } from "./goFetch";

export type GoDownloadProbe = { kind: "ready"; href: string } | { kind: "accepted"; jobId: string; href: string };

async function ask(href: string, signal?: AbortSignal): Promise<Response> {
    return fetch(href, { method: "GET", credentials: "same-origin", redirect: "manual", headers: { Accept: "application/json" }, signal });
}

/** Asks a download route whether its file is ready (see the module comment); `path` is a Go `/v1/...` path. */
export async function probeGoDownload(path: string, query?: QueryParams, signal?: AbortSignal): Promise<GoDownloadProbe> {
    const href = goUrl(path, query);
    let res = await ask(href, signal);
    if (res.status === 401) {
        // goFetch refreshes the session (or ends it) on a 401; then the route is asked once more.
        await goFetch<unknown>("/v1/me", { signal });
        res = await ask(href, signal);
    }
    if (res.type === "opaqueredirect" || (res.status >= 300 && res.status < 400)) return { kind: "ready", href };
    if (res.status === 202) {
        const body = (await res.json().catch(() => null)) as { data?: { jobId?: unknown } } | null;
        const jobId = body?.data?.jobId;
        if (typeof jobId === "string" && jobId !== "") return { kind: "accepted", jobId, href };
    }
    throw await apiErrorFromResponse(res);
}

/** Saves the file behind a ready route: the browser follows the redirect to the attachment. */
export function saveGoDownload(href: string): void {
    window.location.assign(href);
}

/** Probes and, when the file is ready, saves it; returns the probe for the caller to follow a job. */
export async function downloadFromGo(path: string, query?: QueryParams, signal?: AbortSignal): Promise<GoDownloadProbe> {
    const probe = await probeGoDownload(path, query, signal);
    if (probe.kind === "ready") saveGoDownload(probe.href);
    return probe;
}
