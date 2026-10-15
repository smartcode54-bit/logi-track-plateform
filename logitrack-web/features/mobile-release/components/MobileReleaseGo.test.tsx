// T54S (issue #74, security-center slice): mobile-clients and mobile-release over Go (T52S routes). The web
// has no Firebase SDK since TW7 (features/security-center/firebaseImports.test.ts walks the graph). The ADR 0007 guards are kept (no floor without a download link, none
// above the latest release, a typed confirmation with the impact before a floor that blocks more drivers).
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import React from "react";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { fakeWeb, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import type { MeDTO } from "@/features/auth/api/me";
import { renderInLanguage } from "@/features/security-center/test-utils/renderInLanguage";
import { MobileClientsPage } from "./MobileClientsPage";
import { floorBlockedReason, MobileReleasePage, raisesFloor } from "./MobileReleasePage";
import type { GateViewDTO } from "../api/releases";

beforeAll(() => {
    Element.prototype.hasPointerCapture ??= () => false;
    Element.prototype.releasePointerCapture ??= () => undefined;
    Element.prototype.scrollIntoView ??= () => undefined;
    globalThis.ResizeObserver ??= class {
        observe() {}
        unobserve() {}
        disconnect() {}
    } as unknown as typeof ResizeObserver;
});
beforeEach(() => {
    window.history.replaceState(null, "", "/app/security-center/mobile-release");
});
afterEach(() => {
    vi.unstubAllGlobals();
});

function setup(meOver: Partial<MeDTO> = {}) {
    const web = fakeWeb();
    const me = makeMe({
        id: "u-self",
        capabilities: ["security:view_mobile_clients", "security:manage_mobile_release"],
        ...meOver,
    });
    web.on("GET", "/api/go/v1/me", () => json(200, { data: me }));
    return web;
}

const GATE: GateViewDTO = {
    flavor: "prod",
    minAllowedVersion: "3.1.0",
    latestVersion: "3.2.0",
    latestBuildNumber: "12",
    apkDownloadUrl: "https://media.test/app_releases/prod/3.2.0.apk",
    apkSha256: "ab".repeat(32),
    apkSizeBytes: 31_457_280,
    releaseNotes: "Fixes",
    releasedAt: "2026-10-10T02:00:00Z",
    minAllowedVersionSetAt: "2026-10-01T02:00:00Z",
    minAllowedVersionSetBy: "admin@own.test",
    releasedBy: "cmd/release",
    updatedAt: "2026-10-10T02:00:00Z",
};

const t = (key: string, params?: Record<string, string | number>) => (params ? `${key} ${JSON.stringify(params)}` : key);

describe("floor rules (ADR 0007)", () => {
    it("refuses a floor without a download link, above the latest release, malformed or unchanged", () => {
        expect(floorBlockedReason(GATE, "3.2.0", t)).toBeNull();
        // The Go page has no link field: the copy names the remedy (publish again with cmd/release).
        expect(floorBlockedReason({ ...GATE, apkDownloadUrl: null }, "3.2.0", t)).toBe("securityCenter.mobileRelease.noDownloadLinkGo");
        expect(floorBlockedReason({ ...GATE, apkDownloadUrl: " " }, "3.2.0", t)).toBe("securityCenter.mobileRelease.noDownloadLinkGo");
        // Nothing published yet: "publish a build first", not "set a link".
        expect(floorBlockedReason({ ...GATE, latestVersion: null, apkDownloadUrl: null }, "3.2.0", t)).toBe("securityCenter.mobileRelease.forceDisabledNoLatest");
        expect(floorBlockedReason(GATE, "3.3.0", t)).toContain("securityCenter.mobileRelease.aboveLatest");
        expect(floorBlockedReason(GATE, "3.2", t)).toBe("securityCenter.mobileRelease.invalidVersion");
        // Go's releaseVersionRx: canonical parts only (no leading zeros, at most nine digits), so a
        // spelling Go refuses never reaches the impact count or the typed confirmation.
        for (const v of ["3.02.0", "3.1.00", "03.1.0", "1234567890.0.0", "3.1.0-rc1", " 3.1.0"]) {
            expect(floorBlockedReason(GATE, v, t), v).toBe("securityCenter.mobileRelease.invalidVersion");
        }
        expect(floorBlockedReason({ ...GATE, latestVersion: "999999999.0.0" }, "999999999.0.0", t)).toBeNull();
        expect(floorBlockedReason({ ...GATE, latestVersion: "3.2.0" }, "0.0.0", t)).toBeNull();
        expect(floorBlockedReason(GATE, "3.1.0", t)).toBe("securityCenter.mobileRelease.sameFloor");
        // Lifting the floor is always possible while there is one.
        expect(floorBlockedReason({ ...GATE, apkDownloadUrl: null }, null, t)).toBeNull();
        expect(floorBlockedReason({ ...GATE, minAllowedVersion: null }, null, t)).toBe("securityCenter.mobileRelease.noFloorToRemove");
    });

    it("asks for the typed confirmation only when the floor blocks more", () => {
        expect(raisesFloor("3.1.0", "3.2.0")).toBe(true);
        expect(raisesFloor(null, "3.0.0")).toBe(true);
        expect(raisesFloor("3.1.0", "3.0.0")).toBe(false);
        expect(raisesFloor("3.1.0", null)).toBe(false);
    });
});

describe("Mobile release (Go)", () => {
    function routes(web: ReturnType<typeof setup>, gate: GateViewDTO = GATE) {
        web.on("GET", "/api/go/v1/app-releases", () =>
            json(200, {
                data: [
                    { id: "r-2", flavor: "prod", version: "3.2.0", buildNumber: "12", apkKey: "k", apkUrl: gate.apkDownloadUrl, apkSizeBytes: 1, apkSha256: "x", releaseNotes: null, releasedAt: "2026-10-10T02:00:00Z", releasedBy: "cmd/release", storageBackend: "local", latest: true },
                    { id: "r-1", flavor: "prod", version: "3.1.0", buildNumber: "11", apkKey: "k", apkUrl: null, apkSizeBytes: 1, apkSha256: "y", releaseNotes: null, releasedAt: "2026-09-10T02:00:00Z", releasedBy: "cmd/release", storageBackend: "local", latest: false },
                ],
                meta: { settings: gate },
            })
        );
        web.on("GET", "/api/go/v1/app-installations/stats", (c) => {
            const below = new URL(c.url, "http://web.test").searchParams.get("belowVersion");
            return json(200, {
                data: {
                    since: "2026-10-04T00:00:00Z",
                    total: 30,
                    byStatus: { current: 20, outdated: 6, blocked: 2, ahead: 0, unknown: 2 },
                    byFlavor: { prod: 30, dev: 0 },
                    byPlatform: { android: 30, ios: 0, other: 0 },
                    belowVersion: below ? { version: below, count: 8 } : null,
                    flavor: "prod",
                    minAllowedVersion: gate.minAllowedVersion,
                    latestVersion: gate.latestVersion,
                },
            });
        });
    }

    it("shows the published build read-only and forces the floor to the latest after a typed confirmation", async () => {
        const web = setup();
        routes(web);
        web.on("PUT", "/api/go/v1/app-releases/floor", () =>
            json(200, { data: { ...GATE, minAllowedVersion: "3.2.0" }, meta: { changed: true, blocked7d: 8 } })
        );
        renderWithProviders(<MobileReleasePage />);
        const u = userEvent.setup();
        expect(await screen.findByTestId("published-build")).toHaveTextContent("3.2.0");
        expect(screen.getAllByTestId("release-row")).toHaveLength(2);
        expect(screen.getByTestId("current-floor")).toHaveTextContent("3.1.0");
        // No form writes the release half any more (ADR 0007: cmd/release owns it).
        expect(screen.queryByLabelText("APK download URL")).toBeNull();

        await u.click(screen.getByRole("button", { name: "Force all drivers to 3.2.0" }));
        const dialog = await screen.findByRole("dialog");
        expect(await within(dialog).findByTestId("floor-impact")).toHaveTextContent("This will block 8 of 30 installations");
        const stats = web.calls.find((c) => c.url.startsWith("/api/go/v1/app-installations/stats"));
        expect(new URL(stats!.url, "http://web.test").searchParams.get("belowVersion")).toBe("3.2.0");
        const confirm = within(dialog).getByRole("button", { name: "Force update" });
        expect(confirm).toBeDisabled();
        await u.type(within(dialog).getByLabelText("Type 3.2.0 to confirm"), "3.2.0");
        await u.click(confirm);
        await waitFor(() => expect(web.count("PUT", "/api/go/v1/app-releases/floor")).toBe(1));
        expect(web.calls.find((c) => c.method === "PUT")?.body).toEqual({ minAllowedVersion: "3.2.0" });
        // The floor write invalidates the release list (and the gate and installations).
        await waitFor(() => expect(web.count("GET", "/api/go/v1/app-releases")).toBe(2));
        expect(web.calls.every((c) => c.url.startsWith("/api/go/") || c.url.startsWith("/api/auth/"))).toBe(true);
    });

    it("cannot force or set a floor while the gate has no download link, but can lift the floor", async () => {
        const web = setup();
        routes(web, { ...GATE, apkDownloadUrl: null });
        web.on("PUT", "/api/go/v1/app-releases/floor", () => json(200, { data: { ...GATE, minAllowedVersion: null }, meta: { changed: true, blocked7d: 0 } }));
        renderWithProviders(<MobileReleasePage />);
        const u = userEvent.setup();
        expect(await screen.findByTestId("force-blocked")).toHaveTextContent("The published build has no download link. Ask the release owner to publish it again");
        expect(screen.getByRole("button", { name: "Force all drivers to 3.2.0" })).toBeDisabled();
        await u.type(screen.getByLabelText("New minimum version"), "3.0.0");
        expect(screen.getByRole("button", { name: "Set minimum" })).toBeDisabled();

        await u.click(screen.getByRole("button", { name: "Remove minimum" }));
        const dialog = await screen.findByRole("dialog");
        await u.click(within(dialog).getByRole("button", { name: "Remove minimum" }));
        await waitFor(() => expect(web.count("PUT", "/api/go/v1/app-releases/floor")).toBe(1));
        expect(web.calls.find((c) => c.method === "PUT")?.body).toEqual({ minAllowedVersion: null });
    });

    it("keeps Set disabled for a version spelling Go refuses, without asking for its impact", async () => {
        const web = setup();
        routes(web);
        renderWithProviders(<MobileReleasePage />);
        const u = userEvent.setup();
        await screen.findByTestId("published-build");
        await u.type(screen.getByLabelText("New minimum version"), "3.1.00");
        expect(screen.getByText("Enter a version like 1.2.3")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Set minimum" })).toBeDisabled();
        expect(web.calls.some((c) => c.url.startsWith("/api/go/v1/app-installations/stats"))).toBe(false);
    });
});

describe("Mobile clients (Go)", () => {
    it("lists installations with Go's version status and the 7-day summary; a platform principal reads every tenant", async () => {
        const web = setup({ tenant: null, tenants: [], platformRoles: ["platform_admin"] });
        web.on("GET", "/api/go/v1/app-installations", () =>
            json(200, {
                data: [
                    { driverId: "d-1", driverName: "สมชาย", installId: "i-1", tenantId: "t-car", tenantName: "Carrier One", platform: "android", appVersion: "3.0.0", buildNumber: "9", flavor: "prod", firstSeenAt: "2026-01-01T00:00:00Z", lastSeenAt: "2026-10-10T00:00:00Z", versionStatus: "blocked" },
                    { driverId: "d-2", driverName: null, installId: "i-2", tenantId: "t-own", tenantName: "Own fleet", platform: "android", appVersion: "3.2.0", buildNumber: "12", flavor: "dev", firstSeenAt: "2026-01-01T00:00:00Z", lastSeenAt: "2026-10-09T00:00:00Z", versionStatus: "current" },
                ],
                nextCursor: "c-2",
                meta: { flavor: "prod", minAllowedVersion: "3.1.0", latestVersion: "3.2.0" },
            })
        );
        web.on("GET", "/api/go/v1/app-installations/stats", () =>
            json(200, {
                data: { since: "x", total: 30, byStatus: { current: 20, outdated: 6, blocked: 2, ahead: 0, unknown: 2 }, byFlavor: {}, byPlatform: {}, belowVersion: null, flavor: "prod", minAllowedVersion: "3.1.0", latestVersion: "3.2.0" },
            })
        );
        renderWithProviders(<MobileClientsPage />);
        const rows = await screen.findAllByTestId("installation-row");
        expect(rows).toHaveLength(2);
        expect(within(rows[0]).getByText("Blocked")).toBeInTheDocument();
        expect(within(rows[0]).getByText("Carrier One")).toBeInTheDocument();
        expect(within(rows[1]).getByText("Test build")).toHaveClass("bg-destructive");
        expect(await screen.findByTestId("mobile-summary")).toHaveTextContent("30 devices · 2 blocked · 6 outdated · 20 on the latest build");
        expect(screen.getByRole("button", { name: "Load more" })).toBeInTheDocument();
        const list = web.calls.find((c) => c.url.startsWith("/api/go/v1/app-installations?"));
        expect(list?.url).toBe("/api/go/v1/app-installations?limit=100");
        expect(web.calls.filter((c) => c.url.startsWith("/api/go/v1/app-installations")).every((c) => c.headers["x-act-on-tenant"] === "*")).toBe(true);
    });
});

describe.each(["en", "th"] as const)("UI4 mobile language in %s", (language) => {
    const labels = language === "th"
        ? { build: "ประเภทบิลด์", production: "ใช้งานจริง", test: "บิลด์ทดสอบ", unknown: "ไม่ทราบประเภทบิลด์", all: "ทุกประเภทบิลด์" }
        : { build: "Build type", production: "Production", test: "Test build", unknown: "Unknown build type", all: "All build types" };

    it("localizes build types in published and historical releases", async () => {
        const web = setup();
        web.on("GET", "/api/go/v1/app-releases", () => json(200, { data: ["prod", "dev", "future"].map((flavor, index) => ({
            id: String(index), flavor, version: `3.${index}.0`, buildNumber: String(index), apkKey: "k", apkUrl: GATE.apkDownloadUrl,
            releasedAt: GATE.releasedAt, releasedBy: "cmd/release", latest: index === 0,
        })), meta: { settings: GATE } }));
        renderInLanguage(<MobileReleasePage />, language);
        expect(await screen.findByTestId("published-build")).toHaveTextContent(labels.production);
        const rows = await screen.findAllByTestId("release-row");
        expect(rows.map((row) => row.children[2].textContent)).toEqual([labels.production, labels.test, labels.unknown]);
        for (const row of rows) {
            expect(within(row).getByText("cmd/release").closest("details")?.open).toBe(false);
            if (language === "th") expect(row).not.toHaveTextContent(/Oct|ago/);
        }
    });

    it("labels device build filters and platforms while keeping IDs in row evidence", async () => {
        const web = setup();
        web.on("GET", "/api/go/v1/app-installations", () => json(200, { data: ["prod", "dev", "future"].map((flavor, index) => ({
            driverId: `driver-uuid-${index}`, installId: `install-uuid-${index}`, driverName: "Driver", tenantName: index === 1 ? "Linked carrier" : "Own fleet",
            flavor, platform: index === 1 ? "ios" : "android", appVersion: "3.2.0", buildNumber: "12", lastSeenAt: GATE.releasedAt, versionStatus: "current",
        })) }));
        web.on("GET", "/api/go/v1/app-installations/stats", () => json(200, { data: { total: 3, byStatus: {}, latestVersion: "3.2.0", minAllowedVersion: "3.1.0", flavor: "prod" } }));
        renderInLanguage(<MobileClientsPage />, language);
        const rows = await screen.findAllByTestId("installation-row");
        expect(screen.getByTestId("scope-caption")).toHaveTextContent(language === "th" ? "ผู้ขนส่งที่เชื่อมโยง" : "linked carriers");
        expect(rows[1]).toHaveTextContent("Linked carrier");
        expect(web.calls.filter((c) => c.url.startsWith("/api/go/v1/app-installations")).every((c) => c.headers["x-act-on-tenant"] === undefined)).toBe(true);
        expect(rows.map((row) => row.children[5].textContent)).toEqual([labels.production, labels.test, labels.unknown]);
        expect(rows[0]).toHaveTextContent("Android");
        expect(rows[1]).toHaveTextContent("iOS");
        expect(within(rows[1]).getByText(labels.test)).toHaveClass("bg-destructive");
        expect(screen.getByRole("columnheader", { name: language === "th" ? "หมายเลขบิลด์" : "Build" })).toBeInTheDocument();
        for (const [index, row] of rows.entries()) {
            expect(row).not.toHaveTextContent(`driver-uuid-${index}`);
            expect(row).not.toHaveTextContent(`install-uuid-${index}`);
            expect(row.title).toContain(`driver-uuid-${index}`);
            expect(row.title).toContain(`install-uuid-${index}`);
        }
        const u = userEvent.setup();
        await u.click(screen.getByRole("combobox", { name: labels.build }));
        expect(screen.getByRole("option", { name: labels.all })).toBeInTheDocument();
        await u.click(screen.getByRole("option", { name: labels.test }));
        await waitFor(() => expect(web.calls.some((call) => call.url.includes("flavor=dev"))).toBe(true));
    });
});
