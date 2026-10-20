/** Main's consolidated assignment flow, against the existing UI4I synthetic API and a production build. */
import fs from "node:fs";
import path from "node:path";
import { expect, test } from "@playwright/test";

const BASE = process.env.PAGE_LAYOUT_BASE_URL ?? "";
const TOKEN = process.env.PAGE_LAYOUT_TOKEN ?? "";
const SHOTS = process.env.PAGE_LAYOUT_SHOTS ?? "";

test.describe("Main assignment feature sync", () => {
    test.skip(!BASE || !TOKEN, "needs production build and UI4I fixture session");
    for (const language of ["en", "th"] as const) {
        for (const theme of ["light", "dark"] as const) {
            for (const width of [360, 1280]) {
                test(`${language} ${theme} ${width}`, async ({ browser }) => {
                    const context = await browser.newContext({ viewport: { width, height: 900 }, isMobile: width < 768, hasTouch: width < 768 });
                    try {
                        await context.addCookies([{ name: "lt_at", value: TOKEN, url: BASE }, { name: "sidebar_state", value: "true", url: BASE }]);
                        await context.addInitScript(({ language, theme }) => {
                            localStorage.setItem("language", language);
                            localStorage.setItem("theme", theme);
                        }, { language, theme });
                        const page = await context.newPage();
                        await page.goto(BASE + "/app/job-assign");
                        const add = page.getByRole("button", { name: language === "en" ? "Add job" : "เพิ่มงาน", exact: true });
                        await expect(add).toBeVisible();
                        await expect(page.getByRole("heading", { level: 1 })).toHaveText(language === "en" ? "Job assignment" : "มอบหมายงาน");
                        await expect(page.locator("article").first().or(page.getByRole("table"))).toBeVisible();
                        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
                        if (width < 768) await page.locator("[data-slot=sidebar-trigger]").click();
                        const nav = page.getByRole("navigation", { name: language === "en" ? "Main menu" : "เมนูหลัก", exact: true });
                        await expect(nav.locator('a[href="/app/job-assign"]')).toHaveCount(1);
                        await expect(nav.locator('a[href="/app/first-mile"], a[href="/app/line-haul"]')).toHaveCount(0);
                        if (width < 768) await nav.locator('a[href="/app/job-assign"]').click();
                        if (width >= 768) {
                            const choose = page.getByRole("button", { name: language === "en" ? "Choose columns" : "เลือกคอลัมน์" });
                            await choose.click();
                            const item = page.getByRole("menuitemcheckbox", { name: language === "en" ? "Actual pickup" : "วันรับงานจริง", exact: true });
                            await item.click();
                            await page.keyboard.press("Escape");
                            await page.reload();
                            await expect(page.getByRole("table")).toBeVisible();
                            await expect(page.getByRole("columnheader", { name: language === "en" ? "Actual pickup" : "วันรับงานจริง", exact: true })).toHaveCount(0);
                        } else {
                            await expect(page.getByRole("button", { name: language === "en" ? "Choose columns" : "เลือกคอลัมน์" })).toHaveCount(0);
                        }
                        if (SHOTS) {
                            fs.mkdirSync(SHOTS, { recursive: true });
                            await page.screenshot({ path: path.join(SHOTS, `main-sync-${language}-${theme}-${width}.png`), fullPage: true });
                        }
                        await add.click();
                        const dialog = page.getByRole("dialog");
                        await expect(dialog).toHaveCount(1);
                        await dialog.getByRole("combobox", { name: language === "en" ? "Job type" : "ชนิดงาน", exact: true }).click();
                        await page.getByRole("option", { name: "Line Haul", exact: true }).click();
                        await expect(dialog.getByRole("heading")).toHaveText(language === "en" ? "Create Line Haul Task" : "สร้างงาน Line Haul");
                        await expect(page.getByRole("dialog")).toHaveCount(1);
                        expect(await dialog.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
                        if (SHOTS) await page.screenshot({ path: path.join(SHOTS, `main-sync-form-${language}-${theme}-${width}.png`), fullPage: true });
                    } finally {
                        await context.close();
                    }
                });
            }
        }
    }
});
