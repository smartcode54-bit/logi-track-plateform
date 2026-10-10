/**
 * The BFF session routes, complete list (R38; developer-spec.md §10.4, Appendix E §E.8.3, Appendix C
 * §C.4.5). Go answers this internal caller with tokens in the JSON body (`platform: "web"`) and never
 * sets cookies; only these handlers do (R36), and no token is ever returned to the browser.
 *
 * | Route | Go (internal listener) | Effect |
 * |---|---|---|
 * | `POST /api/auth/login` | `POST /v1/auth/login` | sets `lt_at` + `lt_rt`; 200 with Go's body minus the tokens |
 * | `GET /api/auth/google/nonce` | `GET /v1/auth/google/nonce` | pass-through |
 * | `POST /api/auth/google` | `POST /v1/auth/google` | as login |
 * | `POST /api/auth/refresh` `{force?}` | `POST /v1/auth/refresh` | 204: a no-op while `lt_at` has more than 120 s left unless `force` (R78), else both cookies rotated; 401 clears both |
 * | `GET /api/auth/refresh?next=` | same | the proxy.ts bounce of a navigation: rotate, then 303 to `next` (an `/app` path) or 303 `/login?next=` |
 * | `POST /api/auth/logout` | `POST /v1/auth/logout` | revokes the session, expires both cookies, 204 |
 * | `POST /api/auth/tenant` `{tenantId}` | `POST /v1/auth/tenant` | replaces `lt_at`, 204 |
 * | `POST /api/auth/firebase-token` | `POST /v1/bridge/firebase-token` | pass-through of the custom token (P0 until TW7, R80) |
 *
 * Every POST passes the Origin check first (403 `permission_denied`, `details.reason = "origin"`).
 * Errors from Go pass through with their status, envelope, `X-Request-Id` and `Retry-After`.
 */
import "server-only";

import { secondsLeft, verifyAccessToken } from "./accessToken";
import { bffConfig, type BffConfig } from "./config";
import {
    callUpstream,
    cookie,
    errorResponse,
    forwardedFor,
    HEADER_REQUEST_ID,
    loginLocation,
    misconfigured,
    originRefused,
    requestIdFor,
    sameOrigin,
    upstreamFailure,
} from "./http";
import {
    ACCESS_COOKIE,
    accessCookie,
    clearAccessCookie,
    clearRefreshCookie,
    isCookieSafeToken,
    REFRESH_COOKIE,
    refreshCookie,
    setCookies,
} from "./session";

/** POST /api/auth/refresh is a no-op while the access token has more than this left (R37, R78). */
export const REFRESH_NOOP_SECONDS = 120;
/** Where a refresh lands when `next` is missing or unacceptable. */
export const DEFAULT_NEXT = "/app";

const MAX_BODY_BYTES = 16 * 1024;
const PASSED_HEADERS = ["content-type", "retry-after"];

export interface AuthDeps {
    config?: BffConfig;
    fetchImpl?: typeof fetch;
    /** ms since the epoch (tests). */
    now?: () => number;
}

interface Ctx {
    cfg: BffConfig;
    requestId: string;
    deps: AuthDeps;
}

function context(req: Request, deps: AuthDeps): Ctx | Response {
    const requestId = requestIdFor(req);
    try {
        return { cfg: deps.config ?? bffConfig(), requestId, deps };
    } catch {
        return misconfigured(requestId);
    }
}

function noStore(headers: Headers, requestId: string): Headers {
    headers.set("Cache-Control", "no-store");
    headers.set(HEADER_REQUEST_ID, requestId);
    return headers;
}

/** The body as text, read no further than `max` bytes; `undefined` when it is longer. */
async function readBoundedText(req: Request, max: number): Promise<string | undefined> {
    if (!req.body) return "";
    const reader = req.body.getReader();
    const chunks: Uint8Array[] = [];
    let size = 0;
    for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        size += value.byteLength;
        if (size > max) {
            await reader.cancel().catch(() => undefined);
            return undefined;
        }
        chunks.push(value);
    }
    const all = new Uint8Array(size);
    let at = 0;
    for (const c of chunks) {
        all.set(c, at);
        at += c.byteLength;
    }
    return new TextDecoder().decode(all);
}

