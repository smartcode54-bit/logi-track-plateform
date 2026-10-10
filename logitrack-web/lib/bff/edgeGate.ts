/**
 * The `/app/*` edge gate run by `proxy.ts` before any page code or data request (R39, R89;
 * developer-spec.md §10.5, Appendix E §E.8.4, Appendix C §C.2.7, §C.4.5):
 *
 * 1. No `lt_at` -> 307 `/api/auth/refresh?next=<path+query>` (that route can read `lt_rt`).
 * 2. Verify `lt_at` against the Go JWKS (lib/bff/accessToken.ts). Expired -> step 1. Invalid -> expire
 *    `lt_at`, 307 `/login?next=`. JWKS unreadable -> 503 (nobody is signed out by an api restart). A
 *    `kid` the cached key set lacks is looked up in a fresh fetch first, so a key rotation signs
 *    nobody out either.
 * 3. A principal whose only role is `driver` -> 307 `/app/unauthorized` (drivers hold only `mobile:*`).
 * 4. Capabilities are not in the token: internal `GET /v1/me` with the same bearer, cached in this
 *    process per `(sid, ver, tid)` for 60 s, so a role change (new `ver`) or a tenant switch (new
 *    `tid`) misses at once and an override (same `ver`) is seen within 60 s. A 401 `token_expired`
 *    (stale `ver`, R50) -> step 1; any other 401 -> expire `lt_at`, `/login?next=` (`reason=revoked` for
 *    `session_revoked`); a 403 -> `/app/unauthorized`; Go unreachable -> 503.
 * 5. `/app` exactly -> 307 to the role's home route (lib/routeCapabilities.ts `homeRouteFor`); the
 *    page's own `redirect()` stays as a fallback.
 * 6. lib/routeCapabilities.ts decides; an unmapped path is denied -> 307 `/app/unauthorized?from=`.
 *
 * Redirects are absolute on `WEB_PUBLIC_ORIGIN` (Next requires an absolute Location from proxy.ts, and
 * the internal host of the standalone server must never leak). Go authorises every request anyway.
 */
import "server-only";

import { verifyAccessToken, type AccessClaims } from "./accessToken";
import { bffConfig, type BffConfig } from "./config";
import { callUpstream, cookie, forwardedFor, HEADER_REQUEST_ID, loginLocation, requestIdFor } from "./http";
import { ACCESS_COOKIE, clearAccessCookie } from "./session";
import { homeRouteFor, normalizeAppPath, routeAllowed, APP_ROOT, UNAUTHORIZED_PATH } from "../routeCapabilities";

export const ME_CACHE_TTL_MS = 60_000;
export const ME_CACHE_MAX_ENTRIES = 5_000;
export const REFRESH_BOUNCE_PATH = "/api/auth/refresh";

/** The part of `GET /v1/me` the gate needs. */
export interface GatePrincipal {
    capabilities: string[];
    tenantRole?: string;
    tenantKind?: string;
    customerScope: boolean;
    dispatcher: boolean;
}

type MeResult =
    | { ok: true; me: GatePrincipal }
    | { ok: false; kind: "expired" | "signed_out" | "revoked" | "forbidden" | "unavailable" };

export interface GateDeps {
    config?: BffConfig;
    fetchImpl?: typeof fetch;
    /** ms since the epoch (tests). */
    now?: () => number;
}

let meCache = new Map<string, { expiresAt: number; me: GatePrincipal }>();
let meInflight = new Map<string, Promise<MeResult>>();

/** Tests: forget every cached `/v1/me`. */
export function resetGateCacheForTests(): void {
    meCache = new Map();
    meInflight = new Map();
}

function cacheKey(c: AccessClaims): string {
    return `${c.sid}|${c.ver}|${c.tid ?? ""}`;
}

function isRecord(v: unknown): v is Record<string, unknown> {
    return typeof v === "object" && v !== null && !Array.isArray(v);
}

function toPrincipal(data: unknown): GatePrincipal | undefined {
    if (!isRecord(data) || !Array.isArray(data.capabilities)) return undefined;
    const tenant = isRecord(data.tenant) ? data.tenant : undefined;
    return {
        capabilities: data.capabilities.filter((c): c is string => typeof c === "string"),
        tenantRole: typeof tenant?.role === "string" ? tenant.role : undefined,
        tenantKind: typeof tenant?.kind === "string" ? tenant.kind : undefined,
        customerScope: Array.isArray(data.customerScopes) && data.customerScopes.length > 0,
        dispatcher: data.dispatcher === true,
    };
}

async function fetchMe(req: Request, token: string, cfg: BffConfig, requestId: string, deps: GateDeps): Promise<MeResult> {
    const headers = new Headers({
        Accept: "application/json",
        Authorization: `Bearer ${token}`,
        [HEADER_REQUEST_ID]: requestId,
        "Accept-Encoding": "identity",
    });
    const xff = forwardedFor(req);
    if (xff) headers.set("X-Forwarded-For", xff);
    const ua = req.headers.get("user-agent");
    if (ua) headers.set("User-Agent", ua);
    const r = await callUpstream(`${cfg.goApiInternalUrl}/v1/me`, { method: "GET", headers }, { cfg, fetchImpl: deps.fetchImpl });
    if (!r.ok) return { ok: false, kind: "unavailable" };
    const res = r.res;
    let body: unknown;
    try {
        body = await res.json();
    } catch {
        body = undefined;
    }
    if (res.status === 200) {
        const me = toPrincipal(isRecord(body) ? body.data : undefined);
        return me ? { ok: true, me } : { ok: false, kind: "unavailable" };
    }
    const code = isRecord(body) && isRecord(body.error) ? body.error.code : undefined;
    if (res.status === 401) {
        if (code === "token_expired") return { ok: false, kind: "expired" };
        return { ok: false, kind: code === "session_revoked" ? "revoked" : "signed_out" };
    }
    if (res.status === 403) return { ok: false, kind: "forbidden" };
    return { ok: false, kind: "unavailable" };
}

