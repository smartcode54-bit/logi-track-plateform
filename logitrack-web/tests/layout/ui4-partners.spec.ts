/** Production-build page-header-layout harness; no server is started here.
 * PAGE_LAYOUT_BASE_URL=http://127.0.0.1:3901 PAGE_LAYOUT_TOKEN=<stub session>
 * PAGE_LAYOUT_PARTNER_ID=ui4c-partner [PAGE_LAYOUT_SHOTS=/tmp/ui4c-shots]
 * pnpm exec playwright test --config playwright.layout.config.ts ui4-partners.spec.ts
 */
import fs from "node:fs";
import path from "node:path";
import { expect, test } from "@playwright/test";
import en from "../../context/locales/en/subcontractors";
import th from "../../context/locales/th/subcontractors";

const BASE = process.env.PAGE_LAYOUT_BASE_URL ?? "";
const TOKEN = process.env.PAGE_LAYOUT_TOKEN ?? "";
const ID = process.env.PAGE_LAYOUT_PARTNER_ID ?? "";
const SHOTS = process.env.PAGE_LAYOUT_SHOTS ?? "";
const label = (value: string) => new RegExp(`^${value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}`);

test.describe("Partner pages (UI4C)", () => {
    test.skip(!BASE || !TOKEN, "needs PAGE_LAYOUT_BASE_URL and PAGE_LAYOUT_TOKEN");

    for (const language of ["en", "th"] as const) {
        for (const theme of ["light", "dark"] as const) {
            for (const width of [360, 1280]) {
                for (const kind of ["list", "detail", "new"] as const) {
                    test(`${kind}: ${language} ${theme} at ${width} px`, async ({ browser }) => {
                        test.skip(kind === "detail" && !ID, "needs PAGE_LAYOUT_PARTNER_ID");
                        const context = await browser.newContext({ viewport: { width, height: 900 }, isMobile: width < 768, hasTouch: width < 768 });
                        try {
                            await context.addCookies([{ name: "lt_at", value: TOKEN, url: BASE }]);
                            await context.addInitScript(({ language, theme }) => {
                                localStorage.setItem("language", language);
                                localStorage.setItem("theme", theme);
                            }, { language, theme });
                            const copy = language === "en" ? en : th;
                            const page = await context.newPage();
                            const url = `/app/subcontractors${kind === "detail" ? `/${ID}` : kind === "new" ? "/new" : ""}`;
                            await page.goto(BASE + url);
                            const header = page.locator("[data-slot=page-header]").first();
                            await expect(header).toBeVisible({ timeout: 30_000 });
                            await expect(page.locator("main h1")).toHaveCount(1);
                            await expect(page.locator("html")).toHaveAttribute("lang", language);
                            await expect(page.locator("html")).toHaveClass(new RegExp(theme));
                            const actions = header.locator("[data-slot=page-header-actions]");
                            if (await actions.count()) {
                                const boxes = await actions.evaluate((el) => [...el.children].map((child) => {
                                    const r = child.getBoundingClientRect();
                                    return { x: r.x, y: r.y, width: r.width, height: r.height };
                                }));
                                const heading = await header.locator("h1").boundingBox();
                                const actionBox = await actions.boundingBox();
                                if (width < 768) {
                                    for (const box of boxes) {
                                        expect(box.y).toBeGreaterThanOrEqual(heading!.y + heading!.height);
                                        expect(Math.round(box.width)).toBe(Math.round(actionBox!.width));
                                    }
                                } else {
                                    expect(actionBox!.x).toBeGreaterThan(heading!.x);
                                    expect(Math.max(...boxes.map((box) => box.y + box.height / 2)) - Math.min(...boxes.map((box) => box.y + box.height / 2))).toBeLessThan(8);
                                }
                            }
                            if (kind === "new") {
                                await page.getByLabel(label(copy["subcontractors.form.legalName"])).fill("UI4C partner");
                                await page.getByLabel(label(copy["subcontractors.form.nationalId"])).fill("1234567890121");
                                await page.getByRole("button", { name: copy["subcontractors.form.nextStep"], exact: true }).click();
                                await page.getByLabel(label(copy["subcontractors.form.fullName"])).fill("Fixture contact");
                                await page.getByLabel(label(copy["subcontractors.form.mobilePhone"])).fill("0812345678");
                                await page.getByRole("button", { name: copy["subcontractors.form.nextStep"], exact: true }).click();
                                await page.getByLabel(copy["subcontractors.form.serviceRegions"], { exact: true }).fill("Chiang Mai, Lamphun");
                                await page.getByRole("button", { name: copy["subcontractors.form.nextStep"], exact: true }).click();
                                await expect(page.getByText("Chiang Mai, Lamphun", { exact: true })).toBeVisible();
                                await expect(page.getByRole("button", { name: copy["subcontractors.form.completeRegistration"], exact: true })).toBeVisible();
                            } else if (kind === "detail") {
                                await expect(page.getByRole("heading", { name: copy["subcontractors.edit.documents"], exact: true })).toBeVisible();
                                for (const link of await page.getByRole("link").filter({ hasText: copy["subcontractors.ui4.openDocument"] }).all()) {
                                    await expect(link).toHaveAttribute("target", "_blank");
                                    await expect(link).toHaveAttribute("rel", "noopener noreferrer");
                                    await expect(link).not.toHaveAttribute("download");
                                }
                            } else {
                                const firstRow = page.locator("main table tbody tr").first();
                                await expect(firstRow).toBeVisible();
                                const headerTextX = await page.locator("main table thead th").first().getByRole("button").evaluate((button) => {
                                    const textNode = [...button.childNodes].find((node) => node.nodeType === Node.TEXT_NODE && node.textContent?.trim());
                                    if (!textNode) return Number.NaN;
                                    const range = document.createRange();
                                    range.selectNodeContents(textNode);
                                    return range.getBoundingClientRect().x;
                                });
                                const cellTextX = await firstRow.locator("td").first().locator(".font-semibold").evaluate((element) => element.getBoundingClientRect().x);
                                expect(Math.abs(headerTextX - cellTextX)).toBeLessThanOrEqual(1);
                                if (width < 768) {
                                    const rowAction = firstRow.locator("td").last().getByRole("button").first();
                                    const actionBox = await rowAction.boundingBox();
                                    expect(actionBox!.width).toBeGreaterThanOrEqual(44);
                                    expect(actionBox!.height).toBeGreaterThanOrEqual(44);
                                    await rowAction.click();
                                    for (const item of await page.getByRole("menuitem").all()) {
                                        const box = await item.boundingBox();
                                        expect(box!.width).toBeGreaterThanOrEqual(44);
                                        expect(box!.height).toBeGreaterThanOrEqual(44);
                                    }
                                    await page.getByRole("menuitem", { name: copy["subcontractors.action.suspendPartner"], exact: true }).click();
                                    const dialog = page.getByRole("alertdialog", { name: copy["subcontractors.ui4.suspendTitle"], exact: true });
                                    await expect(dialog).toBeVisible();
                                    for (const button of await dialog.getByRole("button").all()) {
                                        const box = await button.boundingBox();
                                        expect(box!.width).toBeGreaterThanOrEqual(44);
                                        expect(box!.height).toBeGreaterThanOrEqual(44);
                                    }
                                    await dialog.getByRole("button", { name: copy["subcontractors.form.cancel"], exact: true }).click();
                                    await expect(rowAction).toBeFocused();
                                }
                            }
                            await expect(page.locator('main a[href="#"]')).toHaveCount(0);
                            await expect(page.locator("main")).not.toContainText(/XX-XXXX882|Chicago, Illinois|email@example.com|Bangkok, TH|insurance_cert_2024|Midwest|Northeast|↑5%|↑12%|↓2%/);
                            const dimensions = await page.evaluate(() => ({ width: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }));
                            expect(dimensions.scroll).toBeLessThanOrEqual(dimensions.width + 1);
                            await page.evaluate(() => document.fonts.ready);
                            if (SHOTS) {
                                fs.mkdirSync(SHOTS, { recursive: true });
                                await page.screenshot({ path: path.join(SHOTS, `partners-${kind}-${language}-${theme}-${width}.png`), fullPage: true });
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
