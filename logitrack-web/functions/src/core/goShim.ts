/**
 * Callable shims to the Go API (mv-go, developer-spec.md §13.8 "Shims"; Appendix C §C.6.5; R25, R45).
 *
 * While installed 3.x APKs call the legacy callables (until P7b), a migrated callable forwards the mobile call to its
 * `/v1/mobile/*` shim target on the PUBLIC Go listener: `X-Api-Key` (a platform `api_keys` row of scope `cf_shim`) and
 * the caller's own Firebase ID token as `Authorization: Bearer` (Go verifies it and acts as that user, driver:self;
 * there is no shared token and no acting-user header). The callable keeps its response contract.
 *
 * Functions params (developer-spec.md §16.1; names only, never values):
 *   LOGITRACK_API_BASE_URL    the public API origin, https only (plain http only on localhost, for the emulator; any
 *                             other http value turns every shim off: each call carries the key and an ID token);
 *                             empty = every shim off
 *   LOGITRACK_API_KEY         the cf_shim key (a Secret Manager secret: firebase functions:secrets:set LOGITRACK_API_KEY)
 *   LOGITRACK_SHIM_CALLABLES  comma list of the callables that forward (each goes live in its own phase: setDriverClaims
 *                             and addDeliveryStop at P2, computeTripBillingSnapshot at P3 with billing in
 *                             PG_OWNED_DOMAINS, sendCustomerLineNotification and markBroadcastRead at P5); empty = none
 *
 * Deploy precondition (all three, even with every shim off): this module declares the params when it loads, and
 * firebase-tools resolves every declared param of the codebase on each functions deploy, `--only functions:<other>`
 * included. So before the first deploy of a tree that contains this file, in every target project (logitrack-prod and
 * logi-track-wrt-dev): the Secret Manager secret LOGITRACK_API_KEY must exist with an enabled version (a placeholder is
 * fine while LOGITRACK_SHIM_CALLABLES is empty), and the functions .env (FUNCTIONS_ENV_DEV / FUNCTIONS_ENV_PROD of
 * deploy.yml) must carry `LOGITRACK_API_BASE_URL=` and `LOGITRACK_SHIM_CALLABLES=` lines. A non-interactive deploy
 * (deploy.yml) ignores param defaults and aborts the whole hosting+functions+firestore deploy without them; an
 * interactive one prompts and creates the secret. Turning a shim on is then only a change of those values.
 *
 * Shims: setDriverClaims -> GET /v1/mobile/me and addDeliveryStop -> POST /v1/mobile/tasks/{id}/stops (T32, P2;
 * goShimCallables.ts rebuilds their legacy answers), computeTripBillingSnapshot -> POST
 * /v1/mobile/trips/{id}/billing/compute (T38, P3), markBroadcastRead -> POST /v1/mobile/broadcasts/{id}/read (T48, P5,
 * with comms in PG_OWNED_DOMAINS: the read receipts are PostgreSQL's from then on); sendCustomerLineNotification (T49)
 * joins with its targets. Every call goes through callShim, which never follows a redirect (the key would go on to another
 * origin) and logs one line per call (callable, HTTP status, duration: the latency evidence of the shims, AC < 300 ms
 * p95 added) and an error line when the API refuses the shim's credentials (401: a revoked, expired or unknown
 * LOGITRACK_API_KEY, or a token Go rejects), fails (5xx), answers something that is not a Go envelope, redirects or
 * cannot be reached; never the key or the token.
 */

import { logger } from "firebase-functions";
import { defineSecret, defineString } from "firebase-functions/params";

export const logitrackApiBaseUrl = defineString("LOGITRACK_API_BASE_URL", {
    default: "",
    description: "Public origin of the LogiTrack Go API (mv-go shim targets); empty keeps the legacy callables.",
});

export const logitrackApiKey = defineSecret("LOGITRACK_API_KEY");

export const logitrackShimCallables = defineString("LOGITRACK_SHIM_CALLABLES", {
    default: "",
    description: "Comma list of the callables that forward to their Go shim target (empty: none).",
});

