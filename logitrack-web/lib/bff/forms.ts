/**
 * The anonymous BFF form handlers (R44, R77; developer-spec.md §10.4 "Anonymous forms", Appendix E
 * §E.8.3; T54R): `POST /api/forms/waitlist` -> internal `POST /v1/waitlist`, `POST /api/forms/partner-interest`
 * -> internal `POST /v1/partner-interest`. They replaced the landing page's anonymous Firestore writes
 * (the waitlist dialog, deleted in UI3C: the landing links to `/waitlist`; `features/auth/hooks/useWaitlist.ts`;
 * `app/join-network/page.tsx`).
 *
 * - Unauthenticated: no cookie is read and no `Authorization` is sent, whatever the browser sends.
 * - The mutation Origin check (`sameOrigin`, Appendix C §C.4.5) runs first: a cross-origin post gets
 *   403 `permission_denied` (`details.reason = "origin"`) before the body is read and without reaching Go.
 * - The body must be one JSON object of at most 16 KiB (400 `bad_request`, 413 `payload_too_large`); it
 *   is forwarded as sent, Go validates the fields (422 `invalid_argument`) and holds the honeypot.
 * - `X-Forwarded-For` (nginx's / Caddy's value) goes to Go, so the per-IP bucket `public_form_ip`
 *   (`RATE_LIMIT_PUBLIC_FORMS`, Go trusts the header from `TRUSTED_PROXY_CIDRS`) counts the visitor,
 *   not the web server; with `X-Request-Id`, `Accept-Language` and `User-Agent`.
 * - Go's answer passes through with its status and envelope (201 `{data: {received}}`, 422, 429 with
 *   `Retry-After`), `Cache-Control: no-store`; any other header (a `Set-Cookie` included) is dropped.
 *   Go unreachable: 502, too slow: 504 (`GO_API_INTERNAL_TIMEOUT_MS`).
 */
import "server-only";

import { bffConfig, type BffConfig } from "./config";
import {
    callUpstream,
    errorResponse,
    forwardedFor,
    HEADER_REQUEST_ID,
    misconfigured,
    originRefused,
    readBoundedText,
    requestIdFor,
    sameOrigin,
    upstreamFailure,
} from "./http";

/** The two forms and their Go routes (internal listener only; 404 on the public one). */
export const FORM_ROUTES = {
    waitlist: "/v1/waitlist",
    "partner-interest": "/v1/partner-interest",
} as const;

export type PublicForm = keyof typeof FORM_ROUTES;

/** The largest body forwarded (both forms are a few short fields). */
export const MAX_FORM_BYTES = 16 * 1024;

/** Request headers passed to Go besides the ones the BFF sets itself. */
const FORWARDED_HEADERS = ["accept-language", "user-agent"];
/** Response headers passed back to the browser. */
const PASSED_HEADERS = ["content-type", "retry-after"];

export interface FormDeps {
    config?: BffConfig;
    fetchImpl?: typeof fetch;
}

/** Whether `text` is one JSON object (Go's `httpx.DecodeJSON` accepts nothing else). */
function isJsonObject(text: string): boolean {
    try {
        const v: unknown = JSON.parse(text);
        return typeof v === "object" && v !== null && !Array.isArray(v);
    } catch {
        return false;
    }
}

/** The handler of `POST /api/forms/<form>`. */
export async function postForm(req: Request, form: PublicForm, deps: FormDeps = {}): Promise<Response> {
    const requestId = requestIdFor(req);
    let cfg: BffConfig;
    try {
        cfg = deps.config ?? bffConfig();
    } catch {
        return misconfigured(requestId);
    }
    if (req.method.toUpperCase() !== "POST") {
        return errorResponse(405, "method_not_allowed", "method not allowed", requestId, {}, { Allow: "POST" });
    }
    if (!sameOrigin(req, cfg)) return originRefused(requestId);

    const text = await readBoundedText(req, MAX_FORM_BYTES);
    if (text === undefined) {
        return errorResponse(413, "payload_too_large", `the form body is larger than ${MAX_FORM_BYTES} bytes`, requestId, { maxBytes: MAX_FORM_BYTES });
    }
    if (!isJsonObject(text)) return errorResponse(400, "bad_request", "the form body must be a JSON object", requestId);

    // A fresh header set: the browser's Cookie and Authorization never reach Go.
    const headers = new Headers({
        Accept: "application/json",
        "Content-Type": "application/json",
        [HEADER_REQUEST_ID]: requestId,
        "Accept-Encoding": "identity",
    });
    for (const name of FORWARDED_HEADERS) {
        const v = req.headers.get(name);
        if (v !== null) headers.set(name, v);
    }
    const xff = forwardedFor(req);
    if (xff) headers.set("X-Forwarded-For", xff);

    const path = FORM_ROUTES[form];
    const result = await callUpstream(`${cfg.goApiInternalUrl}${path}`, { method: "POST", headers, body: text }, {
        cfg,
        signal: req.signal,
        fetchImpl: deps.fetchImpl,
    });
    if (!result.ok) {
        if (result.kind !== "aborted") console.warn(`[bff] POST ${path} ${result.kind} request_id=${requestId}`);
        return upstreamFailure(result.kind, requestId);
    }
    const res = result.res;
    const out = new Headers();
    for (const name of PASSED_HEADERS) {
        const v = res.headers.get(name);
        if (v !== null) out.set(name, v);
    }
    out.set("Cache-Control", "no-store");
    out.set(HEADER_REQUEST_ID, res.headers.get(HEADER_REQUEST_ID) ?? requestId);
    const noBody = res.status === 204 || res.status === 205 || res.status === 304;
    const body = noBody ? null : await res.text();
    return new Response(body, { status: res.status, headers: out });
}

/** No CORS preflight: the landing pages post same-origin only. */
export function formOptions(req: Request): Response {
    return errorResponse(405, "method_not_allowed", "method not allowed", requestIdFor(req), {}, { Allow: "POST" });
}
