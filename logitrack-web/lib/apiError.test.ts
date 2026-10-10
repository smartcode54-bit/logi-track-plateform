// T17 (R48, R76; Appendix B §B.1.5): the UI text of an API error comes from the en/th catalogues by
// error.code; Go's message is only a fallback.
import { describe, expect, it } from "vitest";
import en from "@/context/locales/en";
import th from "@/context/locales/th";
import { API_ERROR_CODES, ApiError, apiErrorFromResponse, apiErrorKey, apiErrorText, CLIENT_ERROR_CODES } from "./apiError";

function translator(dictionary: Record<string, string>) {
    return (key: string, fallback?: string) => {
        const v = dictionary[key];
        return v !== undefined && v !== "" ? v : fallback !== undefined ? fallback : key;
    };
}

describe("API error catalogue", () => {
    it("has an English and a Thai text for every Appendix B code, the client codes and unknown", () => {
        const keys = [...API_ERROR_CODES, "unknown"].map(apiErrorKey);
        expect(keys.filter((k) => !en[k])).toEqual([]);
        expect(keys.filter((k) => !th[k])).toEqual([]);
        expect(new Set(API_ERROR_CODES).size).toBe(API_ERROR_CODES.length);
        expect(API_ERROR_CODES).toEqual(expect.arrayContaining([...CLIENT_ERROR_CODES, "token_expired", "session_revoked", "invalid_token"]));
        // No stray key: every apiError.* key is a known code.
        const known = new Set(keys);
        expect(Object.keys(en).filter((k) => k.startsWith("apiError.") && !known.has(k))).toEqual([]);
    });

    it("renders by code in the active language, falling back to the message, then to unknown", () => {
        const err = new ApiError({ status: 409, code: "billing_period_locked", message: "period locked" });
        expect(apiErrorText(err, translator(en))).toBe(en["apiError.billing_period_locked"]);
        expect(apiErrorText(err, translator(th))).toBe(th["apiError.billing_period_locked"]);
        const novel = new ApiError({ status: 409, code: "some_future_code", message: "Server text" });
        expect(apiErrorText(novel, translator(th))).toBe("Server text");
        expect(apiErrorText(new ApiError({ status: 500, code: "some_future_code", message: "" }), translator(th))).toBe(th["apiError.unknown"]);
        expect(apiErrorText(new Error("boom"), translator(en))).toBe(en["apiError.unknown"]);
    });
});

describe("apiErrorFromResponse", () => {
    it("keeps the four envelope fields and fills requestId from the header when missing", async () => {
        const res = new Response(JSON.stringify({ error: { code: "permission_denied", message: "no", details: { missingCapability: "users:view" }, requestId: "" } }), {
            status: 403,
            headers: { "X-Request-Id": "hdr-1" },
        });
        const err = await apiErrorFromResponse(res);
        expect(err).toMatchObject({ status: 403, code: "permission_denied", message: "no", details: { missingCapability: "users:view" }, requestId: "hdr-1" });
    });

    it("never invents a code from a body without the envelope", async () => {
        expect(await apiErrorFromResponse(new Response("{}", { status: 400 }))).toMatchObject({ code: "bad_response" });
        expect(await apiErrorFromResponse(new Response("", { status: 504 }))).toMatchObject({ code: "unavailable" });
    });
});
