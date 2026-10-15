/** UI4I: page-header-layout harness against a production build and local stub API.
 * PAGE_LAYOUT_BASE_URL=http://127.0.0.1:3931 PAGE_LAYOUT_TOKEN=<stub lt_at>
 * [PAGE_LAYOUT_SHOTS=<directory>] pnpm exec playwright test --config playwright.layout.config.ts ui4-operations.spec.ts
 */
import fs from "node:fs";
import path from "node:path";
import { expect, test, type Locator } from "@playwright/test";

const BASE = process.env.PAGE_LAYOUT_BASE_URL ?? "";
const TOKEN = process.env.PAGE_LAYOUT_TOKEN ?? "";
const SHOTS = process.env.PAGE_LAYOUT_SHOTS ?? "";
const PAGES = ["/app/dashboard", "/app/sources", "/app/first-mile", "/app/settings/company-profile", "/app/companies", "/app/chat"];

/** Resolve CSS Color 4 tokens and transparent ancestor surfaces in the browser's sRGB canvas. */
async function textContrast(text: Locator) {
    return text.evaluate((el) => {
        const canvas = document.createElement("canvas");
        canvas.width = canvas.height = 1;
        const ctx = canvas.getContext("2d", { willReadFrequently: true })!;
        const ancestors = [];
        for (let node: Element | null = el; node; node = node.parentElement) ancestors.push(node);
        ctx.fillStyle = "white";
        ctx.fillRect(0, 0, 1, 1);
        for (const node of ancestors.reverse()) {
            ctx.fillStyle = getComputedStyle(node).backgroundColor;
            ctx.fillRect(0, 0, 1, 1);
        }
        const luminance = () => {
            const [r, g, b] = [...ctx.getImageData(0, 0, 1, 1).data].slice(0, 3).map((channel) => {
                const v = channel / 255;
                return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
            });
            return 0.2126 * r + 0.7152 * g + 0.0722 * b;
        };
        const background = luminance();
        ctx.fillStyle = getComputedStyle(el).color;
        ctx.fillRect(0, 0, 1, 1);
        const foreground = luminance();
        return (Math.max(foreground, background) + 0.05) / (Math.min(foreground, background) + 0.05);
    });
}

