import { afterEach, describe, expect, it, vi } from "vitest";
import { fakeWeb, json } from "@/test-utils/fakeWeb";
import { suspendSubcontractor } from "./subcontractors";

afterEach(() => vi.unstubAllGlobals());

describe("partner suspension", () => {
    it("sends exactly one status-only PATCH through the BFF", async () => {
        const web = fakeWeb();
        web.on("PATCH", "/api/go/v1/tenants/carrier-1", () => json(200, { data: { id: "carrier-1", status: "suspended" } }));
        await suspendSubcontractor("carrier-1");
        expect(web.calls).toEqual([{
            method: "PATCH", url: "/api/go/v1/tenants/carrier-1", body: { status: "suspended" },
            headers: expect.objectContaining({ "content-type": "application/json" }),
        }]);
    });
});
