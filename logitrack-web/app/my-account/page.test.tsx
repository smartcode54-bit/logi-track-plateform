import React from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";

const replace = vi.hoisted(() => vi.fn());
const router = { replace, push: vi.fn(), refresh: vi.fn() };
vi.mock("next/navigation", () => ({ useRouter: () => router, usePathname: () => "/my-account" }));

import { AuthProvider } from "@/context/auth";
import { LanguageProvider } from "@/context/language";
import * as languageContext from "@/context/language";
import { createQueryClient } from "@/lib/queryClient";
import { fakeWeb, goErr, json, makeMe } from "@/test-utils/fakeWeb";
import MyAccountPage from "./page";

function renderAccount(language: "en" | "th" = "en") {
    window.localStorage.setItem("language", language);
    const client = createQueryClient();
    client.setDefaultOptions({ queries: { retry: false } });
    return render(<QueryClientProvider client={client}><LanguageProvider><AuthProvider><MyAccountPage /></AuthProvider></LanguageProvider></QueryClientProvider>);
}

beforeEach(() => {
    replace.mockClear();
    window.history.replaceState(null, "", "/my-account");
    window.localStorage.clear();
});
afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
});

describe("My account", () => {
    it.each([
        ["en", "My account", "Profile", "Name", "Change password", "Own fleet · Tenant admin"],
        ["th", "บัญชีของฉัน", "ข้อมูลบัญชี", "ชื่อ", "เปลี่ยนรหัสผ่าน", "กองรถ · ผู้ดูแลองค์กร"],
    ] as const)("renders in %s with one email, no UUID and a localized title", async (language, title, profile, name, password, scope) => {
        const web = fakeWeb();
        const me = makeMe({ id: "0199c000-0000-7000-8000-0000000000a1", displayName: "เกศินี" });
        web.on("GET", "/api/go/v1/me", () => json(200, { data: me }));
        renderAccount(language);
        expect(await screen.findByRole("heading", { level: 1, name: title })).toBeInTheDocument();
        const main = within(screen.getByRole("main"));
        expect(await main.findByRole("heading", { name: profile })).toBeInTheDocument();
        expect(main.getByText(name, { exact: true })).toBeInTheDocument();
        expect(main.getByText("เกศินี")).toBeInTheDocument();
        expect(main.getByRole("region", { name: profile })).toHaveTextContent(scope);
        expect(await main.findByRole("heading", { name: password })).toBeInTheDocument();
        expect(screen.getAllByText(me.email!)).toHaveLength(1);
        expect(document.body).not.toHaveTextContent(me.id);
        expect(main.getByText("ก", { exact: true })).toBeInTheDocument();
        expect(main.queryByRole("img")).toBeNull();
        expect(document.title).toBe(`${title} · LogiTrack Pro`);
        expect(replace).not.toHaveBeenCalled();
        expect(document.body.textContent).not.toMatch(/auth\.account\.|nav\.myAccount/);
    });

    it("uses the translated brand in the document title", async () => {
        const useLanguage = languageContext.useLanguage;
        vi.spyOn(languageContext, "useLanguage").mockImplementation(function useBrandedLanguage() {
            const context = useLanguage();
            return { ...context, t: (key, params) => key === "brand.name" ? "Translated brand" : context.t(key, params) };
        });
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe() }));
        renderAccount();
        await screen.findByRole("heading", { name: "Profile" });
        expect(document.title).toBe("My account · Translated brand");
    });

    it("does not duplicate the email as a missing display name", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ displayName: null }) }));
        renderAccount();
        expect(await screen.findByText("Not set")).toBeInTheDocument();
        expect(screen.getAllByText("admin@own.test")).toHaveLength(1);
    });

    it("shows loading without a profile flash and redirects only once signed-out is resolved", async () => {
        const web = fakeWeb();
        let finish!: (response: Response) => void;
        web.on("GET", "/api/go/v1/me", () => new Promise<Response>((resolve) => { finish = resolve; }));
        web.on("POST", "/api/auth/refresh", () => goErr(401, "unauthenticated"));
        renderAccount();
        expect(await screen.findByText("Loading...")).toBeInTheDocument();
        expect(screen.queryByRole("heading", { name: "Profile" })).toBeNull();
        expect(replace).not.toHaveBeenCalled();
        finish(goErr(401, "unauthenticated"));
        expect(await screen.findByText("Redirecting to sign in...")).toBeInTheDocument();
        await waitFor(() => expect(replace).toHaveBeenCalledTimes(1));
        expect(replace).toHaveBeenCalledWith("/login");
        expect(screen.queryByLabelText(/^Current password/)).toBeNull();
    });

    it("shows a translated load error without redirecting", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => goErr(503, "unavailable"));
        renderAccount("th");
        expect(await screen.findByRole("alert")).toHaveTextContent("ไม่สามารถโหลดข้อมูลบัญชีได้ กรุณาลองใหม่อีกครั้ง");
        expect(replace).not.toHaveBeenCalled();
        expect(screen.queryByLabelText(/^รหัสผ่านปัจจุบัน/)).toBeNull();
    });

    it("keeps driver access to the account page", async () => {
        const web = fakeWeb();
        const tenant = { id: "own", nameTh: "กองรถ", nameEn: "Own fleet", kind: "own_fleet", role: "driver" };
        web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ tenant, tenants: [tenant], driver: { id: "d-1" }, steward: false, capabilities: [] }) }));
        renderAccount();
        expect(await screen.findByLabelText(/^Current password/)).toBeInTheDocument();
        expect(replace).not.toHaveBeenCalled();
    });
});
