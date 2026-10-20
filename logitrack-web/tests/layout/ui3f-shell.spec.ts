/**
 * UI3F shell acceptance against a production build on 3821, with the stub API on 3822.
 * PAGE_LAYOUT_{TOKEN,CUSTOMER_TOKEN,PARTNER_TOKEN} must belong to the corresponding stub principals.
 * Missing prerequisites fail this spec; --list needs neither a build nor a session.
 */
import { expect, test, type Browser, type Page } from "@playwright/test";
import type { MeDTO } from "../../features/auth/api/me";

const BASE = process.env.PAGE_LAYOUT_BASE_URL ?? "";
const TOKENS = {
    staff: process.env.PAGE_LAYOUT_TOKEN ?? "",
    customer: process.env.PAGE_LAYOUT_CUSTOMER_TOKEN ?? "",
    partner: process.env.PAGE_LAYOUT_PARTNER_TOKEN ?? "",
};
const THAI_NAME = "บริษัทขนส่งวันเพ็ญรัชดาและพันธมิตรทั่วประเทศไทยจำกัด";
const ENGLISH_NAME = "Wanpen Ratchada Transport and Nationwide Logistics Partners Limited";
const ROLES = {
    staff: { en: "Tenant admin", th: "ผู้ดูแลองค์กร" },
    customer: { en: "Customer view", th: "มุมมองลูกค้า" },
    partner: { en: "Partner admin", th: "ผู้ดูแลรถร่วม" },
};

type Profile = keyof typeof TOKENS;
type Language = "en" | "th";

test.beforeAll(() => {
    expect(BASE, "needs PAGE_LAYOUT_BASE_URL for a local production build").toMatch(/^http:\/\/(localhost|127\.0\.0\.1):\d+$/);
    for (const [profile, token] of Object.entries(TOKENS)) expect(token, `needs the ${profile} session token`).not.toBe("");
});

async function openShell(browser: Browser, profile: Profile, language: Language, width: number, expanded = false, route = "/app/support") {
    const context = await browser.newContext({ viewport: { width, height: 900 }, isMobile: width < 768, hasTouch: width < 768 });
    await context.addCookies([
        { name: "lt_at", value: TOKENS[profile], url: BASE },
        { name: "sidebar_state", value: String(expanded), url: BASE },
    ]);
    const session = await context.request.get(BASE + "/api/go/v1/me");
    expect(session.ok(), `needs a valid ${profile} session from the stub API`).toBe(true);
    const principal = (await session.json()).data as MeDTO;
    if (profile === "customer") {
        expect(principal.tenant).toBeNull();
        expect(principal.customerScopes.length).toBeGreaterThan(0);
        expect(principal.platformRoles).toEqual([]);
        expect(principal.dispatcher).toBe(false);
    } else {
        expect(principal.tenant?.kind).toBe(profile === "partner" ? "carrier" : "own_fleet");
        expect(principal.tenant?.role).toBe("tenant_admin");
    }
    await context.addInitScript((lang) => localStorage.setItem("language", lang), language);
    // Keep the token principal's identity/capabilities; deterministic display fields stress the shell alone.
    await context.route("**/api/go/v1/me", async (intercept) => {
        const response = await intercept.fetch();
        const body = await response.json();
        const me = body.data;
        me.displayName = language === "th" ? THAI_NAME : ENGLISH_NAME;
        if (me.tenant) Object.assign(me.tenant, { nameTh: THAI_NAME, nameEn: ENGLISH_NAME });
        if (me.customerScopes.length) me.customerScopes[0].name = language === "th" ? THAI_NAME : ENGLISH_NAME;
        await intercept.fulfill({ response, json: body });
    });
    await context.route("**/api/go/v1/me/tenants", async (intercept) => {
        const response = await intercept.fetch();
        const body = await response.json();
        if (body.data.length) {
            const tenant = { ...body.data[0], nameTh: THAI_NAME, nameEn: ENGLISH_NAME };
            body.data = Array.from({ length: 9 }, (_, index) => ({ ...tenant, id: index === 0 ? tenant.id : `shell-membership-${index}`, status: "active" }));
        }
        await intercept.fulfill({ response, json: body });
    });
    const page = await context.newPage();
    await page.goto(BASE + route);
    await expect(page.getByTestId("user-menu-trigger")).toBeVisible();
    if (profile !== "customer") await expect(page.getByTestId("tenant-switcher")).toBeVisible();
    await page.evaluate(() => document.fonts.ready);
    return { page, context };
}

async function noOverflow(page: Page) {
    const dimensions = await page.evaluate(() => ({ width: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }));
    expect(dimensions.scroll).toBeLessThanOrEqual(dimensions.width + 1);
}

async function targetSize(page: Page, selector: string) {
    const bounds = await page.locator(selector).boundingBox();
    expect(bounds!.width).toBeGreaterThanOrEqual(44);
    expect(bounds!.height).toBeGreaterThanOrEqual(44);
}

