// @vitest-environment node
import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";

import { parseProxyTarget, proxyToGo } from "../goProxy";
import { FakeGo, goError, SAME_ORIGIN, WEB_ORIGIN, webRequest } from "./fakeGo";

const go = new FakeGo();
beforeAll(async () => {
    await go.start();
});
afterAll(async () => {
    await go.stop();
});
afterEach(() => {
    go.requests.length = 0;
    go.routes.clear();
});

async function envelope(res: Response) {
    const body = (await res.json()) as { error: { code: string; message: string; details: Record<string, unknown>; requestId: string } };
    expect(Object.keys(body)).toEqual(["error"]);
    expect(Object.keys(body.error).sort()).toEqual(["code", "details", "message", "requestId"]);
    expect(body.error.requestId).toBe(res.headers.get("X-Request-Id"));
    return body.error;
}

describe("parseProxyTarget", () => {
    it("maps /api/go/v1/... to checked, decoded segments and keeps the query", () => {
        expect(parseProxyTarget(`${WEB_ORIGIN}/api/go/v1/trips/monitor?from=2026-10-01&x=a%20b`)).toEqual({
            ok: true,
            segments: ["v1", "trips", "monitor"],
            search: "?from=2026-10-01&x=a%20b",
            isEvents: false,
            isLocalUpload: false,
        });
        expect(parseProxyTarget(`${WEB_ORIGIN}/api/go/v1/customers/a%20b`)).toMatchObject({ ok: true, segments: ["v1", "customers", "a b"] });
        expect(parseProxyTarget(`${WEB_ORIGIN}/api/go/v1/events?lastEventId=7-0`)).toMatchObject({ ok: true, isEvents: true, isLocalUpload: false });
        expect(parseProxyTarget(`${WEB_ORIGIN}/api/go/v1/uploads/local/trips/a/b.jpg?X-LT-Expires=1`)).toMatchObject({ ok: true, isLocalUpload: true });
        expect(parseProxyTarget(`${WEB_ORIGIN}/api/go/v1/uploads/presign`)).toMatchObject({ ok: true, isLocalUpload: false });
    });

    it.each([
        "/api/go/v1/auth/login",
        "/api/go/v1/auth/google",
        "/api/go/v1/auth/refresh",
        "/api/go/v1/auth/tenant",
        "/api/go/v1/auth/logout",
        "/api/go/v1/auth/logout-all",
        "/api/go/v1/auth/sse-ticket",
        "/api/go/v1/auth/exchange",
        "/api/go/v1/AUTH/Login",
        "/api/go/v1/auth/log%69n",
        "/api/go/v1/bridge/firebase-token",
        "/api/go/v1/Bridge/firebase-token",
        "/api/go/v1/bridge",
        "/api/go/v2/me",
        "/api/go/healthz",
        "/api/go/v1",
    ])("blocks %s with 404 (R38)", (path) => {
        expect(parseProxyTarget(`${WEB_ORIGIN}${path}`)).toMatchObject({ ok: false, status: 404 });
    });

    it.each(["/api/go/v1/auth/google/nonce", "/api/go/v1/auth/password/forgot", "/api/go/v1/auth/password/reset", "/api/go/v1/auth/password/change"])(
        "lets %s through",
        (path) => {
            expect(parseProxyTarget(`${WEB_ORIGIN}${path}`).ok).toBe(true);
        }
    );

    it.each([
        "/api/go/v1/%2e%2e/auth/login",
        "/api/go/v1/trips/%2E%2e",
        "/api/go/v1/trips/.",
        "/api/go/v1/trips//x",
        "/api/go/v1/trips/",
        "/api/go/v1/a%2Fb",
        "/api/go/v1/a%5Cb",
        "/api/go/v1/a%0Ab",
        "/api/go/v1/a%zz",
        "/api/go/",
    ])("refuses %s with 400", (path) => {
        expect(parseProxyTarget(`${WEB_ORIGIN}${path}`)).toMatchObject({ ok: false, status: 400 });
    });
});

