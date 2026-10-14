// UI3C (#143): support lives inside the console (`/app/support`), in both languages, open to every signed-in
// principal, and names no contact the owner has not configured (NEXT_PUBLIC_SUPPORT_EMAIL).
import React, { useEffect } from "react";
import { screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("next/navigation", () => ({
    usePathname: () => "/app/support",
    useRouter: () => ({ push: vi.fn(), replace: vi.fn(), refresh: vi.fn() }),
    useSearchParams: () => new URLSearchParams(),
}));

import SupportPage from "@/app/app/support/page";
import { BreadcrumbProvider, useBreadcrumb } from "@/context/breadcrumb";
import { useLanguage } from "@/context/language";
import { routeAllowed } from "@/lib/routeCapabilities";
import { supportEmail, supportMailto } from "@/lib/supportContact";
import { fakeWeb, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import nextConfig from "@/next.config";

afterEach(() => {
    vi.unstubAllGlobals();
    vi.unstubAllEnvs();
});

function Crumb() {
    const { customLastItem } = useBreadcrumb();
    return <output data-testid="crumb">{customLastItem ?? ""}</output>;
}

function Thai({ children }: { children: React.ReactNode }) {
    const { language, setLanguage } = useLanguage();
    useEffect(() => setLanguage("th"), [setLanguage]);
    return language === "th" ? <>{children}</> : null;
}

const customer = makeMe({
    displayName: "Somchai",
    tenant: null,
    tenants: [],
    steward: false,
    customerScopes: [{ billingPartyId: "b1", name: "CJSF" }],
    capabilities: ["operations:view_driver_monitor"],
});

function renderSupport(thai = false) {
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/me", () => json(200, { data: customer }));
    const page = (
        <BreadcrumbProvider>
            <Crumb />
            <SupportPage />
        </BreadcrumbProvider>
    );
    return renderWithProviders(thai ? <Thai>{page}</Thai> : page);
}

describe("/app/support", () => {
    it("is open to every signed-in principal, a customer without any capability included", () => {
        expect(routeAllowed([], "/app/support")).toBe(true);
        expect(routeAllowed(["operations:view_driver_monitor"], "/app/support")).toBe(true);
    });

    it("takes the place of the public /support page", async () => {
        expect(await nextConfig.redirects!()).toContainEqual({ source: "/support", destination: "/app/support", permanent: false });
    });

    it("renders in English with no contact block while NEXT_PUBLIC_SUPPORT_EMAIL is unset", async () => {
        vi.stubEnv("NEXT_PUBLIC_SUPPORT_EMAIL", "");
        renderSupport();
        expect(await screen.findByRole("heading", { level: 1, name: "Support Center" })).toBeInTheDocument();
        expect(screen.getByRole("heading", { name: "Your account and access" })).toBeInTheDocument();
        expect(screen.getByRole("heading", { name: "When you report a problem" })).toBeInTheDocument();
        expect(screen.queryByTestId("support-contact")).toBeNull();
        expect(document.querySelector('a[href^="mailto:"]')).toBeNull();
        expect(document.body.textContent).not.toMatch(/@gmail\.com|help tips|LogisticsPro/);
        await waitFor(() => expect(screen.getByText("Signed in as Somchai · CJSF · Customer view")).toBeInTheDocument());
    });

    it("renders in Thai, the heading the menu's label", async () => {
        vi.stubEnv("NEXT_PUBLIC_SUPPORT_EMAIL", "");
        renderSupport(true);
        expect(await screen.findByRole("heading", { level: 1, name: "ศูนย์ช่วยเหลือ" })).toBeInTheDocument();
        expect(screen.getByRole("heading", { name: "บัญชีและสิทธิ์การใช้งาน" })).toBeInTheDocument();
        expect(screen.getByRole("heading", { name: "เมื่อแจ้งปัญหา" })).toBeInTheDocument();
        expect(screen.queryByTestId("support-contact")).toBeNull();
    });

    it("names itself in the header breadcrumb until the menu definition holds it", async () => {
        renderSupport();
        await waitFor(() => expect(screen.getByTestId("crumb")).toHaveTextContent("Support Center"));
    });

    it("shows the configured address as a mailto link with a subject in the reader's language", async () => {
        vi.stubEnv("NEXT_PUBLIC_SUPPORT_EMAIL", "support@goratech.example");
        renderSupport();
        expect(await screen.findByRole("heading", { name: "Contact GORATECH support" })).toBeInTheDocument();
        expect(screen.getByRole("link", { name: "support@goratech.example" })).toHaveAttribute(
            "href",
            "mailto:support@goratech.example?subject=LogiTrack%20Pro%20support%20request"
        );
    });
});

describe("lib/supportContact", () => {
    it("accepts a plain address only", () => {
        expect(supportEmail(" help@goratech.example ")).toBe("help@goratech.example");
        for (const bad of [undefined, "", "help", "help@", "a b@c.d", "Help <help@goratech.example>", "x@y"]) expect(supportEmail(bad), String(bad)).toBeNull();
        expect(supportMailto("a@b.co", "ขอความช่วยเหลือ")).toBe(`mailto:a@b.co?subject=${encodeURIComponent("ขอความช่วยเหลือ")}`);
    });
});