test.describe("Operations page bodies (UI4I)", () => {
    test.skip(!BASE || !TOKEN, "needs PAGE_LAYOUT_BASE_URL and PAGE_LAYOUT_TOKEN");

    for (const pagePath of PAGES) {
        for (const language of ["en", "th"] as const) {
            for (const theme of ["light", "dark"] as const) {
                for (const width of [360, 1280]) {
                    test(`${pagePath} ${language} ${theme} at ${width} px`, async ({ browser }) => {
                        const context = await browser.newContext({ viewport: { width, height: 900 }, isMobile: width < 768, hasTouch: width < 768 });
                        try {
                            await context.addCookies([{ name: "lt_at", value: TOKEN, url: BASE }]);
                            await context.addInitScript(({ language, theme }) => {
                                localStorage.setItem("language", language);
                                localStorage.setItem("theme", theme);
                            }, { language, theme });
                            const page = await context.newPage();
                            await page.goto(BASE + pagePath);
                            const header = page.locator("[data-slot=page-header]").first();
                            await expect(header).toBeVisible({ timeout: 30_000 });
                            await expect(page.locator("main h1")).toHaveCount(1);
                            await expect(page.locator("html")).toHaveAttribute("lang", language);
                            await expect(page.locator("html")).toHaveClass(new RegExp(theme));
                            await page.evaluate(() => document.fonts.ready);
                            const actions = await header.evaluate((el) => {
                                const container = el.querySelector("[data-slot=page-header-actions]");
                                const heading = el.querySelector("h1")!.getBoundingClientRect();
                                return container ? {
                                    width: container.getBoundingClientRect().width,
                                    headingBottom: heading.bottom,
                                    children: [...container.children].map((child) => {
                                        const r = child.getBoundingClientRect();
                                        return { y: r.y, width: r.width, height: r.height, icon: (child.getAttribute("data-size") ?? "").startsWith("icon") };
                                    }),
                                } : null;
                            });
                            if (actions) {
                                for (const action of actions.children) {
                                    if (action.icon) {
                                        expect(Math.round(action.width)).toBe(36);
                                        expect(Math.round(action.height)).toBe(36);
                                    } else if (width < 768) {
                                        expect(action.y).toBeGreaterThanOrEqual(actions.headingBottom);
                                        expect(Math.round(action.width)).toBe(Math.round(actions.width));
                                    }
                                }
                            }
                            if (pagePath === "/app/dashboard") {
                                const urgent = page.getByTestId("chats-urgent");
                                await expect(urgent).toBeVisible();
                                const label = urgent.locator("p").first();
                                expect(await textContrast(label), "urgent-chat text contrast").toBeGreaterThanOrEqual(4.5);
                                await urgent.hover();
                                await urgent.evaluate(async (el) => { await Promise.all(el.getAnimations({ subtree: true }).map((animation) => animation.finished)); });
                                expect(await textContrast(label), "urgent-chat hover text contrast").toBeGreaterThanOrEqual(4.5);
                                const axis = page.locator('[data-testid="activity-chart"] svg + div');
                                expect(await axis.evaluate((el) => {
                                    const frame = el.parentElement!.getBoundingClientRect();
                                    const labels = el.getBoundingClientRect();
                                    return labels.bottom <= frame.bottom && parseFloat(getComputedStyle(el).fontSize) >= 12;
                                }), "chart captions are visible inside the frame").toBe(true);
                                const markers = page.locator(".dashboard-vehicle-marker");
                                await expect(markers.first()).toBeVisible();
                                const labels = markers.locator(":scope > div > span");
                                for (const label of await labels.all()) {
                                    const style = await label.evaluate((el) => {
                                        const actual = getComputedStyle(el);
                                        const probe = document.createElement("span");
                                        probe.style.color = "var(--foreground)";
                                        probe.style.backgroundColor = "var(--card)";
                                        el.append(probe);
                                        const tokens = getComputedStyle(probe);
                                        const result = { size: parseFloat(actual.fontSize), foreground: actual.color, background: actual.backgroundColor, tokenForeground: tokens.color, tokenBackground: tokens.backgroundColor };
                                        probe.remove();
                                        return result;
                                    });
                                    expect(style.size).toBeGreaterThanOrEqual(12);
                                    expect(style.foreground).toBe(style.tokenForeground);
                                    expect(style.background).toBe(style.tokenBackground);
                                }
                                await markers.first().click();
                                const popup = page.locator(".leaflet-popup-content");
                                await expect(popup).toContainText(language === "en" ? /Engine (ON|OFF)/ : /เครื่องยนต์/);
                                expect(await popup.locator("p").evaluateAll((nodes) => nodes.every((el) => parseFloat(getComputedStyle(el).fontSize) >= 12))).toBe(true);
                            }
                            if (pagePath === "/app/sources") {
                                const action = page.getByRole("button", { name: language === "en" ? "Click to filter" : "คลิกเพื่อกรอง", exact: true });
                                await expect(action).toBeVisible();
                                if (width < 768) {
                                    const box = await action.boundingBox();
                                    expect(box!.height, "warning filter touch target height").toBeGreaterThanOrEqual(44);
                                    expect(box!.width, "warning filter touch target width").toBeGreaterThanOrEqual(44);
                                }
                                const filter = page.getByRole("button", { name: language === "en" ? /^New\s*1$/ : /^ใหม่\s*1$/ });
                                const count = filter.locator('[data-slot="badge"]');
                                expect(await textContrast(count), "inactive filter count contrast").toBeGreaterThanOrEqual(4.5);
                                await action.click();
                                await expect(filter).toHaveAttribute("data-variant", "default");
                                await filter.evaluate(async (el) => { await Promise.all(el.getAnimations({ subtree: true }).map((animation) => animation.finished)); });
                                expect(await textContrast(count), "active filter count contrast").toBeGreaterThanOrEqual(4.5);
                                await filter.hover();
                                await filter.evaluate(async (el) => { await Promise.all(el.getAnimations({ subtree: true }).map((animation) => animation.finished)); });
                                expect(await textContrast(count), "active filter hover count contrast").toBeGreaterThanOrEqual(4.5);
                            }
                            const dimensions = await page.evaluate(() => ({ width: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }));
                            expect(dimensions.scroll, "no page-level horizontal overflow").toBeLessThanOrEqual(dimensions.width + 1);
                            if (SHOTS) {
                                fs.mkdirSync(SHOTS, { recursive: true });
                                const name = pagePath.replace(/^\/app\//, "").replace(/\//g, "-");
                                await page.screenshot({ path: path.join(SHOTS, `${name}-${language}-${theme}-${width}.png`), fullPage: true });
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
