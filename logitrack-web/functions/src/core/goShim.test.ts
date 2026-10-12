import { describe, it, expect, vi, afterEach } from "vitest";
import {
    computeTripShimUrl,
    isMobileShimCall,
    mapComputeShimAnswer,
    postShim,
    shimBaseUrl,
    shimListed,
} from "./goShim";

describe("goShim (mv-go T38: computeTripBillingSnapshot -> POST /v1/mobile/trips/{id}/billing/compute)", () => {
    afterEach(() => vi.unstubAllGlobals());

    it("reads the API origin and the callable list", () => {
        expect(shimBaseUrl(" https://logi-api.example.test/ ")).toBe("https://logi-api.example.test");
        expect(shimBaseUrl("https://logi-api.example.test/base")).toBe("https://logi-api.example.test/base");
        expect(shimBaseUrl("")).toBe("");
        expect(shimBaseUrl("logi-api.example.test")).toBe("");
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
        const out = await postShim("https://api.test/v1/mobile/trips/T1/billing/compute", "test-cf-shim-key", "Bearer fb-token", {});
        expect(out).toEqual({ ok: true, skipped: true });
        const headers = calls[0].init.headers as Record<string, string>;
        expect(headers["X-Api-Key"]).toBe("test-cf-shim-key");
        expect(headers.Authorization).toBe("Bearer fb-token");
        expect(calls[0].init.method).toBe("POST");
        vi.stubGlobal("fetch", async () => {
            throw new TypeError("network down");
        });
        expect(await postShim("https://api.test/x", "k", "Bearer t", {})).toEqual({ ok: false, error: "go_unavailable" });
    });
});
