// UI3E (#143, the third shell critique's front door): every public page has its own tab title in the active language,
// one h1 and a named navigation; a signed-in visitor finds "Open console" in the bar and the footer; the partner form
// ends on a success card that says only what the form collected; the public copy is in sentence case.
import React from "react";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { toast } from "sonner";

vi.mock("next/navigation", () => ({
    usePathname: () => "/about",
    useRouter: () => ({ push: vi.fn(), replace: vi.fn(), refresh: vi.fn() }),
    useSearchParams: () => new URLSearchParams(),
}));
vi.mock("@/lib/loginGeo", () => ({ resolveLoginGeoForClient: async () => null }));
// The root layout's fonts are a build-time loader; only its metadata is read here.
vi.mock("next/font/google", () => {
    const font = () => ({ variable: "", className: "" });
    return { Geist: font, Geist_Mono: font, Sarabun: font };
});

import { metadata as rootMetadata } from "@/app/layout";
import * as aboutLayout from "@/app/about/layout";
import AboutPage from "@/app/about/page";
import * as joinLayout from "@/app/join-network/layout";
import JoinNetworkPage from "@/app/join-network/page";
import * as forgotLayout from "@/app/(auth)/forgot-password/layout";
import ForgotPasswordPage from "@/app/(auth)/forgot-password/page";
import * as loginLayout from "@/app/(auth)/login/layout";
import LoginPage from "@/app/(auth)/login/page";
import * as resetLayout from "@/app/(auth)/reset-password/layout";
import ResetPasswordPage from "@/app/(auth)/reset-password/page";
import * as waitlistLayout from "@/app/(auth)/waitlist/layout";
import WaitlistPage from "@/app/(auth)/waitlist/page";
import { Footer } from "@/components/landing/Footer";
import { useLanguage } from "@/context/language";
import enAbout from "@/context/locales/en/about";
import enAuth from "@/context/locales/en/auth";
import enLanding from "@/context/locales/en/landing";
import enCommon from "@/context/locales/en/common";
import thAbout from "@/context/locales/th/about";
import { PUBLIC_PAGE_TITLE_KEYS } from "@/lib/publicPageTitle";
import { fakeWeb, goErr, json, makeMe } from "@/test-utils/fakeWeb";
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
beforeEach(() => {
    document.title = "LogiTrack Pro by GORATECH";
});
afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
});

function signedOut() {
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/me", () => goErr(401, "unauthenticated"));
    web.on("POST", "/api/auth/refresh", () => goErr(401, "unauthenticated"));
    return web;
}

function signedIn(me = makeMe()) {
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/me", () => json(200, { data: me }));
    return web;
}

/** Switches the interface to Thai, as the language switch of the navigation does. */
function ToThai() {
    const { setLanguage } = useLanguage();
    return (
        <button type="button" onClick={() => setLanguage("th")}>
            to-thai
        </button>
    );
}

type Layout = { metadata: { title?: unknown }; default: (props: { children: React.ReactNode }) => React.ReactNode };

const PAGES: { route: keyof typeof PUBLIC_PAGE_TITLE_KEYS; layout: Layout; Page: () => React.ReactNode; en: string; th: string }[] = [
    { route: "/login", layout: loginLayout, Page: LoginPage, en: "Sign in · LogiTrack Pro", th: "เข้าสู่ระบบ · LogiTrack Pro" },
    { route: "/waitlist", layout: waitlistLayout, Page: WaitlistPage, en: "Join the waitlist · LogiTrack Pro", th: "เข้าร่วมรายการรอ · LogiTrack Pro" },
    { route: "/forgot-password", layout: forgotLayout, Page: ForgotPasswordPage, en: "Forgot password · LogiTrack Pro", th: "ลืมรหัสผ่าน · LogiTrack Pro" },
    { route: "/reset-password", layout: resetLayout, Page: ResetPasswordPage, en: "Set a new password · LogiTrack Pro", th: "ตั้งรหัสผ่านใหม่ · LogiTrack Pro" },
    { route: "/join-network", layout: joinLayout, Page: JoinNetworkPage, en: "Join as a partner · LogiTrack Pro", th: "ร่วมเป็นรถร่วม · LogiTrack Pro" },
    { route: "/about", layout: aboutLayout, Page: AboutPage, en: "About · LogiTrack Pro", th: "เกี่ยวกับเรา · LogiTrack Pro" },
];

