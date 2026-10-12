// @vitest-environment node
import { createServer, type Server } from "node:http";
import type { AddressInfo } from "node:net";
import { describe, it, expect, vi, afterEach } from "vitest";
import {
    addStopShimUrl,
    callShim,
    computeTripShimUrl,
    isMobileShimCall,
    mapComputeShimAnswer,
    mobileMeShimUrl,
    postShim,
    shimBaseUrl,
    shimError,
    shimFailureLogged,
    shimListed,
    shimOrigin,
    type ShimLogger,
} from "./goShim";

/** A logger that records its lines. */
function recorder(): ShimLogger & { lines: { level: string; msg: string; data?: Record<string, unknown> }[] } {
    const lines: { level: string; msg: string; data?: Record<string, unknown> }[] = [];
    return {
        lines,
        info: (msg, data) => lines.push({ level: "info", msg, data }),
        error: (msg, data) => lines.push({ level: "error", msg, data }),
    };
}

describe("goShim (mv-go T38: computeTripBillingSnapshot -> POST /v1/mobile/trips/{id}/billing/compute)", () => {
    afterEach(() => vi.unstubAllGlobals());

    it("reads the API origin and the callable list", () => {
        expect(shimBaseUrl(" https://logi-api.example.test/ ")).toBe("https://logi-api.example.test");
        expect(shimBaseUrl("https://logi-api.example.test/base")).toBe("https://logi-api.example.test/base");
        expect(shimBaseUrl("")).toBe("");
        expect(shimBaseUrl("logi-api.example.test")).toBe("");
        // The key and the ID token never travel in cleartext: plain http only on localhost (the emulator).
        expect(shimBaseUrl("http://logi-api.example.test")).toBe("");
        expect(shimBaseUrl("http://logi-api.example.test:8080/")).toBe("");
        expect(shimBaseUrl("http://localhost:3302/")).toBe("http://localhost:3302");
        expect(shimBaseUrl("http://127.0.0.1:3302")).toBe("http://127.0.0.1:3302");
        expect(shimBaseUrl("http://[::1]:3302")).toBe("http://[::1]:3302");
        expect(shimBaseUrl("ftp://logi-api.example.test")).toBe("");
        expect(shimListed("setDriverClaims, computeTripBillingSnapshot", "computeTripBillingSnapshot")).toBe(true);
        expect(shimListed("computeTripBillingSnapshots", "computeTripBillingSnapshot")).toBe(false);
        expect(shimListed("", "computeTripBillingSnapshot")).toBe(false);
    });

    it("escapes the trip id (a Firestore doc id or a trip number) in the target path", () => {
        expect(computeTripShimUrl("https://api.test", "TRIP 1/x")).toBe("https://api.test/v1/mobile/trips/TRIP%201%2Fx/billing/compute");
    });

    it("forwards only a driver's plain call", () => {
        expect(isMobileShimCall({ role: "driver", driverId: "d1" }, false)).toBe(true);
        expect(isMobileShimCall({ driverId: "d1" }, false)).toBe(true);
        expect(isMobileShimCall({ role: "driver" }, true)).toBe(false); // forceRecompute is an admin action
        expect(isMobileShimCall({ role: "driver", admin: true }, false)).toBe(false);
        expect(isMobileShimCall({ role: "admin" }, false)).toBe(false);
        expect(isMobileShimCall(undefined, false)).toBe(false);
    });

    it("keeps the callable contract {ok, skipped?, error?} and never an amount", () => {
        expect(mapComputeShimAnswer(200, { data: { ok: true } })).toEqual({ ok: true });
        expect(mapComputeShimAnswer(200, { data: { ok: true, skipped: true, error: "not_mirrored", billingEstimateThb: 9 } }))
            .toEqual({ ok: true, skipped: true, error: "not_mirrored" });
        expect(mapComputeShimAnswer(200, { data: { ok: false, error: "no_rate" } })).toEqual({ ok: false, error: "no_rate" });
        expect(mapComputeShimAnswer(403, { error: { code: "not_task_owner", message: "x" } })).toEqual({ ok: false, error: "not_task_owner" });
        expect(mapComputeShimAnswer(502, null)).toEqual({ ok: false, error: "http_502" });
    });

    it("sends the cf_shim key with the caller's token and survives an unreachable API", async () => {
        const calls: { url: string; init: RequestInit }[] = [];
        vi.stubGlobal("fetch", async (url: string, init: RequestInit) => {
            calls.push({ url, init });
            return new Response(JSON.stringify({ data: { ok: true, skipped: true } }), { status: 200 });
        });
        const out = await postShim("https://api.test/v1/mobile/trips/T1/billing/compute", "test-cf-shim-key", "Bearer fb-token", {}, recorder());
        expect(out).toEqual({ ok: true, skipped: true });
        const headers = calls[0].init.headers as Record<string, string>;
        expect(headers["X-Api-Key"]).toBe("test-cf-shim-key");
        expect(headers.Authorization).toBe("Bearer fb-token");
        expect(calls[0].init.method).toBe("POST");
        vi.stubGlobal("fetch", async () => {
            throw new TypeError("network down");
        });
        expect(await postShim("https://api.test/x", "k", "Bearer t", {}, recorder())).toEqual({ ok: false, error: "go_unavailable" });
    });
});

