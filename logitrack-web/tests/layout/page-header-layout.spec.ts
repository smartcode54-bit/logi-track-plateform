/**
 * The page header's layout in a real browser (UI3D review): at 1280 px a page's actions sit on one row beside the
 * heading and icon-only buttons stay 36 x 36; at 360 px the actions stack under the heading, full width, icon buttons
 * keeping their size. jsdom has no layout, so this runs in Chromium against a production build:
 *
 *   PAGE_LAYOUT_BASE_URL=http://localhost:3397 \
 *   PAGE_LAYOUT_TOKEN=<lt_at of a tenant admin> PAGE_LAYOUT_PLATFORM_TOKEN=<lt_at of a platform admin> \
 *   [PAGE_LAYOUT_TRUCK_ID=<a truck id>] [PAGE_LAYOUT_CHAT_ID=<a chat id>] [PAGE_LAYOUT_SHOTS=<dir>] \
 *   pnpm exec playwright test --config playwright.layout.config.ts
 *
 * Without the base URL and token it skips (the default config's dev server has no session to use).
 */
import fs from "node:fs";
import path from "node:path";
import { expect, test, type Page } from "@playwright/test";

const BASE = process.env.PAGE_LAYOUT_BASE_URL ?? "";
const TOKEN = process.env.PAGE_LAYOUT_TOKEN ?? "";
const PLATFORM_TOKEN = process.env.PAGE_LAYOUT_PLATFORM_TOKEN ?? "";
const SHOTS = process.env.PAGE_LAYOUT_SHOTS ?? "";

/** Pages with header actions, and the principal that opens them. */
const PAGES: Array<{ path: string; platform?: boolean }> = [
    { path: "/app/accounting/income" },
    { path: "/app/accounting/fuel" },
    { path: "/app/trucks" },
    { path: "/app/drivers" },
    { path: "/app/renewals" },
    { path: "/app/holidays" },
    { path: "/app/sources" },
    { path: "/app/security-center/users" },
    { path: "/app/security-center/tenants", platform: true },
    // The platform admin's layer picker sits in the actions.
    { path: "/app/security-center/roles", platform: true },
    { path: "/app/payroll" },
    { path: "/app/maintenance" },
    { path: "/app/security-center/audit" },
    { path: "/app/driver-monitor" },
    { path: "/app/first-mile" },
    { path: "/app/incident-reports" },
    { path: "/app/standby-records" },
    ...(process.env.PAGE_LAYOUT_TRUCK_ID ? [{ path: `/app/trucks/view?id=${process.env.PAGE_LAYOUT_TRUCK_ID}` }] : []),
    ...(process.env.PAGE_LAYOUT_CHAT_ID ? [{ path: `/app/chat/room?chatId=${process.env.PAGE_LAYOUT_CHAT_ID}` }] : []),
];

interface Box {
    x: number;
    y: number;
    w: number;
    h: number;
    icon: boolean;
}

async function headerLayout(page: Page) {
    const header = page.locator("[data-slot=page-header]").first();
    await expect(header).toBeVisible({ timeout: 30_000 });
    await expect(page.locator("main h1")).toHaveCount(1);
    return header.evaluate((el) => {
        const actions = el.querySelector<HTMLElement>("[data-slot=page-header-actions]");
        const box = (node: Element) => node.getBoundingClientRect();
        const heading = box(el.querySelector("h1")!);
        return {
            heading: { x: heading.x, y: heading.y, w: heading.width, h: heading.height },
            container: actions ? { x: box(actions).x, w: box(actions).width } : null,
            children: actions
                ? [...actions.children].map((child) => {
                      const r = box(child);
                      return { x: r.x, y: r.y, w: r.width, h: r.height, icon: (child.getAttribute("data-size") ?? "").startsWith("icon") };
                  })
                : [],
        };
    });
}

function shotName(p: string, width: number) {
    return `${p.replace(/^\/app\//, "").replace(/[^a-z0-9]+/gi, "-")}-${width}.png`;
}

test.describe("PageHeader layout (UI3D)", () => {
    test.skip(!BASE || !TOKEN, "needs PAGE_LAYOUT_BASE_URL and PAGE_LAYOUT_TOKEN");

    for (const { path: pagePath, platform } of PAGES) {
        for (const width of [1280, 360]) {
            test(`${pagePath} at ${width} px`, async ({ browser }) => {
                const token = platform ? PLATFORM_TOKEN : TOKEN;
                test.skip(!token, "needs PAGE_LAYOUT_PLATFORM_TOKEN");
                const context = await browser.newContext({ viewport: { width, height: 900 }, isMobile: width < 768, hasTouch: width < 768 });
                await context.addCookies([{ name: "lt_at", value: token, url: BASE }]);
                const page = await context.newPage();
                await page.goto(BASE + pagePath);
                const layout = await headerLayout(page);
                await page.waitForTimeout(300);
                if (SHOTS) {
                    fs.mkdirSync(SHOTS, { recursive: true });
                    await page.screenshot({ path: path.join(SHOTS, shotName(pagePath, width)) });
                }
                const children: Box[] = layout.children;
                expect(children.length, `${pagePath}: header actions`).toBeGreaterThan(0);
                for (const icon of children.filter((c) => c.icon)) {
                    expect(Math.round(icon.w), `${pagePath}: icon button width`).toBe(36);
                    expect(Math.round(icon.h), `${pagePath}: icon button height`).toBe(36);
                }
                if (width >= 768) {
                    // One row (centres aligned), right of the heading's start, the heading never squeezed into a column
                    // (it keeps at least 16 rem; actions that do not fit beside it move below it, still one row).
                    const centres = children.map((c) => c.y + c.h / 2);
                    expect(Math.max(...centres) - Math.min(...centres), `${pagePath}: actions on one row`).toBeLessThan(8);
                    expect(layout.container!.x, `${pagePath}: actions right of the heading`).toBeGreaterThan(layout.heading.x);
                    expect(layout.heading.h, `${pagePath}: heading on at most two lines`).toBeLessThanOrEqual(72);
                } else {
                    // Stacked under the heading, each non-icon action the full width of the column.
                    for (const c of children) expect(c.y, `${pagePath}: actions under the heading`).toBeGreaterThanOrEqual(layout.heading.y + layout.heading.h);
                    for (let i = 1; i < children.length; i++) expect(children[i].y, `${pagePath}: stacked`).toBeGreaterThan(children[i - 1].y);
                    for (const c of children.filter((c) => !c.icon)) expect(Math.round(c.w), `${pagePath}: full width`).toBe(Math.round(layout.container!.w));
                }
                await context.close();
            });
        }
    }
});
