// @vitest-environment node
// The real Next proxy adapter needs the same AsyncLocalStorage global as the Node server.
import "next/dist/server/node-environment-baseline";
import { adapter } from "next/dist/server/web/adapter";
import { NextResponse } from "next/server";
import { afterAll, beforeAll, beforeEach, describe, expect, it } from "vitest";

import { canonicalAppPath, routeAllowed, routeFor } from "../routeCapabilities";
import { resetJwksForTests } from "./accessToken";
import { edgeGate, resetGateCacheForTests } from "./edgeGate";
import { FakeGo, newSigningKey, signAccess, WEB_ORIGIN, webRequest } from "./__tests__/fakeGo";

const go = new FakeGo();
const capabilities = ["drivers:view"];
let token: string;

beforeAll(async () => {
    await go.start();
    const key = await newSigningKey();
    go.published = [key];
    token = await signAccess(key, { rol: "manager", tid: "t1" });
});
afterAll(async () => {
    await go.stop();
});
beforeEach(() => {
    resetJwksForTests();
    resetGateCacheForTests();
    go.requests.length = 0;
    go.on("GET", "/v1/me", () => ({
        status: 200,
        body: { data: { capabilities, tenant: { kind: "own_fleet", role: "manager" }, customerScopes: [], dispatcher: false } },
    }));
});

function gate(raw: string, headers: Record<string, string> = {}, signedIn = true) {
    return edgeGate(webRequest(raw, { headers, cookies: signedIn ? { lt_at: token } : undefined }), { config: go.config() });
}

async function proxyGate(raw: string, headers: Record<string, string>, signedIn = true) {
    let request: Request | undefined;
    let decision: Response | undefined;
    const result = await adapter({
        page: "/proxy",
        request: { url: `${WEB_ORIGIN}${raw}`, method: "GET", headers: { ...(signedIn ? { cookie: `lt_at=${token}` } : {}), ...headers }, signal: new AbortController().signal },
        handler: async (req) => {
            request = req;
            decision = await edgeGate(req, { config: go.config() });
            return decision ?? NextResponse.next();
        },
    });
    await result.waitUntil;
    return { request: request!, decision, response: result.response };
}

const unauthorized = (from: string) => `/app/unauthorized?from=${encodeURIComponent(from)}`;

/** null status means the gate returns undefined, as proxy.ts then calls NextResponse.next(). */
const spellings: { raw: string; status: number | null; destination?: string }[] = [
    { raw: "/app/payroll", status: 307, destination: unauthorized("/app/payroll") },
    { raw: "/app/%70ayroll", status: 307, destination: unauthorized("/app/payroll") },
    { raw: "/app/payrol%6c", status: 307, destination: unauthorized("/app/payroll") },
    { raw: "/app/accounting/%69ncome", status: 307, destination: unauthorized("/app/accounting/income") },
    { raw: "/app/%74rucks/%65dit", status: 307, destination: unauthorized("/app/trucks/edit") },
    { raw: "/app/drivers/%6eew", status: 307, destination: unauthorized("/app/drivers/new") },
    { raw: "/app/drivers/new", status: 307, destination: unauthorized("/app/drivers/new") },
    { raw: "/app/drivers", status: null },
    { raw: "/app/drivers/view", status: null },
    { raw: "/app/%64rivers/view", status: 308, destination: "/app/drivers/view" },
    { raw: "/app/%2570ayroll", status: 404 },
    { raw: "/app/%25%37%30ayroll", status: 308, destination: "/app/%2570ayroll" },
    { raw: "/app/%7A", status: 308, destination: "/app/z" },
    { raw: "/app/%7a", status: 308, destination: "/app/z" },
    { raw: "/app/z", status: 404 },
    { raw: "/app/%C0%AF", status: 307, destination: "/app/unauthorized" },
    { raw: "/app/%E0%A4%A", status: 307, destination: "/app/unauthorized" },
    { raw: "/app/%G1", status: 307, destination: "/app/unauthorized" },
    { raw: "/app/drivers%2Fnew", status: 307, destination: "/app/unauthorized" },
    { raw: "/app/drivers/a%2fb", status: 307, destination: "/app/unauthorized" },
    { raw: "/app/drivers%5Cnew", status: 307, destination: "/app/unauthorized" },
    { raw: "/app/drivers/a%5cb", status: 307, destination: "/app/unauthorized" },
    // WHATWG URL parsing collapses both literal and encoded dot segments before edgeGate sees req.url.
    { raw: "/app/%2e/payroll", status: 307, destination: unauthorized("/app/payroll") },
    { raw: "/app/%2E/payroll", status: 307, destination: unauthorized("/app/payroll") },
    { raw: "/app/x/%2e%2e/payroll", status: 307, destination: unauthorized("/app/payroll") },
    { raw: "/app/./payroll", status: 307, destination: unauthorized("/app/payroll") },
    { raw: "/app/x/../payroll", status: 307, destination: unauthorized("/app/payroll") },
    { raw: "/app/drivers/%2e%2e/payroll", status: 307, destination: unauthorized("/app/payroll") },
    { raw: "/app/drivers/../payroll", status: 307, destination: unauthorized("/app/payroll") },
    { raw: "/app/payroll;x", status: 308, destination: "/app/payroll%3Bx" },
    { raw: "/app/payroll%3bx", status: 308, destination: "/app/payroll%3Bx" },
    { raw: "/app/payroll%3Bx", status: 404 },
    { raw: "/app/payroll%00", status: 307, destination: "/app/unauthorized" },
    { raw: "/app/payroll%09", status: 307, destination: "/app/unauthorized" },
    { raw: "/app/payroll%20", status: 404 },
    { raw: "/app/payroll+", status: 308, destination: "/app/payroll%2B" },
    { raw: "/app/payroll%2B", status: 404 },
    { raw: "/app/payroll%3F", status: 404 },
    { raw: "/app/payroll%23", status: 404 },
    // URL serialisation already percent-encodes fullwidth letters; they never become ASCII letters.
    { raw: "/app/ｐayroll", status: 404 },
    { raw: "/app/payroll/", status: 307, destination: unauthorized("/app/payroll") },
    { raw: "/app//payroll", status: 307, destination: unauthorized("/app/payroll") },
    { raw: "/app//drivers", status: 308, destination: "/app/drivers" },
    // Next handles trailing slash redirects before proxy.ts; the gate itself trims them.
    { raw: "/app/drivers/", status: null },
    // These are outside the gate, and Next's production router returns 404 (paths are case-sensitive).
    { raw: "/APP/payroll", status: null },
    { raw: "/App/payroll", status: null },
];

