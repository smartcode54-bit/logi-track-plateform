import { expect, test } from "@playwright/test";

// Required prerequisites fail normally; this suite never silently skips an unavailable production server.
for (const profile of ["customer", "partner"] as const) {
    for (const language of ["en", "th"] as const) {
        for (const theme of ["light", "dark"] as const) {
            test(`${profile}, ${language}, ${theme}: phone trip geometry, targets, overflow and desktop boundary`, async ({ page, context, request }, testInfo) => {
                const session = await request.get(`http://127.0.0.1:3832/ui3f4/session?profile=${profile}`);
                expect(session.ok()).toBeTruthy();
                const { token } = await session.json();
                await context.addCookies([{ name: "lt_at", value: token, url: "http://127.0.0.1:3831" }]);
                await context.addInitScript(({ language, theme }) => {
                    localStorage.setItem("language", language);
                    localStorage.setItem("theme", theme);
                }, { language, theme });
                await page.setViewportSize({ width: 375, height: 812 });
                await page.goto("/app/driver-monitor");
                await page.evaluate(() => document.fonts.ready);
                const title = language === "en" ? profile === "customer" ? "Your trips" : "Fleet trips" : profile === "customer" ? "เที่ยวขนส่งของคุณ" : "เที่ยวรถของบริษัท";
                const summaryName = language === "en" ? "Trip summary" : "สรุปเที่ยวขนส่ง";
                const filtersName = language === "en" ? "More filters" : "ตัวกรองเพิ่มเติม";
                const dateName = language === "en" ? "Date range" : "ช่วงวันที่";
                const dateRangeButton = page.getByRole("button", { name: new RegExp(`^${dateName} \\d{2}\\/\\d{2}\\/\\d{4} – \\d{2}\\/\\d{2}\\/\\d{4}$`) });
                const region = page.getByRole("region", { name: title });
                const open = page.getByRole("button", { name: language === "en" ? "View trip FM-UI3F4-0 and evidence" : "ดูเที่ยว FM-UI3F4-0 และหลักฐาน" });
                await expect(open).toBeVisible();
                const first = region.locator("tbody tr[data-index]").first();
                expect((await first.boundingBox())!.y).toBeLessThanOrEqual(480);
                const button = (await open.boundingBox())!;
                expect(button.y + button.height).toBeLessThan(700);
                expect(button.height).toBeGreaterThanOrEqual(44);
                await expect(page.getByRole("button", { name: summaryName })).toHaveAttribute("aria-expanded", "false");
                await expect(page.getByRole("button", { name: filtersName })).toHaveAttribute("aria-expanded", "false");
                await expect(dateRangeButton).toBeVisible();
                expect(await region.locator("tbody tr[data-index]").count()).toBeLessThan(100);
                await page.screenshot({ path: testInfo.outputPath("phone-375.png") });
                const noOverflow = async () => expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(1);
                await page.setViewportSize({ width: 360, height: 812 });
                await noOverflow();
                expect(await region.evaluate((el) => el.scrollWidth - el.clientWidth)).toBeLessThanOrEqual(1);
                for (const control of await page.locator("main button:visible").all()) {
                    const box = (await control.boundingBox())!;
                    expect(box.height).toBeGreaterThanOrEqual(44);
                    expect(box.width).toBeGreaterThanOrEqual(44);
                }
                await page.getByRole("button", { name: filtersName }).click();
                await noOverflow();
                const search = page.getByRole("textbox", { name: language === "en" ? "Search trips" : "ค้นหาเที่ยวขนส่ง" });
                await search.fill("FM");
                await dateRangeButton.click();
                await noOverflow();
                await expect(page.locator("[data-slot=calendar] table")).toHaveCount(1);
                for (const day of await page.locator("[data-slot=calendar] button:visible").all()) {
                    const box = (await day.boundingBox())!;
                    expect(box.width).toBeGreaterThanOrEqual(44);
                    expect(box.height).toBeGreaterThanOrEqual(44);
                }
                await page.keyboard.press("Escape");
                await page.getByRole("button", { name: new RegExp(`^${filtersName}`) }).click();
                await page.setViewportSize({ width: 767, height: 812 });
                await expect(page.getByRole("button", { name: summaryName })).toHaveAttribute("aria-expanded", "false");
                await page.setViewportSize({ width: 768, height: 900 });
                await expect(page.getByRole("button", { name: summaryName })).toHaveCount(0);
                await expect(dateRangeButton).toBeVisible();
                await expect(search).toHaveValue("FM");
                await expect(region.getByRole("columnheader")).toHaveCount(12);
                await page.setViewportSize({ width: 1280, height: 900 });
                await page.screenshot({ path: testInfo.outputPath("desktop-1280.png") });
            });
        }
    }
}
