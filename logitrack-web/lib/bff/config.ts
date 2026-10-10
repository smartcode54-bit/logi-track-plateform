/**
 * Runtime configuration of the BFF route handlers (`app/api/go`, `app/api/auth`) and the `proxy.ts`
 * edge gate: the developer-spec.md §16.1 `web-server` names (Appendix E §E.8.7). Read at request
 * time, never at import, because `next build` imports these modules (CI builds with sentinel values,
 * ci.yml `build-standalone`). A bad or missing value is reported by name only, never by value.
 *
 * | Name | Use | Default |
 * |---|---|---|
 * | `GO_API_INTERNAL_URL` | upstream of every BFF call and the JWKS source (http/https origin, no credentials) | required |
 * | `GO_API_INTERNAL_TIMEOUT_MS` | upstream timeout of non-SSE calls | 30000 |
 * | `SESSION_COOKIE_DOMAIN` | `Domain` of `lt_at` and `lt_rt`; host-only when empty | empty |
 * | `SESSION_COOKIE_SECURE` | `Secure` on both cookies (`true` / `false`) | true |
 * | `WEB_PUBLIC_ORIGIN` | the browser's origin: CSRF check and absolute redirects | required |
 * | `JWT_ISSUER`, `JWT_AUDIENCE` | `iss` / `aud` of `lt_at`, same values as Go (R39) | required |
 * | `JWT_ACCESS_TTL` | `lt_at` Max-Age, Go duration (`15m`) | 15m |
 * | `REFRESH_TOKEN_TTL_WEB` | `lt_rt` Max-Age, Go duration (`168h`) | 168h |
 */
import "server-only";

export interface BffConfig {
    /** Origin of the api's internal listener, without a trailing slash. */
    goApiInternalUrl: string;
    goTimeoutMs: number;
    /** `undefined`: host-only cookies. */
    cookieDomain?: string;
    cookieSecure: boolean;
    /** `scheme://host[:port]` as the browser sends it in `Origin`. */
    webPublicOrigin: string;
    jwtIssuer: string;
    jwtAudience: string;
    accessTtlSeconds: number;
    refreshTtlSeconds: number;
}

/** A missing or invalid variable; `message` and `names` carry names only. */
export class BffConfigError extends Error {
    readonly names: string[];

    constructor(names: string[]) {
        super(`web server configuration: missing or invalid ${names.join(", ")} (developer-spec.md §16.1)`);
        this.name = "BffConfigError";
        this.names = names;
    }
}

type Env = Record<string, string | undefined>;

const UNIT_NS: Record<string, number> = {
    ns: 1,
    us: 1e3,
    "µs": 1e3,
    "μs": 1e3,
    ms: 1e6,
    s: 1e9,
    m: 60e9,
    h: 3600e9,
};

/**
 * Parses a Go `time.ParseDuration` string (`15m`, `168h`, `1h30m`, `1.5h`) into whole seconds, the
 * unit of a cookie's Max-Age. `undefined` for anything Go would refuse, a sign or a zero.
 */
export function parseGoDurationSeconds(value: string): number | undefined {
    if (!/^(?:\d+(?:\.\d*)?|\.\d+)(?:ns|us|µs|μs|ms|s|m|h)(?:(?:\d+(?:\.\d*)?|\.\d+)(?:ns|us|µs|μs|ms|s|m|h))*$/.test(value)) {
        return undefined;
    }
    let ns = 0;
    for (const m of value.matchAll(/(\d+(?:\.\d*)?|\.\d+)(ns|us|µs|μs|ms|s|m|h)/g)) {
        ns += Number(m[1]) * UNIT_NS[m[2]];
    }
    const seconds = Math.floor(ns / 1e9);
    return Number.isFinite(seconds) && seconds > 0 ? seconds : undefined;
}

