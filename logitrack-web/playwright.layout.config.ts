import { defineConfig, devices } from "@playwright/test";

/**
 * The page-header layout check (UI3D, tests/layout): Chromium against an already running production build given by
 * PAGE_LAYOUT_BASE_URL (no dev server is started). See tests/layout/page-header-layout.spec.ts for the variables.
 */
export default defineConfig({
    testDir: "./tests/layout",
    testIgnore: "**/driver-monitor-compact.spec.ts",
    fullyParallel: true,
    retries: 0,
    reporter: "list",
    use: { trace: "off" },
    projects: [
        {
            name: "chromium",
            use: {
                ...devices["Desktop Chrome"],
                // A local Chromium build when the bundled one is not installed.
                launchOptions: process.env.PAGE_LAYOUT_CHROMIUM ? { executablePath: process.env.PAGE_LAYOUT_CHROMIUM } : {},
            },
        },
    ],
});
