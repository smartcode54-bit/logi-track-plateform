/**
 * Test double of the api's internal listener for the BFF tests: a real HTTP server on 127.0.0.1 with
 * a programmable route table, a request log, and an Ed25519 key set published at
 * `/.well-known/jwks.json` in Go's format (kid = RFC 7638 thumbprint, alg EdDSA), so jose runs its real
 * remote-JWKS path. Tokens are signed with Go's claim set (Appendix C §C.4.1).
 */
import http from "node:http";
import type { AddressInfo } from "node:net";

import { calculateJwkThumbprint, exportJWK, generateKeyPair, SignJWT, type CryptoKey, type JWK } from "jose";

import type { BffConfig } from "../config";

export const ISSUER = "http://go.test";
export const AUDIENCE = "logitrack-test";
export const WEB_ORIGIN = "https://web.test";

export interface Recorded {
    method: string;
    url: string;
    headers: http.IncomingHttpHeaders;
    body: string;
}

export interface Reply {
    status: number;
    headers?: Record<string, string | string[]>;
    body?: unknown;
    /** Wait before answering (ms). */
    delayMs?: number;
    /** Stream these chunks with a pause between them instead of a body. */
    chunks?: { data: string; afterMs: number }[];
}

export type Route = (req: Recorded) => Reply | undefined;

export interface SigningKey {
    kid: string;
    privateKey: CryptoKey;
    jwk: JWK;
}

export async function newSigningKey(): Promise<SigningKey> {
    const { publicKey, privateKey } = await generateKeyPair("EdDSA", { extractable: true });
    const jwk = await exportJWK(publicKey);
    const kid = await calculateJwkThumbprint(jwk);
    return { kid, privateKey, jwk: { ...jwk, kid, use: "sig", alg: "EdDSA" } };
}

export interface TokenClaims {
    sub?: string;
    sid?: string;
    ver?: number;
    tid?: string;
    rol?: string;
    plt?: string[];
    dsp?: boolean;
    drv?: string;
    cs?: string[];
    /** Seconds from now until exp (default 900). */
    ttl?: number;
    iss?: string;
    aud?: string;
}

/** A Go-shaped access token signed with `key`. */
export async function signAccess(key: SigningKey, c: TokenClaims = {}): Promise<string> {
    const now = Math.floor(Date.now() / 1000);
    const { sub = "user-1", sid = "session-1", ver = 1, ttl = 900, iss = ISSUER, aud = AUDIENCE, ...rest } = c;
    return new SignJWT({ sid, ver, amr: "pwd", ...rest })
        .setProtectedHeader({ alg: "EdDSA", typ: "JWT", kid: key.kid })
        .setIssuer(iss)
        .setAudience(aud)
        .setSubject(sub)
        .setJti(crypto.randomUUID())
        .setIssuedAt(now)
        .setNotBefore(now)
        .setExpirationTime(now + ttl)
        .sign(key.privateKey);
}

export class FakeGo {
    readonly requests: Recorded[] = [];
    routes = new Map<string, Route>();
    /** Keys published in the JWKS, in order (active first). */
    published: SigningKey[] = [];
    jwksStatus = 200;
    /** JWKS documents served (they are not in `requests`). */
    jwksFetches = 0;
    private server?: http.Server;
    url = "";

    /** Registers `METHOD /path` (exact path, query ignored). */
    on(method: string, path: string, route: Route): this {
        this.routes.set(`${method} ${path}`, route);
        return this;
    }

    calls(method: string, path: string): Recorded[] {
        return this.requests.filter((r) => r.method === method && r.url.split("?")[0] === path);
    }