/** An http(s) URL that is only an origin (no credentials, path, query or fragment), normalised. */
function parseOrigin(value: string | undefined): string | undefined {
    if (!value) return undefined;
    let url: URL;
    try {
        url = new URL(value);
    } catch {
        return undefined;
    }
    if (url.protocol !== "http:" && url.protocol !== "https:") return undefined;
    if (url.username || url.password || url.search || url.hash || (url.pathname !== "/" && url.pathname !== "")) {
        return undefined;
    }
    if (value.endsWith("#") || value.endsWith("?")) return undefined;
    return url.origin;
}

/** Builds the configuration from `env`; throws BffConfigError naming every bad variable. */
export function parseBffConfig(env: Env): BffConfig {
    const bad: string[] = [];
    const goApiInternalUrl = parseOrigin(env.GO_API_INTERNAL_URL?.trim());
    if (!goApiInternalUrl) bad.push("GO_API_INTERNAL_URL");
    const webPublicOrigin = parseOrigin(env.WEB_PUBLIC_ORIGIN?.trim());
    if (!webPublicOrigin) bad.push("WEB_PUBLIC_ORIGIN");

    let goTimeoutMs = 30_000;
    const rawTimeout = env.GO_API_INTERNAL_TIMEOUT_MS?.trim();
    if (rawTimeout) {
        const n = Number(rawTimeout);
        if (/^\d+$/.test(rawTimeout) && n >= 1 && n <= 600_000) goTimeoutMs = n;
        else bad.push("GO_API_INTERNAL_TIMEOUT_MS");
    }

    const cookieDomain = env.SESSION_COOKIE_DOMAIN?.trim() || undefined;
    if (cookieDomain && !/^\.?[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)*$/.test(cookieDomain)) {
        bad.push("SESSION_COOKIE_DOMAIN");
    }

    let cookieSecure = true;
    const rawSecure = env.SESSION_COOKIE_SECURE?.trim().toLowerCase();
    if (rawSecure) {
        if (rawSecure === "true") cookieSecure = true;
        else if (rawSecure === "false") cookieSecure = false;
        else bad.push("SESSION_COOKIE_SECURE");
    }

    const jwtIssuer = env.JWT_ISSUER?.trim() ?? "";
    if (!jwtIssuer) bad.push("JWT_ISSUER");
    const jwtAudience = env.JWT_AUDIENCE?.trim() ?? "";
    if (!jwtAudience) bad.push("JWT_AUDIENCE");

    const accessTtlSeconds = env.JWT_ACCESS_TTL?.trim() ? parseGoDurationSeconds(env.JWT_ACCESS_TTL.trim()) : 15 * 60;
    if (accessTtlSeconds === undefined) bad.push("JWT_ACCESS_TTL");
    const refreshTtlSeconds = env.REFRESH_TOKEN_TTL_WEB?.trim() ? parseGoDurationSeconds(env.REFRESH_TOKEN_TTL_WEB.trim()) : 168 * 3600;
    if (refreshTtlSeconds === undefined) bad.push("REFRESH_TOKEN_TTL_WEB");

    if (bad.length > 0) throw new BffConfigError(bad);
    return {
        goApiInternalUrl: goApiInternalUrl!,
        goTimeoutMs,
        cookieDomain,
        cookieSecure,
        webPublicOrigin: webPublicOrigin!,
        jwtIssuer,
        jwtAudience,
        accessTtlSeconds: accessTtlSeconds!,
        refreshTtlSeconds: refreshTtlSeconds!,
    };
}

let cached: BffConfig | undefined;
let reported = false;

/**
 * The process configuration, parsed once from `process.env` on first use. A failure is logged once
 * (names only) and thrown on every call, so each request answers 500 until the env is fixed and the
 * server restarted.
 */
export function bffConfig(): BffConfig {
    if (cached) return cached;
    try {
        cached = parseBffConfig(process.env);
        return cached;
    } catch (error) {
        if (!reported && error instanceof BffConfigError) {
            reported = true;
            console.error(`[bff] ${error.message}`);
        }
        throw error;
    }
}

/** Tests: forget the parsed configuration. */
export function resetBffConfigForTests(): void {
    cached = undefined;
    reported = false;
}
