/**
 * A multipart write through the BFF (T42): the few Go routes that take a spreadsheet as a form upload
 * (`POST /v1/expenses/toll-import`, Appendix B §B.2.15) parse it on the server, so the browser sends the
 * file as it is instead of parsing it. `goFetch` sends JSON only (files go to object storage, §9.4); this
 * helper sends a `FormData` body (the browser sets the multipart boundary), refreshes the session once
 * through `goFetch` on a 401 (`GET /v1/me`) and resolves the envelope's `data` like `goFetch`.
 */
import { apiErrorFromResponse, networkError } from "./apiError";
import { goFetch, goUrl } from "./goFetch";

async function send(href: string, form: FormData, signal?: AbortSignal): Promise<Response> {
    try {
        return await fetch(href, { method: "POST", credentials: "same-origin", body: form, headers: { Accept: "application/json" }, signal });
    } catch (e) {
        if (signal?.aborted) throw e;
        throw networkError(e);
    }
}

export async function goPostForm<T>(path: string, form: FormData, signal?: AbortSignal): Promise<T> {
    const href = goUrl(path);
    let res = await send(href, form, signal);
    if (res.status === 401) {
        await goFetch<unknown>("/v1/me", { signal });
        res = await send(href, form, signal);
    }
    if (!res.ok) throw await apiErrorFromResponse(res);
    const body = (await res.json()) as { data: T };
    return body.data;
}