    async start(): Promise<this> {
        this.server = http.createServer((req, res) => {
            const chunks: Buffer[] = [];
            req.on("data", (c: Buffer) => chunks.push(c));
            req.on("end", () => {
                const rec: Recorded = { method: req.method ?? "", url: req.url ?? "", headers: req.headers, body: Buffer.concat(chunks).toString("utf8") };
                const path = rec.url.split("?")[0];
                if (path === "/.well-known/jwks.json") {
                    this.jwksFetches += 1;
                    res.writeHead(this.jwksStatus, { "Content-Type": "application/json", "Cache-Control": "public, max-age=300" });
                    res.end(JSON.stringify({ keys: this.published.map((k) => k.jwk) }));
                    return;
                }
                this.requests.push(rec);
                // Like Fiber, HEAD is served by the GET route.
                const route = this.routes.get(`${rec.method} ${path}`) ?? (rec.method === "HEAD" ? this.routes.get(`GET ${path}`) : undefined);
                const reply = route?.(rec) ?? { status: 404, body: goError("not_found") };
                const requestId = String(rec.headers["x-request-id"] ?? "go-id");
                const send = () => {
                    const headers: Record<string, string | string[]> = { "X-Request-Id": requestId, ...reply.headers };
                    if (isRecord(reply.body) && isRecord(reply.body.error) && reply.body.error.requestId === undefined) {
                        reply.body.error.requestId = requestId; // Go's envelope echoes X-Request-Id
                    }
                    if (reply.chunks) {
                        res.writeHead(reply.status, headers);
                        let at = 0;
                        for (const c of reply.chunks) {
                            at += c.afterMs;
                            setTimeout(() => res.write(c.data), at);
                        }
                        setTimeout(() => res.end(), at + 5);
                        return;
                    }
                    if (reply.body === undefined) {
                        res.writeHead(reply.status, headers);
                        res.end();
                        return;
                    }
                    const text = typeof reply.body === "string" ? reply.body : JSON.stringify(reply.body);
                    res.writeHead(reply.status, { "Content-Type": "application/json", ...headers });
                    res.end(text);
                };
                if (reply.delayMs) setTimeout(send, reply.delayMs);
                else send();
            });
        });
        await new Promise<void>((resolve) => this.server!.listen(0, "127.0.0.1", resolve));
        const { port } = this.server.address() as AddressInfo;
        this.url = `http://127.0.0.1:${port}`;
        return this;
    }

    async stop(): Promise<void> {
        if (!this.server) return;
        this.server.closeAllConnections();
        await new Promise<void>((resolve) => this.server!.close(() => resolve()));
    }

    config(over: Partial<BffConfig> = {}): BffConfig {
        return {
            goApiInternalUrl: this.url,
            goTimeoutMs: 2_000,
            cookieSecure: true,
            webPublicOrigin: WEB_ORIGIN,
            jwtIssuer: ISSUER,
            jwtAudience: AUDIENCE,
            accessTtlSeconds: 900,
            refreshTtlSeconds: 604_800,
            ...over,
        };
    }
}

function isRecord(v: unknown): v is Record<string, unknown> {
    return typeof v === "object" && v !== null && !Array.isArray(v);
}

/** Go's error envelope; FakeGo fills `requestId` with the request's X-Request-Id, as Go does. */
export function goError(code: string, details: Record<string, unknown> = {}): { error: Record<string, unknown> } {
    return { error: { code, message: code, details } };
}

/** The Set-Cookie lines of a response. */
export function setCookies(res: Response): string[] {
    return res.headers.getSetCookie();
}

/** Parses one Set-Cookie line into name, value and lower-cased attributes. */
export function parseSetCookie(line: string): { name: string; value: string; attrs: Map<string, string> } {
    const [pair, ...attrs] = line.split(";").map((s) => s.trim());
    const eq = pair.indexOf("=");
    const map = new Map<string, string>();
    for (const a of attrs) {
        const i = a.indexOf("=");
        map.set((i === -1 ? a : a.slice(0, i)).toLowerCase(), i === -1 ? "" : a.slice(i + 1));
    }
    return { name: pair.slice(0, eq), value: pair.slice(eq + 1), attrs: map };
}

/** A request to the web origin, as the browser sends it. */
export function webRequest(path: string, init: RequestInit & { cookies?: Record<string, string> } = {}): Request {
    const headers = new Headers(init.headers);
    if (init.cookies) {
        headers.set("cookie", Object.entries(init.cookies).map(([k, v]) => `${k}=${v}`).join("; "));
    }
    const { cookies: _cookies, ...rest } = init;
    void _cookies;
    return new Request(`${WEB_ORIGIN}${path}`, { ...rest, headers, ...(rest.body ? { duplex: "half" } : {}) } as RequestInit);
}

/** Same-origin mutation headers of a modern browser. */
export const SAME_ORIGIN = { Origin: WEB_ORIGIN, "Sec-Fetch-Site": "same-origin" };
