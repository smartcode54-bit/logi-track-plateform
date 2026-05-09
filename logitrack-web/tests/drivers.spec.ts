import { test, expect } from "@playwright/test";
import { login, hasTestCredentials } from "./helpers/auth";

test.describe("Admin Drivers", () => {
  test.beforeEach(async ({ page }) => {
    test.skip(!hasTestCredentials(), "No test credentials set");
    await login(page);
  });

  test("drivers list loads", async ({ page }) => {
    await page.goto("/app/drivers");
    await expect(page).toHaveURL(/\/app\/drivers/);
    await expect(page.locator("main")).toBeVisible({ timeout: 10000 });
    await expect(page.getByText(/drivers|พนักงานขับรถ/i).first()).toBeVisible({ timeout: 5000 });
  });
});
