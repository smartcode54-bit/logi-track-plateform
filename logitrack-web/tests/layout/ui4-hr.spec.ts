/** UI4H: production-build HR bodies via the page-header-layout harness, with no dev server.
 * PAGE_LAYOUT_BASE_URL=http://127.0.0.1:3921 PAGE_LAYOUT_TOKEN=<stub lt_at>
 * pnpm exec playwright test --config playwright.layout.config.ts ui4-hr.spec.ts
 */
import fs from "node:fs";
import path from "node:path";
import { expect, test, type Page } from "@playwright/test";
import type { HolidayDTO } from "../../features/holidays/api/holidays.go";

const BASE = process.env.PAGE_LAYOUT_BASE_URL ?? "";
const TOKEN = process.env.PAGE_LAYOUT_TOKEN ?? "";
const SHOTS = process.env.PAGE_LAYOUT_SHOTS ?? "";
const PAGES = ["maintenance", "payroll", "leave-requests", "standby-records", "holidays"];

async function headerLayout(page: Page, width: number) {
    const header = page.locator("[data-slot=page-header]").first();
    await expect(header).toBeVisible();
    await expect(page.locator("main h1")).toHaveCount(1);
    const layout = await header.evaluate((el) => {
        const actions = el.querySelector<HTMLElement>("[data-slot=page-header-actions]")!;
        const heading = el.querySelector("h1")!.getBoundingClientRect();
        return {
            heading: { x: heading.x, bottom: heading.bottom, height: heading.height },
            actions: { x: actions.getBoundingClientRect().x, width: actions.getBoundingClientRect().width },
            children: [...actions.children].map((child) => {
                const r = child.getBoundingClientRect();
                return { y: r.y, width: r.width, height: r.height, icon: (child.getAttribute("data-size") ?? "").startsWith("icon") };
            }),
        };
    });
    expect(layout.children.length).toBeGreaterThan(0);
    for (const icon of layout.children.filter((c) => c.icon)) {
        expect(Math.round(icon.width)).toBe(36);
        expect(Math.round(icon.height)).toBe(36);
    }
    if (width >= 768) {
        const centres = layout.children.map((c) => c.y + c.height / 2);
        expect(Math.max(...centres) - Math.min(...centres)).toBeLessThan(8);
        expect(layout.actions.x).toBeGreaterThan(layout.heading.x);
        expect(layout.heading.height).toBeLessThanOrEqual(72);
    } else {
        for (const child of layout.children) expect(child.y).toBeGreaterThanOrEqual(layout.heading.bottom);
        for (let i = 1; i < layout.children.length; i++) expect(layout.children[i].y).toBeGreaterThan(layout.children[i - 1].y);
        for (const child of layout.children.filter((c) => !c.icon)) expect(Math.round(child.width)).toBe(Math.round(layout.actions.width));
    }
}

async function calendarContrast(page: Page) {
    const events = page.locator(".fc-daygrid-event:visible");
    await expect(events.first()).toBeVisible();
    const ratios = await events.evaluateAll((nodes) => {
        const canvas = document.createElement("canvas");
        canvas.width = canvas.height = 1;
        const ctx = canvas.getContext("2d")!;
        const luminance = (color: string) => {
            ctx.clearRect(0, 0, 1, 1);
            ctx.fillStyle = color;
            ctx.fillRect(0, 0, 1, 1);
            const rgb = [...ctx.getImageData(0, 0, 1, 1).data].slice(0, 3).map((v) => {
                const c = v / 255;
                return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
            });
            return rgb[0] * 0.2126 + rgb[1] * 0.7152 + rgb[2] * 0.0722;
        };
        return nodes.map((node) => {
            const title = node.querySelector(".fc-event-title")!;
            const fg = luminance(getComputedStyle(title).color);
            const bg = luminance(getComputedStyle(node).backgroundColor);
            return (Math.max(fg, bg) + 0.05) / (Math.min(fg, bg) + 0.05);
        });
    });
    for (const ratio of ratios) expect(ratio, "calendar event text contrast").toBeGreaterThanOrEqual(4.5);
}

