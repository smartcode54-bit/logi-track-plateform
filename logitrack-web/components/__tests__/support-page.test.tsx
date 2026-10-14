// UI3C (#143): support lives inside the console (`/app/support`), in both languages, open to every console principal,
// names no contact the owner has not configured (NEXT_PUBLIC_SUPPORT_EMAIL), and always says where to go: the GORATECH
// address when there is one (with what to put in a report), else the people who invited the reader.
import React, { useEffect } from "react";
import { screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("next/navigation", () => ({
    usePathname: () => "/app/support",
    useRouter: () => ({ push: vi.fn(), replace: vi.fn(), refresh: vi.fn() }),
    useSearchParams: () => new URLSearchParams(),
}));

import SupportPage from "@/app/app/support/page";
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
    const page = <SupportPage />;
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

    it("with NEXT_PUBLIC_SUPPORT_EMAIL unset (en): no contact, no report checklist, and who to ask instead", async () => {
        vi.stubEnv("NEXT_PUBLIC_SUPPORT_EMAIL", "");
        renderSupport();
        expect(await screen.findByRole("heading", { level: 1, name: "Support" })).toBeInTheDocument();
        const ask = screen.getByRole("region", { name: "Who to ask for help" });
        expect(ask).toHaveTextContent("your company's administrator");
        expect(ask).toHaveTextContent("the transport company you work with");
        expect(screen.queryByTestId("support-contact")).toBeNull();
        expect(screen.queryByTestId("support-report")).toBeNull();
        expect(screen.queryByRole("heading", { name: "When you report a problem" })).toBeNull();
        expect(document.querySelector('a[href^="mailto:"]')).toBeNull();
        expect(document.body.textContent).not.toMatch(/@gmail\.com|help tips|LogisticsPro/);
        await waitFor(() => expect(screen.getByText("Signed in as Somchai · CJSF · Customer view")).toBeInTheDocument());
    });

    it("with NEXT_PUBLIC_SUPPORT_EMAIL unset (th): the same, in Thai, the heading the menu's label", async () => {
        vi.stubEnv("NEXT_PUBLIC_SUPPORT_EMAIL", "");
        renderSupport(true);
        expect(await screen.findByRole("heading", { level: 1, name: "ศูนย์ช่วยเหลือ" })).toBeInTheDocument();
        expect(screen.getByRole("region", { name: "ขอความช่วยเหลือจากใคร" })).toHaveTextContent("ผู้ดูแลระบบของบริษัทคุณ");
        expect(screen.queryByRole("heading", { name: "เมื่อแจ้งปัญหา" })).toBeNull();
        expect(screen.queryByTestId("support-contact")).toBeNull();
    });

    it("with an address set (en): the mailto link, the account card and what to put in a report", async () => {
        vi.stubEnv("NEXT_PUBLIC_SUPPORT_EMAIL", "support@goratech.example");
        renderSupport();
        expect(await screen.findByRole("heading", { name: "Contact GORATECH support" })).toBeInTheDocument();
        expect(screen.getByRole("link", { name: "support@goratech.example" })).toHaveAttribute(
            "href",
            "mailto:support@goratech.example?subject=LogiTrack%20Pro%20support%20request"
        );
        expect(screen.getByRole("region", { name: "Your account and access" })).toBeInTheDocument();
        expect(screen.getByRole("region", { name: "When you report a problem" })).toHaveTextContent("the date and time it happened");
        expect(screen.queryByTestId("support-ask")).toBeNull();
    });

    it("with an address set (th): the subject and the report card in Thai", async () => {
        vi.stubEnv("NEXT_PUBLIC_SUPPORT_EMAIL", "support@goratech.example");
        renderSupport(true);
        expect(await screen.findByRole("heading", { name: "ติดต่อฝ่ายช่วยเหลือของ GORATECH" })).toBeInTheDocument();
        expect(screen.getByRole("link", { name: "support@goratech.example" })).toHaveAttribute(
            "href",
            `mailto:support@goratech.example?subject=${encodeURIComponent("ขอความช่วยเหลือ LogiTrack Pro")}`
        );
        expect(screen.getByRole("region", { name: "เมื่อแจ้งปัญหา" })).toBeInTheDocument();
    });
});

describe("lib/supportContact", () => {
    it("accepts a plain address only: nothing that could add a header, a fragment or a path to the link", () => {
        expect(supportEmail(" help@goratech.example ")).toBe("help@goratech.example");
        expect(supportEmail("first.last+tag@sub.goratech-th.co.th")).toBe("first.last+tag@sub.goratech-th.co.th");
        for (const bad of [
            undefined,
            "",
            "help",
            "help@",
            "a b@c.d",
            "Help <help@goratech.example>",
            "x@y",
            "help@x.co?bcc=y",
            "help@x.co&cc=y",
            "help@x.co#top",
            "help@x.co/path",
            "he%6Cp@x.co",
            "help=x@x.co",
            "help@x..co",
        ])
            expect(supportEmail(bad), String(bad)).toBeNull();
        expect(supportMailto("a@b.co", "ขอความช่วยเหลือ")).toBe(`mailto:a@b.co?subject=${encodeURIComponent("ขอความช่วยเหลือ")}`);
    });
});
