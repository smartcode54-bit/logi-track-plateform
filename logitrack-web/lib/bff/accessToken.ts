/**
 * Verifies the `lt_at` access JWT in the web server (R39; developer-spec.md §10.5 step 2, Appendix C
 * §C.4.1, §C.4.2): EdDSA only, `iss` = `JWT_ISSUER`, `aud` = `JWT_AUDIENCE`, 30 s clock tolerance,
 * against the Go JWKS at `{GO_API_INTERNAL_URL}/.well-known/jwks.json` (internal listener only).
 * jose caches the key set in memory for 5 min (the rotation runbook's web JWKS period) and refetches
 * on an unknown `kid` at most every 30 s, so a key rotation needs no web restart.
 *
 * Callers: `proxy.ts` (the edge gate) and `POST /api/auth/refresh` (the 120 s no-op, R78). Go
 * verifies every request again; this is a UX and bundle boundary, not the authority.
 */
import "server-only";

import { createRemoteJWKSet, errors, jwtVerify, type JWTPayload } from "jose";

import type { BffConfig } from "./config";

/** The claims of a Go access token (Appendix C §C.4.1); optional ones are absent when empty. */
export interface AccessClaims {
    sub: string;
    sid: string;
    ver: number;
    exp: number;
    tid?: string;
    rol?: string;
    plt?: string[];
    dsp?: boolean;
    drv?: string;
    cs?: string[];
    amr?: string;
}

export type VerifyResult =
    | { ok: true; claims: AccessClaims }
    /** expired: refresh; invalid: sign in again; unavailable: the JWKS could not be read. */
    | { ok: false; reason: "expired" | "invalid" | "unavailable" };

export const JWKS_PATH = "/.well-known/jwks.json";
export const CLOCK_TOLERANCE_SECONDS = 30;
const JWKS_CACHE_MAX_AGE_MS = 5 * 60_000;
const JWKS_COOLDOWN_MS = 30_000;
const JWKS_TIMEOUT_MS = 5_000;

type KeySet = ReturnType<typeof createRemoteJWKSet>;
let keySets = new Map<string, KeySet>();

/** The remote key set of an upstream, created once per process and URL. */
export function jwksFor(cfg: Pick<BffConfig, "goApiInternalUrl">): KeySet {
    const url = new URL(JWKS_PATH, cfg.goApiInternalUrl);
    let set = keySets.get(url.href);
    if (!set) {
        set = createRemoteJWKSet(url, {
            cacheMaxAge: JWKS_CACHE_MAX_AGE_MS,
            cooldownDuration: JWKS_COOLDOWN_MS,
            timeoutDuration: JWKS_TIMEOUT_MS,
        });
        keySets.set(url.href, set);
    }
    return set;
}

/** Tests: drop the cached key sets. */
export function resetJwksForTests(): void {
    keySets = new Map();
}

function isStringArray(v: unknown): v is string[] {
    return Array.isArray(v) && v.every((x) => typeof x === "string");
}

function toClaims(p: JWTPayload): AccessClaims | undefined {
    const { sub, exp } = p;
    const sid = p.sid;
    const ver = p.ver;
    if (typeof sub !== "string" || !sub || typeof sid !== "string" || !sid || typeof ver !== "number" || typeof exp !== "number") {
        return undefined;
    }
    const c: AccessClaims = { sub, sid, ver, exp };
    if (typeof p.tid === "string") c.tid = p.tid;
    if (typeof p.rol === "string") c.rol = p.rol;
    if (isStringArray(p.plt)) c.plt = p.plt;
    if (p.dsp === true) c.dsp = true;
    if (typeof p.drv === "string") c.drv = p.drv;
    if (isStringArray(p.cs)) c.cs = p.cs;
    if (typeof p.amr === "string") c.amr = p.amr;
    return c;
}

/**
 * Verifies `token`. A failure to read the key set (Go down, timeout, a malformed document) is
 * `unavailable`, never `invalid`, so an api restart does not sign anybody out.
 */
export async function verifyAccessToken(
    token: string,
    cfg: Pick<BffConfig, "goApiInternalUrl" | "jwtIssuer" | "jwtAudience">,
    now?: Date
): Promise<VerifyResult> {
    try {
        const { payload } = await jwtVerify(token, jwksFor(cfg), {
            algorithms: ["EdDSA"],
            issuer: cfg.jwtIssuer,
            audience: cfg.jwtAudience,
            clockTolerance: CLOCK_TOLERANCE_SECONDS,
            requiredClaims: ["exp", "sub", "sid"],
            ...(now ? { currentDate: now } : {}),
        });
        const claims = toClaims(payload);
        return claims ? { ok: true, claims } : { ok: false, reason: "invalid" };
    } catch (error) {
        if (error instanceof errors.JWTExpired) return { ok: false, reason: "expired" };
        if (error instanceof errors.JWKSTimeout || error instanceof errors.JWKSInvalid) return { ok: false, reason: "unavailable" };
        // A generic JOSEError is a key-set fetch or parse failure (non-200, not JSON); anything that is
        // not a JOSEError is a network error of the key-set fetch.
        if (!(error instanceof errors.JOSEError) || error.code === "ERR_JOSE_GENERIC") return { ok: false, reason: "unavailable" };
        return { ok: false, reason: "invalid" };
    }
}

/** Seconds of life left in verified claims at `nowMs`. */
export function secondsLeft(claims: Pick<AccessClaims, "exp">, nowMs: number = Date.now()): number {
    return claims.exp - nowMs / 1000;
}
