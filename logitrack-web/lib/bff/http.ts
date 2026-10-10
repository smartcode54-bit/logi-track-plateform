/**
 * Shared plumbing of the BFF (developer-spec.md §10.3, §10.4; Appendix E §E.8.2, §E.8.3): the R48
 * error envelope for errors the BFF raises itself, request ids, cookies, the mutation Origin check
 * and the upstream call to the api's internal listener. Server only (route handlers and proxy.ts).
 */
import "server-only";

import type { BffConfig } from "./config";

export const HEADER_REQUEST_ID = "X-Request-Id";

/** Go accepts an incoming id of this shape and echoes it (internal/platform/httpx validRequestID). */
const REQUEST_ID_RX = /^[A-Za-z0-9._-]{8,128}$/;

/** The incoming `X-Request-Id` when Go would accept it, else a new uuid. */
export function requestIdFor(req: Request): string {
    const incoming = req.headers.get(HEADER_REQUEST_ID);
    return incoming && REQUEST_ID_RX.test(incoming) ? incoming : crypto.randomUUID();
}

/**
 * An error raised by the BFF itself, in exactly Go's envelope
 * `{"error":{"code","message","details","requestId"}}` (Appendix B §B.1.5, R48), with `X-Request-Id`.
 */
export function errorResponse(
    status: number,
    code: string,
    message: string,
    requestId: string,
    details: Record<string, unknown> = {},
    headers?: HeadersInit
): Response {
    const h = new Headers(headers);
    h.set("Content-Type", "application/json; charset=utf-8");
    h.set("Cache-Control", "no-store");
    h.set(HEADER_REQUEST_ID, requestId);
    return new Response(JSON.stringify({ error: { code, message, details, requestId } }), { status, headers: h });
}

/** The cookies of the request (first value of each name wins, as browsers send the most specific first). */
export function parseCookies(header: string | null): Map<string, string> {
    const out = new Map<string, string>();
    if (!header) return out;
    for (const part of header.split(";")) {
        const eq = part.indexOf("=");
        if (eq <= 0) continue;
        const name = part.slice(0, eq).trim();
        let value = part.slice(eq + 1).trim();
        if (value.startsWith('"') && value.endsWith('"') && value.length >= 2) value = value.slice(1, -1);
        if (name && !out.has(name)) out.set(name, value);
    }
    return out;
}

export function cookie(req: Request, name: string): string | undefined {
    const v = parseCookies(req.headers.get("cookie")).get(name);
    return v ? v : undefined;
}

/** Methods that change state: they need the Origin check (Appendix E §E.8.2). */
export function isMutation(method: string): boolean {
    return method !== "GET" && method !== "HEAD";
}

/**
 * CSRF check of every BFF mutation (developer-spec.md §10.3, Appendix C §C.4.5): `Origin` must equal
 * `WEB_PUBLIC_ORIGIN`, or, when a browser left `Origin` out, `Sec-Fetch-Site` must be `same-origin`;
 * a `Sec-Fetch-Site` that is present must be `same-origin` in either case. A request with neither
 * header is refused. Cookies are SameSite=Lax, so there is no CSRF token.
 */
export function sameOrigin(req: Request, cfg: Pick<BffConfig, "webPublicOrigin">): boolean {
    const origin = req.headers.get("origin");
    const site = req.headers.get("sec-fetch-site");
    if (site !== null && site !== "same-origin") return false;
    if (origin !== null) return origin === cfg.webPublicOrigin;
    return site === "same-origin";
}

/** The 403 of a refused Origin check (`details.reason = "origin"`), raised before Go is called. */
export function originRefused(requestId: string): Response {
    return errorResponse(403, "permission_denied", "cross-origin request refused", requestId, { reason: "origin" });
}

// An X-Forwarded-For chain as Caddy sets it: addresses separated by commas (IPv4, IPv6, optional ports).
const XFF_RX = /^[0-9A-Fa-f.:[\], ]{1,512}$/;

/**
 * The `X-Forwarded-For` chain to send to Go: Caddy's value, unchanged (Go trusts it only from
 * `TRUSTED_PROXY_CIDRS`, where the web container is). Absent or malformed: none, and Go uses the
 * web container's address.
 */
export function forwardedFor(req: Request): string | undefined {
    const v = req.headers.get("x-forwarded-for");
    return v && XFF_RX.test(v) ? v : undefined;
}

export type UpstreamResult =
    | { ok: true; res: Response }
    | { ok: false; kind: "timeout" | "unreachable" | "aborted"; error: unknown };

/**
 * One request to the api's internal listener. `signal` is the caller's (a closed tab), the timeout
 * is `GO_API_INTERNAL_TIMEOUT_MS` unless `timeout: false` (SSE). Redirects are not followed.
 */
export async function callUpstream(
    url: string,
    init: RequestInit & { duplex?: "half" },
    opts: { cfg: BffConfig; signal?: AbortSignal; timeout?: boolean; fetchImpl?: typeof fetch }
): Promise<UpstreamResult> {
    const timeoutSignal = opts.timeout === false ? undefined : AbortSignal.timeout(opts.cfg.goTimeoutMs);
    const signals = [opts.signal, timeoutSignal].filter((s): s is AbortSignal => s !== undefined);
    const signal = signals.length === 0 ? undefined : signals.length === 1 ? signals[0] : AbortSignal.any(signals);
    try {
        const res = await (opts.fetchImpl ?? fetch)(url, { ...init, redirect: "manual", signal });
        return { ok: true, res };
    } catch (error) {
        if (timeoutSignal?.aborted) return { ok: false, kind: "timeout", error };
        if (opts.signal?.aborted) return { ok: false, kind: "aborted", error };
        return { ok: false, kind: "unreachable", error };
    }
}

/** The envelope for an upstream failure: 504 on timeout, 502 when Go could not be reached. */
export function upstreamFailure(kind: "timeout" | "unreachable" | "aborted", requestId: string): Response {
    if (kind === "timeout") return errorResponse(504, "unavailable", "the api did not answer in time", requestId);
    if (kind === "aborted") {
        // The browser went away; nobody reads this answer.
        return new Response(null, { status: 499, headers: { [HEADER_REQUEST_ID]: requestId } });
    }
    return errorResponse(502, "unavailable", "the api is unreachable", requestId);
}

/** The 500 of a web server whose environment is incomplete (the names are logged by bffConfig). */
export function misconfigured(requestId: string): Response {
    return errorResponse(500, "internal", "the web server is not configured", requestId);
}

/** The login page of the web; `next` brings the user back after signing in. */
export const LOGIN_PATH = "/login";

/** `/login?next=<next>` (plus `reason=revoked` for a revoked session, as lib/sessionEnd.ts does). */
export function loginLocation(next: string, revoked = false): string {
    const params = new URLSearchParams({ next });
    if (revoked) params.set("reason", "revoked");
    return `${LOGIN_PATH}?${params.toString()}`;
}
