/** UI4F: production-build account checks, with the page-header-layout harness (no dev server).
 * PAGE_LAYOUT_BASE_URL=http://localhost:3861 PAGE_LAYOUT_TOKEN=<stub lt_at>
 * pnpm exec playwright test --config playwright.layout.config.ts ui4-account.spec.ts
 */
import fs from "node:fs";
import path from "node:path";
import { expect, test } from "@playwright/test";

const BASE = process.env.PAGE_LAYOUT_BASE_URL ?? "";
const TOKEN = process.env.PAGE_LAYOUT_TOKEN ?? "";
const SHOTS = process.env.PAGE_LAYOUT_SHOTS ?? "";

const COPY = {
    en: { title: "My account", current: "Current password", next: "New password", confirm: "Confirm new password", submit: "Change password", forgot: "Request a password reset link" },
    th: { title: "บัญชีของฉัน", current: "รหัสผ่านปัจจุบัน", next: "รหัสผ่านใหม่", confirm: "ยืนยันรหัสผ่านใหม่", submit: "เปลี่ยนรหัสผ่าน", forgot: "ขอลิงก์ตั้งรหัสผ่าน" },
};

test.describe("Account layout (UI4F)", () => {
    test.skip(!BASE || !TOKEN, "needs PAGE_LAYOUT_BASE_URL and PAGE_LAYOUT_TOKEN");

    for (const language of ["en", "th"] as const) {
        for (const theme of ["light", "dark"] as const) {
            for (const width of [360, 1280]) {
                test(`${language} ${theme} at ${width} px`, async ({ browser }) => {
                    const context = await browser.newContext({ viewport: { width, height: 900 }, isMobile: width < 768, hasTouch: width < 768 });
                    try {
                        await context.addCookies([{ name: "lt_at", value: TOKEN, url: BASE }]);
                        await context.addInitScript(({ language, theme }) => {
                            localStorage.setItem("language", language);
                            localStorage.setItem("theme", theme);
                        }, { language, theme });
                        const meResponse = await context.request.get(`${BASE}/api/go/v1/me`);
                        expect(meResponse.ok(), "valid stub session").toBe(true);
                        const me = (await meResponse.json()).data;
                        const copy = COPY[language];
                        const page = await context.newPage();
                        await page.goto(`${BASE}/my-account`);
                        await expect(page.getByRole("heading", { level: 1, name: copy.title })).toBeVisible();
                        await expect(page).toHaveTitle(`${copy.title} · LogiTrack Pro`);
                        await expect(page.locator("html")).toHaveAttribute("lang", language);
                        await expect(page.locator("html")).toHaveClass(new RegExp(theme));
                        await expect(page.getByText(me.email, { exact: true })).toHaveCount(1);
                        await expect(page.getByRole("main")).not.toContainText(me.id);
                        await expect(page.locator("main h1")).toHaveCount(1);
                        const current = page.getByLabel(new RegExp(`^${copy.current}`));
                        const next = page.getByLabel(new RegExp(`^${copy.next}`));
                        const confirm = page.getByLabel(new RegExp(`^${copy.confirm}`));
                        const submit = page.getByRole("button", { name: copy.submit, exact: true });
                        await expect(current).toBeVisible();
                        await current.focus();
                        for (const control of [next, confirm, submit, page.getByRole("link", { name: copy.forgot })]) {
                            await page.keyboard.press("Tab");
                            await expect(control).toBeFocused();
                        }
                        if (width < 768) {
                            for (const control of [current, next, confirm, submit]) {
                                const box = await control.boundingBox();
                                expect(box!.height).toBeGreaterThanOrEqual(44);
                            }
                        }
                        const dimensions = await page.evaluate(() => ({ width: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }));
                        expect(dimensions.scroll).toBeLessThanOrEqual(dimensions.width + 1);
                        await page.evaluate(() => document.fonts.ready);
                        if (SHOTS) {
                            fs.mkdirSync(SHOTS, { recursive: true });
                            await page.screenshot({ path: path.join(SHOTS, `account-${language}-${theme}-${width}.png`), fullPage: true });
                        }
                    } finally {
                        await context.close();
                    }
                });
            }
        }
    }

    test("the console menu reaches the account page in keyboard order", async ({ browser }) => {
        const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
        try {
            await context.addCookies([{ name: "lt_at", value: TOKEN, url: BASE }]);
            await context.addInitScript(() => localStorage.setItem("language", "en"));
            const page = await context.newPage();
            await page.goto(`${BASE}/app/support`);
            const trigger = page.getByTestId("user-menu-trigger");
            await expect(trigger).toBeVisible();
            await expect(page.getByRole("menuitem", { name: "My account" })).toHaveCount(0);
            await trigger.focus();
            await page.keyboard.press("Enter");
            const account = page.getByRole("menuitem", { name: "My account" });
            await expect(account).toBeFocused();
            await expect(account).toHaveAttribute("href", "/my-account");
            await page.keyboard.press("ArrowDown");
            await expect(page.getByRole("menuitem", { name: "Sign out", exact: true })).toBeFocused();
            await page.keyboard.press("ArrowUp");
            await page.keyboard.press("Enter");
            await expect(page).toHaveURL(`${BASE}/my-account`);
            await expect(page.getByLabel(/^Current password/)).toBeVisible();
        } finally {
            await context.close();
        }
    });
});