describe("the tab title of each public page", () => {
    it("keeps the product's full name on the landing page", () => {
        expect(rootMetadata.title).toBe("LogiTrack Pro by GORATECH");
    });

    it("covers every public page but the landing, each with its own name", () => {
        expect(Object.keys(PUBLIC_PAGE_TITLE_KEYS).sort()).toEqual(PAGES.map((p) => p.route).sort());
        expect(new Set(PAGES.map((p) => p.en)).size).toBe(PAGES.length);
        expect(new Set(PAGES.map((p) => p.th)).size).toBe(PAGES.length);
    });

    for (const { route, layout, Page, en, th } of PAGES) {
        it(`${route}: "${th}" from the server, then the active language`, async () => {
            expect(layout.metadata).toEqual({ title: th });
            signedOut();
            const Layout = layout.default;
            renderWithProviders(
                <>
                    <ToThai />
                    <Layout>
                        <Page />
                    </Layout>
                </>
            );
            await waitFor(() => expect(document.title).toBe(en));
            // One h1 per page.
            await waitFor(() => expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1));
            await userEvent.setup().click(screen.getByRole("button", { name: "to-thai" }));
            await waitFor(() => expect(document.title).toBe(th));
            if (route === "/join-network") {
                expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("ร่วมงานกับ GORATECH ในฐานะรถร่วม");
            }
            // Next writes its metadata title back after a load; the page's title is put back.
            document.title = en;
            await waitFor(() => expect(document.title).toBe(th));
        });
    }
});

describe("a signed-in visitor of a public page", () => {
    it("finds Open console in the bar and in the footer, leading to the role's home, and no Sign in", async () => {
        signedIn(makeMe({ tenant: null, tenants: [], steward: false, customerScopes: [{ billingPartyId: "b1", name: "Shipper" }], capabilities: ["operations:view_driver_monitor"] }));
        renderWithProviders(<AboutPage />);
        const bar = await screen.findByTestId("nav-open-console");
        expect(bar).toHaveAttribute("href", "/app/driver-monitor");
        const footer = screen.getByRole("navigation", { name: "Site links" });
        expect(within(footer).getByRole("link", { name: "Open console" })).toHaveAttribute("href", "/app/driver-monitor");
        expect(screen.queryByRole("link", { name: /^sign in$/i })).toBeNull();
    });

    it("who only drives is told to use the driver app, with no console link", async () => {
        signedIn(
            makeMe({
                tenant: { id: "t-own", nameTh: "กองรถ", nameEn: "Own fleet", kind: "own_fleet", role: "driver" },
                tenants: [],
                steward: false,
                driver: { id: "d-1" },
                capabilities: ["mobile:check_in"],
            })
        );
        renderWithProviders(<AboutPage />);
        const footer = await screen.findByRole("navigation", { name: "Site links" });
        expect(await within(footer).findByTestId("footer-driver-app")).toHaveTextContent("Use the LogiTrack driver app");
        expect(screen.queryByRole("link", { name: "Open console" })).toBeNull();
        expect(screen.queryByRole("link", { name: /^sign in$/i })).toBeNull();
    });

    it("while the session is still loading, shows neither Sign in nor Open console (no flash)", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => new Promise<Response>(() => undefined));
        renderWithProviders(<AboutPage />);
        const footer = await screen.findByRole("navigation", { name: "Site links" });
        expect(within(footer).getByRole("link", { name: "About" })).toBeInTheDocument();
        expect(screen.queryByRole("link", { name: /^sign in$/i })).toBeNull();
        expect(screen.queryByRole("link", { name: "Open console" })).toBeNull();
    });

    it("signed out, the footer offers Sign in", async () => {
        signedOut();
        renderWithProviders(<Footer />);
        expect(await screen.findByRole("link", { name: "Sign in" })).toHaveAttribute("href", "/login");
        expect(screen.queryByRole("link", { name: "Open console" })).toBeNull();
    });
});