/** Reads a JSON-object body of at most 16 KiB; `{}` when empty; `undefined` when not an object. */
async function readJsonObject(req: Request): Promise<Record<string, unknown> | undefined> {
    const text = await readBoundedText(req, MAX_BODY_BYTES);
    if (text === undefined) return undefined;
    if (text.trim() === "") return {};
    try {
        const v: unknown = JSON.parse(text);
        return typeof v === "object" && v !== null && !Array.isArray(v) ? (v as Record<string, unknown>) : undefined;
    } catch {
        return undefined;
    }
}

function badBody(requestId: string): Response {
    return errorResponse(400, "bad_request", "request body must be a JSON object of at most 16 KiB", requestId);
}

interface GoCall {
    method: "GET" | "POST";
    path: string;
    body?: unknown;
    bearer?: string;
}

/**
 * Calls Go for a session route. Only the timeout applies, never the browser's abort: a refresh whose
 * answer is lost after Go rotated would leave the browser with a superseded refresh token (the 30 s
 * reuse grace covers a retry, an abort would not).
 */
async function goCall(req: Request, ctx: Ctx, call: GoCall): Promise<{ ok: true; res: Response } | { ok: false; response: Response }> {
    const headers = new Headers({ Accept: "application/json", [HEADER_REQUEST_ID]: ctx.requestId, "Accept-Encoding": "identity" });
    for (const name of ["user-agent", "accept-language"]) {
        const v = req.headers.get(name);
        if (v !== null) headers.set(name, v);
    }
    const xff = forwardedFor(req);
    if (xff) headers.set("X-Forwarded-For", xff);
    if (call.bearer) headers.set("Authorization", `Bearer ${call.bearer}`);
    let body: string | undefined;
    if (call.body !== undefined) {
        body = JSON.stringify(call.body);
        headers.set("Content-Type", "application/json");
    }
    const result = await callUpstream(`${ctx.cfg.goApiInternalUrl}${call.path}`, { method: call.method, headers, body }, {
        cfg: ctx.cfg,
        fetchImpl: ctx.deps.fetchImpl,
    });
    if (!result.ok) {
        console.warn(`[bff] ${call.method} ${call.path} ${result.kind} request_id=${ctx.requestId}`);
        return { ok: false, response: upstreamFailure(result.kind, ctx.requestId) };
    }
    return { ok: true, res: result.res };
}

/** Go's answer as it came (status, body, Content-Type, X-Request-Id, Retry-After), no-store, plus Set-Cookie lines. */
async function passThrough(res: Response, ctx: Ctx, ...setCookie: string[]): Promise<Response> {
    const headers = new Headers();
    for (const name of PASSED_HEADERS) {
        const v = res.headers.get(name);
        if (v !== null) headers.set(name, v);
    }
    headers.set("Cache-Control", "no-store");
    headers.set(HEADER_REQUEST_ID, res.headers.get(HEADER_REQUEST_ID) ?? ctx.requestId);
    setCookies(headers, ...setCookie);
    const body = res.status === 204 || res.status === 304 ? null : await res.text();
    return new Response(body, { status: res.status, headers });
}

function badUpstream(ctx: Ctx): Response {
    return errorResponse(502, "unavailable", "unexpected answer from the api", ctx.requestId);
}

/** `{"data": {...}}` of a Go success body, or undefined. */
async function readData(res: Response): Promise<Record<string, unknown> | undefined> {
    try {
        const body: unknown = await res.json();
        if (typeof body !== "object" || body === null) return undefined;
        const data = (body as { data?: unknown }).data;
        return typeof data === "object" && data !== null && !Array.isArray(data) ? (data as Record<string, unknown>) : undefined;
    } catch {
        return undefined;
    }
}

