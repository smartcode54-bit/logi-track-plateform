// @vitest-environment node
// T54R (issue #74; R44, R77; developer-spec.md §10.4, Appendix E §E.8.3): the anonymous BFF form handlers
// POST /api/forms/waitlist and POST /api/forms/partner-interest against a fake of Go's internal listener.
// AC: a cross-origin post gets 403 without reaching Go; no cookie is read and no Authorization is sent;
// the visitor's X-Forwarded-For reaches Go, whose answer (201, 422, 429 + Retry-After) passes through.
import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";

import { formOptions, MAX_FORM_BYTES, postForm } from "../forms";
import * as waitlistRoute from "@/app/api/forms/waitlist/route";
import * as partnerRoute from "@/app/api/forms/partner-interest/route";
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

const WAITLIST = { email: "visitor@example.test", name: "Visitor", countryCode: "+66", phone: "081 234 5678", website: "" };

function post(path: string, body: unknown, headers: Record<string, string> = SAME_ORIGIN, cookies?: Record<string, string>) {
    return webRequest(path, {
        method: "POST",
        headers: { "Content-Type": "application/json", ...headers },
        body: typeof body === "string" ? body : JSON.stringify(body),
        cookies,
    });
}

async function errorOf(res: Response) {
    const body = (await res.json()) as { error: { code: string; details: Record<string, unknown>; requestId: string } };
    expect(body.error.requestId).toBe(res.headers.get("X-Request-Id"));
    return body.error;
}

