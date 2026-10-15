/** Production-build finance checks. Uses the page-header-layout session harness; no dev server. */
import fs from "node:fs";
import path from "node:path";
import { expect, test } from "@playwright/test";

const BASE = process.env.PAGE_LAYOUT_BASE_URL ?? "";
const TOKEN = process.env.PAGE_LAYOUT_TOKEN ?? "";
const SHOTS = process.env.PAGE_LAYOUT_SHOTS ?? "";
const routes = ["/app/accounting/income", "/app/accounting/rate-card", "/app/accounting/billing-document", "/app/utilities/backfill"];

test.describe("Finance bodies (UI4D)", () => {
    test.skip(!BASE || !TOKEN, "needs PAGE_LAYOUT_BASE_URL and PAGE_LAYOUT_TOKEN");
    for (const language of ["en", "th"]) for (const theme of ["light", "dark"]) for (const width of [360, 1280]) for (const route of routes) {
        test(`${route} ${language} ${theme} at ${width}px`, async ({ browser }) => {
            const context = await browser.newContext({ viewport: { width, height: 900 }, isMobile: width < 768, hasTouch: width < 768, timezoneId: "America/New_York" });
            try {
                await context.addCookies([{ name: "lt_at", value: TOKEN, url: BASE }]);
                await context.addInitScript(({ language, theme }) => { localStorage.setItem("language", language); localStorage.setItem("theme", theme); }, { language, theme });
                const page = await context.newPage();
                const errors: string[] = [];
                page.on("pageerror", (error) => errors.push(error.message));
                await page.goto(BASE + route);
                const header = page.locator('[data-slot="page-header"]');
                await expect(header).toBeVisible();
                await expect(page.locator("main h1")).toHaveCount(1);
                await expect(page.locator("html")).toHaveAttribute("lang", language);
                await expect(page.locator("html")).toHaveClass(new RegExp(theme));
                if (route.endsWith("/income")) {
                    await expect(header.locator('[data-slot="page-header-actions"] button').first()).toBeVisible();
                    const backfill = page.getByRole("button", { name: language === "en" ? "Price or reprice records" : "คำนวณยอดวางบิลย้อนหลัง", exact: true });
                    await backfill.focus();
                    await page.keyboard.press("Enter");
                    const dialog = page.getByRole("dialog");
                    await expect(dialog).toBeVisible();
                    await expect(dialog.getByRole("button", { name: language === "en" ? "Billing range (Asia/Bangkok)" : "ช่วงวันที่วางบิล (Asia/Bangkok)" })).toBeVisible();
                    await page.keyboard.press("Escape");
                    await expect(dialog).toHaveCount(0);
                    await expect(backfill).toBeFocused();
                }
                if (route.endsWith("/backfill")) await expect(page.getByRole("link", { name: language === "en" ? "Review billing impact" : "ตรวจสอบผลกระทบต่อยอดวางบิล" })).toHaveAttribute("href", "/app/utilities/billing-impact");
                const dimensions = await page.evaluate(() => ({ viewport: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }));
                expect(dimensions.scroll).toBeLessThanOrEqual(dimensions.viewport + 1);
                expect(errors).toEqual([]);
                await page.evaluate(() => document.fonts.ready);
                if (SHOTS) {
                    fs.mkdirSync(SHOTS, { recursive: true });
                    await page.screenshot({ path: path.join(SHOTS, `${route.replaceAll("/", "-")}-${language}-${theme}-${width}.png`), fullPage: true });
                }
            } finally { await context.close(); }
        });
    }
});
