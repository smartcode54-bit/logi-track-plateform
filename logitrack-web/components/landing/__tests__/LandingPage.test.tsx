// UI3C (#143, the critique's front-door P1): a signed-in visitor of `/` goes to their console (the role's home, the
// shell's Home crumb), a signed-out one gets the landing with the sign-in form on the page itself (sign-in primary, the
// waitlist a secondary text link under it), and the brand is LogiTrack Pro by GORATECH.
import React from "react";
import { screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const replace = vi.fn();
vi.mock("next/navigation", () => ({
    usePathname: () => "/",
    useRouter: () => ({ push: vi.fn(), replace, refresh: vi.fn() }),
    useSearchParams: () => new URLSearchParams(),
}));
vi.mock("@/lib/loginGeo", () => ({ resolveLoginGeoForClient: async () => null }));

import { LandingPage } from "../LandingPage";
import { homeRouteOf } from "@/features/auth/api/homeRoute";
import { fakeWeb, goErr, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";

beforeEach(() => replace.mockReset());
afterEach(() => {
    vi.unstubAllGlobals();
    vi.unstubAllEnvs();
});

function signedOut() {
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/me", () => goErr(401, "unauthenticated"));
    web.on("POST", "/api/auth/refresh", () => goErr(401, "unauthenticated"));
    return web;
}

describe("the landing page, signed out", () => {
    it("names the product and puts the email/password form on the page, no dialog in between", async () => {
        signedOut();
        renderWithProviders(<LandingPage />);
        const card = await screen.findByTestId("sign-in-card");
        expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Run your trucking operation from assignment to payout");
        // The card's title is a heading under the page's h1; the navigation has a name.
        expect(within(card).getByRole("heading", { level: 2, name: "Sign in" })).toBeInTheDocument();
        expect(screen.getByRole("navigation", { name: "Main" })).toBeInTheDocument();
        expect(screen.getAllByText("LogiTrack Pro").length).toBeGreaterThan(0);
        expect(screen.getAllByText("by GORATECH").length).toBeGreaterThan(0);
        expect(within(card).getByLabelText("Email")).toBeInTheDocument();
        expect(within(card).getByLabelText("Password")).toBeInTheDocument();
        expect(screen.queryByRole("dialog")).toBeNull();
        expect(screen.queryByText(/Logi-Track|LogisticsPro|Logistics Pro/)).toBeNull();
    });

    it("makes sign-in the primary action and the waitlist a text link below it", async () => {
        signedOut();
        renderWithProviders(<LandingPage />);
        const card = await screen.findByTestId("sign-in-card");
        const submit = within(card).getByTestId("sign-in-submit");
        expect(submit).toHaveAccessibleName("Sign in");
        expect(submit.className).toContain("bg-primary");
        const waitlist = within(card).getByRole("link", { name: "Request access on the waitlist" });
        expect(waitlist).toHaveAttribute("href", "/waitlist");
        expect(waitlist.className).not.toMatch(/bg-primary|bg-/);
        // Below the submit button in reading order.
        expect(submit.compareDocumentPosition(waitlist) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    });

    it("links the features section the navigation points to, and no page that does not exist", async () => {
        signedOut();
        const { container } = renderWithProviders(<LandingPage />);
        await screen.findByTestId("sign-in-card");
        expect(container.querySelector("#solutions")).not.toBeNull();
        for (const link of screen.getAllByRole("link")) {
            const href = link.getAttribute("href") ?? "";
            expect(href, link.textContent ?? "").not.toBe("#");
            expect(href).not.toMatch(/pricing/);
        }
        expect(screen.queryByRole("link", { name: /pricing|api docs|demo/i })).toBeNull();
    });

    it("signs the footer with GORATECH and the current year", async () => {
        signedOut();
        renderWithProviders(<LandingPage />);
        await screen.findByTestId("sign-in-card");
        expect(screen.getByText(`© ${new Date().getFullYear()} GORATECH. All rights reserved.`)).toBeInTheDocument();
        expect(screen.queryByText(/Wanpen|Radchada|2025 /i)).toBeNull();
    });
});

describe("the landing page, signed in", () => {
    it("sends the visitor to the role's home and offers Open console instead of a sign-in form", async () => {
        const me = makeMe();
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => json(200, { data: me }));
        renderWithProviders(<LandingPage sessionHint />);
        await waitFor(() => expect(replace).toHaveBeenCalledWith("/app/dashboard"));
        expect(screen.getByRole("link", { name: "Open console" })).toHaveAttribute("href", homeRouteOf(me));
        expect(screen.queryByTestId("sign-in-card")).toBeNull();
        expect(screen.queryByLabelText("Password")).toBeNull();
    });

    it("sends a customer to the driver monitor, as the shell's Home crumb does", async () => {
        const me = makeMe({ tenant: null, tenants: [], steward: false, customerScopes: [{ billingPartyId: "b1", name: "Shipper" }], capabilities: ["operations:view_driver_monitor"] });
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => json(200, { data: me }));
        renderWithProviders(<LandingPage sessionHint />);
        await waitFor(() => expect(replace).toHaveBeenCalledWith("/app/driver-monitor"));
    });

    it("tells a driver-only account to use the driver app, with no console link and no redirect", async () => {
        const me = makeMe({
            tenant: { id: "t-own", nameTh: "กองรถ", nameEn: "Own fleet", kind: "own_fleet", role: "driver" },
            tenants: [],
            steward: false,
            driver: { id: "d-1" },
            capabilities: ["mobile:check_in"],
        });
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => json(200, { data: me }));
        renderWithProviders(<LandingPage sessionHint />);
        expect(await screen.findByRole("heading", { level: 1, name: "Use the LogiTrack driver app" })).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Sign out" })).toBeInTheDocument();
        expect(screen.queryByRole("link", { name: "Open console" })).toBeNull();
        expect(screen.queryByTestId("sign-in-card")).toBeNull();
        expect(replace).not.toHaveBeenCalled();
    });

    it("with a stale session cookie (no session behind it) shows the landing, and redirects nowhere", async () => {
        const web = signedOut();
        renderWithProviders(<LandingPage sessionHint />);
        expect(await screen.findByTestId("sign-in-card")).toBeInTheDocument();
        expect(screen.queryByTestId("opening-console")).toBeNull();
        expect(replace).not.toHaveBeenCalled();
        // One look at ['me'] and one refused refresh: no loop.
        expect(web.count("GET", "/api/go/v1/me")).toBe(1);
        expect(web.count("POST", "/api/auth/refresh")).toBeLessThanOrEqual(1);
    });

    it("with a session cookie, shows the opening state (not a sign-in form) while ['me'] loads", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => new Promise<Response>(() => undefined));
        renderWithProviders(<LandingPage sessionHint />);
        expect(await screen.findByTestId("opening-console")).toBeInTheDocument();
        expect(screen.getByRole("heading", { level: 1, name: "Opening your console…" })).toBeInTheDocument();
        expect(screen.getByRole("link", { name: "Open console" })).toHaveAttribute("href", "/app");
        expect(screen.queryByTestId("sign-in-card")).toBeNull();
    });
});
