// T49 (issue #69, review), TW7: the LINE actions of the Go trip editor and standby detail. Since TW7 the web has no
// callable and no web flags, so both buttons show for `operations:edit_trip_details` whatever any flag said, and never
// for anyone else. A revocation that could not close a link the legacy system sent says so instead of reporting the
// link closed.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import React from "react";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";

import { fakeWeb, goErr, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { LineNotifyActions } from "./LineNotifyActions";

beforeEach(() => {
    window.history.replaceState(null, "", "/app/driver-monitor");
});
afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
});

function setup(capabilities: string[] = ["operations:edit_trip_details"]) {
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ capabilities }) }));
    return web;
}

function renderActions(kind: "trips" | "standby" = "trips") {
    return renderWithProviders(<LineNotifyActions kind={kind} id="r1" />);
}

describe("LINE actions over Go", () => {
    it("shows neither button without operations:edit_trip_details, and asks Go for nothing", async () => {
        const web = setup(["operations:view_trips"]);
        renderActions();
        await waitFor(() => expect(web.count("GET", "/api/go/v1/me")).toBeGreaterThan(0));
        await new Promise((r) => setTimeout(r, 50));
        expect(screen.queryByRole("button", { name: /Send LINE notification/ })).toBeNull();
        expect(screen.queryByRole("button", { name: /Revoke photo link/ })).toBeNull();
        expect(web.calls.some((c) => c.method === "POST" && c.url.startsWith("/api/go/"))).toBe(false);
        expect(web.calls.some((c) => c.url.includes("web-flags") || c.url.includes("firebase-token"))).toBe(false);
    });

    it("shows both buttons with no web flag to wait for (TW7)", async () => {
        const web = setup();
        renderActions("standby");
        expect(await screen.findByRole("button", { name: /Send LINE notification/ })).toBeTruthy();
        expect(screen.getByRole("button", { name: /Revoke photo link/ })).toBeTruthy();
        expect(web.calls.some((c) => c.url.includes("web-flags"))).toBe(false);
    });

    it("reports a revocation by what it closed: every link, not the legacy one, or refused", async () => {
        const web = setup();
        let answer: () => Response = () => new Response(null, { status: 204 });
        web.on("POST", "/api/go/v1/trips/r1/evidence/revoke", () => answer());
        vi.stubGlobal("confirm", () => true);
        const success = vi.spyOn(toast, "success");
        const warning = vi.spyOn(toast, "warning");
        const error = vi.spyOn(toast, "error");
        const user = userEvent.setup();
        renderActions();
        const revoke = await screen.findByRole("button", { name: /Revoke photo link/ });

        await user.click(revoke);
        await waitFor(() => expect(success).toHaveBeenCalledWith("Photo link revoked. Send the LINE notification again to share a new link."));

        answer = () => json(200, { data: { revoked: true, legacyLinkOpen: true } });
        await user.click(revoke);
        await waitFor(() => expect(warning).toHaveBeenCalledWith(expect.stringContaining("sent by the old system may still open it")));

        answer = () => goErr(409, "failed_precondition", { reason: "legacy_evidence_link" });
        await user.click(revoke);
        await waitFor(() => expect(error).toHaveBeenCalledWith(expect.stringContaining("cannot be revoked here")));
        expect(success).toHaveBeenCalledTimes(1);
        expect(web.count("POST", "/api/go/v1/trips/r1/evidence/revoke")).toBe(3);
    });
});
