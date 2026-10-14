// UI3C (#143): `/` reads the session cookie on the server (app/page.tsx); `/waitlist` and `/join-network` read as
// Thai in Thai mode; the partner form shows its errors on the fields, not only in a toast.
import React, { useEffect } from "react";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";

const jar = new Set<string>();
vi.mock("next/headers", () => ({ cookies: async () => ({ has: (name: string) => jar.has(name) }) }));
vi.mock("next/navigation", () => ({
    usePathname: () => "/join-network",
    useRouter: () => ({ push: vi.fn(), replace: vi.fn(), refresh: vi.fn() }),
    useSearchParams: () => new URLSearchParams(),
}));

import Home from "@/app/page";
import JoinNetworkPage from "@/app/join-network/page";
import WaitlistForm from "@/features/auth/components/WaitlistForm";
import LoginForm from "@/features/auth/components/LoginForm";
import { LandingPage } from "../LandingPage";
import { useLanguage } from "@/context/language";
import { fakeWeb, goErr } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";

beforeAll(() => {
    Element.prototype.hasPointerCapture ??= () => false;
    Element.prototype.scrollIntoView ??= () => undefined;
    globalThis.ResizeObserver ??= class {
        observe() {}
        unobserve() {}
        disconnect() {}
    } as unknown as typeof ResizeObserver;
});
afterEach(() => {
    jar.clear();
    vi.unstubAllGlobals();
});

function Thai({ children }: { children: React.ReactNode }) {
    const { language, setLanguage } = useLanguage();
    useEffect(() => setLanguage("th"), [setLanguage]);
    return language === "th" ? <>{children}</> : null;
}

function signedOut() {
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/me", () => goErr(401, "unauthenticated"));
    web.on("POST", "/api/auth/refresh", () => goErr(401, "unauthenticated"));
    return web;
}

describe("app/page.tsx", () => {
    it("passes the presence of the session cookie to the landing as sessionHint", async () => {
        jar.add("lt_at");
        const withCookie = await Home();
        expect(withCookie.type).toBe(LandingPage);
        expect(withCookie.props).toEqual({ sessionHint: true });
        jar.clear();
        expect((await Home()).props).toEqual({ sessionHint: false });
    });
});

describe("Thai mode", () => {
    it("/waitlist reads in Thai", async () => {
        signedOut();
        renderWithProviders(
            <Thai>
                <WaitlistForm />
            </Thai>
        );
        expect(await screen.findByText("เข้าร่วมรายการรอ")).toBeInTheDocument();
        expect(screen.getByLabelText(/^ชื่อ-นามสกุล/)).toBeInTheDocument();
        expect(screen.getByLabelText(/^เบอร์โทรศัพท์/)).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "ขอสิทธิ์เข้าใช้งาน" })).toBeInTheDocument();
        expect(screen.getByRole("link", { name: "มีบัญชีอยู่แล้ว? เข้าสู่ระบบ" })).toHaveAttribute("href", "/login");
        expect(document.body.textContent).not.toMatch(/Full name|Phone number|Request access|🇹🇭/i);
    });

    it("the sign-in card says who to ask for an account, and offers the waitlist to transport companies, in Thai (UI3E)", async () => {
        signedOut();
        renderWithProviders(
            <Thai>
                <LoginForm />
            </Thai>
        );
        expect(await screen.findByTestId("sign-in-no-account")).toHaveTextContent("ยังไม่มีบัญชี? สอบถามบริษัทขนส่งหรือผู้ดูแลระบบที่เชิญคุณเข้าใช้งาน");
        expect(screen.getByText(/^ทำธุรกิจขนส่ง\?/)).toBeInTheDocument();
        expect(screen.getByRole("link", { name: "ลงชื่อรอใช้ LogiTrack Pro" })).toHaveAttribute("href", "/waitlist");
        expect(screen.getByRole("heading", { level: 1, name: "เข้าสู่ระบบ" })).toBeInTheDocument();
        expect(screen.getByLabelText(/^อีเมล/)).toHaveAttribute("aria-required", "true");
    });

    it("/join-network reads in Thai", async () => {
        signedOut();
        renderWithProviders(
            <Thai>
                <JoinNetworkPage />
            </Thai>
        );
        expect(await screen.findByRole("heading", { level: 1, name: "ร่วมงานกับ GORATECH ในฐานะรถร่วม" })).toBeInTheDocument();
        expect(screen.getByRole("heading", { level: 2, name: "แบบฟอร์มสมัครเป็นรถร่วม" })).toBeInTheDocument();
        expect(screen.getByLabelText(/^ชื่อบริษัท/)).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "ส่งใบสมัคร" })).toBeInTheDocument();
        expect(screen.getByRole("navigation", { name: "เมนูหลัก" })).toBeInTheDocument();
        expect(document.body.textContent).not.toMatch(/LogisticsPro|Company name|Submit Application/);
    });
});

describe("the partner form", () => {
    it("marks each missing or wrong field with its own error, and focuses the first", async () => {
        const web = signedOut();
        const user = userEvent.setup();
        renderWithProviders(<JoinNetworkPage />);
        const company = await screen.findByLabelText(/^Company name/);
        await user.type(screen.getByLabelText(/^Email/), "not-an-address");
        await user.click(screen.getByRole("button", { name: "Send application" }));

        expect(company).toHaveAttribute("aria-invalid", "true");
        expect(company).toHaveAccessibleDescription("Enter the company name.");
        expect(screen.getByLabelText(/^Contact name/)).toHaveAccessibleDescription("Enter the name of the person to contact.");
        expect(screen.getByLabelText(/^Email/)).toHaveAccessibleDescription("Enter an email address such as name@company.co.th.");
        expect(screen.getByLabelText("Phone number")).not.toHaveAttribute("aria-invalid");
        await waitFor(() => expect(company).toHaveFocus());
        expect(web.count("POST", "/api/forms/partner-interest")).toBe(0);

        // Editing a field clears its error.
        await user.type(company, "Somchai Transport");
        expect(company).not.toHaveAttribute("aria-invalid");
        expect(company).not.toHaveAccessibleDescription();
    });
});