/** Whether callable is in a LOGITRACK_SHIM_CALLABLES value (names trimmed, exact, case-sensitive). */
export function shimListed(list: string, callable: string): boolean {
    return list
        .split(",")
        .map((s) => s.trim())
        .includes(callable);
}

/** The callable's name in LOGITRACK_SHIM_CALLABLES (T38: forwarded to POST /v1/mobile/trips/{id}/billing/compute). */
export const COMPUTE_TRIP_SHIM = "computeTripBillingSnapshot";

/** The callable's name in LOGITRACK_SHIM_CALLABLES (T32: forwarded to GET /v1/mobile/me). */
export const SET_DRIVER_CLAIMS_SHIM = "setDriverClaims";

/** The callable's name in LOGITRACK_SHIM_CALLABLES (T32: forwarded to POST /v1/mobile/tasks/{id}/stops). */
export const ADD_DELIVERY_STOP_SHIM = "addDeliveryStop";

/** The callable's name in LOGITRACK_SHIM_CALLABLES (T48: forwarded to POST /v1/mobile/broadcasts/{id}/read). */
export const MARK_BROADCAST_READ_SHIM = "markBroadcastRead";

/**
 * The API origin a callable forwards to, or "" while it keeps its legacy path: the origin must be set and the callable
 * listed in LOGITRACK_SHIM_CALLABLES (each shim goes live in its own phase from one code base).
 */
export function shimOrigin(callable: string, rawBase: string, list: string): string {
    const base = shimBaseUrl(rawBase);
    return base !== "" && shimListed(list, callable) ? base : "";
}

/** shimOrigin with the deployed params. */
export function shimTarget(callable: string): string {
    return shimOrigin(callable, logitrackApiBaseUrl.value(), logitrackShimCallables.value());
}

/**
 * Whether billing is priced by the Go API (P3): the compute shim is listed and the API origin set. The legacy 15-minute
 * sweeps (autoComputeBillingOnDelivery, autoComputeStandbyBilling) then stand down: billing.compute and its safety net
 * price every delivered trip and standby record in PostgreSQL and firestore.shadow writes the billing fields back, so
 * two writers never price one trip.
 */
export function goPricesBilling(): boolean {
    return shimBaseUrl(logitrackApiBaseUrl.value()) !== "" && shimListed(logitrackShimCallables.value(), COMPUTE_TRIP_SHIM);
}

/** Bound on one shim call; the APK treats the callable as best-effort. */
export const SHIM_TIMEOUT_MS = 10_000;

/** The hosts a plain-http LOGITRACK_API_BASE_URL may name: the emulator and local tests only. */
const LOCAL_HOSTS = ["localhost", "127.0.0.1", "[::1]"];

/**
 * The base URL of the shim targets, or "" when the shims are off (a trailing slash dropped). Every shim call carries
 * the cf_shim key and the caller's Firebase ID token, so only an https origin is accepted (plain http only on
 * localhost, for the emulator): an http value turns the shims off instead of sending both in cleartext.
 */
export function shimBaseUrl(raw: string): string {
    const v = raw.trim().replace(/\/+$/, "");
    if (!/^https?:\/\/[^\s/]+(\/\S*)?$/.test(v)) return "";
    let u: URL;
    try {
        u = new URL(v);
    } catch {
        return "";
    }
    if (u.protocol === "https:") return v;
    return u.protocol === "http:" && LOCAL_HOSTS.includes(u.hostname) ? v : "";
}

/** The shim target of a trip compute: `{base}/v1/mobile/trips/{id}/billing/compute`, the id path-escaped. */
export function computeTripShimUrl(base: string, tripId: string): string {
    return `${base}/v1/mobile/trips/${encodeURIComponent(tripId)}/billing/compute`;
}

/** The shim target of setDriverClaims: `{base}/v1/mobile/me`. */
export function mobileMeShimUrl(base: string): string {
    return `${base}/v1/mobile/me`;
}

/** The shim target of addDeliveryStop: `{base}/v1/mobile/tasks/{id}/stops`, the id (a Firestore doc id) path-escaped. */
export function addStopShimUrl(base: string, taskId: string): string {
    return `${base}/v1/mobile/tasks/${encodeURIComponent(taskId)}/stops`;
}