describe("POST /api/forms/*", () => {
    it("forwards a same-origin post to Go's internal route with the visitor's address, no cookie and no bearer", async () => {
        go.on("POST", "/v1/waitlist", () => ({ status: 201, headers: { "Set-Cookie": "evil=1", "X-Internal": "x" }, body: { data: { received: true } } }));
        const res = await postForm(
            post("/api/forms/waitlist", WAITLIST, { ...SAME_ORIGIN, "X-Forwarded-For": "203.0.113.7", Authorization: "Bearer stolen", "Accept-Language": "th" }, { lt_at: "access", lt_rt: "refresh" }),
            "waitlist",
            { config: go.config() }
        );
        expect(res.status).toBe(201);
        expect(await res.json()).toEqual({ data: { received: true } });
        expect(res.headers.get("Cache-Control")).toBe("no-store");
        expect(res.headers.get("Set-Cookie")).toBeNull();
        expect(res.headers.get("X-Internal")).toBeNull();

        const [call] = go.calls("POST", "/v1/waitlist");
        expect(JSON.parse(call.body)).toEqual(WAITLIST);
        expect(call.headers["x-forwarded-for"]).toBe("203.0.113.7");
        expect(call.headers["content-type"]).toBe("application/json");
        expect(call.headers["accept-language"]).toBe("th");
        expect(call.headers.authorization).toBeUndefined();
        expect(call.headers.cookie).toBeUndefined();
        expect(call.headers["x-request-id"]).toBe(res.headers.get("X-Request-Id"));
    });

    it("routes the partner-interest form to its own Go route", async () => {
        go.on("POST", "/v1/partner-interest", () => ({ status: 201, body: { data: { received: true } } }));
        const res = await postForm(post("/api/forms/partner-interest", { companyName: "Co", contactName: "A", email: "a@example.test" }), "partner-interest", {
            config: go.config(),
        });
        expect(res.status).toBe(201);
        expect(go.calls("POST", "/v1/partner-interest")).toHaveLength(1);
        expect(go.calls("POST", "/v1/waitlist")).toHaveLength(0);
    });

    it.each([
        ["another Origin", { Origin: "https://evil.test" }],
        ["a cross-site fetch", { "Sec-Fetch-Site": "cross-site" }],
        ["a same-site but other-origin fetch", { Origin: WEB_ORIGIN, "Sec-Fetch-Site": "same-site" }],
        ["no Origin and no Sec-Fetch-Site", {}],
    ])("refuses %s with 403 before Go is called (AC)", async (_name, headers) => {
        go.on("POST", "/v1/waitlist", () => ({ status: 201, body: { data: { received: true } } }));
        const res = await postForm(post("/api/forms/waitlist", WAITLIST, headers as Record<string, string>), "waitlist", { config: go.config() });
        expect(res.status).toBe(403);
        const error = await errorOf(res);
        expect(error.code).toBe("permission_denied");
        expect(error.details).toEqual({ reason: "origin" });
        expect(go.requests).toHaveLength(0);
    });

    it("passes Go's refusals through: 422 with the fields, 429 with Retry-After", async () => {
        go.on("POST", "/v1/waitlist", (req) =>
            JSON.parse(req.body).email === "bad"
                ? { status: 422, body: goError("invalid_argument", { fields: [{ field: "email", reason: "invalid" }] }) }
                : { status: 429, headers: { "Retry-After": "3600" }, body: goError("resource_exhausted", { bucket: "public_form_ip" }) }
        );
        const invalid = await postForm(post("/api/forms/waitlist", { ...WAITLIST, email: "bad" }), "waitlist", { config: go.config() });
        expect(invalid.status).toBe(422);
        expect((await errorOf(invalid)).details).toEqual({ fields: [{ field: "email", reason: "invalid" }] });

        const limited = await postForm(post("/api/forms/waitlist", WAITLIST), "waitlist", { config: go.config() });
        expect(limited.status).toBe(429);
        expect(limited.headers.get("Retry-After")).toBe("3600");
        expect((await errorOf(limited)).code).toBe("resource_exhausted");
    });

    it("refuses a body that is not one JSON object, or is too large, without calling Go", async () => {
        for (const body of ["[1]", "not json", '"x"', "", "{} {}"]) {
            const res = await postForm(post("/api/forms/waitlist", body), "waitlist", { config: go.config() });
            expect(res.status, body).toBe(400);
            expect((await errorOf(res)).code).toBe("bad_request");
        }
        const big = await postForm(post("/api/forms/waitlist", { ...WAITLIST, name: "x".repeat(MAX_FORM_BYTES) }), "waitlist", { config: go.config() });
        expect(big.status).toBe(413);
        expect((await errorOf(big)).code).toBe("payload_too_large");
        expect(go.requests).toHaveLength(0);
    });

    it("answers 502 when Go is unreachable and 504 when it is too slow", async () => {
        const down = await postForm(post("/api/forms/waitlist", WAITLIST), "waitlist", { config: go.config({ goApiInternalUrl: "http://127.0.0.1:1" }) });
        expect(down.status).toBe(502);
        expect((await errorOf(down)).code).toBe("unavailable");

        go.on("POST", "/v1/waitlist", () => ({ status: 201, delayMs: 300, body: { data: { received: true } } }));
        const slow = await postForm(post("/api/forms/waitlist", WAITLIST), "waitlist", { config: go.config({ goTimeoutMs: 50 }) });
        expect(slow.status).toBe(504);
    });

    it("has no preflight and only POST", async () => {
        const res = formOptions(webRequest("/api/forms/waitlist", { method: "OPTIONS" }));
        expect(res.status).toBe(405);
        expect(res.headers.get("Allow")).toBe("POST");
        const get = await postForm(webRequest("/api/forms/waitlist", { method: "GET" }), "waitlist", { config: go.config() });
        expect(get.status).toBe(405);
    });

    it("the route modules export POST and OPTIONS only, on the Node runtime", () => {
        for (const route of [waitlistRoute, partnerRoute]) {
            expect(Object.keys(route).sort()).toEqual(["OPTIONS", "POST", "dynamic", "runtime"]);
            expect(route.runtime).toBe("nodejs");
            expect(route.dynamic).toBe("force-dynamic");
        }
    });
});