describe("the partner application, once sent", () => {
    async function apply(phone: string) {
        const web = signedOut();
        web.on("POST", "/api/forms/partner-interest", () => json(201, { data: { received: true } }));
        const success = vi.spyOn(toast, "success");
        const user = userEvent.setup();
        renderWithProviders(<JoinNetworkPage />);
        await user.type(await screen.findByLabelText(/^Company name/), "Somchai Transport");
        await user.type(screen.getByLabelText(/^Contact name/), "Somchai");
        await user.type(screen.getByLabelText(/^Email/), "somchai@example.test");
        if (phone) await user.type(screen.getByLabelText("Phone number"), phone);
        await user.click(screen.getByRole("button", { name: "Send application" }));
        return { user, success, card: await screen.findByTestId("join-success") };
    }

    it("replaces the form with a success card that says what happens next, from what the form collected", async () => {
        const { success, card } = await apply("0812345678");
        const heading = within(card).getByRole("heading", { level: 2, name: "Application sent" });
        await waitFor(() => expect(heading).toHaveFocus());
        expect(card).toHaveTextContent("Thank you, Somchai. The application from Somchai Transport to work as a partner carrier has reached GORATECH.");
        expect(within(card).getByRole("heading", { level: 3, name: "What happens next" })).toBeInTheDocument();
        expect(card).toHaveTextContent("Our team will contact you at the email address or phone number you gave:");
        expect(card).toHaveTextContent("somchai@example.test");
        expect(card).toHaveTextContent("0812345678");
        // No timeline or outcome is promised, and the card is the one confirmation (no toast as well).
        expect(card.textContent).not.toMatch(/\d+\s*(hour|day|business|working)|within|guarantee|approved/i);
        expect(success).not.toHaveBeenCalled();
        expect(screen.queryByRole("button", { name: "Send application" })).toBeNull();
        expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
    });

    it("names only the email address when no phone number was given, and can start a new application", async () => {
        const { user, card } = await apply("");
        expect(card).toHaveTextContent("Our team will contact you at the email address you gave:");
        expect(card.textContent).not.toMatch(/phone/i);
        expect(within(card).getByRole("link", { name: "Back to home" })).toHaveAttribute("href", "/");
        await user.click(within(card).getByRole("button", { name: "Send another application" }));
        expect(await screen.findByRole("button", { name: "Send application" })).toBeInTheDocument();
        expect(screen.getByLabelText(/^Company name/)).toHaveValue("");
        await waitFor(() => expect(screen.getByLabelText(/^Company name/)).toHaveFocus());
    });
});

describe("the waitlist form (DESIGN.md form style, UI3E review)", () => {
    it("shows each missing or malformed field's error under it, focuses the first, and posts nothing", async () => {
        const web = signedOut();
        const summary = vi.spyOn(toast, "error");
        const user = userEvent.setup();
        renderWithProviders(<WaitlistPage />);
        const name = await screen.findByLabelText(/^Full name/);
        const email = screen.getByLabelText(/^Email/);
        const phone = screen.getByLabelText(/^Phone number/);
        for (const input of [name, email, phone]) {
            // No browser popup: the form validates itself.
            expect(input).not.toHaveAttribute("required");
            expect(input).toHaveAttribute("aria-required", "true");
        }
        expect(name.closest("form")).toHaveAttribute("novalidate");
        await user.type(email, "not-an-address");
        await user.click(screen.getByRole("button", { name: "Request access" }));

        expect(name).toHaveAttribute("aria-invalid", "true");
        expect(name).toHaveAccessibleDescription("Enter your full name.");
        expect(email).toHaveAccessibleDescription("Enter an email address such as name@company.co.th.");
        expect(phone).toHaveAccessibleDescription("Enter your phone number.");
        await waitFor(() => expect(name).toHaveFocus());
        expect(summary).toHaveBeenCalledWith("Check the highlighted fields (3). First: Enter your full name.");
        expect(web.count("POST", "/api/forms/waitlist")).toBe(0);

        // Editing a field clears its error.
        await user.type(name, "Somchai");
        expect(name).not.toHaveAttribute("aria-invalid");
        expect(name).not.toHaveAccessibleDescription();
    });

    it("once sent, the success card is the one confirmation and takes the focus (no toast as well)", async () => {
        const web = signedOut();
        web.on("POST", "/api/forms/waitlist", () => json(201, { data: { received: true } }));
        const success = vi.spyOn(toast, "success");
        const user = userEvent.setup();
        renderWithProviders(<WaitlistPage />);
        await user.type(await screen.findByLabelText(/^Full name/), "Somchai");
        await user.type(screen.getByLabelText(/^Email/), "somchai@example.test");
        await user.type(screen.getByLabelText(/^Phone number/), "812345678");
        await user.click(screen.getByRole("button", { name: "Request access" }));
        const heading = await screen.findByRole("heading", { level: 1, name: "You are on the waitlist" });
        await waitFor(() => expect(heading).toHaveFocus());
        expect(success).not.toHaveBeenCalled();
    });
});