for (const width of [360, 1280]) {
    for (const language of ["en", "th"] as const) {
        for (const route of ["/app/support", "/app/security-center/api-keys"]) {
            test(`display preferences: ${width}px ${language} ${route}`, async ({ browser }) => {
                const { page, context } = await openShell(browser, "staff", language, width, true, route);
                try {
                    const mobile = width < 768;
                    if (mobile) await page.setViewportSize({ width, height: 640 });
                    if (mobile) await page.locator("[data-slot=sidebar-trigger]").click();
                    const surface = mobile ? page.getByRole("dialog") : page.getByRole("banner");
                    const controls = surface.locator("[data-slot=display-preferences]");
                    await expect(controls).toBeVisible();
                    if (mobile) {
                        await expect(page.getByRole("banner").locator("[data-slot=display-preferences]")).toBeHidden();
                        const support = surface.getByRole("link", { name: language === "en" ? "Support" : "ศูนย์ช่วยเหลือ", exact: true });
                        const before = (await controls.boundingBox())!;
                        expect(before.y + before.height).toBeLessThanOrEqual((await support.boundingBox())!.y);
                        expect(await surface.locator("[data-sidebar-scroll]").evaluate((node) => node.scrollHeight > node.clientHeight)).toBe(true);
                        await surface.locator("[data-sidebar-scroll]").evaluate((node) => { node.scrollTop = node.scrollHeight; });
                        expect((await controls.boundingBox())!.y).toBe(before.y);
                        expect(before.y + before.height).toBeLessThanOrEqual(640);
                        for (const button of await controls.getByRole("button").all()) {
                            const box = (await button.boundingBox())!;
                            expect(box.width).toBeGreaterThanOrEqual(44);
                            expect(box.height).toBeGreaterThanOrEqual(44);
                        }
                    }
                    for (const [label, theme] of [[language === "en" ? "Dark" : "มืด", "dark"], [language === "en" ? "Light" : "สว่าง", "light"]]) {
                        await controls.getByRole("button", { name: language === "en" ? "Theme" : "ธีม", exact: true }).click();
                        await page.getByRole("menuitemradio", { name: label, exact: true }).click();
                        await expect(page.locator("html")).toHaveClass(new RegExp(`\\b${theme}\\b`));
                        if (mobile) await expect(surface).toBeVisible();
                        const output = process.env.SHELL_PREFERENCES_SCREENSHOTS;
                        if (output) await page.screenshot({ path: `${output}/${width}-${language}-${route.includes("security-center") ? "security" : "main"}-${theme}.png` });
                    }
                    await controls.getByRole("button", { name: language === "en" ? "ไทย" : "English", exact: true }).click();
                    await expect(page.locator("html")).toHaveAttribute("lang", language === "en" ? "th" : "en");
                    if (mobile) await expect(surface).toBeVisible();
                    await noOverflow(page);
                } finally {
                    await context.close();
                }
            });
        }
    }
}

async function startKeyboardSequence(page: Page) {
    await page.evaluate(() => {
        document.body.tabIndex = -1;
        document.body.focus();
        document.body.removeAttribute("tabindex");
    });
}

