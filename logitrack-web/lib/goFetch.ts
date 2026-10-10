/**
 * The browser's only way to reach Go (developer-spec.md §10.4, §10.6; Appendix E §E.8.2; R37, R41,
 * R48, R76, R78). It calls the same-origin BFF `/api/go/v1/...`, which forwards to the api's
 * internal listener; the browser holds no Go host, no build-time API URL and no token (the BFF
 * turns the HttpOnly `lt_at` cookie into the bearer, R36).
 *
 * - Success: `{"data", "nextCursor"?, "meta"?}` is parsed; `goFetch` returns `data`,
 *   `goFetchEnvelope` the whole envelope (keyset lists). 204 yields `undefined`.
 * - Errors: the `{"error": {...}}` envelope becomes an `ApiError` (lib/apiError.ts); the UI renders
 *   `apiErrorText(error, t)` from the en/th catalogues by `code`.
 * - `signal` aborts the BFF request and, through it, the Go request (TanStack passes its own).
 * - `idempotencyKey` (`true` for a fresh uuid, or a uuid string) is sent as `Idempotency-Key`; the
 *   key is fixed per call, so the retry after a refresh carries the same key (R53, R63).
 * - 401 (R37, R78): `token_expired`, or `unauthenticated` because the access cookie has expired,
 *   runs the shared refresh (lib/sharedRefresh.ts; forced only for `details.reason =
 *   "claims_changed"`) and retries exactly once. `session_revoked`, `invalid_token`, a refused
 *   refresh or a 401 on the retry ends the session (lib/sessionEnd.ts: listeners, then
 *   `/login?next=`). A refresh that cannot reach the BFF fails the call and ends nothing.
 *   `anonymous: true` calls skip all of this.
 *
 * Browser only: the path is relative to the web origin.
 */
import { ApiError, apiErrorFromResponse, networkError } from "./apiError";
import { endSession } from "./sessionEnd";
import { sharedRefresh } from "./sharedRefresh";

export { ApiError, isApiError } from "./apiError";

/** The BFF prefix; Go paths start with /v1/ after it. */
export const GO_API_PREFIX = "/api/go";

export type GoMethod = "GET" | "HEAD" | "POST" | "PUT" | "PATCH" | "DELETE";

export type QueryValue = string | number | boolean | null | undefined;
export type QueryParams = Record<string, QueryValue | readonly QueryValue[]>;

export interface GoFetchOptions {
    method?: GoMethod;
    /** Query parameters; `undefined` and `null` are left out, arrays repeat the name. */
    query?: QueryParams;
    /** JSON body. Files never go through the BFF (presigned upload, developer-spec.md §9.4). */
    body?: unknown;
    signal?: AbortSignal;
    /** `true` sends a fresh uuid; a string must already be a uuid (Go refuses anything else, 400). */
    idempotencyKey?: string | true;
    /** Extra request headers (`If-None-Match`, `If-Match`, `X-Act-On-Tenant`, ...). */
    headers?: Record<string, string>;
    /**
     * The call needs no session (the password forgot / reset / change forms, R79): a 401 is returned
     * as an ApiError like any other error, with no refresh and no sign-out.
     */
    anonymous?: boolean;
}

export interface GoEnvelope<T> {
    data: T;
    nextCursor?: string;
    meta?: Record<string, unknown>;
}

// Credentials are the BFF's business; the key has its own option so it cannot drift between tries.
const FORBIDDEN_HEADERS = new Set(["authorization", "cookie", "idempotency-key"]);
const UUID_RX = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
// A 401 that a refresh can cure: an expired access token (claims_changed included), or no access
// cookie at all because it outlived its Max-Age (Go answers `unauthenticated` only without a token).
const REFRESHABLE = new Set(["token_expired", "unauthenticated"]);

/** A new Idempotency-Key: one per user action, reused when that action is retried. */
export function newIdempotencyKey(): string {
    return crypto.randomUUID();
}

/**
 * Builds a Go path from a template, percent-encoding each interpolated value as one segment or
 * query value: goPath`/v1/trips/${id}/photos`.
 */
export function goPath(strings: TemplateStringsArray, ...values: Array<string | number>): string {
    return strings.reduce((acc, s, i) => acc + s + (i < values.length ? encodeURIComponent(String(values[i])) : ""), "");
}

/** `/api/go` + a checked `/v1/...` path + the merged query string. Anything else is a programming error. */
export function goUrl(path: string, query?: QueryParams): string {
    if (typeof path !== "string" || !path.startsWith("/v1/")) {
        throw new TypeError("goFetch: the path must be a Go path starting with /v1/ (the BFF adds /api/go)");
    }
    const q = path.indexOf("?");
    const pathname = q === -1 ? path : path.slice(0, q);
    if (pathname.includes("#") || pathname.includes("\\")) {
        throw new TypeError("goFetch: the path must not contain '#' or '\\'");
    }
    for (const segment of pathname.slice(1).split("/")) {
        if (segment === "" || segment === "." || segment === ".." || /%(2f|5c)/i.test(segment)) {
            throw new TypeError("goFetch: empty, dot or encoded-slash path segments are refused");
        }
    }
    const params = new URLSearchParams(q === -1 ? "" : path.slice(q + 1));
    for (const [name, value] of Object.entries(query ?? {})) {
        for (const v of Array.isArray(value) ? value : [value]) {
            if (v !== undefined && v !== null) params.append(name, String(v));
        }
    }
    const qs = params.toString();
    return `${GO_API_PREFIX}${pathname}${qs ? `?${qs}` : ""}`;
}

