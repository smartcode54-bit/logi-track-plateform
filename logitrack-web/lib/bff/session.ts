/**
 * The two session cookies of the web (R36; developer-spec.md §10.4, Appendix C §C.4.5). Only the BFF
 * reads or sets them; neither is readable by script.
 *
 * | Cookie | Value | Path | Max-Age |
 * |---|---|---|---|
 * | `lt_at` | access JWT | `/` (proxy.ts reads it on `/app/*`, the proxy turns it into the bearer) | `JWT_ACCESS_TTL` |
 * | `lt_rt` | opaque refresh token | `/api/auth` (never sent to pages or `/api/go/*`) | `REFRESH_TOKEN_TTL_WEB` |
 *
 * Both `HttpOnly; SameSite=Lax`, `Secure` per `SESSION_COOKIE_SECURE`, `Domain` only when
 * `SESSION_COOKIE_DOMAIN` is set (host-only otherwise).
 */
import "server-only";

import type { BffConfig } from "./config";

export const ACCESS_COOKIE = "lt_at";
export const REFRESH_COOKIE = "lt_rt";
export const ACCESS_COOKIE_PATH = "/";
export const REFRESH_COOKIE_PATH = "/api/auth";

/** A JWT (base64url segments and dots) or a base64url refresh token: safe as a raw cookie value. */
const TOKEN_RX = /^[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)*$/;

export function isCookieSafeToken(value: unknown): value is string {
    return typeof value === "string" && value.length > 0 && value.length <= 4096 && TOKEN_RX.test(value);
}

type CookieConfig = Pick<BffConfig, "cookieDomain" | "cookieSecure" | "accessTtlSeconds" | "refreshTtlSeconds">;

function serialize(name: string, value: string, path: string, maxAge: number, cfg: CookieConfig): string {
    const parts = [`${name}=${value}`, `Path=${path}`, `Max-Age=${maxAge}`];
    if (maxAge === 0) parts.push("Expires=Thu, 01 Jan 1970 00:00:00 GMT");
    if (cfg.cookieDomain) parts.push(`Domain=${cfg.cookieDomain}`);
    parts.push("HttpOnly", "SameSite=Lax");
    if (cfg.cookieSecure) parts.push("Secure");
    return parts.join("; ");
}

export function accessCookie(token: string, cfg: CookieConfig): string {
    return serialize(ACCESS_COOKIE, token, ACCESS_COOKIE_PATH, cfg.accessTtlSeconds, cfg);
}

export function refreshCookie(token: string, cfg: CookieConfig): string {
    return serialize(REFRESH_COOKIE, token, REFRESH_COOKIE_PATH, cfg.refreshTtlSeconds, cfg);
}

export function clearAccessCookie(cfg: CookieConfig): string {
    return serialize(ACCESS_COOKIE, "", ACCESS_COOKIE_PATH, 0, cfg);
}

export function clearRefreshCookie(cfg: CookieConfig): string {
    return serialize(REFRESH_COOKIE, "", REFRESH_COOKIE_PATH, 0, cfg);
}

/** Appends Set-Cookie lines to a response's headers. */
export function setCookies(headers: Headers, ...lines: string[]): void {
    for (const line of lines) headers.append("Set-Cookie", line);
}
