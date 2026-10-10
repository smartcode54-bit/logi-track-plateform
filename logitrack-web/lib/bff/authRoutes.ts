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
 * | `POST /api/auth/refresh` `{force?}` | `POST /v1/auth/refresh` | 204: a no-op while `lt_at` has more than 120 s left unless `force` (R78), else both cookies rotated; refused (no `lt_rt`, Go 401): 401, both cleared; other failures pass through, cookies kept |
 * | `GET /api/auth/refresh?next=` | same | the proxy.ts bounce of a navigation: rotate, then 303 to `next` (an `/app` path); refused: 303 `/login?next=`, both cleared; Go unreachable or failing: 503/504, cookies kept; not a navigation: 400 |
 * | `POST /api/auth/logout` | `POST /v1/auth/logout` | revokes the session, expires both cookies, 204 |
 * | `POST /api/auth/tenant` `{tenantId}` | `POST /v1/auth/tenant` | replaces `lt_at`, 204 |
 * | `POST /api/auth/firebase-token` | `POST /v1/bridge/firebase-token` | pass-through of the custom token (P0 until TW7, R80) |
 *
 * Every POST passes the Origin check first (403 `permission_denied`, `details.reason = "origin"`).
 * Errors from Go pass through with their status, envelope, `X-Request-Id` and `Retry-After`.
 */
import "server-only";

import { createHash } from "node:crypto";

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

/** A rotation's new pair also answers requests that present the same refresh token this long after it. */
export const ROTATION_SHARE_MS = 10_000;
/** At most this many finished rotations are kept for sharing (the oldest are dropped first). */
export const ROTATION_SHARE_MAX_ENTRIES = 1_000;

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

/** A response as data: one Go call may answer several requests, and a Response body is read once. */
interface Answer {
    status: number;
    headers: [string, string][];
    body: string | null;
}

function hasNoBody(status: number): boolean {
    return status === 204 || status === 205 || status === 304;
}

async function toAnswer(res: Response): Promise<Answer> {
    return { status: res.status, headers: [...res.headers], body: hasNoBody(res.status) || res.body === null ? null : await res.text() };
}

function fromAnswer(a: Answer, ...setCookie: string[]): Response {
    const headers = new Headers(a.headers);
    setCookies(headers, ...setCookie);
    return new Response(a.body, { status: a.status, headers });
}

/** Go's answer as it came (status, body, Content-Type, X-Request-Id, Retry-After), no-store. */
async function passThroughAnswer(res: Response, ctx: Ctx): Promise<Answer> {
    const headers = new Headers();
    for (const name of PASSED_HEADERS) {
        const v = res.headers.get(name);
        if (v !== null) headers.set(name, v);
    }
    headers.set("Cache-Control", "no-store");
    headers.set(HEADER_REQUEST_ID, res.headers.get(HEADER_REQUEST_ID) ?? ctx.requestId);
    return { status: res.status, headers: [...headers], body: hasNoBody(res.status) ? null : await res.text() };
}

