// @vitest-environment node
import { NextRequest } from "next/server";
import { afterAll, beforeAll, describe, expect, it } from "vitest";

import { resetBffConfigForTests } from "./lib/bff/config";
import { FakeGo, newSigningKey, signAccess, WEB_ORIGIN, ISSUER, AUDIENCE, type SigningKey } from "./lib/bff/__tests__/fakeGo";
import { config, proxy } from "./proxy";

const go = new FakeGo();
let key: SigningKey;
const saved = { ...process.env };

beforeAll(async () => {
    await go.start();
    key = await newSigningKey();
    go.published = [key];
    go.on("GET", "/v1/me", () => ({ status: 200, body: { data: { tenant: { kind: "own_fleet", role: "manager" }, customerScopes: [], capabilities: ["drivers:view"] } } }));
    Object.assign(process.env, { GO_API_INTERNAL_URL: go.url, WEB_PUBLIC_ORIGIN: WEB_ORIGIN, JWT_ISSUER: ISSUER, JWT_AUDIENCE: AUDIENCE });
    resetBffConfigForTests();
});
afterAll(async () => {
    process.env = saved;
    resetBffConfigForTests();
    await go.stop();
});

describe("proxy.ts", () => {
    it("runs on /app and every path below it only (R39)", () => {
        expect(config.matcher).toEqual(["/app/:path*"]);
    });

    it("redirects a navigation without lt_at to the refresh bounce, reading the environment", async () => {
        const res = await proxy(new NextRequest(`${WEB_ORIGIN}/app/drivers`));
        expect(res.status).toBe(307);
        expect(res.headers.get("location")).toBe(`${WEB_ORIGIN}/api/auth/refresh?next=%2Fapp%2Fdrivers`);
    });

    it("lets an allowed navigation through with NextResponse.next()", async () => {
        const at = await signAccess(key);
        const res = await proxy(new NextRequest(`${WEB_ORIGIN}/app/drivers`, { headers: { cookie: `lt_at=${at}` } }));
        expect(res.headers.get("x-middleware-next")).toBe("1");
    });
});
