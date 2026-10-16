import { expect, test } from "@playwright/test";

const BASE = "http://localhost:4051";
const API = "http://localhost:4052";

test.describe("PSAG1CWEB consent and ratification", () => {
    test.skip(process.env.PSA_CONSENT_BROWSER !== "1", "needs the production web on 4051 and psa-consent-api.mjs on 4052");
    for (const language of ["en", "th"] as const) for (const theme of ["light", "dark"] as const) for (const width of [360, 1280]) {
        test(`${language} ${theme} ${width}px`, async ({ browser, request }, info) => {
            const response = await request.get(`${API}/psa-consent/session`);
            const { data: { token } } = await response.json();
            const context = await browser.newContext({ viewport: { width, height: 1000 }, isMobile: width === 360, hasTouch: width === 360 });
            try {
                await context.addCookies([{ name: "lt_at", value: token, url: BASE }]);
                await context.addInitScript(({ language, theme }) => {
                    localStorage.setItem("language", language);
                    localStorage.setItem("theme", theme);
                }, { language, theme });
                const page = await context.newPage();
                const writes: unknown[] = [];
                page.on("request", (req) => {
                    if (req.url().includes("/api/go/v1/")) {
                        expect(req.headers()["x-act-on-tenant"]).toBeUndefined();
                        if (["PUT", "POST"].includes(req.method())) writes.push(req.postDataJSON());
                    }
                });
                await page.goto(`${BASE}/app/settings/support-access`);
                const control = page.getByRole("switch");
                await expect(control).not.toBeChecked();
                await expect(page.getByTestId("pending-member")).toHaveCount(2);
                await expect(page.locator("html")).toHaveAttribute("lang", language);
                await expect(page.locator("html")).toHaveClass(new RegExp(theme));
                expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(1);
                if (width === 360) {
                    expect((await page.locator('label[for="support-access-switch"]').boundingBox())!.height).toBeGreaterThanOrEqual(44);
                    for (const button of await page.getByRole("main").getByRole("button").all()) expect((await button.boundingBox())!.height).toBeGreaterThanOrEqual(44);
                }
                await page.screenshot({ path: info.outputPath("consent.png"), fullPage: true });
                await control.click();
                const dialog = page.getByRole("alertdialog");
                await expect(dialog).toBeVisible();
                await expect(dialog.getByRole("button", { name: language === "en" ? "Cancel" : "ยกเลิก", exact: true })).toBeFocused();
                expect(writes).toHaveLength(0);
                await dialog.getByRole("button", { name: language === "en" ? "Confirm change" : "ยืนยันการเปลี่ยนแปลง", exact: true }).click();
                await expect(dialog).toBeHidden();
                await expect(control).toBeChecked();
                expect(writes[0]).toEqual({ enabled: true, expectedVersion: "5" });
                await page.getByRole("button", { name: language === "en" ? "Ratify New administrator" : "รับรอง New administrator", exact: true }).click();
                await expect(dialog).toBeVisible();
                await expect(dialog).toContainText("New administrator");
                await dialog.getByRole("button", { name: language === "en" ? "Ratify" : "รับรอง", exact: true }).click();
                await expect(dialog).toBeHidden();
                await expect(page.getByTestId("pending-member")).toHaveCount(1);
                expect(writes[1]).toEqual({ expectedGrantVersion: "7" });
                expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(1);
                await page.screenshot({ path: info.outputPath("ratified.png"), fullPage: true });
            } finally {
                await context.close();
            }
        });
    }
});