describe("goShim (mv-go T32: setDriverClaims and addDeliveryStop)", () => {
    afterEach(() => vi.unstubAllGlobals());

    it("forwards a callable only when the origin is set and the callable listed", () => {
        expect(shimOrigin("setDriverClaims", "https://api.test/", "setDriverClaims,addDeliveryStop")).toBe("https://api.test");
        expect(shimOrigin("addDeliveryStop", "https://api.test", "setDriverClaims")).toBe("");
        expect(shimOrigin("setDriverClaims", "", "setDriverClaims")).toBe("");
        expect(shimOrigin("setDriverClaims", "https://api.test", "")).toBe("");
    });

    it("builds the targets on the public listener, escaping the Firestore task doc id", () => {
        expect(mobileMeShimUrl("https://api.test")).toBe("https://api.test/v1/mobile/me");
        expect(addStopShimUrl("https://api.test", "task/1 x")).toBe("https://api.test/v1/mobile/tasks/task%2F1%20x/stops");
    });

    it("reads the Go error envelope", () => {
        expect(shimError({ error: { code: "failed_precondition", message: "x", details: { reason: "no_primary_destination" } } }))
            .toEqual({ code: "failed_precondition", reason: "no_primary_destination", field: "" });
        expect(shimError({ error: { code: "invalid_argument", details: { fields: [{ field: "destination", reason: "required" }] } } }))
            .toEqual({ code: "invalid_argument", reason: "", field: "destination" });
        expect(shimError(null)).toEqual({ code: "", reason: "", field: "" });
    });

    it("sends GET without a body and POST as JSON, with the key and the caller's token, and logs the duration", async () => {
        const calls: { url: string; init: RequestInit }[] = [];
        vi.stubGlobal("fetch", async (url: string, init: RequestInit) => {
            calls.push({ url, init });
            return new Response(JSON.stringify({ data: { ok: true } }), { status: 200 });
        });
        const log = recorder();
        const get = await callShim("setDriverClaims", "GET", "https://api.test/v1/mobile/me", "test-cf-shim-key", "Bearer fb", undefined, log);
        expect(get.status).toBe(200);
        expect(calls[0].init.method).toBe("GET");
        expect(calls[0].init.body).toBeUndefined();
        const h = calls[0].init.headers as Record<string, string>;
        expect(h["X-Api-Key"]).toBe("test-cf-shim-key");
        expect(h.Authorization).toBe("Bearer fb");
        await callShim("addDeliveryStop", "POST", "https://api.test/v1/mobile/tasks/t/stops", "k", "Bearer fb", { destination: "A" }, log);
        expect(calls[1].init.body).toBe('{"destination":"A"}');
        expect(calls.map((c) => c.init.redirect)).toEqual(["error", "error"]);
        expect((calls[1].init.headers as Record<string, string>)["Content-Type"]).toBe("application/json");
        expect(log.lines.map((l) => [l.level, l.data?.callable, l.data?.status])).toEqual([
            ["info", "setDriverClaims", 200],
            ["info", "addDeliveryStop", 200],
        ]);
        expect(typeof log.lines[0].data?.ms).toBe("number");
    });

    it("logs a revoked key (401) without the key or the token, and an unreachable API", async () => {
        vi.stubGlobal("fetch", async () =>
            new Response(JSON.stringify({ error: { code: "invalid_token", message: "x", details: { reason: "revoked" } } }), { status: 401 }));
        const log = recorder();
        const a = await callShim("setDriverClaims", "GET", "https://api.test/v1/mobile/me", "secret-key-value", "Bearer secret-token", undefined, log);
        expect(a.status).toBe(401);
        const err = log.lines.find((l) => l.level === "error");
        expect(err?.data).toMatchObject({ callable: "setDriverClaims", status: 401, code: "invalid_token", reason: "revoked" });
        expect(JSON.stringify(log.lines)).not.toMatch(/secret-key-value|secret-token/);
        vi.stubGlobal("fetch", async () => {
            throw new TypeError("network down");
        });
        const down = await callShim("addDeliveryStop", "POST", "https://api.test/x", "k", "Bearer t", {}, log);
        expect(down.status).toBe(0);
        expect(log.lines.at(-1)).toMatchObject({ level: "error", data: { callable: "addDeliveryStop" } });
    });

    it("logs a server error and an answer that is not a Go envelope (a wrong LOGITRACK_API_BASE_URL) as errors", async () => {
        const log = recorder();
        vi.stubGlobal("fetch", async () =>
            new Response(JSON.stringify({ error: { code: "unavailable", message: "x", details: { reason: "write_back_unconfigured" } } }), { status: 503 }));
        const down = await callShim("addDeliveryStop", "POST", "https://api.test/x", "k", "Bearer t", {}, log);
        expect(shimFailureLogged(down)).toBe(true);
        vi.stubGlobal("fetch", async () => new Response("<html>404 Not Found</html>", { status: 404, headers: { "Content-Type": "text/html" } }));
        const html = await callShim("addDeliveryStop", "POST", "https://api.test/x", "k", "Bearer t", {}, log);
        expect(shimFailureLogged(html)).toBe(true);
        vi.stubGlobal("fetch", async () => new Response(JSON.stringify({ error: { code: "not_found", message: "x" } }), { status: 404 }));
        const notFound = await callShim("addDeliveryStop", "POST", "https://api.test/x", "k", "Bearer t", {}, log);
        expect(shimFailureLogged(notFound)).toBe(false); // a Go envelope: the callable decides what it means
        expect(log.lines.filter((l) => l.level === "error").map((l) => [l.data?.status, l.data?.reason])).toEqual([
            [503, "write_back_unconfigured"],
            [404, ""],
        ]);
    });

    it("never follows a redirect: the key and the token stay with the API origin", async () => {
        const seen: { path: string; key: string | undefined; auth: string | undefined }[] = [];
        const other = await listen((req, res) => {
            seen.push({ path: req.url ?? "", key: req.headers["x-api-key"] as string | undefined, auth: req.headers.authorization });
            res.writeHead(200, { "Content-Type": "application/json" }).end("{}");
        });
        const api = await listen((_req, res) => {
            res.writeHead(301, { Location: `http://127.0.0.1:${port(other)}/elsewhere` }).end();
        });
        try {
            const log = recorder();
            const a = await callShim("addDeliveryStop", "POST", `http://127.0.0.1:${port(api)}/v1/mobile/tasks/t/stops`,
                "FAKE-cf-shim-key", "Bearer FAKE-id-token", { destination: "A" }, log);
            expect(a.status).toBe(0);
            expect(seen).toEqual([]);
            expect(log.lines.at(-1)).toMatchObject({ level: "error", data: { callable: "addDeliveryStop" } });
            expect(JSON.stringify(log.lines)).not.toMatch(/FAKE-cf-shim-key|FAKE-id-token/);
        } finally {
            await close(api);
            await close(other);
        }
    });
});

/** A local HTTP server on an ephemeral loopback port. */
function listen(handler: Parameters<typeof createServer>[1]): Promise<Server> {
    return new Promise((resolve) => {
        const srv = createServer(handler);
        srv.listen(0, "127.0.0.1", () => resolve(srv));
    });
}

function port(srv: Server): number {
    return (srv.address() as AddressInfo).port;
}

function close(srv: Server): Promise<void> {
    return new Promise((resolve) => srv.close(() => resolve()));
}
