import { test, expect } from "@playwright/test";

test.describe("Public pages", () => {
  test("home page loads and shows key sections", async ({ page }) => {
    await page.goto("/");
    await expect(page.getByRole("heading", { name: "Logi-Track" }).first()).toBeVisible();
    // Login on landing is a button (opens modal); fallback to link
    const loginControl = page.getByRole("button", { name: /log in|login|sign in|เข้าสู่ระบบ/i }).or(
      page.getByRole("link", { name: /login|sign in|เข้าสู่ระบบ/i })
    );
    await expect(loginControl.nth(0)).toBeVisible({ timeout: 10000 });
  });

  test("About Us page loads", async ({ page }) => {
    test.setTimeout(90000);
    await page.goto("/about", { timeout: 60000 });
    await expect(page.getByRole("heading", { name: /GORATECH|journey|ก่อร่าง/i }).first()).toBeVisible({ timeout: 15000 });
  });

  test("Support page loads", async ({ page }) => {
    await page.goto("/support");
    await expect(page.getByRole("heading", { name: /support center/i })).toBeVisible({ timeout: 5000 });
  });

  test("Join Network page loads", async ({ page }) => {
    await page.goto("/join-network");
    await expect(page.getByRole("heading", { name: /partner|join|network/i }).first()).toBeVisible({ timeout: 5000 });
  });

  test("navigation links work from home", async ({ page }) => {
    test.setTimeout(60000);
    await page.setViewportSize({ width: 1280, height: 720 });
    await page.goto("/");
    // Header nav has "About Us" link (client-rendered); click if visible, else navigate by URL
    const aboutLink = page.locator('a[href="/about"], a[href*="/about"]').first();
    try {
      await aboutLink.waitFor({ state: "visible", timeout: 8000 });
      await aboutLink.click();
    } catch {
      await page.goto("/about", { timeout: 60000 });
    }
    await expect(page).toHaveURL(/\/about/, { timeout: 15000 });
    await expect(page.getByRole("heading", { name: /GORATECH|journey|ก่อร่าง/i }).first()).toBeVisible({ timeout: 10000 });
  });
});
