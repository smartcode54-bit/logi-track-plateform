// T49 (issue #69, review): the LINE actions of the Go trip editor and standby detail are P5 (`comms`) routes on pages
// that switch with `operations` (P2). Until the `comms` web flag is `go` the legacy callable sends the cards and the
// legacy gallery serves their links, so neither button is shown; once it is, both are. A revocation that could not
// close a link the legacy system sent says so instead of reporting the link closed.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import React from "react";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";

vi.mock("@/firebase/client", () => {
    throw new Error("the Go LINE actions must not load the Firebase client");
});
vi.mock("firebase/functions", () => {
    throw new Error("the Go LINE actions must not call a Cloud Function");
});

import { useDomainSource } from "@/features/platform/api/useWebFlags";
import { configureFirebaseBridge } from "@/lib/firebaseBridge";
import { fakeWeb, goErr, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { LineNotifyActions } from "./LineNotifyActions";

beforeEach(() => {
    window.history.replaceState(null, "", "/app/driver-monitor");
    configureFirebaseBridge(async () => {
        throw new Error("no Firebase SDK here");
    });
});
afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    configureFirebaseBridge(undefined);
});

function setup(domains: Record<string, string>) {
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ capabilities: ["operations:edit_trip_details"] }) }));
    web.on("GET", "/api/go/v1/config/web-flags", () => json(200, { data: { domains: { auth: "go", operations: "go", ...domains } } }));
    web.on("POST", "/api/auth/firebase-token", () => goErr(403, "permission_denied"));
    return web;
}

/** The comms source the component sees, so a test knows the flags have loaded before it looks for the buttons. */
function CommsProbe() {
    return <span data-testid="comms">{useDomainSource("comms") ?? "loading"}</span>;
}

function renderActions(kind: "trips" | "standby" = "trips") {
    return renderWithProviders(
        <>
            <CommsProbe />
            <LineNotifyActions kind={kind} id="r1" />
        </>
    );
}

describe("LINE actions over Go", () => {
    it("shows neither button while comms is still Firestore's, with operations on Go", async () => {
        const web = setup({ comms: "firebase" });
        renderActions();
        await waitFor(() => expect(screen.getByTestId("comms").textContent).toBe("firebase"));
        await waitFor(() => expect(web.count("GET", "/api/go/v1/me")).toBeGreaterThan(0));
        expect(screen.queryByRole("button", { name: /Send LINE notification/ })).toBeNull();
        expect(screen.queryByRole("button", { name: /Revoke photo link/ })).toBeNull();
        expect(web.calls.some((c) => c.method === "POST" && c.url.startsWith("/api/go/"))).toBe(false);
    });

    it("shows both buttons once comms is on Go", async () => {
        setup({ comms: "go" });
        renderActions("standby");
        expect(await screen.findByRole("button", { name: /Send LINE notification/ })).toBeTruthy();
        expect(screen.getByRole("button", { name: /Revoke photo link/ })).toBeTruthy();
    });

    it("reports a revocation by what it closed: every link, not the legacy one, or refused", async () => {
        const web = setup({ comms: "go" });
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
