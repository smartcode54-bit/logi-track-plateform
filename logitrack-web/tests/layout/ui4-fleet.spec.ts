/** Production-build fleet acceptance. Use the page-header layout harness's URL/token; no dev server. */
import fs from "node:fs";
import path from "node:path";
import { expect, test, type Page } from "@playwright/test";

const BASE = process.env.PAGE_LAYOUT_BASE_URL ?? "";
const TOKEN = process.env.PAGE_LAYOUT_TOKEN ?? "";
const SHOTS = process.env.PAGE_LAYOUT_SHOTS ?? "";
const PAGES = [
    "/app/trucks", "/app/drivers", "/app/renewals", "/app/truck-assignment",
    "/app/trucks/new", "/app/drivers/new",
    ...(process.env.PAGE_LAYOUT_TRUCK_ID ? [`/app/trucks/edit?id=${process.env.PAGE_LAYOUT_TRUCK_ID}`] : []),
    ...(process.env.PAGE_LAYOUT_DRIVER_ID ? [`/app/drivers/edit?id=${process.env.PAGE_LAYOUT_DRIVER_ID}`] : []),
];

async function noOverflow(page: Page) {
    const widths = await page.evaluate(() => ({ viewport: innerWidth, body: document.body.scrollWidth, root: document.documentElement.scrollWidth }));
    expect(widths.body).toBeLessThanOrEqual(widths.viewport + 1);
    expect(widths.root).toBeLessThanOrEqual(widths.viewport + 1);
}

async function actionBarLayout(page: Page, width: number) {
    const bar = page.locator("[data-form-action-bar]");
    await expect(bar).toBeVisible();
    await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));
    const layout = await bar.evaluate((el) => {
        const rect = el.getBoundingClientRect();
        const previous = el.previousElementSibling!.getBoundingClientRect();
        const style = getComputedStyle(el);
        const actions = [...el.children].map((child) => {
            const r = child.getBoundingClientRect();
            return { top: r.top, bottom: r.bottom, left: r.left, right: r.right };
        });
        return { top: rect.top, left: rect.left, right: rect.right, previousBottom: previous.bottom, position: style.position, background: style.backgroundColor, actions };
    });
    expect(layout.position).toBe("sticky");
    expect(layout.background).not.toMatch(/(?:rgba\([^)]*, 0\)|\/ 0\)|transparent)/);
    expect(layout.previousBottom).toBeLessThanOrEqual(layout.top + 1);
    expect(layout.left).toBeGreaterThanOrEqual(0);
    expect(layout.right).toBeLessThanOrEqual(width);
    if (width < 768) {
        for (let i = 1; i < layout.actions.length; i++) expect(layout.actions[i].top).toBeGreaterThanOrEqual(layout.actions[i - 1].bottom);
    }
}

test.describe("UI4 fleet bodies", () => {
    test.skip(!BASE || !TOKEN, "needs PAGE_LAYOUT_BASE_URL and PAGE_LAYOUT_TOKEN");
    for (const pagePath of PAGES) {
        for (const width of [360, 1280]) {
            for (const theme of ["light", "dark"] as const) {
                for (const language of ["en", "th"] as const) {
                    test(`${pagePath} ${width}px ${theme} ${language}`, async ({ browser }) => {
                        const context = await browser.newContext({ viewport: { width, height: 900 }, isMobile: width < 768, hasTouch: width < 768, reducedMotion: "reduce" });
                        try {
                            await context.addCookies([{ name: "lt_at", value: TOKEN, url: BASE }]);
                            await context.addInitScript(({ theme, language }) => {
                                localStorage.setItem("theme", theme);
                                localStorage.setItem("language", language);
                            }, { theme, language });
                            const page = await context.newPage();
                            await page.goto(BASE + pagePath);
                            await expect(page.locator("main h1")).toBeVisible({ timeout: 30_000 });
                            await expect(page.locator("html")).toHaveClass(new RegExp(theme));
                            await page.evaluate(() => document.fonts.ready);
                            await noOverflow(page);
                            if (/\/(new|edit)(?:\?|$)/.test(pagePath)) await actionBarLayout(page, width);
                            if (SHOTS) {
                                fs.mkdirSync(SHOTS, { recursive: true });
                                await page.screenshot({ path: path.join(SHOTS, `ui4-${pagePath.replace(/[^a-z0-9]+/gi, "-")}-${width}-${theme}-${language}.png`), fullPage: true });
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