/** Login and Google sign-in: forward with `platform: "web"`, set both cookies, return the body without tokens. */
async function signIn(req: Request, deps: AuthDeps, path: "/v1/auth/login" | "/v1/auth/google", pick: string[]): Promise<Response> {
    const ctx = context(req, deps);
    if (ctx instanceof Response) return ctx;
    if (!sameOrigin(req, ctx.cfg)) return originRefused(ctx.requestId);
    const input = await readJsonObject(req);
    if (!input) return badBody(ctx.requestId);
    // Web sessions have no install id or app version; the platform is fixed by the BFF.
    const body: Record<string, unknown> = { platform: "web" };
    for (const k of pick) if (input[k] !== undefined) body[k] = input[k];

    const call = await goCall(req, ctx, { method: "POST", path, body });
    if (!call.ok) return call.response;
    const res = call.res;
    // A must-change-password user gets 403 password_change_required with a ticket and no cookie (R79).
    if (res.status !== 200) return passThrough(res, ctx);
    const data = await readData(res);
    if (!data || !isCookieSafeToken(data.accessToken) || !isCookieSafeToken(data.refreshToken)) return badUpstream(ctx);
    const { accessToken, refreshToken, ...rest } = data;
    const headers = noStore(new Headers({ "Content-Type": "application/json; charset=utf-8" }), res.headers.get(HEADER_REQUEST_ID) ?? ctx.requestId);
    setCookies(headers, accessCookie(accessToken, ctx.cfg), refreshCookie(refreshToken, ctx.cfg));
    return new Response(JSON.stringify({ data: rest }), { status: 200, headers });
}

/** POST /api/auth/login `{email, password, geo?}`. */
export function login(req: Request, deps: AuthDeps = {}): Promise<Response> {
    return signIn(req, deps, "/v1/auth/login", ["email", "password", "geo"]);
}

/** POST /api/auth/google `{idToken, nonce}` (GIS button, NEXT_PUBLIC_GOOGLE_OIDC_CLIENT_ID). */
export function google(req: Request, deps: AuthDeps = {}): Promise<Response> {
    return signIn(req, deps, "/v1/auth/google", ["idToken", "nonce"]);
}

/** GET /api/auth/google/nonce: the single-use nonce for the GIS button (Appendix C §C.4.10). */
export async function googleNonce(req: Request, deps: AuthDeps = {}): Promise<Response> {
    const ctx = context(req, deps);
    if (ctx instanceof Response) return ctx;
    const call = await goCall(req, ctx, { method: "GET", path: "/v1/auth/google/nonce" });
    if (!call.ok) return call.response;
    return passThrough(call.res, ctx);
}

type Rotation =
    | { kind: "rotated"; setCookie: string[]; requestId: string }
    | { kind: "refused"; res: Response; setCookie: string[] }
    | { kind: "failed"; response: Response };

/** `POST /v1/auth/refresh` with the `lt_rt` value: new cookies, or a refusal that clears both. */
async function rotate(req: Request, ctx: Ctx, refreshToken: string): Promise<Rotation> {
    const call = await goCall(req, ctx, { method: "POST", path: "/v1/auth/refresh", body: { refreshToken } });
    if (!call.ok) return { kind: "failed", response: call.response };
    const res = call.res;
    if (res.status === 401) return { kind: "refused", res, setCookie: [clearAccessCookie(ctx.cfg), clearRefreshCookie(ctx.cfg)] };
    if (res.status !== 200) return { kind: "failed", response: await passThrough(res, ctx) };
    const data = await readData(res);
    if (!data || !isCookieSafeToken(data.accessToken) || !isCookieSafeToken(data.refreshToken)) {
        return { kind: "failed", response: badUpstream(ctx) };
    }
    return {
        kind: "rotated",
        setCookie: [accessCookie(data.accessToken, ctx.cfg), refreshCookie(data.refreshToken, ctx.cfg)],
        requestId: res.headers.get(HEADER_REQUEST_ID) ?? ctx.requestId,
    };
}

/**
 * POST /api/auth/refresh `{force?}`, called only by the browser's shared refresh (lib/sharedRefresh.ts).
 * 204 when the session has a fresh access cookie (rotated here, or still valid for more than 120 s
 * and not forced); 401 with both cookies expired when the refresh token is missing, expired, revoked
 * or reused; any other failure passes through and keeps the cookies (the session may still be valid).
 */