test.describe("HR body layout (UI4H)", () => {
    test.skip(!BASE || !TOKEN, "needs PAGE_LAYOUT_BASE_URL and PAGE_LAYOUT_TOKEN");
    for (const route of PAGES) {
        for (const language of ["en", "th"]) {
            for (const theme of ["light", "dark"]) {
                for (const width of [360, 1280]) {
                    test(`${route}: ${language} ${theme} at ${width} px`, async ({ browser }) => {
                        const context = await browser.newContext({ viewport: { width, height: 900 }, isMobile: width < 768, hasTouch: width < 768 });
                        try {
                            await context.addCookies([{ name: "lt_at", value: TOKEN, url: BASE }]);
                            await context.addInitScript(({ language, theme }) => {
                                localStorage.setItem("language", language);
                                localStorage.setItem("theme", theme);
                            }, { language, theme });
                            const page = await context.newPage();
                            let sameDayHolidays: HolidayDTO[] = [];
                            if (route === "holidays") {
                                await page.route("**/api/go/v1/holidays?*", async (request) => {
                                    const upstream = await request.fetch();
                                    const body = await upstream.json();
                                    // Keep the tenant's permissions and records; place two holidays on one day.
                                    expect(body.data.length, "needs at least two holiday fixtures").toBeGreaterThanOrEqual(2);
                                    sameDayHolidays = [body.data[0], { ...body.data[1], date: body.data[0].date }];
                                    await request.fulfill({ response: upstream, json: { ...body, data: [...sameDayHolidays, ...body.data.slice(2)] } });
                                });
                            }
                            const response = page.waitForResponse((r) => r.url().includes(`/api/go/v1/${route === "standby-records" ? "standby" : route}`) && r.request().method() === "GET");
                            await page.goto(`${BASE}/app/${route}`);
                            expect((await response).ok(), "body data loaded").toBe(true);
                            await expect(page.locator("html")).toHaveAttribute("lang", language);
                            await expect(page.locator("html")).toHaveClass(new RegExp(theme));
                            await headerLayout(page, width);
                            await expect(page.locator("main [role=alert]")).toHaveCount(0);
                            if (route === "holidays") {
                                await expect(page.locator(".fc-dayGridMonth-view")).toBeVisible();
                                // Fixtures or a configured tenant must have holidays in the visible month.
                                await calendarContrast(page);
                                await page.getByRole("button", { name: language === "th" ? "รายปี" : "Year", exact: true }).click();
                                const yearView = page.locator(".fc-multiMonthYear-view");
                                await expect(yearView).toBeVisible();
                                const day = yearView.locator(`.fc-daygrid-day:not(.fc-day-other)[data-date="${sameDayHolidays[0].date}"]`);
                                for (const holiday of sameDayHolidays) {
                                    const name = (language === "th" ? holiday.nameTh : holiday.nameEn) || holiday.name;
                                    const event = day.locator(".fc-daygrid-event").filter({ has: page.getByText(name, { exact: true }) });
                                    await expect(event).toBeVisible();
                                    await expect(event).toHaveCSS("position", "relative");
                                    expect(await event.evaluate((el) => Number(getComputedStyle(el).opacity)), "holiday label is opaque").toBeGreaterThan(0);
                                    await event.click();
                                    const dialog = page.getByRole("dialog");
                                    await expect(dialog.getByRole("heading", { name: language === "th" ? "แก้ไขวันหยุด" : "Edit Holiday", exact: true })).toBeVisible();
                                    await expect(dialog.locator("#holiday-name-en")).toHaveValue(holiday.nameEn ?? "");
                                    await dialog.getByRole("button", { name: language === "th" ? "ยกเลิก" : "Cancel", exact: true }).click();
                                    await expect(dialog).toHaveCount(0);
                                }
                                await calendarContrast(page);
                            }
                            await page.evaluate(() => document.fonts.ready);
                            const dimensions = await page.evaluate(() => ({ width: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }));
                            expect(dimensions.scroll, "no page overflow").toBeLessThanOrEqual(dimensions.width + 1);
                            if (SHOTS) {
                                fs.mkdirSync(SHOTS, { recursive: true });
                                await page.screenshot({ path: path.join(SHOTS, `ui4h-${route}-${language}-${theme}-${width}.png`), fullPage: true });
                            }
                        } finally {
                            await context.close();
                        }
                    });
                }
            }
        }
    }
});
