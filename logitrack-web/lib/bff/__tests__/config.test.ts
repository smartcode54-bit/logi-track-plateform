// @vitest-environment node
import { describe, expect, it } from "vitest";

import { BffConfigError, parseBffConfig, parseGoDurationSeconds } from "../config";

const base = {
    GO_API_INTERNAL_URL: "http://api:8080",
    WEB_PUBLIC_ORIGIN: "https://logitrack.example",
    JWT_ISSUER: "https://api.logitrack.example",
    JWT_AUDIENCE: "logitrack-prod",
};

describe("parseGoDurationSeconds (Go time.ParseDuration)", () => {
    it.each([
        ["15m", 900],
        ["168h", 604_800],
        ["1h30m", 5_400],
        ["1.5h", 5_400],
        ["90s", 90],
        ["2160h", 7_776_000],
        ["1500ms", 1],
    ])("%s -> %d s", (v, want) => {
        expect(parseGoDurationSeconds(v)).toBe(want);
    });

    it.each(["", "15", "m", "-15m", "+15m", "15 m", "15d", "0s", "500ms", "ci-sentinel-access-ttl"])("refuses %j", (v) => {
        expect(parseGoDurationSeconds(v)).toBeUndefined();
    });
});

describe("parseBffConfig", () => {
    it("reads the §16.1 web-server names with their defaults", () => {
        expect(parseBffConfig(base)).toEqual({
            goApiInternalUrl: "http://api:8080",
            goTimeoutMs: 30_000,
            cookieDomain: undefined,
            cookieSecure: true,
            webPublicOrigin: "https://logitrack.example",
            jwtIssuer: "https://api.logitrack.example",
            jwtAudience: "logitrack-prod",
            accessTtlSeconds: 900,
            refreshTtlSeconds: 604_800,
        });
    });

    it("reads the optional values", () => {
        const c = parseBffConfig({
            ...base,
            GO_API_INTERNAL_URL: "http://127.0.0.1:8080/",
            WEB_PUBLIC_ORIGIN: "http://localhost:3000",
            GO_API_INTERNAL_TIMEOUT_MS: "15000",
            SESSION_COOKIE_DOMAIN: "logitrack.example",
            SESSION_COOKIE_SECURE: "false",
            JWT_ACCESS_TTL: "10m",
            REFRESH_TOKEN_TTL_WEB: "72h",
        });
        expect(c).toMatchObject({
            goApiInternalUrl: "http://127.0.0.1:8080",
            webPublicOrigin: "http://localhost:3000",
            goTimeoutMs: 15_000,
            cookieDomain: "logitrack.example",
            cookieSecure: false,
            accessTtlSeconds: 600,
            refreshTtlSeconds: 259_200,
        });
    });

    it("names every bad variable and never echoes a value", () => {
        const secret = "s3cr3t-pa55word";
        let error: unknown;
        try {
            parseBffConfig({
                GO_API_INTERNAL_URL: `http://user:${secret}@api:8080`,
                WEB_PUBLIC_ORIGIN: "https://web.example/path",
                GO_API_INTERNAL_TIMEOUT_MS: "soon",
                SESSION_COOKIE_DOMAIN: "bad domain;",
                SESSION_COOKIE_SECURE: "yes",
                JWT_ACCESS_TTL: "15 minutes",
                REFRESH_TOKEN_TTL_WEB: `${secret}h`,
            });
        } catch (e) {
            error = e;
        }
        expect(error).toBeInstanceOf(BffConfigError);
        const e = error as BffConfigError;
        expect(e.names).toEqual([
            "GO_API_INTERNAL_URL",
            "WEB_PUBLIC_ORIGIN",
            "GO_API_INTERNAL_TIMEOUT_MS",
            "SESSION_COOKIE_DOMAIN",
            "SESSION_COOKIE_SECURE",
            "JWT_ISSUER",
            "JWT_AUDIENCE",
            "JWT_ACCESS_TTL",
            "REFRESH_TOKEN_TTL_WEB",
        ]);
        expect(e.message).not.toContain(secret);
        expect(e.message).not.toContain("web.example");
    });

    it.each(["ftp://api:21", "api:8080", "http://api:8080/v1", "http://api:8080?x=1"])("refuses GO_API_INTERNAL_URL %s", (v) => {
        expect(() => parseBffConfig({ ...base, GO_API_INTERNAL_URL: v })).toThrow(BffConfigError);
    });
});
