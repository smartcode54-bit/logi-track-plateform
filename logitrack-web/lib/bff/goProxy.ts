/**
 * The generic BFF proxy `app/api/go/[...path]/route.ts` (developer-spec.md §10.3, Appendix E
 * §E.8.2, §E.8.5; R37, R38, R48): `/api/go/v1/<rest>?<query>` -> `{GO_API_INTERNAL_URL}/v1/<rest>?<query>`.
 *
 * - Path: only `v1/...`; every segment is decoded, checked and re-encoded; a dot segment (after
 *   decoding, so `%2e%2e` counts), an empty segment, an encoded `/` or `\`, a control character or a
 *   malformed escape is 400 `bad_request`. Outside `v1/` and the session routes
 *   `v1/auth/(login|google|refresh|tenant|logout|logout-all|sse-ticket|exchange)` and `v1/bridge/*`
 *   are 404 `not_found` (compared case-insensitively, as Fiber routes): they mint, rotate or end
 *   sessions and go through `/api/auth/*`, so no token ever reaches browser JavaScript.
 *   `v1/auth/google/nonce` and `v1/auth/password/*` pass.
 * - Headers out: an allow-list (`Accept`, `Accept-Language`, `Content-Type`, `If-None-Match`,
 *   `If-Match`, `Range`, `Idempotency-Key`, `Last-Event-ID`, `User-Agent`, `X-Act-On-Tenant`); then
 *   `Authorization: Bearer <lt_at>` (a browser `Authorization` is dropped), `X-Request-Id`,
 *   `X-Forwarded-For` (Caddy's chain) and `Accept-Encoding: identity`.
 * - Mutations need the Origin check (lib/bff/http.ts `sameOrigin`), else 403 before Go is called.
 * - Bodies stream both ways; hop-by-hop headers, `Content-Length`, `Content-Encoding` and any
 *   `Set-Cookie` of the answer are dropped; redirects are passed to the browser (`GET /v1/files`).
 * - `GO_API_INTERNAL_TIMEOUT_MS` and the browser's abort apply; `v1/events` (SSE) has no timeout,
 *   gets `Cache-Control: no-cache, no-transform` and `X-Accel-Buffering: no`, and its `lastEventId`
 *   query parameter becomes `Last-Event-ID` (Appendix E §E.5.1).
 * - It never refreshes (R37): `lt_rt` has `Path=/api/auth` and never reaches it; a 401 goes back to
 *   the browser's goFetch, which runs the shared refresh. No body inspection, caching or decision.
 */
import "server-only";

import { bffConfig, type BffConfig } from "./config";
import {
    callUpstream,
    cookie,
    errorResponse,
    forwardedFor,
    HEADER_REQUEST_ID,
    isMutation,
    misconfigured,
    originRefused,
    requestIdFor,
    sameOrigin,
    upstreamFailure,
} from "./http";
import { ACCESS_COOKIE } from "./session";

export const PROXY_PREFIX = "/api/go/";
export const PROXY_METHODS = ["GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"] as const;

/** The `v1/auth/*` routes that mint, rotate or end a session: only `/api/auth/*` may call them (R38). */
export const BLOCKED_AUTH_ROUTES = ["login", "google", "refresh", "tenant", "logout", "logout-all", "sse-ticket", "exchange"];

const FORWARDED_REQUEST_HEADERS = [
    "accept",
    "accept-language",
    "content-type",
    "if-none-match",
    "if-match",
    "range",
    "idempotency-key",
    "last-event-id",
    "user-agent",
    "x-act-on-tenant",
];

const DROPPED_RESPONSE_HEADERS = [
    "connection",
    "keep-alive",
    "proxy-authenticate",
    "proxy-authorization",
    "te",
    "trailer",
    "transfer-encoding",
    "upgrade",
    "content-encoding",
    "content-length",
    "set-cookie",
];

/** Statuses whose response has no body (the Response constructor refuses one). */
const NULL_BODY_STATUS = new Set([101, 103, 204, 205, 304]);

export type ProxyTarget =
    | { ok: true; segments: string[]; search: string; isEvents: boolean }
    | { ok: false; status: 400 | 404; message: string };

/** C0 controls and DEL. */
function hasControlChar(text: string): boolean {
    for (let i = 0; i < text.length; i++) {
        const c = text.charCodeAt(i);
        if (c < 0x20 || c === 0x7f) return true;
    }
    return false;
}

/**
 * Splits the request URL into the checked Go path (decoded segments) and the raw query. The path is
 * taken from the URL string itself, so percent-escapes are judged as sent.
 */