describe("proxyToGo", () => {
    it("forwards the cookie as the bearer with an allow-list of headers and streams the answer", async () => {
        go.on("GET", "/v1/trips/monitor", () => ({
            status: 200,
            headers: { "Set-Cookie": "evil=1", "Cache-Control": "private, max-age=10", ETag: '"v1"' },
            body: { data: { ok: true } },
        }));
        const res = await proxyToGo(
            webRequest("/api/go/v1/trips/monitor?from=1", {
                headers: {
                    Authorization: "Bearer browser-supplied",
                    "X-Request-Id": "req-12345678",
                    "X-Forwarded-For": "203.0.113.7",
                    "Accept-Language": "th",
                    "If-None-Match": '"v0"',
                    "X-Act-On-Tenant": "*",
                    "X-Custom": "dropped",
                },
                cookies: { lt_at: "access.jwt.value", lt_rt: "never-forwarded", other: "x" },
            }),
            { config: go.config() }
        );
        expect(res.status).toBe(200);
        expect(await res.json()).toEqual({ data: { ok: true } });
        expect(res.headers.get("set-cookie")).toBeNull();
        expect(res.headers.get("cache-control")).toBe("private, max-age=10");
        expect(res.headers.get("etag")).toBe('"v1"');
        expect(res.headers.get("x-request-id")).toBe("req-12345678");

        const [seen] = go.calls("GET", "/v1/trips/monitor");
        expect(seen.url).toBe("/v1/trips/monitor?from=1");
        expect(seen.headers.authorization).toBe("Bearer access.jwt.value");
        expect(seen.headers.cookie).toBeUndefined();
        expect(seen.headers["x-request-id"]).toBe("req-12345678");
        expect(seen.headers["x-forwarded-for"]).toBe("203.0.113.7");
        expect(seen.headers["accept-language"]).toBe("th");
        expect(seen.headers["if-none-match"]).toBe('"v0"');
        expect(seen.headers["x-act-on-tenant"]).toBe("*");
        expect(seen.headers["x-custom"]).toBeUndefined();
        expect(seen.headers["accept-encoding"]).toBe("identity");
    });

    it("sends no bearer without the cookie and passes Go's 401 through untouched, never refreshing (R37)", async () => {
        go.on("GET", "/v1/me", (r) => ({
            status: 401,
            body: goError(r.headers.authorization ? "token_expired" : "unauthenticated", r.headers.authorization ? { reason: "expired" } : {}),
        }));
        go.on("POST", "/v1/auth/refresh", () => ({ status: 200, body: { data: {} } }));

        const noCookie = await proxyToGo(webRequest("/api/go/v1/me"), { config: go.config() });
        expect(noCookie.status).toBe(401);
        expect((await envelope(noCookie)).code).toBe("unauthenticated");

        const expired = await proxyToGo(webRequest("/api/go/v1/me", { cookies: { lt_at: "expired.jwt.token", lt_rt: "rt" } }), { config: go.config() });
        expect(expired.status).toBe(401);
        const e = await envelope(expired);
        expect(e.code).toBe("token_expired");
        expect(e.details).toEqual({ reason: "expired" });

        expect(go.calls("GET", "/v1/me")).toHaveLength(2);
        expect(go.calls("GET", "/v1/me")[0].headers.authorization).toBeUndefined();
        expect(go.calls("POST", "/v1/auth/refresh")).toHaveLength(0);
    });

    it.each(["/api/go/v1/auth/login", "/api/go/v1/auth/refresh", "/api/go/v1/bridge/firebase-token"])(
        "answers %s with 404 not_found without calling Go (acceptance criterion)",
        async (path) => {
            const res = await proxyToGo(webRequest(path, { method: "POST", headers: SAME_ORIGIN, body: "{}" }), { config: go.config() });
            expect(res.status).toBe(404);
            expect((await envelope(res)).code).toBe("not_found");
            expect(go.requests).toHaveLength(0);
        }
    );

    it("lets the password routes through (R79) and streams the request body", async () => {
        go.on("POST", "/v1/auth/password/change", (r) => ({ status: 204, headers: { "X-Echo": r.body } }));
        const body = JSON.stringify({ passwordChangeTicket: "t", newPassword: "a long passphrase 1" });
        const res = await proxyToGo(
            webRequest("/api/go/v1/auth/password/change", { method: "POST", headers: { ...SAME_ORIGIN, "Content-Type": "application/json" }, body }),
            { config: go.config() }
        );
        expect(res.status).toBe(204);
        expect(res.body).toBeNull();
        expect(res.headers.get("x-echo")).toBe(body);
        expect(go.calls("POST", "/v1/auth/password/change")[0].headers["content-type"]).toBe("application/json");
    });

    describe("Origin check on mutations (acceptance criterion: foreign Origin -> 403)", () => {
        it.each([
            ["a foreign Origin", { Origin: "https://evil.test", "Sec-Fetch-Site": "cross-site" }],
            ["a foreign Origin without Sec-Fetch-Site", { Origin: "https://evil.test" }],
            ["the right Origin with Sec-Fetch-Site cross-site", { Origin: WEB_ORIGIN, "Sec-Fetch-Site": "cross-site" }],
            ["Origin null", { Origin: "null" }],
            ["neither header", {}],
            ["Sec-Fetch-Site same-site", { "Sec-Fetch-Site": "same-site" }],
        ])("refuses %s before calling Go", async (_label, headers) => {
            for (const method of ["POST", "PUT", "PATCH", "DELETE"]) {
                const res = await proxyToGo(webRequest("/api/go/v1/customers", { method, headers, body: method === "DELETE" ? undefined : "{}" }), {
                    config: go.config(),
                });
                expect(res.status).toBe(403);
                const e = await envelope(res);
                expect(e.code).toBe("permission_denied");
                expect(e.details).toEqual({ reason: "origin" });
            }
            expect(go.requests).toHaveLength(0);
        });

        it.each([
            ["Origin and Sec-Fetch-Site", SAME_ORIGIN],
            ["Origin only (older browser)", { Origin: WEB_ORIGIN }],
            ["Sec-Fetch-Site same-origin without Origin", { "Sec-Fetch-Site": "same-origin" }],
        ])("accepts %s", async (_label, headers) => {
            go.on("POST", "/v1/customers", () => ({ status: 201, body: { data: { id: "c1" } } }));
            const res = await proxyToGo(webRequest("/api/go/v1/customers", { method: "POST", headers, body: "{}" }), { config: go.config() });
            expect(res.status).toBe(201);
        });

        it("does not check GET and HEAD", async () => {
            go.on("GET", "/v1/customers", () => ({ status: 200, body: { data: [] } }));
            const res = await proxyToGo(webRequest("/api/go/v1/customers", { headers: { Origin: "https://evil.test" } }), { config: go.config() });
            expect(res.status).toBe(200);
            const head = await proxyToGo(webRequest("/api/go/v1/customers", { method: "HEAD" }), { config: go.config() });
            expect(head.status).toBe(200);
            expect(head.body).toBeNull();
        });
    });

    it("passes Go redirects to the browser instead of following them (GET /v1/files)", async () => {
        go.on("GET", "/v1/files", () => ({ status: 302, headers: { Location: "https://media.test/bucket/key?X-Amz-Signature=s" } }));
        const res = await proxyToGo(webRequest("/api/go/v1/files?key=a/b.jpg", { cookies: { lt_at: "t.t.t" } }), { config: go.config() });
        expect(res.status).toBe(302);
        expect(res.headers.get("location")).toBe("https://media.test/bucket/key?X-Amz-Signature=s");
    });

    it("answers 504 unavailable when Go exceeds GO_API_INTERNAL_TIMEOUT_MS and 502 when it is unreachable", async () => {
        go.on("GET", "/v1/slow", () => ({ status: 200, delayMs: 500, body: { data: 1 } }));
        const slow = await proxyToGo(webRequest("/api/go/v1/slow"), { config: go.config({ goTimeoutMs: 100 }) });
        expect(slow.status).toBe(504);
        expect((await envelope(slow)).code).toBe("unavailable");

        const down = await proxyToGo(webRequest("/api/go/v1/me"), { config: go.config({ goApiInternalUrl: "http://127.0.0.1:1" }) });
        expect(down.status).toBe(502);
        expect((await envelope(down)).code).toBe("unavailable");
    });

    it("streams v1/events without the timeout, with SSE headers and lastEventId as Last-Event-ID", async () => {
        go.on("GET", "/v1/events", () => ({
            status: 200,
            headers: { "Content-Type": "text/event-stream", "Cache-Control": "no-store" },
            chunks: [
                { data: ": ping\n\n", afterMs: 10 },
                { data: "id: 8-0\nevent: x\ndata: {}\n\n", afterMs: 250 },
            ],
        }));
        const res = await proxyToGo(webRequest("/api/go/v1/events?topics=a&lastEventId=7-0", { cookies: { lt_at: "t.t.t" } }), {
            config: go.config({ goTimeoutMs: 100 }),
        });
        expect(res.status).toBe(200);
        expect(res.headers.get("cache-control")).toBe("no-cache, no-transform");
        expect(res.headers.get("x-accel-buffering")).toBe("no");
        expect(res.headers.get("content-type")).toBe("text/event-stream");
        const text = await res.text();
        expect(text).toContain("id: 8-0");
        const [seen] = go.calls("GET", "/v1/events");
        expect(seen.url).toBe("/v1/events?topics=a");
        expect(seen.headers["last-event-id"]).toBe("7-0");
    });

    describe("local uploads (developer-spec.md §9.11, §10.14 #12)", () => {
        /** A body of `chunks` x 1 KiB, one chunk every `everyMs`: a slow uplink. */
        function throttled(chunks: number, everyMs: number): ReadableStream<Uint8Array> {
            let sent = 0;
            return new ReadableStream({
                async pull(c) {
                    if (sent === chunks) return c.close();
                    await new Promise((r) => setTimeout(r, everyMs));
                    sent += 1;
                    c.enqueue(new Uint8Array(1024).fill(65));
                },
            });
        }

        function upload(path: string, chunks: number, everyMs: number, method = "PUT") {
            return new Request(`${WEB_ORIGIN}${path}`, {
                method,
                headers: { ...SAME_ORIGIN, "Content-Type": "image/jpeg", "Content-Length": String(chunks * 1024) },
                body: throttled(chunks, everyMs),
                duplex: "half",
            } as RequestInit);
        }

        it("streams a throttled PUT that outlasts GO_API_INTERNAL_TIMEOUT_MS to Go with the browser's Content-Length", async () => {
            go.on("PUT", "/v1/uploads/local/trips/t1/p.jpg", (r) => ({ status: 200, body: { data: { size: r.body.length } } }));
            const res = await proxyToGo(upload("/api/go/v1/uploads/local/trips/t1/p.jpg?X-LT-Expires=9&X-LT-Signature=s", 6, 60), {
                config: go.config({ goTimeoutMs: 100 }),
            });
            expect(res.status).toBe(200);
            expect(await res.json()).toEqual({ data: { size: 6 * 1024 } });
            const [seen] = go.calls("PUT", "/v1/uploads/local/trips/t1/p.jpg");
            expect(seen.url).toBe("/v1/uploads/local/trips/t1/p.jpg?X-LT-Expires=9&X-LT-Signature=s");
            expect(seen.headers["content-length"]).toBe(String(6 * 1024));
            expect(seen.headers["transfer-encoding"]).toBeUndefined();
            expect(seen.headers["content-type"]).toBe("image/jpeg");
        });

        it("keeps GO_API_INTERNAL_TIMEOUT_MS and a chunked body on every other route", async () => {
            go.on("PUT", "/v1/customers/c1", () => ({ status: 200, body: { data: {} } }));
            const slow = await proxyToGo(upload("/api/go/v1/customers/c1", 6, 60), { config: go.config({ goTimeoutMs: 100 }) });
            expect(slow.status).toBe(504);
            go.requests.length = 0;
            const fast = await proxyToGo(upload("/api/go/v1/customers/c1", 2, 1), { config: go.config() });
            expect(fast.status).toBe(200);
            const [seen] = go.calls("PUT", "/v1/customers/c1");
            expect(seen.headers["content-length"]).toBeUndefined();
            expect(seen.headers["transfer-encoding"]).toBe("chunked");
        });
    });

    it("aborts the Go request when the browser goes away", async () => {
        go.on("GET", "/v1/long", () => ({ status: 200, delayMs: 1_000, body: { data: 1 } }));
        const ac = new AbortController();
        const pending = proxyToGo(new Request(`${WEB_ORIGIN}/api/go/v1/long`, { signal: ac.signal }), { config: go.config() });
        setTimeout(() => ac.abort(), 50);
        const res = await pending;
        expect(res.status).toBe(499);
    });

    it("refuses OPTIONS and unknown methods with 405 method_not_allowed", async () => {
        const res = await proxyToGo(webRequest("/api/go/v1/me", { method: "OPTIONS" }), { config: go.config() });
        expect(res.status).toBe(405);
        expect(res.headers.get("allow")).toBe("GET, HEAD, POST, PUT, PATCH, DELETE");
        expect((await envelope(res)).code).toBe("method_not_allowed");
    });

    it("re-encodes the checked segments for Go", async () => {
        go.on("GET", "/v1/customers/a%20b", () => ({ status: 200, body: { data: {} } }));
        const res = await proxyToGo(webRequest("/api/go/v1/customers/a%20b"), { config: go.config() });
        expect(res.status).toBe(200);
        expect(go.requests[0].url).toBe("/v1/customers/a%20b");
    });

    it("answers 500 internal without a configuration, naming nothing", async () => {
        const res = await proxyToGo(webRequest("/api/go/v1/me"));
        expect(res.status).toBe(500);
        expect((await envelope(res)).code).toBe("internal");
    });
});
