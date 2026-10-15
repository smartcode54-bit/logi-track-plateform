/** Production-build maintenance acceptance against the local 3942 stub, with no staging writes. */
import fs from "node:fs";
import path from "node:path";
import { expect, test, type Page } from "@playwright/test";

const BASE = process.env.PAGE_LAYOUT_BASE_URL ?? "";
const API = "http://127.0.0.1:3942";
const SHOTS = process.env.PAGE_LAYOUT_SHOTS ?? "";

test.describe("UI4K3 maintenance demo", () => {
    test.skip(!BASE || !process.env.UI4K3_STUB, "needs a production server on 3941 and UI4K3_STUB=1 on 3942");
    test.describe.configure({ mode: "serial" });
    test.beforeEach(async ({ request, context }) => {
        await request.post(`${API}/ui4k3/state`, { data: { reset: true } });
        const { token } = await (await request.get(`${API}/ui4k3/session`)).json();
        await context.addCookies([{ name: "lt_at", value: token, url: BASE }]);
    });

    async function edit(page: Page, id: string, language = "en") {
        const serviceName = id === "cost-only" ? "Total-only repair" : id === "cancelled" ? "Cancelled repair" : id;
        await page.locator(`[data-maintenance-id="${id}"]`).getByRole("button", { name: language === "en" ? `Actions for ${serviceName}` : `ตัวเลือกของ ${serviceName}` }).click();
        await page.getByRole("menuitem", { name: language === "en" ? "Edit / Update" : "แก้ไข / อัปเดต" }).click();
    }

    test("edits a total-only record, keeps all invoices, and completes with date-only payloads", async ({ page, request }) => {
        await page.addInitScript(() => { localStorage.setItem("language", "en"); localStorage.setItem("theme", "light"); });
        await page.goto(`${BASE}/app/trucks/maintenance?id=truck-1`);
        await expect(page.getByText("Total-only repair", { exact: true })).toBeVisible();
        await edit(page, "cost-only");
        const serviceDate = page.getByRole("group", { name: /Service date/ });
        const serviceDateTrigger = serviceDate.getByRole("button", { name: "Service date", exact: true });
        await expect(serviceDateTrigger).toHaveAttribute("aria-invalid", "false");
        await expect(page.getByRole("group", { name: "Pickup appointment (Bangkok time)" })).toBeVisible();
        await serviceDate.locator('[role="button"]').click();
        await page.getByRole("button", { name: "Save Record", exact: true }).click();
        await expect(serviceDateTrigger).toHaveAttribute("aria-invalid", "true");
        await expect(serviceDateTrigger).toHaveAttribute("aria-describedby", "maintenance-startDate-error");
        await expect(page.getByText("Choose a service date.", { exact: true })).toBeVisible();
        await expect(serviceDateTrigger).toBeFocused();
        await page.reload();
        await expect(page.getByText("Total-only repair", { exact: true })).toBeVisible();
        await edit(page, "cost-only");
        await expect(page.getByLabel("Total Cost", { exact: true })).toHaveValue("3500.00");
        await expect(page.getByText("Loaded records: 4", { exact: true })).toHaveCount(1);
        await page.getByLabel("Notes", { exact: true }).fill("Reviewed without a cost change");
        await page.getByRole("button", { name: "Save Record", exact: true }).click();
        await expect(page.getByText("Total-only repair", { exact: true })).toBeVisible();
        let state = await (await request.get(`${API}/ui4k3/state`)).json();
        const patch = state.calls.find((c: { method: string }) => c.method === "PATCH").body;
        expect(patch.startDate).toMatch(/^\d{4}-\d{2}-\d{2}$/);
        expect(patch).not.toHaveProperty("totalCostThb");
        expect(state.records.find((r: { id: string }) => r.id === "cost-only").totalCostThb).toBe(3500);
        const row = page.locator('[data-maintenance-id="cost-only"]');
        await row.getByRole("button", { name: "View file", exact: true }).click();
        const dialog = page.getByRole("dialog");
        await expect(dialog.getByText("1 / 20", { exact: true })).toBeVisible();
        await expect(dialog.locator("img")).toHaveCount(0);
        await expect(dialog.getByRole("link", { name: "Open / download file" })).toHaveAttribute("href", /invoice-0\.pdf/);
        await dialog.getByRole("button", { name: "Next", exact: true }).click();
        await expect(dialog.getByRole("link", { name: "Open / download file" })).toHaveAttribute("href", /invoice-1\.pdf/);
        await page.keyboard.press("Escape");
        await edit(page, "cancelled");
        await expect(page.getByRole("button", { name: "Complete Maintenance", exact: true })).toHaveCount(0);
        await page.getByRole("button", { name: "Cancel", exact: true }).click();
        await edit(page, "cost-only");
        await page.getByRole("button", { name: "Complete Maintenance", exact: true }).click();
        await page.getByRole("button", { name: "Yes, complete", exact: true }).click();
        await expect(page.getByText("Total-only repair", { exact: true })).toBeVisible();
        state = await (await request.get(`${API}/ui4k3/state`)).json();
        const completed = state.calls.filter((c: { method: string }) => c.method === "PATCH").at(-1).body;
        expect(completed.status).toBe("completed");
        expect(completed.endDate).toMatch(/^\d{4}-\d{2}-\d{2}$/);
        expect(state.records.find((r: { id: string }) => r.id === "cost-only").files.invoices).toHaveLength(20);
    });

    test("Refresh reloads both queues; failures show retry and keep loaded data", async ({ page, request }) => {
        await page.addInitScript(() => localStorage.setItem("language", "en"));
        await page.goto(`${BASE}/app/maintenance`);
        await expect(page.getByRole("button", { name: "Remind driver", exact: true })).toBeVisible();
        await page.getByRole("button", { name: "Refresh", exact: true }).click();
        await expect.poll(async () => {
            const { calls } = await (await request.get(`${API}/ui4k3/state`)).json();
            return ["pm_booking", "scheduled"].every((s) => calls.filter((c: { path: string }) => c.path.includes(`status=${s}`)).length >= 2);
        }).toBe(true);
        await request.post(`${API}/ui4k3/state`, { data: { failures: true } });
        await page.getByRole("button", { name: "Refresh", exact: true }).click();
        await expect(page.getByText("Could not load awaiting schedule. Try again.", { exact: true })).toBeVisible({ timeout: 15000 });
        await expect(page.getByText("Could not load awaiting schedule. Try again.", { exact: true })).toHaveCount(1);
        await expect(page.getByRole("button", { name: "Try again", exact: true }).first()).toBeVisible();
        await expect(page.getByRole("button", { name: "Remind driver", exact: true })).toBeVisible();
        await request.post(`${API}/ui4k3/state`, { data: { failures: false } });
        await page.getByRole("button", { name: "Try again", exact: true }).first().click();
        await expect(page.getByText("Could not load maintenance totals. Try again.", { exact: true })).toHaveCount(0);
    });

    test("an initial history failure shows no empty history or zero-cost cards", async ({ page, request }) => {
        await page.setViewportSize({ width: 360, height: 900 });
        await page.addInitScript(() => localStorage.setItem("language", "th"));
        await request.post(`${API}/ui4k3/state`, { data: { failures: true } });
        await page.goto(`${BASE}/app/trucks/maintenance?id=truck-1`);
        await expect(page.getByText("โหลดประวัติการซ่อมบำรุงไม่สำเร็จ กรุณาลองอีกครั้ง", { exact: true })).toBeVisible({ timeout: 15000 });
        await expect(page.getByText("โหลดประวัติการซ่อมบำรุงไม่สำเร็จ กรุณาลองอีกครั้ง", { exact: true })).toHaveCount(1);
        await expect(page.getByRole("button", { name: "ลองอีกครั้ง", exact: true })).toHaveCount(1);
        const retryBox = await page.getByRole("button", { name: "ลองอีกครั้ง", exact: true }).boundingBox();
        expect(retryBox?.width).toBeGreaterThanOrEqual(44);
        expect(retryBox?.height).toBeGreaterThanOrEqual(44);
        await expect(page.getByText("ไม่มีประวัติการซ่อมบำรุง", { exact: true })).toHaveCount(0);
        await expect(page.getByRole("heading", { name: "ค่าใช้จ่ายเชิงป้องกัน", exact: true })).toHaveCount(0);
        await request.post(`${API}/ui4k3/state`, { data: { failures: false } });
        await page.getByRole("button", { name: "ลองอีกครั้ง", exact: true }).last().click();
        await expect(page.getByText("Total-only repair", { exact: true })).toBeVisible();
    });

    for (const language of ["en", "th"]) for (const theme of ["light", "dark"]) for (const width of [360, 1280]) {
        test(`${language} ${theme} at ${width}px: both maintenance pages and form`, async ({ browser, request }) => {
            const { token } = await (await request.get(`${API}/ui4k3/session`)).json();
            const context = await browser.newContext({ viewport: { width, height: 900 }, timezoneId: "America/New_York" });
            try {
                await context.addCookies([{ name: "lt_at", value: token, url: BASE }]);
                await context.addInitScript(({ language, theme }) => { localStorage.setItem("language", language); localStorage.setItem("theme", theme); }, { language, theme });
                const page = await context.newPage();
                const errors: string[] = [];
                page.on("pageerror", (error) => errors.push(error.message));
                for (const route of ["/app/maintenance", "/app/trucks/maintenance?id=truck-1"]) {
                    await page.goto(BASE + route);
                    await expect(page.locator("h1")).toBeVisible();
                    await expect(page.getByText("Total-only repair", { exact: true })).toBeVisible();
                    expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1)).toBe(true);
                    if (route.includes("truck-1")) {
                        const actionName = language === "en" ? "Actions for Total-only repair" : "ตัวเลือกของ Total-only repair";
                        const action = page.locator('[data-maintenance-id="cost-only"]').getByRole("button", { name: actionName });
                        const actionBox = await action.boundingBox();
                        if (width === 360) {
                            expect(actionBox?.width).toBeGreaterThanOrEqual(44);
                            expect(actionBox?.height).toBeGreaterThanOrEqual(44);
                        }
                        await edit(page, "cost-only", language);
                        await expect(page.getByLabel(language === "en" ? "Total Cost" : "ราคารวม", { exact: true })).toHaveValue("3500.00");
                        expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1)).toBe(true);
                        if (theme === "dark") {
                            await page.waitForSelector(".leaflet-control-geosearch .results");
                            const colors = await page.locator(".leaflet-control-geosearch .results").evaluate((results) => {
                                const active = document.createElement("div");
                                active.className = "active";
                                results.append(active);
                                const style = getComputedStyle(active);
                                const tokenProbe = document.createElement("div");
                                tokenProbe.style.color = "var(--accent-foreground)";
                                tokenProbe.style.backgroundColor = "var(--accent)";
                                document.body.append(tokenProbe);
                                const expected = getComputedStyle(tokenProbe);
                                const output = { foreground: style.color, background: style.backgroundColor, expectedForeground: expected.color, expectedBackground: expected.backgroundColor };
                                active.remove();
                                tokenProbe.remove();
                                return output;
                            });
                            expect(colors.foreground).toBe(colors.expectedForeground);
                            expect(colors.background).toBe(colors.expectedBackground);
                        }
                    }
                    if (SHOTS) {
                        fs.mkdirSync(SHOTS, { recursive: true });
                        await page.screenshot({ path: path.join(SHOTS, `maintenance-${route.includes("truck-1") ? "form" : "overview"}-${language}-${theme}-${width}.png`), fullPage: true });
                    }
                }
                expect(errors).toEqual([]);
            } finally { await context.close(); }
        });
    }
});
