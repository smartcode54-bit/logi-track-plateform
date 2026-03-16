import { Page } from "@playwright/test";

/**
 * Perform login with credentials from PLAYWRIGHT_TEST_USER_EMAIL and PLAYWRIGHT_TEST_USER_PASSWORD.
 * Throws if env vars are not set.
 */
export async function login(page: Page): Promise<void> {
  const email = process.env.PLAYWRIGHT_TEST_USER_EMAIL;
  const password = process.env.PLAYWRIGHT_TEST_USER_PASSWORD;

  if (!email || !password) {
    throw new Error(
      `Missing test credentials! Set PLAYWRIGHT_TEST_USER_EMAIL and PLAYWRIGHT_TEST_USER_PASSWORD in .env.local`
    );
  }

  await page.goto("/login");
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password").fill(password);
  await page.getByRole("button", { name: /login|เข้าสู่ระบบ/i }).click();

  await page.waitForURL("**/admin/**", { timeout: 60000 });
  await page.waitForTimeout(2000);
}

export function hasTestCredentials(): boolean {
  return !!(
    process.env.PLAYWRIGHT_TEST_USER_EMAIL &&
    process.env.PLAYWRIGHT_TEST_USER_PASSWORD
  );
}