export function parseProxyTarget(rawUrl: string): ProxyTarget {
    const afterOrigin = rawUrl.replace(/^[A-Za-z][A-Za-z0-9+.-]*:\/\/[^/?#]*/, "");
    const hashAt = afterOrigin.indexOf("#");
    const noHash = hashAt === -1 ? afterOrigin : afterOrigin.slice(0, hashAt);
    const q = noHash.indexOf("?");
    const pathname = q === -1 ? noHash : noHash.slice(0, q);
    const search = q === -1 ? "" : noHash.slice(q);
    if (!pathname.startsWith(PROXY_PREFIX)) return { ok: false, status: 404, message: "not found" };
    const raw = pathname.slice(PROXY_PREFIX.length).split("/");
    const segments: string[] = [];
    for (const s of raw) {
        let decoded: string;
        try {
            decoded = decodeURIComponent(s);
        } catch {
            return { ok: false, status: 400, message: "malformed percent-escape in the path" };
        }
        if (decoded === "" || decoded === "." || decoded === ".." || /[/\\]/.test(decoded) || hasControlChar(decoded)) {
            return { ok: false, status: 400, message: "empty, dot, encoded-slash or control-character path segment" };
        }
        segments.push(decoded);
    }
    const lower = segments.map((s) => s.toLowerCase());
    if (lower[0] !== "v1" || lower.length < 2) return { ok: false, status: 404, message: "not found" };
    if (lower[1] === "bridge") return { ok: false, status: 404, message: "not found" };
    if (lower[1] === "auth" && lower.length === 3 && BLOCKED_AUTH_ROUTES.includes(lower[2])) {
        return { ok: false, status: 404, message: "not found" };
    }
    const isEvents = lower.length === 2 && lower[1] === "events";
    return { ok: true, segments, search, isEvents };
}

/** `{GO_API_INTERNAL_URL}/v1/...` with re-encoded segments and the query kept. */
export function upstreamUrl(cfg: Pick<BffConfig, "goApiInternalUrl">, segments: string[], search: string): string {
    return `${cfg.goApiInternalUrl}/${segments.map(encodeURIComponent).join("/")}${search}`;
}

export interface ProxyDeps {
    config?: BffConfig;
    fetchImpl?: typeof fetch;
}

/** The single handler behind every exported method of `app/api/go/[...path]/route.ts`. */
export async function proxyToGo(req: Request, deps: ProxyDeps = {}): Promise<Response> {
    const requestId = requestIdFor(req);
    const method = req.method.toUpperCase();
    if (!(PROXY_METHODS as readonly string[]).includes(method)) {
        return errorResponse(405, "method_not_allowed", "method not allowed", requestId, {}, { Allow: PROXY_METHODS.join(", ") });
    }
    let cfg: BffConfig;
    try {
        cfg = deps.config ?? bffConfig();
    } catch {
        return misconfigured(requestId);
    }

    const target = parseProxyTarget(req.url);
    if (!target.ok) {
        return errorResponse(target.status, target.status === 404 ? "not_found" : "bad_request", target.message, requestId);
    }
    if (isMutation(method) && !sameOrigin(req, cfg)) return originRefused(requestId);

    const headers = new Headers();
    for (const name of FORWARDED_REQUEST_HEADERS) {
        const v = req.headers.get(name);
        if (v !== null) headers.set(name, v);
    }
    const at = cookie(req, ACCESS_COOKIE);
    if (at) headers.set("Authorization", `Bearer ${at}`);
    headers.set(HEADER_REQUEST_ID, requestId);
    const xff = forwardedFor(req);
    if (xff) headers.set("X-Forwarded-For", xff);
    headers.set("Accept-Encoding", "identity");

    let search = target.search;
    if (target.isEvents && search) {
        // EventSource cannot set headers: a recreated stream passes its position as ?lastEventId=.
        const params = new URLSearchParams(search);
        const last = params.get("lastEventId");
        if (last !== null) {
            params.delete("lastEventId");
            if (!headers.has("last-event-id") && last !== "" && !hasControlChar(last)) headers.set("Last-Event-ID", last);
            const rest = params.toString();
            search = rest ? `?${rest}` : "";
        }
    }

    const hasBody = method !== "GET" && method !== "HEAD" && req.body !== null;
    const result = await callUpstream(
        upstreamUrl(cfg, target.segments, search),
        { method, headers, ...(hasBody ? { body: req.body, duplex: "half" as const } : {}) },
        { cfg, signal: req.signal, timeout: !target.isEvents, fetchImpl: deps.fetchImpl }
    );
    if (!result.ok) {
        if (result.kind !== "aborted") {
            // The route's first two segments only: ids further down may identify people.
            const route = `/${target.segments.slice(0, 2).join("/")}${target.segments.length > 2 ? "/..." : ""}`;
            console.warn(`[bff] ${method} ${route} ${result.kind} request_id=${requestId}`);
        }
        return upstreamFailure(result.kind, requestId);
    }

    const upstream = result.res;
    const out = new Headers(upstream.headers);
    for (const name of DROPPED_RESPONSE_HEADERS) out.delete(name);
    if (!out.has(HEADER_REQUEST_ID)) out.set(HEADER_REQUEST_ID, requestId);
    if (target.isEvents) {
        out.set("Cache-Control", "no-cache, no-transform");
        out.set("X-Accel-Buffering", "no");
    }
    const noBody = method === "HEAD" || NULL_BODY_STATUS.has(upstream.status);
    if (noBody) await upstream.body?.cancel().catch(() => undefined);
    return new Response(noBody ? null : upstream.body, { status: upstream.status, statusText: upstream.statusText, headers: out });
}
