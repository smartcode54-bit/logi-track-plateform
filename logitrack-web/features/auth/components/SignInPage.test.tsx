// UI3 (#143, the critique's sign-in P1, `quieter`): one background, painted once from one file; a solid card with
// no blur or translucency; the primary button in the primary token (Route Blue), not black; fields on the Field
// Edge token; a sentence-case "or"; the brand "LogiTrack Pro by GORATECH"; one clear line to the waitlist.
import fs from "node:fs";
import path from "node:path";
import React from "react";
import { screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("next/navigation", () => ({
    usePathname: () => "/login",
    useRouter: () => ({ push: vi.fn(), replace: vi.fn(), refresh: vi.fn() }),
    useSearchParams: () => new URLSearchParams(),
}));
vi.mock("@/lib/loginGeo", () => ({ resolveLoginGeoForClient: async () => null }));

import AuthLayout from "@/app/(auth)/layout";
import LoginPage from "@/app/(auth)/login/page";
import { SIGN_IN_BACKGROUND_SRC } from "./AuthBackground";
import { fakeWeb, goErr } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { WEB_ROOT } from "@/test-utils/importGraph";

afterEach(() => {
    vi.unstubAllGlobals();
    vi.unstubAllEnvs();
});

async function renderSignIn() {
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/me", () => goErr(401, "unauthenticated"));
    web.on("POST", "/api/auth/refresh", () => goErr(401, "unauthenticated"));
    const view = renderWithProviders(
        <AuthLayout>
            <LoginPage />
        </AuthLayout>
    );
    await screen.findByTestId("sign-in-card");
    return view;
}

describe("the sign-in page", () => {
    it("paints its background once, from the one configurable file", async () => {
        const { container } = await renderSignIn();
        const backgrounds = screen.getAllByTestId("auth-background");
        expect(backgrounds).toHaveLength(1);
        expect(backgrounds[0].querySelector("img")).toHaveAttribute("src", "/auth/sign-in-bg.webp");
        expect(SIGN_IN_BACKGROUND_SRC).toBe("/auth/sign-in-bg.webp");
        expect(fs.existsSync(path.join(WEB_ROOT, "public", SIGN_IN_BACKGROUND_SRC))).toBe(true);
        expect(container.innerHTML).not.toContain("driver-app-bg");
    });

    it("shows a solid card: no blur, no translucency, no oversized shadow", async () => {
        await renderSignIn();
        const card = screen.getByTestId("sign-in-card");
        expect(card.className).not.toMatch(/backdrop-blur|bg-background\/|shadow-(xl|2xl)|border-border\//);
        expect(card.className).toContain("bg-card");
        for (const el of document.querySelectorAll("*")) {
            expect(String(el.getAttribute("class") ?? "")).not.toMatch(/backdrop-blur/);
        }
    });

    it("uses the primary button, the field token and a sentence-case divider", async () => {
        vi.stubEnv("NEXT_PUBLIC_GOOGLE_OIDC_CLIENT_ID", "test-client.apps.googleusercontent.com");
        await renderSignIn();
        const submit = screen.getByTestId("sign-in-submit");
        expect(submit).toHaveAccessibleName("Sign in");
        expect(submit.className).toContain("bg-primary");
        expect(submit.className).not.toMatch(/bg-foreground|text-background/);
        for (const input of [screen.getByLabelText("Email"), screen.getByLabelText("Password")]) {
            expect(input.className).toContain("border-input");
            expect(input.className).not.toMatch(/border-muted-foreground/);
        }
        const or = screen.getByText("or");
        expect(or.className).not.toMatch(/uppercase/);
        expect(or.parentElement?.className ?? "").not.toMatch(/uppercase/);
        expect(screen.getByTestId("gis-button")).toBeInTheDocument();
    });

    it("offers no 'or' when Google sign-in is not configured (the button would render nothing)", async () => {
        vi.stubEnv("NEXT_PUBLIC_GOOGLE_OIDC_CLIENT_ID", "");
        await renderSignIn();
        expect(screen.queryByTestId("sign-in-or")).toBeNull();
        expect(screen.queryByText("or")).toBeNull();
        expect(screen.queryByTestId("gis-button")).toBeNull();
    });

    it("names the product and says where the account link leads", async () => {
        await renderSignIn();
        expect(screen.getByText("LogiTrack Pro")).toBeInTheDocument();
        expect(screen.getByText("by GORATECH")).toBeInTheDocument();
        expect(screen.getByText(/No account yet\?/)).toBeInTheDocument();
        expect(screen.getByRole("link", { name: "Request access on the waitlist" })).toHaveAttribute("href", "/waitlist");
        // The navigation's own "Sign In" link is not repeated on the sign-in page.
        expect(screen.queryByRole("link", { name: /^sign in$/i })).toBeNull();
    });
});