for (const profile of ["staff", "customer", "partner"] as const) {
    for (const language of ["en", "th"] as const) {
        for (const width of [360, 375, 767, 768, 1280]) {
            test(`${profile} ${language}: shell, Tab order and open menus at ${width} px`, async ({ browser }) => {
                const { page, context } = await openShell(browser, profile, language, width);
                try {
                    await expect(page.getByRole("banner")).toHaveCount(1);
                    await expect(page.getByRole("main")).toHaveCount(1);
                    const landmarks = await page.evaluate(() => {
                        const banner = document.querySelector("header")!;
                        const sidebar = document.querySelector("[data-slot=sidebar]");
                        const main = document.querySelector("main")!;
                        return {
                            height: banner.getBoundingClientRect().height,
                            outside: !main.contains(banner),
                            beforeMain: Boolean(banner.compareDocumentPosition(main) & Node.DOCUMENT_POSITION_FOLLOWING),
                            beforeSidebar: !sidebar || Boolean(banner.compareDocumentPosition(sidebar) & Node.DOCUMENT_POSITION_FOLLOWING),
                            positiveTabIndex: [...document.querySelectorAll<HTMLElement>("[tabindex]")].some((node) => node.tabIndex > 0),
                        };
                    });
                    expect(landmarks).toEqual({ height: 64, outside: true, beforeMain: true, beforeSidebar: true, positiveTabIndex: false });
                    await noOverflow(page);
                    await targetSize(page, "[data-testid=user-menu-trigger]");
                    if (width < 768) {
                        await expect(page.getByTestId("header-scope")).toBeVisible();
                        const slice = await page.getByTestId("header-scope").locator("span").first().boundingBox();
                        expect(slice!.width).toBeGreaterThanOrEqual(24);
                        const role = page.getByTestId("header-scope").locator("span").last();
                        await expect(role).toHaveText(ROLES[profile][language]);
                        expect(await role.evaluate((node) => node.scrollWidth <= node.clientWidth + 1)).toBe(true);
                        if (profile !== "customer") await targetSize(page, "[data-testid=tenant-switcher] [role=combobox]");
                    }
                    // Start at body, counting the skip link; sidebar controls must follow the account trigger.
                    await startKeyboardSequence(page);
                    const limit = width < 768 ? 5 : 10;
                    const stops: string[] = [];
                    for (let count = 0; count < limit; count++) {
                        await page.keyboard.press("Tab");
                        const stop = await page.evaluate(() => ({
                            account: document.activeElement?.getAttribute("data-testid") === "user-menu-trigger",
                            inSidebar: Boolean(document.activeElement?.closest("[data-slot=sidebar]")),
                            name: document.activeElement?.getAttribute("aria-label") || document.activeElement?.textContent || "",
                        }));
                        stops.push(stop.name);
                        expect(stop.inSidebar, stops.join(" → ")).toBe(false);
                        if (stop.account) break;
                    }
                    await expect(page.getByTestId("user-menu-trigger"), stops.join(" → ")).toBeFocused();
                    await page.getByTestId("user-menu-trigger").click();
                    await expect(page.getByRole("menu")).toBeVisible();
                    await expect(page.getByTestId("user-menu-scope")).toContainText(language === "th" ? THAI_NAME : ENGLISH_NAME);
                    await noOverflow(page);
                    await page.keyboard.press("Escape");
                    if (profile !== "customer") {
                        const picker = page.getByTestId("tenant-switcher").getByRole("combobox");
                        await expect(picker).toHaveAccessibleName(width < 768
                            ? language === "th" ? `เปลี่ยนบริษัท (ปัจจุบัน: ${THAI_NAME})` : `Switch company (current: ${ENGLISH_NAME})`
                            : language === "th" ? "สลับองค์กร" : "Switch organisation");
                        await picker.click();
                        await expect(page.getByRole("listbox")).toBeVisible();
                        await expect(page.getByRole("option")).toHaveCount(9);
                        await noOverflow(page);
                        await page.keyboard.press("Escape");
                    }
                    if (width < 768) {
                        const toggle = page.locator("[data-slot=sidebar-trigger]");
                        await toggle.click();
                        await expect(page.getByRole("dialog")).toBeVisible();
                        await noOverflow(page);
                        await page.keyboard.press("Escape");
                        await expect(toggle).toBeFocused();
                    }
                } finally {
                    await context.close();
                }
            });
        }
    }
}

for (const width of [768, 1280]) {
    test(`expanded and collapsed sidebar grid slots and deepest breadcrumb Tabs at ${width} px`, async ({ browser }) => {
        const { page, context } = await openShell(browser, "staff", "th", width, true, "/app/utilities/billing-impact");
        try {
            for (const sidebarWidth of [256, 48]) {
                await expect.poll(async () => (await page.getByRole("banner").boundingBox())!.x).toBe(sidebarWidth);
                const header = await page.getByRole("banner").boundingBox();
                const sidebar = await page.locator("[data-slot=sidebar-container]").boundingBox();
                expect(sidebar!.x).toBe(0);
                expect(sidebar!.width).toBe(sidebarWidth);
                expect(header!.x).toBe(sidebarWidth);
                expect(header!.height).toBe(64);
                await noOverflow(page);
                await startKeyboardSequence(page);
                for (let count = 0; count < 10; count++) {
                    await page.keyboard.press("Tab");
                    const account = await page.getByTestId("user-menu-trigger").evaluate((node) => node === document.activeElement);
                    expect(await page.evaluate(() => Boolean(document.activeElement?.closest("[data-slot=sidebar]")))).toBe(false);
                    if (account) break;
                }
                await expect(page.getByTestId("user-menu-trigger")).toBeFocused();
                if (sidebarWidth === 256) {
                    await page.locator("[data-slot=sidebar-trigger]").click();
                    await expect(page.locator("[data-slot=sidebar][data-state]")).toHaveAttribute("data-state", "collapsed");
                    await expect.poll(async () => (await page.getByRole("banner").boundingBox())!.x).toBe(48);
                }
            }
            await page.getByRole("link", { name: "ข้ามไปที่เนื้อหาหลัก" }).focus();
            await page.keyboard.press("Enter");
            await expect(page.getByRole("main")).toBeFocused();
            expect((await page.getByRole("main").boundingBox())!.y).toBeGreaterThanOrEqual(64);
        } finally {
            await context.close();
        }
    });
}