function expectDecision(res: Response | undefined, status: number | null, destination?: string) {
    if (status === null) {
        expect(res).toBeUndefined();
        return;
    }
    expect(res).toBeDefined();
    expect(res!.status).toBe(status);
    expect(res!.headers.get("cache-control")).toBe("no-store");
    expect(res!.headers.get("x-middleware-rewrite")).toBeNull();
    expect(res!.headers.get("location")).toBe(destination ? `${WEB_ORIGIN}${destination}` : null);
    expect(res!.headers.get("x-middleware-next")).toBe(status === 404 ? "1" : null);
}

const variants: { label: string; query: string; headers: Record<string, string> }[] = [
    { label: "HTML", query: "", headers: {} },
    { label: "RSC header", query: "", headers: { RSC: "1" } },
    { label: "_rsc query", query: "?_rsc=abc", headers: {} },
    { label: "router prefetch", query: "", headers: { "Next-Router-Prefetch": "1" } },
    { label: "router state tree", query: "", headers: { "Next-Router-State-Tree": '%5B%22%22%2C%7B%7D%5D' } },
    { label: "segment prefetch", query: "", headers: { "Next-Router-Segment-Prefetch": "/_tree" } },
    { label: "all Flight headers and query", query: "?_rsc=abc", headers: {
        RSC: "1", "Next-Router-Prefetch": "1", "Next-Router-State-Tree": '%5B%22%22%2C%7B%7D%5D', "Next-Router-Segment-Prefetch": "/_tree",
    } },
];

describe.each(variants)("URL spelling at the gate: $label (only drivers:view)", ({ query, headers }) => {
    it.each(spellings)("$raw -> $status", async ({ raw, status, destination }) => {
        const target = status === 308 ? destination! + query : destination;
        const res = await gate(raw + query, headers);
        expectDecision(res, status, target);
        const decided = canonicalAppPath(new URL(webRequest(raw).url).pathname);
        if (status === 404) expect(routeFor(decided!.path)).toBeUndefined(); // cannot be an unchecked mapped page
        if (status === null && raw.startsWith("/app/")) expect(routeAllowed(capabilities, decided!.path)).toBe(true);
        if (status === 308) {
            // Follow the actual Location. A canonical unmapped spelling stays 404; only an allowed page passes.
            const canonical = canonicalAppPath(new URL(res!.headers.get("location")!).pathname)!;
            expect(canonical.href).toBe(new URL(res!.headers.get("location")!).pathname);
            const followed = await gate(target!, headers);
            expectDecision(followed, routeAllowed(capabilities, canonical.path) ? null : 404);
        }
    });
});

