import { defineConfig, devices } from "@playwright/test";

// Reproducible UI3F4 run: `node tests/layout/fixtures/ui3f4-monitor-api.mjs` serves the API + JWKS on
// 127.0.0.1:3832. Start the production web with `PORT=3831 GO_API_INTERNAL_URL=http://127.0.0.1:3832
// WEB_PUBLIC_ORIGIN=http://127.0.0.1:3831 JWT_ISSUER=ui3f4 JWT_AUDIENCE=ui3f4-web
// SESSION_COOKIE_SECURE=false pnpm start`. Leave SESSION_COOKIE_DOMAIN unset; the host-only lt_at
// cookie is Path=/; HttpOnly; SameSite=Lax, without Secure. Run this config against 127.0.0.1:3831.
export default defineConfig({
    testDir: "./tests/layout",
    testMatch: "driver-monitor-compact.spec.ts",
    fullyParallel: true,
    workers: 2,
    retries: 0,
    reporter: "list",
    use: {
        ...devices["Desktop Chrome"], baseURL: "http://127.0.0.1:3831", trace: "off",
        launchOptions: process.env.UI3F4_CHROMIUM ? { executablePath: process.env.UI3F4_CHROMIUM } : {},
    },
});