/** The shim target of markBroadcastRead: `{base}/v1/mobile/broadcasts/{id}/read`, the id (a Firestore doc id) path-escaped. */
export function markReadShimUrl(base: string, broadcastId: string): string {
    return `${base}/v1/mobile/broadcasts/${encodeURIComponent(broadcastId)}/read`;
}

/** The Go error envelope's code and details.reason (R48: `{error: {code, message, details}}`), "" when absent. */
export function shimError(body: unknown): { code: string; reason: string; field: string } {
    const obj = (body && typeof body === "object" ? body : {}) as Record<string, unknown>;
    const e = (obj.error && typeof obj.error === "object" ? obj.error : {}) as Record<string, unknown>;
    const d = (e.details && typeof e.details === "object" ? e.details : {}) as Record<string, unknown>;
    const fields = Array.isArray(d.fields) ? (d.fields as Array<Record<string, unknown>>) : [];
    const first = fields.length > 0 && fields[0] && typeof fields[0] === "object" ? fields[0] : {};
    return {
        code: typeof e.code === "string" ? e.code : "",
        reason: typeof d.reason === "string" ? d.reason : "",
        field: typeof d.field === "string" ? d.field : typeof first.field === "string" ? first.field : "",
    };
}

/** The `data` object of a Go success envelope (`{data: {...}}`), {} when absent. */
export function shimData(body: unknown): Record<string, unknown> {
    const obj = (body && typeof body === "object" ? body : {}) as Record<string, unknown>;
    return (obj.data && typeof obj.data === "object" ? obj.data : {}) as Record<string, unknown>;
}

/** The logger callShim writes to (firebase-functions' logger in production; a stub in tests). */
export interface ShimLogger {
    info(msg: string, data?: Record<string, unknown>): void;
    error(msg: string, data?: Record<string, unknown>): void;
}

/** One shim call as the API answered it: status 0 when the API was unreachable, redirected or the call timed out. */
export interface ShimAnswer {
    status: number;
    body: unknown;
    ms: number;
}

/** Whether body is a Go envelope (`{data}` or `{error}`, R48): anything else did not come from the Go API. */
export function isShimEnvelope(body: unknown): boolean {
    if (!body || typeof body !== "object" || Array.isArray(body)) return false;
    const obj = body as Record<string, unknown>;
    return (obj.data !== undefined && obj.data !== null) || (!!obj.error && typeof obj.error === "object");
}

/**
 * Whether callShim already logged the answer as an error: the API unreachable or redirecting (status 0), refusing the
 * shim credentials (401), failing (5xx), or answering something that is not a Go envelope (a wrong
 * LOGITRACK_API_BASE_URL). A callable logs its other unexpected answers itself, once.
 */
export function shimFailureLogged(a: ShimAnswer): boolean {
    return a.status === 0 || a.status === 401 || a.status >= 500 || !isShimEnvelope(a.body);
}

/**
 * Sends one shim call: `X-Api-Key` (the cf_shim key) and the caller's own `Authorization: Bearer <Firebase ID token>`,
 * a JSON body for POST, bounded by SHIM_TIMEOUT_MS. Redirects are refused (`redirect: "error"`, answered as status 0):
 * fetch would send `X-Api-Key` on to another origin. Logs `[shim] call` with the callable, the HTTP status and the
 * duration, and an error when the API refuses the shim credentials (401), fails (5xx), answers something that is not a
 * Go envelope, or cannot be reached (shimFailureLogged); the key and the token are never logged.
 */
