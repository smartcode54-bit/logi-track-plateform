/** UI4E: existing page-header-layout harness against a production build and local stub API.
 * PAGE_LAYOUT_BASE_URL=http://127.0.0.1:3901 PAGE_LAYOUT_TOKEN=<lt_at> PAGE_LAYOUT_PLATFORM_TOKEN=<lt_at>
 * pnpm exec playwright test --config playwright.layout.config.ts ui4-security.spec.ts
 */
import fs from "node:fs";
import path from "node:path";
import { expect, test } from "@playwright/test";

const BASE = process.env.PAGE_LAYOUT_BASE_URL ?? "";
const TOKEN = process.env.PAGE_LAYOUT_PLATFORM_TOKEN ?? process.env.PAGE_LAYOUT_TOKEN ?? "";
const SHOTS = process.env.PAGE_LAYOUT_SHOTS ?? "";
const PAGES = ["", "/users", "/audit", "/roles", "/api-keys", "/mobile-clients", "/mobile-release", "/status", "/tenants"];

test.describe("UI4E security bodies", () => {
    test.skip(!BASE || !TOKEN, "needs PAGE_LAYOUT_BASE_URL and PAGE_LAYOUT_PLATFORM_TOKEN (or PAGE_LAYOUT_TOKEN)");
    for (const language of ["en", "th"] as const) {
        for (const theme of ["light", "dark"] as const) {
            for (const width of [360, 1280]) {
                test(`${language} ${theme} at ${width} px`, async ({ browser }) => {
                    const context = await browser.newContext({ viewport: { width, height: 1000 }, isMobile: width < 768, hasTouch: width < 768 });
                    try {
                        await context.addCookies([{ name: "lt_at", value: TOKEN, url: BASE }]);
                        await context.addInitScript(({ language, theme }) => {
                            localStorage.setItem("language", language);
                            localStorage.setItem("theme", theme);
                        }, { language, theme });
                        const page = await context.newPage();
                        for (const suffix of PAGES) {
                            await page.goto(`${BASE}/app/security-center${suffix}`);
                            const header = page.locator("[data-slot=page-header]").first();
                            await expect(header).toBeVisible();
                            await expect(page.locator("main h1")).toHaveCount(1);
                            await expect(page.locator("html")).toHaveAttribute("lang", language);
                            await expect(page.locator("html")).toHaveClass(new RegExp(theme));
                            const main = page.getByRole("main");
                            const ready = { "": "card-system", "/users": "user-row", "/audit": "event-row", "/roles": "matrix-row", "/api-keys": "api-key-row", "/mobile-clients": "installation-row", "/mobile-release": "published-build", "/status": "status-summary", "/tenants": "tenant-row" }[suffix]!;
                            await expect(main.getByTestId(ready).first()).toBeVisible();
                            const dimensions = await page.evaluate(() => ({ width: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }));
                            expect(dimensions.scroll, suffix).toBeLessThanOrEqual(dimensions.width + 1);
                            const layout = await header.evaluate((element) => {
                                const heading = element.querySelector("h1")!.getBoundingClientRect();
                                const actions = element.querySelector("[data-slot=page-header-actions]");
                                return { headingBottom: heading.bottom, actionWidth: actions?.getBoundingClientRect().width,
                                    controls: [...(actions?.children ?? [])].map((node) => {
                                        const box = node.getBoundingClientRect();
                                        return { top: box.top, width: box.width, centre: box.top + box.height / 2 };
                                    }) };
                            });
                            if (width < 768) {
                                for (const control of layout.controls) {
                                    expect(control.top).toBeGreaterThanOrEqual(layout.headingBottom);
                                    expect(Math.round(control.width)).toBe(Math.round(layout.actionWidth!));
                                }
                            } else if (layout.controls.length > 1) {
                                const centres = layout.controls.map((control) => control.centre);
                                expect(Math.max(...centres) - Math.min(...centres)).toBeLessThan(8);
                            }
                            if (width < 768) {
                                const summaries = main.locator("summary");
                                if (["", "/audit", "/status", "/mobile-release"].includes(suffix)) expect(await summaries.count()).toBeGreaterThan(0);
                                for (const summary of await summaries.all()) {
                                    const box = await summary.boundingBox();
                                    expect(box?.height, `${suffix}: disclosure target`).toBeGreaterThanOrEqual(44);
                                    expect(box?.width, `${suffix}: disclosure width`).toBeGreaterThanOrEqual(44);
                                }
                            }
                            if (["/users", "/audit", "/api-keys", "/mobile-clients"].includes(suffix)) {
                                await expect(header.getByTestId("scope-caption")).toHaveText(language === "th" ? "ทุกองค์กร" : "All organisations");
                            }
                            if (suffix === "") {
                                const donut = page.getByTestId("rbac-distribution");
                                await expect(donut.getByRole("listitem")).toHaveCount(3);
                                const graphic = donut.locator("[aria-hidden=true]").first();
                                expect(await graphic.locator("div").first().evaluate((el) => getComputedStyle(el).backgroundImage)).toContain("conic-gradient");
                            }
                            if (suffix === "/roles") {
                                const toggle = page.getByRole("checkbox", { name: language === "th" ? "แสดงรหัสสิทธิ์ทางเทคนิค" : "Show technical keys" });
                                await expect(toggle).not.toBeChecked();
                                if (width < 768) expect((await toggle.locator("..").boundingBox())?.height, "technical-key toggle target").toBeGreaterThanOrEqual(44);
                                await toggle.focus();
                                await page.keyboard.press("Space");
                                await expect(toggle).toBeChecked();
                                await page.keyboard.press("Space");
                            }
                            if (suffix === "/api-keys") {
                                await header.getByRole("button").click();
                                const dialog = page.getByRole("dialog");
                                const toggle = dialog.getByRole("checkbox", { name: language === "th" ? "แสดงรหัสสิทธิ์ทางเทคนิค" : "Show technical keys" });
                                await expect(toggle).not.toBeChecked();
                                if (width < 768) expect((await toggle.locator("..").boundingBox())?.height, "API key toggle target").toBeGreaterThanOrEqual(44);
                                await page.keyboard.press("Escape");
                                await expect(dialog).not.toBeVisible();
                            }
                            const visibleText = await main.innerText();
                            if (language === "th") expect(visibleText).not.toMatch(/\bago\b|\bOct \d/);
                            expect(visibleText).not.toMatch(/\bRedis\b|\boutbox\b|\bflavor\b|cmd\/release|\bRBAC\b/);
                            await page.evaluate(() => document.fonts.ready);
                            if (SHOTS) {
                                fs.mkdirSync(SHOTS, { recursive: true });
                                await page.screenshot({ path: path.join(SHOTS, `security${suffix.replaceAll("/", "-")}-${language}-${theme}-${width}.png`), fullPage: true });
                            }
                        }
                    } finally {
                        await context.close();
                    }
                });
            }
        }
    }
});