export async function refreshPost(req: Request, deps: AuthDeps = {}): Promise<Response> {
    const ctx = context(req, deps);
    if (ctx instanceof Response) return ctx;
    if (!sameOrigin(req, ctx.cfg)) return originRefused(ctx.requestId);
    const input = await readJsonObject(req);
    if (!input) return badBody(ctx.requestId);
    const force = input.force === true;

    const rt = cookie(req, REFRESH_COOKIE);
    if (!rt) {
        const headers = new Headers();
        setCookies(headers, clearAccessCookie(ctx.cfg), clearRefreshCookie(ctx.cfg));
        return errorResponse(401, "unauthenticated", "no refresh token", ctx.requestId, {}, headers);
    }
    if (!force) {
        const at = cookie(req, ACCESS_COOKIE);
        if (at) {
            const v = await verifyAccessToken(at, ctx.cfg, deps.now ? new Date(deps.now()) : undefined);
            if (v.ok && secondsLeft(v.claims, deps.now?.() ?? Date.now()) > REFRESH_NOOP_SECONDS) {
                // Another tab, or a navigation, already rotated: nothing to do (R37).
                return new Response(null, { status: 204, headers: noStore(new Headers(), ctx.requestId) });
            }
        }
    }
    const r = await rotate(req, ctx, rt);
    if (r.kind === "failed") return r.response;
    if (r.kind === "refused") return passThrough(r.res, ctx, ...r.setCookie);
    const headers = noStore(new Headers(), r.requestId);
    setCookies(headers, ...r.setCookie);
    return new Response(null, { status: 204, headers });
}

/**
 * The `next` of a refresh navigation: a same-origin path in the `/app` route group (the proxy.ts
 * matcher), else `/app`. Absolute URLs, `//host`, backslashes and control characters are refused.
 */
export function safeNext(raw: string | null): string {
    if (!raw || !raw.startsWith("/") || raw.startsWith("//") || raw.includes("\\")) return DEFAULT_NEXT;
    for (let i = 0; i < raw.length; i++) {
        const c = raw.charCodeAt(i);
        if (c < 0x20 || c === 0x7f) return DEFAULT_NEXT;
    }
    let url: URL;
    try {
        url = new URL(raw, "http://web.invalid");
    } catch {
        return DEFAULT_NEXT;
    }
    if (url.origin !== "http://web.invalid") return DEFAULT_NEXT;
    if (url.pathname !== "/app" && !url.pathname.startsWith("/app/")) return DEFAULT_NEXT;
    return url.pathname + url.search;
}

function seeOther(location: string, requestId: string, setCookie: string[] = []): Response {
    const headers = noStore(new Headers({ Location: location }), requestId);
    setCookies(headers, ...setCookie);
    return new Response(null, { status: 303, headers });
}

async function revokedReason(res: Response): Promise<boolean> {
    try {
        const body = (await res.clone().json()) as { error?: { code?: unknown } };
        return body.error?.code === "session_revoked";
    } catch {
        return false;
    }
}

/**
 * GET /api/auth/refresh?next=: the navigation form of the refresh, reached from a proxy.ts redirect
 * when `lt_at` is missing, expired or stale. It always rotates (the gate sends navigations here only
 * when the access token cannot be used), then 303 to `next`; a refused refresh clears both cookies
 * and 303s to `/login?next=`. Top-level navigations only: a request whose `Sec-Fetch-Mode` is not
 * `navigate` (a router prefetch or an RSC fetch following the gate's redirect) is 400 without
 * rotating, and Next falls back to a document navigation to this URL. A rotation outside the browser's
 * refresh lock is absorbed by Go's 30 s reuse grace (R37).
 */
