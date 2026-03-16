import { test, expect } from "@playwright/test";
import { login, hasTestCredentials } from "./helpers/auth";

test.describe("Auth", () => {
  test.describe("Login page", () => {
    test("shows login form", async ({ page }) => {
      await page.goto("/login");
      await expect(page.getByLabel("Email")).toBeVisible();
      await expect(page.getByLabel("Password")).toBeVisible();
      await expect(page.getByRole("button", { name: /login|เข้าสู่ระบบ/i })).toBeVisible();
    });

    test("shows error on invalid credentials", async ({ page }) => {
      await page.goto("/login");
      await page.getByLabel("Email").fill("invalid@example.com");
      await page.getByLabel("Password").fill("wrongpassword");
      await page.getByRole("button", { name: /login|เข้าสู่ระบบ/i }).click();
      // Wait for login attempt to finish; should stay on login, not redirect to admin
      await expect(page).toHaveURL(/\/login/, { timeout: 10000 });
      await expect(page).not.toHaveURL(/\/admin\//);
    });

    test("redirects to admin dashboard on success when credentials are set", async ({ page }) => {
      test.skip(!hasTestCredentials(), "No PLAYWRIGHT_TEST_USER_EMAIL / PASSWORD set");
      await login(page);
      await expect(page).toHaveURL(/\/admin\//);
    });
  });

  test.describe("Forgot password", () => {
    test("forgot password link goes to forgot-password page", async ({ page }) => {
      await page.goto("/login");
      await page.getByRole("link", { name: /forgot|ลืม/i }).click();
      await expect(page).toHaveURL(/\/forgot-password/);
    });
  });

  test.describe("Waitlist", () => {
    test("waitlist page loads", async ({ page }) => {
      await page.goto("/waitlist");
      // CardTitle "Join Waitlist" may render as div, not heading
      await expect(page.getByText(/join waitlist|registration is currently/i).first()).toBeVisible({ timeout: 5000 });
    });
  });

  test.describe("Protected routes", () => {
    test("accessing /admin redirects to login when not authenticated", async ({ page, context }) => {
      // Run only in CI (clean auth state); locally Firebase persistence can keep user
      test.skip(!process.env.CI, "Run in CI with clean auth state");
      await context.clearCookies();
      await page.goto("/");
      await page.evaluate(() => localStorage.clear());
      await page.goto("/admin/dashboard");
      await expect(page).toHaveURL(/\/login/, { timeout: 15000 });
    });
  });
});