export async function callShim(
    callable: string,
    method: "GET" | "POST",
    url: string,
    apiKey: string,
    bearer: string,
    body?: unknown,
    log: ShimLogger = logger,
): Promise<ShimAnswer> {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), SHIM_TIMEOUT_MS);
    const start = Date.now();
    const headers: Record<string, string> = { "X-Api-Key": apiKey, Authorization: bearer };
    if (method === "POST") headers["Content-Type"] = "application/json";
    try {
        const res = await fetch(url, {
            method,
            headers,
            body: method === "POST" ? JSON.stringify(body ?? {}) : undefined,
            redirect: "error",
            signal: controller.signal,
        });
        let parsed: unknown = null;
        try {
            parsed = await res.json();
        } catch {
            parsed = null;
        }
        const ms = Date.now() - start;
        log.info("[shim] call", { callable, status: res.status, ms });
        const e = shimError(parsed);
        if (res.status === 401) {
            log.error("[shim] the Go API refused the shim credentials (LOGITRACK_API_KEY revoked, expired or unknown, or the caller's ID token)", {
                callable,
                status: res.status,
                code: e.code,
                reason: e.reason,
            });
        } else if (res.status >= 500 || !isShimEnvelope(parsed)) {
            log.error("[shim] unexpected answer from the Go API (a server error, or not the Go API: check LOGITRACK_API_BASE_URL)", {
                callable,
                status: res.status,
                code: e.code,
                reason: e.reason,
            });
        }
        return { status: res.status, body: parsed, ms };
    } catch (err) {
        const ms = Date.now() - start;
        // fetch rejects with TypeError("fetch failed") and the reason as its cause ("unexpected redirect", a refused
        // connection, ...); an abort is AbortError. Neither carries a header value.
        const c = err instanceof Error ? (err as Error & { cause?: unknown }).cause : undefined;
        const cause = c instanceof Error ? c.message : err instanceof Error ? err.name : "";
        log.error("[shim] the Go API is unreachable (or answered with a redirect, which the shim never follows)", { callable, ms, cause });
        return { status: 0, body: null, ms };
    } finally {
        clearTimeout(timer);
    }
}

/** The legacy callable's answer (fn tripBillingOnDelivered ComputeBillingResponse) the shim maps to. */
export interface ShimComputeResponse {
    ok: boolean;
    skipped?: boolean;
    error?: string;
}

/**
 * Maps a shim target's HTTP answer onto the callable contract. Go answers `{data: {ok, skipped?, error?}}` (never an
 * amount: drivers never see billing); an error envelope `{error: {code}}` becomes `{ok: false, error: code}`. The APK
 * ignores the result (delivery_trip_repository.dart:107-115), so nothing is thrown: a failed shim call is priced by the
 * Go safety net and the mirror's trip.delivered anyway.
 */
export function mapComputeShimAnswer(status: number, body: unknown): ShimComputeResponse {
    const obj = (body && typeof body === "object" ? body : {}) as Record<string, unknown>;
    if (status >= 200 && status < 300) {
        const d = (obj.data && typeof obj.data === "object" ? obj.data : {}) as Record<string, unknown>;
        const out: ShimComputeResponse = { ok: d.ok === true };
        if (d.skipped === true) out.skipped = true;
        if (typeof d.error === "string" && d.error !== "") out.error = d.error;
        return out;
    }
    const e = (obj.error && typeof obj.error === "object" ? obj.error : {}) as Record<string, unknown>;
    const code = typeof e.code === "string" && e.code !== "" ? e.code : `http_${status}`;
    return { ok: false, error: code };
}

/**
 * Whether a callable request is a mobile call a shim forwards: a driver (the claims setDriverClaims gives: role driver
 * or a driverId), never an admin, never a forced recompute (an admin action the legacy page keeps until its web flag).
 */
export function isMobileShimCall(token: Record<string, unknown> | undefined, forceRecompute: boolean): boolean {
    if (!token || forceRecompute || token.admin === true) return false;
    return token.role === "driver" || (typeof token.driverId === "string" && token.driverId !== "");
}

/** Posts one compute shim call; network failures and timeouts answer `{ok: false, error: "go_unavailable"}`. */
export async function postShim(
    url: string,
    apiKey: string,
    bearer: string,
    body: unknown,
    log: ShimLogger = logger,
): Promise<ShimComputeResponse> {
    const a = await callShim(COMPUTE_TRIP_SHIM, "POST", url, apiKey, bearer, body ?? {}, log);
    if (a.status === 0) return { ok: false, error: "go_unavailable" };
    return mapComputeShimAnswer(a.status, a.body);
}