describe("the public copy is in sentence case (DESIGN.md, UI3E)", () => {
    /** Words that keep their capital inside a sentence: names, the product, codes, the Thai word glossed. */
    const PROPER = /^(LogiTrack|Pro|GORATECH|AppSheet|Thailand|Thai|Bangkok|Chon|Buri|Somchai|Khonsongdee|Google|January|SOCs?|I|(?:\d+)?W[HJ]?|SPX|SPK|[A-Z]{2,})$/;
    const LABEL_KEYS = [
        ...Object.keys(enAuth).filter((k) => /title|submit|backToLogin|companiesWaitlist|alreadyHaveAccount|forgotPassword/.test(k)),
        ...Object.keys(enLanding).filter((k) => /\.title$|nav\.|cta\.(requestAccess|joinNetwork|readStory)|apply$|success\.(again|home)|form\.submit$/.test(k)),
        ...Object.keys(enAbout).filter((k) => /title$/.test(k)),
        "nav.joinNetwork",
        "nav.myAccount",
        "nav.signIn",
        "landing.openConsole",
    ];
    const all: Record<string, string> = { ...enCommon, ...enAuth, ...enLanding, ...enAbout };

    it("capitalises only the first word of each label and heading, and names", () => {
        for (const key of LABEL_KEYS) {
            // Each sentence of a label starts with a capital ("Already have an account? Sign in").
            for (const sentence of (all[key] ?? "").split(/[.?!:]\s+/)) {
                const words = sentence.replace(/[“”"?!:.,()]/g, " ").split(/\s+/).filter(Boolean);
                for (const word of words.slice(1)) {
                    if (/^[A-Z]/.test(word)) expect(PROPER.test(word), `${key}: "${all[key]}" (${word})`).toBe(true);
                }
            }
        }
    });

    it("about: the owner's words, the same keys in both languages, and no claim the product has no evidence for", () => {
        // The owner's origin story stays in the owner's words (UI3E review round).
        expect(enAbout["about.hero.title"]).toBe("The journey of GORATECH: from challenge to building innovation");
        expect(thAbout["about.hero.title"]).toBe("The Journey of GORATECH: จากความท้าทาย สู่การก่อร่างนวัตกรรม");
        expect(enAbout["about.closing"]).toContain("not just a domain name");
        expect(thAbout["about.closing"]).toContain("ไม่ใช่เพียงแค่ชื่อโดเมน");
        expect(enAbout["about.story.p1"]).toContain("Amid the wave of using off-the-shelf tools like AppSheet");
        expect(enAbout["about.story.answer"]).toContain("custom-coded platform");
        expect(thAbout["about.story.answer"]).toContain("Custom-coded Platform");
        expect(Object.keys(thAbout).sort()).toEqual(Object.keys(enAbout).sort());
        for (const dict of [enAbout, thAbout]) {
            for (const [key, value] of Object.entries(dict)) {
                expect(value, key).not.toMatch(
                    /App Check|RBAC|international|ระดับสากล|without limits|ไร้ขีดจำกัด|Low Latency|every line|ทุกบรรทัด|Enterprise|new standard|มาตรฐานใหม่|ecosystem|อาณาจักรเทคโนโลยี|several transport companies|หลายบริษัทขนส่ง/i
                );
            }
        }
    });
});
