/**
 * Verifies the `lt_at` access JWT in the web server (R39; developer-spec.md §10.5 step 2, Appendix C
 * §C.4.1, §C.4.2): EdDSA only, `iss` = `JWT_ISSUER`, `aud` = `JWT_AUDIENCE`, 30 s clock tolerance,
 * against the Go JWKS at `{GO_API_INTERNAL_URL}/.well-known/jwks.json` (internal listener only).
 * jose caches the key set in memory for 5 min (the rotation runbook's web JWKS period) and refetches
 * it on an unknown `kid`, but only once 30 s have passed since its last fetch (the routine one
 * included). A `kid` first seen inside that cooldown may belong to a key the api has just started
 * signing with, so it forces one more fetch (at most one every 5 s per upstream, so forged `kid`s
 * cannot make the server fetch the JWKS on every request); only a `kid` still unknown after a fetch
 * that recent counts as a bad token. A key rotation therefore needs no web restart and signs nobody
 * out.
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
export const JWKS_COOLDOWN_MS = 30_000;
/** An unknown `kid` inside jose's cooldown forces a key-set fetch at most this often per upstream. */
export const JWKS_FORCED_RELOAD_INTERVAL_MS = 5_000;
const JWKS_TIMEOUT_MS = 5_000;

type KeySet = ReturnType<typeof createRemoteJWKSet>;
let keySets = new Map<string, KeySet>();
/** The last key-set fetch each upstream forced past jose's cooldown: when it started, and whether it failed. */
let forcedReloads = new Map<string, { at: number; failed: boolean }>();

function jwksUrl(cfg: Pick<BffConfig, "goApiInternalUrl">): URL {
    return new URL(JWKS_PATH, cfg.goApiInternalUrl);
}

/** The remote key set of an upstream, created once per process and URL. */
export function jwksFor(cfg: Pick<BffConfig, "goApiInternalUrl">): KeySet {
    const url = jwksUrl(cfg);
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

/** Tests: drop the cached key sets and the forced-fetch times. */
export function resetJwksForTests(): void {
    keySets = new Map();
    forcedReloads = new Map();
}

/**
 * One key-set fetch past jose's cooldown, for a `kid` that jose did not look up because it fetched
 * the set less than 30 s ago. A fetch already in flight is joined. Otherwise at most one starts per
 * upstream every 5 s; inside that interval the last one's outcome stands: "limited" when it read the
 * set (a `kid` it lacked is unknown), "failed" when it could not (503, nobody signed out).
 */
async function reloadPastCooldown(set: KeySet, href: string): Promise<"reloaded" | "limited" | "failed"> {
    if (set.reloading) {
        try {
            await set.reload();
            return "reloaded";
        } catch {
            return "failed";
        }
    }
    const now = Date.now();
    const last = forcedReloads.get(href);
    if (last && now >= last.at && now - last.at < JWKS_FORCED_RELOAD_INTERVAL_MS) return last.failed ? "failed" : "limited";
    const record = { at: now, failed: false };
    forcedReloads.set(href, record);
    try {
        await set.reload();
        return "reloaded";
    } catch {
        record.failed = true;
        return "failed";
    }
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

/** A jose failure as a VerifyResult. */
function failure(error: unknown): VerifyResult {
    if (error instanceof errors.JWTExpired) return { ok: false, reason: "expired" };
    if (error instanceof errors.JWKSTimeout || error instanceof errors.JWKSInvalid) return { ok: false, reason: "unavailable" };
    // A generic JOSEError is a key-set fetch or parse failure (non-200, not JSON); anything that is
    // not a JOSEError is a network error of the key-set fetch.
    if (!(error instanceof errors.JOSEError) || error.code === "ERR_JOSE_GENERIC") return { ok: false, reason: "unavailable" };
    return { ok: false, reason: "invalid" };
}

/**
 * Verifies `token`. A failure to read the key set (Go down, timeout, a malformed document) is
 * `unavailable`, never `invalid`, so an api restart does not sign anybody out; neither does a key
 * the api published after the last fetch (see the header).
 */
export async function verifyAccessToken(
    token: string,
    cfg: Pick<BffConfig, "goApiInternalUrl" | "jwtIssuer" | "jwtAudience">,
    now?: Date
): Promise<VerifyResult> {
    const set = jwksFor(cfg);
    const verify = async (): Promise<VerifyResult> => {
        const { payload } = await jwtVerify(token, set, {
            algorithms: ["EdDSA"],
            issuer: cfg.jwtIssuer,
            audience: cfg.jwtAudience,
            clockTolerance: CLOCK_TOLERANCE_SECONDS,
            requiredClaims: ["exp", "sub", "sid"],
            ...(now ? { currentDate: now } : {}),
        });
        const claims = toClaims(payload);
        return claims ? { ok: true, claims } : { ok: false, reason: "invalid" };
    };
    // Outside its cooldown jose refetches the set itself on an unknown kid, and its answer stands.
    // Inside it jose answers from a set that may predate a key rotation.
    const mayBeStale = set.coolingDown;
    try {
        return await verify();
    } catch (error) {
        if (!(error instanceof errors.JWKSNoMatchingKey) || !mayBeStale) return failure(error);
        const reload = await reloadPastCooldown(set, jwksUrl(cfg).href);
        if (reload === "failed") return { ok: false, reason: "unavailable" };
        if (reload === "limited") return failure(error);
        try {
            return await verify();
        } catch (again) {
            return failure(again);
        }
    }
}

/** Seconds of life left in verified claims at `nowMs`. */
export function secondsLeft(claims: Pick<AccessClaims, "exp">, nowMs: number = Date.now()): number {
    return claims.exp - nowMs / 1000;
}