/** {@link passThroughAnswer} as a Response, plus Set-Cookie lines. */
async function passThrough(res: Response, ctx: Ctx, ...setCookie: string[]): Promise<Response> {
    return fromAnswer(await passThroughAnswer(res, ctx), ...setCookie);
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

type Rotated = { kind: "rotated"; accessToken: string; refreshToken: string; requestId: string };
type Rotation =
    | Rotated
    /** Go refused the refresh token (401): the session is over, both cookies are cleared. */
    | { kind: "refused"; answer: Answer; revoked: boolean }
    /** Go unreachable, timed out or failing otherwise: the session may still be valid. */
    | { kind: "failed"; answer: Answer };

function isRevoked(body: string | null): boolean {
    try {
        const parsed = JSON.parse(body ?? "") as { error?: { code?: unknown } };
        return parsed.error?.code === "session_revoked";
    } catch {
        return false;
    }
}

/** `POST /v1/auth/refresh` with the `lt_rt` value. */
async function rotate(req: Request, ctx: Ctx, refreshToken: string): Promise<Rotation> {
    const call = await goCall(req, ctx, { method: "POST", path: "/v1/auth/refresh", body: { refreshToken } });
    if (!call.ok) return { kind: "failed", answer: await toAnswer(call.response) };
    const res = call.res;
    if (res.status === 401) {
        const answer = await passThroughAnswer(res, ctx);
        return { kind: "refused", answer, revoked: isRevoked(answer.body) };
    }
    if (res.status !== 200) return { kind: "failed", answer: await passThroughAnswer(res, ctx) };
    const data = await readData(res);
    if (!data || !isCookieSafeToken(data.accessToken) || !isCookieSafeToken(data.refreshToken)) {
        return { kind: "failed", answer: await toAnswer(badUpstream(ctx)) };
    }
    return {
        kind: "rotated",
        accessToken: data.accessToken,
        refreshToken: data.refreshToken,
        requestId: res.headers.get(HEADER_REQUEST_ID) ?? ctx.requestId,
    };
}

/*
 * Rotations shared within this process. Go answers a refresh token presented again within its 30 s
 * reuse grace with a sibling and revokes the successor it issued first (Appendix C §C.4.4), so two
 * rotations of one token leave the browser signed in only if it applies the later Set-Cookie last.
 * Navigations run outside the browser's refresh lock (two tabs restored at once, a navigation racing
 * another tab's POST), so the requests of one token share one Go call here: concurrent ones wait for
 * it, and an unforced one up to ROTATION_SHARE_MS later (sent before the browser applied the first
 * Set-Cookie) gets the same pair. A forced refresh waits for a rotation in flight but never takes a
 * finished one, which may predate the claims change it is after (R78). Failures are shared only with
 * the requests waiting for them. Keys are sha256 of the token. Keeping a pair for 10 s gives nobody
 * more than Go's grace already does (whoever presents the old token within 30 s gets a valid pair),
 * and once the successor itself comes back to be rotated the pair is dropped, so a late copy of the
 * old token reaches Go (grace or reuse detection) as before. Web replicas do not share this: requests of one
 * token that land on different replicas still rely on Go's grace and on the browser applying the
 * later Set-Cookie last (at worst one forced sign-in).
 */
let rotationsInFlight = new Map<string, Promise<Rotation>>();
let rotationsDone = new Map<string, { expiresAt: number; rotation: Rotated; successor: string }>();
/** The key of a shared rotation's new refresh token -> the key of the token it replaced. */
let rotatedFrom = new Map<string, string>();

/** Tests: forget every shared rotation. */
export function resetRotationsForTests(): void {
    rotationsInFlight = new Map();
    rotationsDone = new Map();
    rotatedFrom = new Map();
}

function rotationKey(cfg: BffConfig, refreshToken: string): string {
    return createHash("sha256").update(cfg.goApiInternalUrl).update("\n").update(refreshToken).digest("hex");
}

function forgetDone(key: string): void {
    const done = rotationsDone.get(key);
    if (!done) return;
    rotationsDone.delete(key);
    if (rotatedFrom.get(done.successor) === key) rotatedFrom.delete(done.successor);
}

function rememberDone(key: string, rotation: Rotated, cfg: BffConfig, now: number): void {
    forgetDone(key);
    // One lifetime for all, so insertion order is age order: drop the expired, then the oldest over the cap.
    for (const [k, d] of rotationsDone) {
        if (d.expiresAt > now && rotationsDone.size < ROTATION_SHARE_MAX_ENTRIES) break;
        forgetDone(k);
    }
    const successor = rotationKey(cfg, rotation.refreshToken);
    rotationsDone.set(key, { expiresAt: now + ROTATION_SHARE_MS, rotation, successor });
    rotatedFrom.set(successor, key);
}

/** The rotation of `refreshToken`, through the sharing described above. */
function sharedRotation(req: Request, ctx: Ctx, refreshToken: string, forced: boolean): Promise<Rotation> {
    const now = ctx.deps.now?.() ?? Date.now();
    const key = rotationKey(ctx.cfg, refreshToken);
    const done = rotationsDone.get(key);
    if (done && done.expiresAt <= now) forgetDone(key);
    else if (done && !forced) return Promise.resolve(done.rotation);
    let pending = rotationsInFlight.get(key);
    if (!pending) {
        // The browser now rotates the successor of a shared rotation: stop handing out that pair.
        const predecessor = rotatedFrom.get(key);
        if (predecessor !== undefined) forgetDone(predecessor);
        pending = rotate(req, ctx, refreshToken)
            .then((r) => {
                if (r.kind === "rotated") rememberDone(key, r, ctx.cfg, ctx.deps.now?.() ?? Date.now());
                return r;
            })
            .finally(() => rotationsInFlight.delete(key));
        rotationsInFlight.set(key, pending);
    }
    return pending;
}

function rotatedCookies(r: Rotated, cfg: BffConfig): string[] {
    return [accessCookie(r.accessToken, cfg), refreshCookie(r.refreshToken, cfg)];
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
    const r = await sharedRotation(req, ctx, rt, force);
    if (r.kind === "failed") return fromAnswer(r.answer);
    if (r.kind === "refused") return fromAnswer(r.answer, clearAccessCookie(ctx.cfg), clearRefreshCookie(ctx.cfg));
    const headers = noStore(new Headers(), r.requestId);
    setCookies(headers, ...rotatedCookies(r, ctx.cfg));
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

/**
 * GET /api/auth/refresh?next=: the navigation form of the refresh, reached from a proxy.ts redirect
 * when `lt_at` is missing, expired or stale. It always rotates (the gate sends navigations here only
 * when the access token cannot be used), then 303 to `next`; a refused refresh clears both cookies
 * and 303s to `/login?next=`. Top-level navigations only: a request whose `Sec-Fetch-Mode` is not
 * `navigate` (a router prefetch or an RSC fetch following the gate's redirect) is 400 without
 * rotating, and Next falls back to a document navigation to this URL. Navigations run outside the
 * browser's refresh lock: concurrent ones, and one racing a tab's POST, share one rotation in this
 * process (see the rotation sharing above); Go's 30 s reuse grace covers a retry and the requests that
 * reach different web replicas (R37).
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
    const r = await sharedRotation(req, ctx, rt, false);
    if (r.kind === "rotated") return seeOther(next, r.requestId, rotatedCookies(r, ctx.cfg));
    if (r.kind === "refused") return seeOther(loginLocation(next, r.revoked), ctx.requestId, [clearAccessCookie(ctx.cfg), clearRefreshCookie(ctx.cfg)]);
    // Go unreachable or failing: the session may be fine, so keep the cookies and let the user retry.
    const status = r.answer.status === 504 ? 504 : 503;
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
