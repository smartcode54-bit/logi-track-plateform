import { test, expect } from "@playwright/test";
import { login, hasTestCredentials } from "./helpers/auth";

test.describe("Admin dashboard", () => {
  test.beforeEach(async ({ page }) => {
    test.skip(!hasTestCredentials(), "No test credentials set");
    await login(page);
  });

  test("dashboard loads after login", async ({ page }) => {
    await page.goto("/app/dashboard");
    await expect(page).toHaveURL(/\/app\/dashboard/);
    await expect(page.locator("h1, h2").first()).toBeVisible({ timeout: 10000 });
  });

  test("admin dashboard page has main content", async ({ page }) => {
    await page.goto("/app/dashboard");
    await expect(page.locator("main")).toBeVisible({ timeout: 5000 });
  });
});
