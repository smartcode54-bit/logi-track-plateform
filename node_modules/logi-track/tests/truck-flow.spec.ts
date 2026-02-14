import { test, expect, Page } from '@playwright/test';

// Helper function to perform login
async function login(page: Page) {
    const email = process.env.PLAYWRIGHT_TEST_USER_EMAIL;
    const password = process.env.PLAYWRIGHT_TEST_USER_PASSWORD;

    // Validate credentials are set
    if (!email || !password) {
        throw new Error(
            `Missing test credentials! Please set PLAYWRIGHT_TEST_USER_EMAIL and PLAYWRIGHT_TEST_USER_PASSWORD in .env.local\n` +
            `Current values: email=${email}, password=${password ? '[SET]' : 'undefined'}`
        );
    }

    await page.goto('/login');
    await page.getByLabel('Email').fill(email);
    await page.getByLabel('Password').fill(password);
    await page.getByRole('button', { name: 'Login' }).click();

    // Wait for successful login - the toast message appears on success 
    // OR wait for URL change to admin area
    await Promise.race([
        page.waitForURL('**/admin/**', { timeout: 60000 }),
        expect(page.getByText(/Logged in successfully/i)).toBeVisible({ timeout: 60000 }),
    ]);

    // Extra wait for auth state to propagate
    await page.waitForTimeout(2000);
}

test.describe('Truck Management', () => {

    test.describe('New Truck Page', () => {
        test.beforeEach(async ({ page }) => {
            // Login first since this is a protected route
            await login(page);
            await page.goto('/admin/trucks/new');
        });

        test('should display the multi-step form', async ({ page }) => {
            // Check that the page loaded correctly
            await expect(page.locator('h1')).toContainText('Truck Details Form');

            // Check that step 1 is active
            await expect(page.getByText('Step 1 of 4')).toBeVisible();
        });

        test('should validate required fields on step 1', async ({ page }) => {
            // Try to proceed without filling required fields
            await page.getByRole('button', { name: /next step/i }).click();

            // Expect validation errors to appear (form should not advance)
            await expect(page.getByText('Step 1 of 4')).toBeVisible();
        });

        test('should navigate through all steps when valid', async ({ page }) => {
            // Step 1: Fill Vehicle Specs
            await page.getByLabel(/license plate/i).fill('กข1234');
            await page.getByLabel(/province/i).click();
            await page.getByRole('option', { name: /กรุงเทพ/i }).first().click();
            await page.getByLabel(/brand/i).click();
            await page.getByRole('option').first().click();
            await page.getByLabel(/model/i).fill('Ranger');
            await page.getByLabel(/color/i).fill('White');
            await page.getByLabel(/type/i).click();
            await page.getByRole('option').first().click();

            // Navigate to Step 2
            await page.getByRole('button', { name: /next step/i }).click();
            await expect(page.getByText('Step 2 of 4')).toBeVisible();

            // Step 2: Compliance (Skip for subcontractor trucks, fill for own)
            await page.getByRole('button', { name: /next step/i }).click();
            await expect(page.getByText('Step 3 of 4')).toBeVisible();

            // Step 3: Maintenance/Engine Info
            await page.getByRole('button', { name: /next step/i }).click();
            await expect(page.getByText('Step 4 of 4')).toBeVisible();

            // Step 4: Documentation - Final review page
            await expect(page.getByRole('button', { name: /save and continue/i })).toBeVisible();
        });
    });

    test.describe('Edit Truck Page', () => {
        // Note: This test requires an existing truck ID. 
        // For real tests, you would create a truck first or use a seeded test database.
        const testTruckId = 'test-truck-id'; // Replace with a real ID for actual testing

        test.skip('should load existing truck data', async ({ page }) => {
            await login(page);
            await page.goto(`/admin/trucks/edit?id=${testTruckId}`);

            // Check that the page loaded
            await expect(page.locator('h1')).toContainText('Edit Truck');

            // Check that form fields are populated (adjust selectors as needed)
            const licensePlateInput = page.getByLabel(/license plate/i);
            await expect(licensePlateInput).not.toHaveValue('');
        });

        test.skip('should save changes successfully', async ({ page }) => {
            await login(page);
            await page.goto(`/admin/trucks/edit?id=${testTruckId}`);

            // Wait for form to load
            await page.waitForSelector('form');

            // Update a field
            const notesField = page.locator('textarea[name="notes"]');
            await notesField.fill('Updated notes from Playwright test');

            // Submit the form
            await page.getByRole('button', { name: /save changes/i }).click();

            // Expect a success toast or redirect
            await expect(page.getByText(/success/i)).toBeVisible({ timeout: 10000 });
        });
    });
});