export async function refreshGet(req: Request, deps: AuthDeps = {}): Promise<Response> {
    const ctx = context(req, deps);
    if (ctx instanceof Response) return ctx;
    const mode = req.headers.get("sec-fetch-mode");
    if (mode !== null && mode !== "navigate") {
        return errorResponse(400, "bad_request", "the refresh bounce serves top-level navigations only", ctx.requestId);
    }
    const next = safeNext(new URL(req.url).searchParams.get("next"));
    const rt = cookie(req, REFRESH_COOKIE);
    if (!rt) return seeOther(loginLocation(next), ctx.requestId, [clearAccessCookie(ctx.cfg)]);
    const r = await rotate(req, ctx, rt);
    if (r.kind === "rotated") return seeOther(next, r.requestId, r.setCookie);
    if (r.kind === "refused") return seeOther(loginLocation(next, await revokedReason(r.res)), ctx.requestId, r.setCookie);
    // Go unreachable or failing: the session may be fine, so keep the cookies and let the user retry.
    const status = r.response.status === 504 ? 504 : 503;
    return new Response("Service temporarily unavailable, please retry.\nระบบไม่พร้อมใช้งานชั่วคราว กรุณาลองใหม่อีกครั้ง\n", {
        status,
        headers: noStore(new Headers({ "Content-Type": "text/plain; charset=utf-8", "Retry-After": "5" }), ctx.requestId),
    });
}

/**
 * POST /api/auth/logout: revokes the current session at Go (bearer from `lt_at`, `refreshToken` from
 * `lt_rt`) and expires both cookies whatever Go answers. 204, or Go's 5xx (cookies still cleared).
 */
export async function logout(req: Request, deps: AuthDeps = {}): Promise<Response> {
    const ctx = context(req, deps);
    if (ctx instanceof Response) return ctx;
    if (!sameOrigin(req, ctx.cfg)) return originRefused(ctx.requestId);
    const clear = [clearAccessCookie(ctx.cfg), clearRefreshCookie(ctx.cfg)];
    const at = cookie(req, ACCESS_COOKIE);
    const rt = cookie(req, REFRESH_COOKIE);
    if (at || rt) {
        const call = await goCall(req, ctx, { method: "POST", path: "/v1/auth/logout", bearer: at, body: rt ? { refreshToken: rt } : {} });
        if (!call.ok) {
            const headers = new Headers(call.response.headers);
            setCookies(headers, ...clear);
            return new Response(call.response.body, { status: call.response.status, headers });
        }
        if (call.res.status >= 500) return passThrough(call.res, ctx, ...clear);
        await call.res.body?.cancel().catch(() => undefined);
    }
    const headers = noStore(new Headers(), ctx.requestId);
    setCookies(headers, ...clear);
    return new Response(null, { status: 204, headers });
}

/** POST /api/auth/tenant `{tenantId}`: switches the session's active tenant and replaces `lt_at` (R83). */
export async function tenant(req: Request, deps: AuthDeps = {}): Promise<Response> {
    const ctx = context(req, deps);
    if (ctx instanceof Response) return ctx;
    if (!sameOrigin(req, ctx.cfg)) return originRefused(ctx.requestId);
    const input = await readJsonObject(req);
    if (!input) return badBody(ctx.requestId);
    const call = await goCall(req, ctx, {
        method: "POST",
        path: "/v1/auth/tenant",
        bearer: cookie(req, ACCESS_COOKIE),
        body: { tenantId: input.tenantId },
    });
    if (!call.ok) return call.response;
    if (call.res.status !== 200) return passThrough(call.res, ctx);
    const data = await readData(call.res);
    if (!data || !isCookieSafeToken(data.accessToken)) return badUpstream(ctx);
    const headers = noStore(new Headers(), call.res.headers.get(HEADER_REQUEST_ID) ?? ctx.requestId);
    setCookies(headers, accessCookie(data.accessToken, ctx.cfg));
    return new Response(null, { status: 204, headers });
}

/**
 * POST /api/auth/firebase-token: the Firebase custom token for `signInWithCustomToken` (R8, R40, R80),
 * from the P0 login switch until TW7. Go answers 404 unless AUTH_FIREBASE_BRIDGE_MODE is web or both.
 */
export async function firebaseToken(req: Request, deps: AuthDeps = {}): Promise<Response> {
    const ctx = context(req, deps);
    if (ctx instanceof Response) return ctx;
    if (!sameOrigin(req, ctx.cfg)) return originRefused(ctx.requestId);
    const call = await goCall(req, ctx, { method: "POST", path: "/v1/bridge/firebase-token", bearer: cookie(req, ACCESS_COOKIE) });
    if (!call.ok) return call.response;
    return passThrough(call.res, ctx);
}