/** `GET /v1/me` through the 60 s `(sid, ver, tid)` cache; concurrent misses share one call. */
async function principalFor(req: Request, token: string, claims: AccessClaims, cfg: BffConfig, requestId: string, deps: GateDeps): Promise<MeResult> {
    const now = deps.now?.() ?? Date.now();
    const key = cacheKey(claims);
    const hit = meCache.get(key);
    if (hit && hit.expiresAt > now) return { ok: true, me: hit.me };
    if (hit) meCache.delete(key);
    let pending = meInflight.get(key);
    if (!pending) {
        pending = fetchMe(req, token, cfg, requestId, deps).finally(() => meInflight.delete(key));
        meInflight.set(key, pending);
    }
    const result = await pending;
    if (result.ok && !meCache.has(key)) {
        if (meCache.size >= ME_CACHE_MAX_ENTRIES) {
            // Insertion order: drop the oldest entry.
            const oldest = meCache.keys().next().value;
            if (oldest !== undefined) meCache.delete(oldest);
        }
        meCache.set(key, { expiresAt: (deps.now?.() ?? Date.now()) + ME_CACHE_TTL_MS, me: result.me });
    }
    return result;
}

function redirect(cfg: BffConfig, location: string, requestId: string, setCookie?: string): Response {
    const headers = new Headers({
        Location: new URL(location, cfg.webPublicOrigin).toString(),
        "Cache-Control": "no-store",
        [HEADER_REQUEST_ID]: requestId,
    });
    if (setCookie) headers.append("Set-Cookie", setCookie);
    return new Response(null, { status: 307, headers });
}

function unavailable(requestId: string): Response {
    return new Response("Service temporarily unavailable, please retry.\nระบบไม่พร้อมใช้งานชั่วคราว กรุณาลองใหม่อีกครั้ง\n", {
        status: 503,
        headers: { "Content-Type": "text/plain; charset=utf-8", "Cache-Control": "no-store", "Retry-After": "5", [HEADER_REQUEST_ID]: requestId },
    });
}

function driverOnly(c: AccessClaims): boolean {
    return c.rol === "driver" && !c.plt?.length && !c.dsp && !c.cs?.length;
}

/**
 * The gate's decision for one `/app` request: a redirect or error Response, or `undefined` to let the
 * request through (`NextResponse.next()` in proxy.ts).
 */
export async function edgeGate(req: Request, deps: GateDeps = {}): Promise<Response | undefined> {
    const requestId = requestIdFor(req);
    let cfg: BffConfig;
    try {
        cfg = deps.config ?? bffConfig();
    } catch {
        return new Response("The web server is not configured.\n", {
            status: 500,
            headers: { "Content-Type": "text/plain; charset=utf-8", "Cache-Control": "no-store", [HEADER_REQUEST_ID]: requestId },
        });
    }
    const url = new URL(req.url);
    const pathname = normalizeAppPath(url.pathname);
    if (pathname !== APP_ROOT && !pathname.startsWith(`${APP_ROOT}/`)) return undefined;
    url.searchParams.delete("_rsc");
    const next = pathname + (url.searchParams.size > 0 ? `?${url.searchParams.toString()}` : "");
    const bounce = () => redirect(cfg, `${REFRESH_BOUNCE_PATH}?${new URLSearchParams({ next }).toString()}`, requestId);

    const token = cookie(req, ACCESS_COOKIE);
    if (!token) return bounce();
    const verified = await verifyAccessToken(token, cfg, deps.now ? new Date(deps.now()) : undefined);
    if (!verified.ok) {
        if (verified.reason === "expired") return bounce();
        if (verified.reason === "unavailable") return unavailable(requestId);
        return redirect(cfg, loginLocation(next, false), requestId, clearAccessCookie(cfg));
    }
    const claims = verified.claims;

    if (driverOnly(claims)) {
        return pathname === UNAUTHORIZED_PATH ? undefined : redirect(cfg, `${UNAUTHORIZED_PATH}?${new URLSearchParams({ from: pathname })}`, requestId);
    }

    const me = await principalFor(req, token, claims, cfg, requestId, deps);
    if (!me.ok) {
        switch (me.kind) {
            case "expired":
                return bounce();
            case "signed_out":
            case "revoked":
                return redirect(cfg, loginLocation(next, me.kind === "revoked"), requestId, clearAccessCookie(cfg));
            case "forbidden":
                return pathname === UNAUTHORIZED_PATH ? undefined : redirect(cfg, `${UNAUTHORIZED_PATH}?${new URLSearchParams({ from: pathname })}`, requestId);
            default:
                return unavailable(requestId);
        }
    }

    if (pathname === APP_ROOT) return redirect(cfg, homeRouteFor(me.me), requestId);
    if (!routeAllowed(me.me.capabilities, pathname)) {
        return redirect(cfg, `${UNAUTHORIZED_PATH}?${new URLSearchParams({ from: pathname })}`, requestId);
    }
    return undefined;
}
