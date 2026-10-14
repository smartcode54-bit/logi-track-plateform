/**
 * Run against a local production build on 3841 and fixtures/ui3f5-navigation-api.mjs on 3842:
 * pnpm exec playwright test --config playwright.layout.config.ts ui3f-navigation.spec.ts
 * The web server uses GO_API_INTERNAL_URL=http://127.0.0.1:3842, WEB_PUBLIC_ORIGIN=http://127.0.0.1:3841,
 * JWT_ISSUER=ui3f5, JWT_AUDIENCE=ui3f5-web and SESSION_COOKIE_SECURE=false. Prerequisites fail rather than skip.
 */
import { expect, test, type Browser, type Page } from "@playwright/test";

const BASE = "http://127.0.0.1:3841";
const API = "http://127.0.0.1:3842";
const customerHrefs = ["/app/driver-monitor", "/app/job-assign", "/app/incident-reports", "/app/support"];
const partnerHrefs = ["/app/driver-monitor", "/app/job-assign", "/app/incident-reports", "/app/chat", "/app/trucks", "/app/drivers", "/app/maintenance", "/app/accounting/billing-document", "/app/support"];

async function openShell(browser: Browser, profile: string, language: "en" | "th", width: number, theme = "light", security = false, apple = false) {
    const context = await browser.newContext({ viewport: { width, height: 812 },
        ...(apple ? { userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/130.0.0.0 Safari/537.36" } : {}) });
    const response = await context.request.get(`${API}/ui3f5/session?profile=${profile}`);
    expect(response.ok(), "needs the UI3F5 stub API on port 3842").toBe(true);
    const { token } = await response.json();
    await context.addCookies([{ name: "lt_at", value: token, url: BASE }, { name: "sidebar_state", value: "true", url: BASE }]);
    await context.addInitScript(({ language, theme }) => { localStorage.setItem("language", language); localStorage.setItem("theme", theme); }, { language, theme });
    const page = await context.newPage();
    const responsePage = await page.goto(BASE + (security ? "/app/security-center" : "/app/support"));
    expect(responsePage?.ok(), "needs a local production build on port 3841").toBe(true);
    await expect(page.getByTestId("quick-jump-trigger")).toBeVisible();
    await page.evaluate(() => document.fonts.ready);
    return { page, context };
}

const options = (page: Page) => page.getByRole("option").evaluateAll((nodes) => nodes.map((node) => node.getAttribute("data-value")));
const noOverflow = async (page: Page) => expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(1);

for (const profile of ["staff", "customer", "partner"]) {
    for (const language of ["en", "th"] as const) {
        for (const theme of ["light", "dark"]) {
            test(`${profile} ${language} ${theme}: 360 px open palette, targets, scrolling and focus`, async ({ browser }, testInfo) => {
                const { page, context } = await openShell(browser, profile, language, 360, theme);
                try {
                    await noOverflow(page);
                    const trigger = page.getByTestId("quick-jump-trigger");
                    const triggerBox = (await trigger.boundingBox())!;
                    expect(triggerBox.width).toBeGreaterThanOrEqual(44);
                    expect(triggerBox.height).toBeGreaterThanOrEqual(44);
                    await trigger.click();
                    const dialog = page.getByRole("dialog", { name: language === "en" ? "Jump to a page" : "ไปยังหน้า" });
                    await expect(dialog).toBeVisible();
                    await expect(page.getByRole("combobox", { name: language === "en" ? "Search pages" : "ค้นหาหน้า" })).toBeFocused();
                    await expect(page.getByRole("listbox", { name: language === "en" ? "Search pages" : "ค้นหาหน้า" })).toBeVisible();
                    await expect.poll(() => dialog.evaluate((node) => node.style.getPropertyValue("--quick-jump-width"))).toBe("360px");
                    await expect.poll(() => dialog.getByRole("button").first().evaluate((node) => node.getBoundingClientRect().height)).toBeGreaterThanOrEqual(44);
                    if (profile !== "staff") expect(await options(page)).toEqual(profile === "customer" ? customerHrefs : partnerHrefs);
                    await noOverflow(page);
                    const bounds = (await dialog.boundingBox())!;
                    expect(bounds.x).toBeGreaterThanOrEqual(16);
                    expect(bounds.x + bounds.width).toBeLessThanOrEqual(344);
                    expect(bounds.y).toBeGreaterThanOrEqual(16);
                    expect(bounds.y + bounds.height).toBeLessThanOrEqual(796);
                    for (const target of await dialog.locator('button, [role="option"]').all()) {
                        const box = (await target.boundingBox())!;
                        expect(box.height).toBeGreaterThanOrEqual(44);
                        expect(box.width).toBeGreaterThanOrEqual(44);
                    }
                    if (profile === "staff") expect(await page.getByRole("listbox").evaluate((node) => node.scrollHeight > node.clientHeight)).toBe(true);
                    await page.screenshot({ path: testInfo.outputPath("palette-360.png") });
                    for (let index = 0; index < 6; index++) {
                        await page.keyboard.press("Tab");
                        expect(await dialog.evaluate((node) => node.contains(document.activeElement))).toBe(true);
                    }
                    await page.keyboard.press("Control+k");
                    await expect(page.getByRole("combobox")).toBeFocused();
                    await page.keyboard.press("Escape");
                    await expect(trigger).toBeFocused();
                    await expect(dialog).toHaveCount(0);
                } finally { await context.close(); }
            });
        }
    }
}

for (const apple of [false, true]) {
    test(`platform shortcut, arrows, Enter, route focus and overlay guard (Apple: ${apple})`, async ({ browser }) => {
        const { page, context } = await openShell(browser, "staff", "en", 1280, "light", false, apple);
        try {
            const trigger = page.getByTestId("quick-jump-trigger");
            await expect(trigger).toHaveAttribute("aria-keyshortcuts", apple ? "Meta+K" : "Control+K");
            const invoker = page.locator("[data-slot=sidebar-trigger]");
            await invoker.focus();
            await page.keyboard.press("k");
            await page.keyboard.press(apple ? "Control+k" : "Meta+k");
            await expect(page.getByRole("dialog")).toHaveCount(0);
            await page.keyboard.press(apple ? "Meta+k" : "Control+k");
            const input = page.getByRole("combobox", { name: "Search pages" });
            await expect(input).toBeFocused();
            expect(await input.evaluate((node) => getComputedStyle(node).boxShadow)).not.toBe("none");
            const first = page.getByRole("option").first();
            await page.keyboard.press("Home");
            await expect(first).toHaveAttribute("aria-selected", "true");
            await page.keyboard.press("ArrowDown");
            const secondHref = (await options(page))[1]!;
            await expect(page.getByRole("option").nth(1)).toHaveAttribute("aria-selected", "true");
            await page.keyboard.press("ArrowUp");
            await expect(first).toHaveAttribute("aria-selected", "true");
            await page.keyboard.press("Escape");
            await expect(invoker).toBeFocused();
            await trigger.click();
            await input.fill(secondHref);
            await page.keyboard.press("Enter");
            await expect(page).toHaveURL(BASE + secondHref);
            await expect(page.getByRole("main")).toBeFocused();
            await trigger.click();
            await input.fill(secondHref);
            await page.keyboard.press("Enter");
            await expect(page.getByRole("main")).toBeFocused();
            await trigger.click();
            await input.fill("/app/support");
            await page.getByRole("option").click();
            await expect(page).toHaveURL(BASE + "/app/support");
            await expect(page.getByRole("main")).toBeFocused();
            await page.getByTestId("user-menu-trigger").click();
            await expect(page.getByRole("menu")).toBeVisible();
            await page.keyboard.press(apple ? "Meta+k" : "Control+k");
            await expect(page.getByRole("dialog")).toHaveCount(0);
            await page.keyboard.press("Escape");
            await page.setViewportSize({ width: 360, height: 812 });
            await invoker.click();
            await expect(page.getByRole("dialog")).toBeVisible();
            await page.keyboard.press(apple ? "Meta+k" : "Control+k");
            await expect(page.locator("[data-quick-jump-dialog]")).toHaveCount(0);
        } finally { await context.close(); }
    });
}

test("matches the expanded staff and Security Center sidebar exactly, even when groups collapse", async ({ browser }) => {
    for (const security of [false, true]) {
        const { page, context } = await openShell(browser, "staff", "en", 1280, "light", security);
        try {
            const menu = page.getByRole("navigation", { name: security ? "Security center menu" : "Main menu" });
            const groups = await menu.locator("button[aria-expanded]").all();
            for (const group of groups) if (await group.getAttribute("aria-expanded") === "false") await group.click();
            const hrefs = await menu.getByRole("link").evaluateAll((nodes) => [...new Set(nodes.map((node) => node.getAttribute("href")))]);
            for (const group of groups) if (await group.getAttribute("aria-expanded") === "true") await group.click();
            await page.getByTestId("quick-jump-trigger").click();
            await expect(page.getByRole("combobox")).toBeFocused();
            expect(await options(page)).toEqual(hrefs);
        } finally { await context.close(); }
    }
});

test("the dialog chunk is lazy and a failed request offers a working retry", async ({ browser }) => {
    const { page, context } = await openShell(browser, "customer", "en", 1280);
    try {
        // A cold route load has no command root; the first opening requests its chunk.
        await expect(page.locator("[cmdk-root]")).toHaveCount(0);
        let aborted = false;
        await page.route("**/_next/static/chunks/*.js", async (route) => {
            if (!aborted) { aborted = true; await route.abort(); }
            else await route.continue();
        });
        await page.getByTestId("quick-jump-trigger").click();
        await expect(page.getByRole("alert")).toContainText("This dialog could not be opened");
        await page.getByRole("button", { name: "Try again" }).click();
        await expect(page.getByRole("combobox", { name: "Search pages" })).toBeFocused();
    } finally { await context.close(); }
});