function buildInit(options: GoFetchOptions): RequestInit {
    const method = options.method ?? "GET";
    const headers = new Headers({ Accept: "application/json" });
    for (const [name, value] of Object.entries(options.headers ?? {})) {
        if (FORBIDDEN_HEADERS.has(name.toLowerCase())) {
            throw new TypeError(`goFetch: the ${name} header is not set by the browser`);
        }
        headers.set(name, value);
    }
    let body: string | undefined;
    if (options.body !== undefined) {
        if (method === "GET" || method === "HEAD") throw new TypeError("goFetch: GET and HEAD carry no body");
        const b = options.body;
        if (
            (typeof Blob !== "undefined" && b instanceof Blob) ||
            (typeof FormData !== "undefined" && b instanceof FormData) ||
            b instanceof ArrayBuffer ||
            ArrayBuffer.isView(b) ||
            (typeof ReadableStream !== "undefined" && b instanceof ReadableStream)
        ) {
            throw new TypeError("goFetch: files are uploaded to a presigned URL, never through the BFF");
        }
        body = JSON.stringify(b);
        headers.set("Content-Type", "application/json");
    }
    if (options.idempotencyKey !== undefined) {
        if (method === "GET" || method === "HEAD") throw new TypeError("goFetch: Idempotency-Key is for mutations");
        const key = options.idempotencyKey === true ? newIdempotencyKey() : options.idempotencyKey;
        if (!UUID_RX.test(key)) throw new TypeError("goFetch: Idempotency-Key must be a uuid");
        headers.set("Idempotency-Key", key);
    }
    return { method, headers, body, credentials: "same-origin" };
}

function isAbort(error: unknown, signal?: AbortSignal): boolean {
    return Boolean(signal?.aborted) || (error instanceof DOMException && error.name === "AbortError");
}

async function send(url: string, init: RequestInit, signal?: AbortSignal): Promise<Response> {
    try {
        return await fetch(url, { ...init, signal });
    } catch (error) {
        if (isAbort(error, signal)) throw error;
        throw networkError(error);
    }
}

async function readEnvelope<T>(res: Response, method: string): Promise<GoEnvelope<T>> {
    // 304 answers a caller's own If-None-Match: no body, the caller keeps its copy.
    if (res.status === 304) return { data: undefined as T };
    if (!res.ok) throw await apiErrorFromResponse(res);
    if (method === "HEAD" || res.status === 204 || res.status === 205) {
        return { data: undefined as T };
    }
    const text = await res.text();
    if (text === "") return { data: undefined as T };
    let body: unknown;
    try {
        body = JSON.parse(text);
    } catch {
        body = undefined;
    }
    if (typeof body !== "object" || body === null || !("data" in body)) {
        throw new ApiError({
            status: res.status,
            code: "bad_response",
            message: "response is not the {data} envelope",
            requestId: res.headers.get("X-Request-Id") ?? "",
        });
    }
    const env = body as { data: T; nextCursor?: unknown; meta?: unknown };
    return {
        data: env.data,
        ...(typeof env.nextCursor === "string" && env.nextCursor !== "" ? { nextCursor: env.nextCursor } : {}),
        ...(typeof env.meta === "object" && env.meta !== null ? { meta: env.meta as Record<string, unknown> } : {}),
    };
}

/** Calls Go through the BFF and returns the whole success envelope (`data`, `nextCursor`, `meta`). */
export async function goFetchEnvelope<T>(path: string, options: GoFetchOptions = {}): Promise<GoEnvelope<T>> {
    const url = goUrl(path, options.query);
    const init = buildInit(options);
    const method = init.method as string;
    const startedAt = Date.now();

    let res = await send(url, init, options.signal);
    if (res.status !== 401 || options.anonymous) return readEnvelope<T>(res, method);

    const first = await apiErrorFromResponse(res);
    if (!REFRESHABLE.has(first.code)) {
        endSession(first);
        throw first;
    }
    const refreshed = await sharedRefresh({ force: first.details.reason === "claims_changed", since: startedAt });
    if (!refreshed) {
        endSession(first);
        throw first;
    }
    options.signal?.throwIfAborted();

    // Exactly one retry, with the same body and Idempotency-Key.
    res = await send(url, init, options.signal);
    if (res.status === 401) {
        const second = await apiErrorFromResponse(res);
        endSession(second);
        throw second;
    }
    return readEnvelope<T>(res, method);
}

/** Calls Go through the BFF and returns `data` (`undefined` for 204). */
export async function goFetch<T>(path: string, options: GoFetchOptions = {}): Promise<T> {
    return (await goFetchEnvelope<T>(path, options)).data;
}
