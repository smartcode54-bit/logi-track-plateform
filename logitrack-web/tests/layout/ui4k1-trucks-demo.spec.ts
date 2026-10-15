import { expect, test } from "@playwright/test";

const base = process.env.PAGE_LAYOUT_BASE_URL ?? "";
const token = process.env.PAGE_LAYOUT_TOKEN ?? "";
const routes = ["/app/trucks", "/app/trucks/new", "/app/trucks/edit?id=truck-1", "/app/trucks/view?id=truck-1", "/app/trucks/renew?id=truck-1", "/app/renewals"];

test.describe("UI4K1 truck production demo", () => {
    test.skip(!base || !token, "Requires the local production web and stub API.");
    for (const width of [360, 1280]) for (const theme of ["light", "dark"]) for (const language of ["th", "en"]) {
        test(`${width}px ${theme} ${language}`, async ({ browser }) => {
            const context = await browser.newContext({ viewport: { width, height: 900 }, reducedMotion: "reduce" });
            await context.addCookies([{ name: "lt_at", value: token, url: base }]);
            await context.addInitScript(({ theme, language }) => {
                localStorage.setItem("theme", theme);
                localStorage.setItem("language", language);
            }, { theme, language });
            try {
                const page = await context.newPage();
                const errors: string[] = [];
                page.on("pageerror", (error) => errors.push(error.message));
                for (const route of routes) {
                    await page.goto(base + route);
                    await expect(page.locator("main h1")).toBeVisible();
                    await expect(page.locator("html")).toHaveClass(new RegExp(theme));
                    await page.evaluate(() => document.fonts.ready);
                    if (route.includes("/edit")) {
                        await expect(page.getByLabel(language === "th" ? "เลขเครื่องยนต์" : "Engine Number", { exact: true })).toHaveValue("12345678901234567");
                        await expect(page.getByLabel(language === "th" ? "ปี" : "Year", { exact: true })).toContainText("2026");
                        await expect(page.getByLabel(language === "th" ? "เชื้อเพลิง" : "Fuel Type", { exact: true })).toContainText(language === "th" ? "ดีเซล" : "Diesel");
                    }
                    if (route.endsWith("/new")) {
                        const plate = page.getByPlaceholder(language === "th" ? "เช่น 1กก-1234" : "e.g. 1กก-1234");
                        await plate.fill("1กก-1234");
                        await expect(plate).toHaveValue("1กก-1234");
                        const inputs = page.locator('input[type="file"]');
                        expect(await inputs.count()).toBe(6);
                        await expect(inputs.first()).toHaveAttribute("accept", "image/jpeg,image/png,image/webp");
                    }
                    const dimensions = await page.evaluate(() => ({ width: innerWidth, body: document.body.scrollWidth, root: document.documentElement.scrollWidth }));
                    expect(dimensions.body, route).toBeLessThanOrEqual(dimensions.width + 1);
                    expect(dimensions.root, route).toBeLessThanOrEqual(dimensions.width + 1);
                    if (process.env.PAGE_LAYOUT_SHOTS && language === "th") await page.screenshot({ path: `${process.env.PAGE_LAYOUT_SHOTS}/${width}-${theme}-${route.replace(/[^a-z0-9]/gi, "_")}.png`, fullPage: true });
                }
                expect(errors).toEqual([]);
            } finally { await context.close(); }
        });
    }
});
