import React from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { AppBreadcrumb } from "@/components/app-breadcrumb";
import { menuFor, menuLinksFor } from "@/components/app-nav";
import { BreadcrumbProvider } from "@/context/breadcrumb";
import { useLanguage } from "@/context/language";
import { RouteNamespaces } from "@/context/locales/RouteNamespaces";
import { homeRouteOf } from "@/features/auth/api/homeRoute";
import { navigationProfileOf, type NavigationProfile } from "@/features/auth/utils/navigationProfile";
import { IncidentReportsPage } from "@/features/incident-reports/components/go/IncidentReportsPage";
import { fakeWeb, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { FirstMileBoardPage, JobAssignBoardPage, LineHaulBoardPage } from "./TaskBoardPage";

function LanguageButton() {
    const { language, setLanguage } = useLanguage();
    return <button onClick={() => setLanguage(language === "en" ? "th" : "en")}>language</button>;
}

const CAPS = ["operations:view_driver_monitor", "operations:view_first_mile", "operations:view_line_haul", "operations:view_incidents"];

function renderPage(profile: NavigationProfile, pathname: string, page: React.ReactElement) {
    const me = makeMe({
        capabilities: CAPS,
        tenant: profile === "customer" ? null : { id: "t", nameTh: "ขนส่ง", nameEn: "Transport", kind: profile === "partner" ? "carrier" : "own_fleet", role: "tenant_admin" },
        customerScopes: profile === "customer" ? [{ billingPartyId: "customer", name: "Customer" }] : [],
    });
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/me", () => json(200, { data: me }));
    for (const path of ["tasks", "incidents", "hubs"]) web.on("GET", `/api/go/v1/${path}`, () => json(200, { data: [] }));
    const home = homeRouteOf(me);
    renderWithProviders(
        <BreadcrumbProvider>
            <AppBreadcrumb pathname={pathname} homeHref={home} />
            <RouteNamespaces pathname={pathname}>{page}</RouteNamespaces>
            <LanguageButton />
        </BreadcrumbProvider>
    );
    return menuLinksFor(menuFor(new Set(CAPS), home, navigationProfileOf(me))).find((link) => link.href === pathname);
}

afterEach(() => vi.unstubAllGlobals());

describe("profile route names rendered in both languages (UI3F)", () => {
    for (const profile of ["staff", "customer", "partner"] as const) {
        it.each([
            { pathname: "/app/job-assign", page: <JobAssignBoardPage />, staffEn: "Job assignment", staffTh: "มอบหมายงาน", scopedEn: "Transport tasks", scopedTh: "งานขนส่ง" },
            { pathname: "/app/incident-reports", page: <IncidentReportsPage />, staffEn: "Incident reports", staffTh: "รายงานปัญหาการจัดส่ง", scopedEn: "Delivery issues", scopedTh: "ปัญหาการจัดส่ง" },
        ])(`${profile}: $pathname shares its menu label, breadcrumb, H1 and document title`, async ({ pathname, page, staffEn, staffTh, scopedEn, scopedTh }) => {
            const link = renderPage(profile, pathname, page);
            expect(link).toBeDefined();
            const en = profile === "staff" ? staffEn : scopedEn;
            const th = profile === "staff" ? staffTh : scopedTh;
            expect(await screen.findByRole("heading", { level: 1, name: en })).toBeInTheDocument();
            expect(within(screen.getByRole("navigation", { name: "Breadcrumb" })).getByText(en)).toBeInTheDocument();
            await waitFor(() => expect(document.title).toBe(`${en} · LogiTrack Pro`));
            if (pathname === "/app/job-assign" && profile !== "staff") {
                expect(screen.getByText("Follow transport tasks and their progress.")).toBeInTheDocument();
            }
            await userEvent.setup().click(screen.getByRole("button", { name: "language" }));
            expect(await screen.findByRole("heading", { level: 1, name: th })).toBeInTheDocument();
            await waitFor(() => expect(document.title).toBe(`${th} · LogiTrack Pro`));
            expect(document.body.textContent).not.toMatch(/nav\.|incidentReports\./);
        });
    }

    it.each([
        { pathname: "/app/first-mile", page: <FirstMileBoardPage />, label: "First mile tasks" },
        { pathname: "/app/line-haul", page: <LineHaulBoardPage />, label: "Line haul tasks" },
    ])("retains $pathname's canonical name for a partner opening the direct route", async ({ pathname, page, label }) => {
        expect(renderPage("partner", pathname, page)).toBeUndefined();
        expect(await screen.findByRole("heading", { level: 1, name: label })).toBeInTheDocument();
        await waitFor(() => expect(document.title).toBe(`${label} · LogiTrack Pro`));
    });
});
