import { expect, test } from "@playwright/test";

const BASE = "http://localhost:4031";
const API = "http://localhost:4032";

test.describe("PSAG1WEB masked platform pages", () => {
    test.skip(process.env.PSAG1WEB_BROWSER !== "1", "needs the production web and isolated D1 stub on 4031/4032");
    for (const language of ["en", "th"] as const) for (const theme of ["light", "dark"] as const) for (const width of [360, 1280]) {
        test(`${language} ${theme} ${width}px`, async ({ browser, request }) => {
            const response = await request.get(`${API}/psag1web/session`);
            const { data: { token } } = await response.json();
            const context = await browser.newContext({ viewport: { width, height: 1000 }, isMobile: width === 360, hasTouch: width === 360 });
            try {
                await context.addCookies([{ name: "lt_at", value: token, url: BASE }]);
                await context.addInitScript(({ language, theme }) => {
                    localStorage.setItem("language", language);
                    localStorage.setItem("theme", theme);
                }, { language, theme });
                const page = await context.newPage();
                const calls: string[] = [];
                page.on("request", (req) => {
                    if (req.url().includes("/api/go/v1/")) {
                        expect(req.headers()["x-act-on-tenant"]).toBeUndefined();
                        calls.push(new URL(req.url()).pathname);
                    }
                });
                for (const [route, row, endpoint] of [
                    ["users", "user-row", "users"],
                    ["audit", "event-row", "security-events"],
                    ["mobile-clients", "installation-row", "app-installations"],
                    ["api-keys", "api-key-row", "api-keys"],
                ]) {
                    await page.goto(`${BASE}/app/security-center/${route}`);
                    await expect(page.getByTestId(row).first()).toBeVisible();
                    await expect(page.getByTestId(row).first().getByRole("img", { name: language === "en" ? "Personal or financial data masked" : "ปกปิดข้อมูลส่วนบุคคลหรือข้อมูลทางการเงิน" }).first()).toHaveText("****");
                    expect(calls).toContain(`/api/go/v1/platform/${endpoint}`);
                    expect(calls).not.toContain(`/api/go/v1/${endpoint}`);
                    await expect(page.locator("html")).toHaveAttribute("lang", language);
                    await expect(page.locator("html")).toHaveClass(new RegExp(theme));
                    const dimensions = await page.evaluate(() => [document.documentElement.clientWidth, document.documentElement.scrollWidth]);
                    expect(dimensions[1]).toBeLessThanOrEqual(dimensions[0] + 1);
                    await expect(page.getByRole("main")).not.toContainText("Original server evidence");
                }
                await page.goto(`${BASE}/app/security-center/tenants`);
                await expect(page.getByTestId("tenant-row").first()).toBeVisible();
                await page.getByTestId("tenant-row").first().getByRole("button").click();
                await page.getByRole("menuitem", { name: language === "en" ? "Administrators" : "ผู้ดูแล" , exact: false }).click();
                await expect(page.getByTestId("tenant-admin").first()).toContainText("****");
                expect(calls).toContain("/api/go/v1/platform/tenants/t-own/members");
                await expect(page.getByRole("dialog")).not.toContainText("HIDDEN ADMIN NAME");
            } finally {
                await context.close();
            }
        });
    }
});
