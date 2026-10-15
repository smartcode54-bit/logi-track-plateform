import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as goClient from "./goFetch";
import { changePassword } from "./authClient";
import { fakeWeb, goErr, json } from "@/test-utils/fakeWeb";

const PATH = "/api/go/v1/auth/password/change";

beforeEach(() => {
    window.localStorage.setItem("language", "en");
    window.history.replaceState(null, "", "/my-account");
});
afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
});

describe("session-bound changePassword", () => {
    it("sends one same-origin bearer BFF POST, without anonymous mode or a ticket", async () => {
        const web = fakeWeb();
        web.on("POST", PATH, () => new Response(null, { status: 204 }));
        const call = vi.spyOn(goClient, "goFetch");
        await expect(changePassword(" old ", " new ")).resolves.toBeUndefined();
        expect(call).toHaveBeenCalledTimes(1);
        expect(call).toHaveBeenCalledWith("/v1/auth/password/change", {
            method: "POST", body: { currentPassword: " old ", newPassword: " new " },
        });
        expect(web.calls).toHaveLength(1);
        expect(web.calls[0]).toMatchObject({ method: "POST", url: PATH, body: { currentPassword: " old ", newPassword: " new " } });
        const init = vi.mocked(fetch).mock.calls[0][1];
        expect(init?.credentials).toBe("same-origin");
        // The BFF supplies the bearer from the cookie; JavaScript never receives or sets a token.
        expect(web.calls[0].headers).not.toHaveProperty("authorization");
    });

    it("keeps the existing claims-refresh flow after the successful change", async () => {
        const web = fakeWeb();
        let reads = 0;
        web.on("POST", PATH, () => new Response(null, { status: 204 }));
        web.on("GET", "/api/go/v1/me", () => ++reads === 1 ? goErr(401, "token_expired", { reason: "claims_changed" }) : json(200, { data: { id: "same-user" } }));
        web.on("POST", "/api/auth/refresh", () => json(200, { data: { expiresIn: 900 } }));
        await changePassword("old", "new");
        await expect(goClient.goFetch("/v1/me")).resolves.toEqual({ id: "same-user" });
        expect(web.count("POST", PATH)).toBe(1);
        expect(web.count("POST", "/api/auth/refresh")).toBe(1);
        expect(web.count("POST", "/api/auth/logout")).toBe(0);
    });
});