describe("Next's proxy adapter decides RSC suffix spellings before calling the gate", () => {
    it.each([
        ["/app/payroll.rsc", "/app/payroll", 307, unauthorized("/app/payroll")],
        ["/app/%70ayroll.rsc", "/app/%70ayroll", 307, unauthorized("/app/payroll")],
        ["/app/drivers/new.rsc", "/app/drivers/new", 307, unauthorized("/app/drivers/new")],
        ["/app/drivers/%6eew.rsc", "/app/drivers/%6eew", 307, unauthorized("/app/drivers/new")],
        ["/app/drivers/view.rsc", "/app/drivers/view", null, undefined],
        ["/app/payroll.txt", "/app/payroll.txt", 404, undefined],
        ["/app/drivers/new.txt", "/app/drivers/new.txt", null, undefined],
        ["/app/payroll.segments/_tree.segment.rsc", "/app/payroll.segments/_tree.segment", 404, undefined],
        ["/app/payroll/.segments/_tree.segment.rsc", "/app/payroll/.segments/_tree.segment", 307, unauthorized("/app/payroll/.segments/_tree.segment")],
        ["/app/drivers/new/.segments/_tree.segment.rsc", "/app/drivers/new/.segments/_tree.segment", 307, unauthorized("/app/drivers/new/.segments/_tree.segment")],
        ["/app/payroll/.segments/", "/app/payroll/.segments/", 307, unauthorized("/app/payroll/.segments")],
        ["/app/payroll%2Ersc", "/app/payroll%2Ersc", 308, "/app/payroll.rsc"],
        ["/app/drivers/new%2Ersc", "/app/drivers/new%2Ersc", 308, "/app/drivers/new.rsc"],
    ] as const)("%s -> proxy pathname %s", async (raw, pathname, status, destination) => {
        for (const { query, headers } of variants) {
            const result = await proxyGate(raw + query, headers);
            expect(new URL(result.request.url).pathname).toBe(pathname);
            for (const name of ["RSC", "Next-Router-Prefetch", "Next-Router-State-Tree", "Next-Router-Segment-Prefetch"]) {
                expect(result.request.headers.has(name)).toBe(false);
            }
            // The adapter removes _rsc before the gate sees the URL. It can make Location relative afterwards.
            expect(new URL(result.request.url).searchParams.has("_rsc")).toBe(false);
            expectDecision(result.decision, status, destination);
            expect(result.response.status).toBe(status ?? 200);
            if (destination) expect(new URL(result.response.headers.get("location")!, WEB_ORIGIN).href).toBe(`${WEB_ORIGIN}${destination}`);
            if (status === 308) {
                const followed = await proxyGate(destination!, headers);
                expectDecision(followed.decision, 307, unauthorized(destination!.replace(/\.rsc$/, "")));
            }
        }
    });

    it.each(variants)("signed-out .rsc request: $label", async ({ query, headers }) => {
        const result = await proxyGate(`/app/drivers/%6eew.rsc${query}`, headers, false);
        expectDecision(result.decision, 307, "/api/auth/refresh?next=%2Fapp%2Fdrivers%2Fnew");
        expect(go.requests).toHaveLength(0);
    });
});

describe.each(variants)("signed-out URL spelling: $label", ({ query, headers }) => {
    it.each([
        ["/app/%70ayroll", "/app/payroll"],
        ["/app/drivers/%6eew", "/app/drivers/new"],
        ["/app/drivers/%2e%2e/payroll", "/app/payroll"],
        ["/app/%2570ayroll", "/app/%70ayroll"],
        ["/app/nope", "/app/nope"],
    ])("%s -> refresh with %s", async (raw, pathname) => {
        expectDecision(await gate(raw + query, headers, false), 307, `/api/auth/refresh?next=${encodeURIComponent(pathname)}`);
        expect(go.requests).toHaveLength(0);
    });

    it.each(["/app/%C0%AF", "/app/%E0%A4%A", "/app/%G1", "/app/drivers%2Fnew", "/app/payroll%00"])("%s -> unauthorized before authentication", async (raw) => {
        expectDecision(await gate(raw + query, headers, false), 307, "/app/unauthorized");
        expect(go.requests).toHaveLength(0);
    });
});
